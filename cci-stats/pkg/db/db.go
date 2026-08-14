package db

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Pipeline struct {
	ID        string
	Number    int64
	Commit    string
	Branch    string
	CreatedAt time.Time
	// Complete is false while the pipeline may still create workflows, or while
	// a matched workflow is unfinished. The ingest high water mark is
	// MAX(created_at) over the table, so a pipeline left unrecorded can never be
	// revisited; recording it as incomplete lets the mark advance while the
	// pipeline stays on the retry list.
	Complete bool
}

type Job struct {
	ID           string
	PipelineID   string
	WorkflowID   string
	WorkflowName string
	Number       int64
	Name         string
	Status       string
	StartedAt    time.Time
	StoppedAt    time.Time
}

type TestResult struct {
	Name    string
	Runtime float64
	Status  string
	Message string
}

type Connection interface {
	LastPipeline(ctx context.Context) (*Pipeline, error)
	IncompletePipelines(ctx context.Context, since time.Time) ([]Pipeline, error)
	RecordPending(ctx context.Context, pipelines []Pipeline) error

	Begin() (Transactor, error)
	Close() error
}

type Transactor interface {
	InsertPipeline(ctx context.Context, p Pipeline) error
	InsertJob(ctx context.Context, j Job) error
	ReplaceTestResults(ctx context.Context, jobID string, results []TestResult) error
	Commit(ctx context.Context) error
	Rollback(ctx context.Context)
}

type PGXDB struct {
	conn *pgxpool.Pool
}

func New(ctx context.Context, uri string) (*PGXDB, error) {
	conn, err := pgxpool.New(ctx, uri)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to db: %w", err)
	}

	return &PGXDB{conn: conn}, nil
}

func (p *PGXDB) LastPipeline(ctx context.Context) (*Pipeline, error) {
	sql := `
SELECT id, number, commit, branch, created_at, complete
FROM pipelines ORDER BY created_at DESC LIMIT 1
`

	row := p.conn.QueryRow(ctx, sql)
	var pl Pipeline
	if err := row.Scan(&pl.ID, &pl.Number, &pl.Commit, &pl.Branch, &pl.CreatedAt, &pl.Complete); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}

		return nil, fmt.Errorf("failed to get last pipeline: %w", err)
	}
	return &pl, nil
}

// IncompletePipelines returns pipelines recorded before they finished, so they
// can be indexed again. The since bound stops a pipeline that never finishes,
// such as one left awaiting approval, from being retried forever.
func (p *PGXDB) IncompletePipelines(ctx context.Context, since time.Time) ([]Pipeline, error) {
	sql := `
SELECT id, number, commit, branch, created_at, complete
FROM pipelines WHERE NOT complete AND created_at >= $1 ORDER BY created_at
`

	rows, err := p.conn.Query(ctx, sql, since)
	if err != nil {
		return nil, fmt.Errorf("failed to query incomplete pipelines: %w", err)
	}
	defer rows.Close()

	var res []Pipeline
	for rows.Next() {
		var pl Pipeline
		if err := rows.Scan(&pl.ID, &pl.Number, &pl.Commit, &pl.Branch, &pl.CreatedAt, &pl.Complete); err != nil {
			return nil, fmt.Errorf("failed to scan incomplete pipeline: %w", err)
		}
		res = append(res, pl)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read incomplete pipelines: %w", err)
	}
	return res, nil
}

// RecordPending records pipelines as incomplete before they are indexed, so a
// run that stops part way leaves them on the retry list rather than behind the
// high water mark. Pipelines already recorded are left alone, so this never
// reopens a completed one.
func (p *PGXDB) RecordPending(ctx context.Context, pipelines []Pipeline) error {
	if len(pipelines) == 0 {
		return nil
	}

	sql := `
INSERT INTO pipelines (id, number, commit, branch, created_at, complete)
VALUES ($1, $2, $3, $4, $5, FALSE) ON CONFLICT (id) DO NOTHING
`

	batch := &pgx.Batch{}
	for _, pl := range pipelines {
		batch.Queue(sql, pl.ID, pl.Number, pl.Commit, pl.Branch, pl.CreatedAt)
	}
	if err := p.conn.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("failed to record pending pipelines: %w", err)
	}
	return nil
}

func (p *PGXDB) Begin() (Transactor, error) {
	tx, err := p.conn.Begin(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}

	return &PGXTransactor{tx: tx}, nil
}

func (p *PGXDB) Close() error {
	p.conn.Close()
	return nil
}

type PGXTransactor struct {
	tx  pgx.Tx
	mtx sync.Mutex
}

func (p *PGXTransactor) InsertPipeline(ctx context.Context, pl Pipeline) error {
	p.mtx.Lock()
	defer p.mtx.Unlock()

	// The complete flag has to be updated on conflict: a pipeline is recorded
	// while it is still running and indexed again once it finishes.
	sql := `
INSERT INTO pipelines (id, number, commit, branch, created_at, complete)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (id) DO UPDATE SET complete = EXCLUDED.complete
`

	if _, err := p.tx.Exec(ctx,
		sql,
		pl.ID,
		pl.Number,
		pl.Commit,
		pl.Branch,
		pl.CreatedAt,
		pl.Complete,
	); err != nil {
		return fmt.Errorf("failed to insert pipeline: %w", err)
	}
	return nil
}

func (p *PGXTransactor) InsertJob(ctx context.Context, j Job) error {
	p.mtx.Lock()
	defer p.mtx.Unlock()

	sql := `
INSERT INTO jobs (id, pipeline_id, workflow_id, workflow_name, number, name, status, started_at, stopped_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (id) DO UPDATE SET status = EXCLUDED.status, started_at = EXCLUDED.started_at, stopped_at = EXCLUDED.stopped_at
`

	if _, err := p.tx.Exec(ctx,
		sql,
		j.ID,
		j.PipelineID,
		j.WorkflowID,
		j.WorkflowName,
		j.Number,
		j.Name,
		j.Status,
		j.StartedAt,
		j.StoppedAt,
	); err != nil {
		return fmt.Errorf("failed to insert job: %w", err)
	}

	return nil
}

// ReplaceTestResults swaps a job's stored results for the ones supplied. The
// caller must not pass an empty slice: clearing the old rows is only safe when
// there are new ones to put in their place, since a job whose metadata fetch
// returns not found is indistinguishable from one that genuinely has none.
func (p *PGXTransactor) ReplaceTestResults(ctx context.Context, jobID string, results []TestResult) error {
	if len(results) == 0 {
		return fmt.Errorf("refusing to replace test results for job %s with none", jobID)
	}

	p.mtx.Lock()
	defer p.mtx.Unlock()

	if _, err := p.tx.Exec(ctx, "DELETE FROM test_results WHERE job_id = $1", jobID); err != nil {
		return fmt.Errorf("failed to clear test results: %w", err)
	}

	sql := `
INSERT INTO test_results (job_id, name, runtime, status, message)
VALUES ($1, $2, $3, $4, $5)
`
	for _, tr := range results {
		if _, err := p.tx.Exec(ctx, sql, jobID, tr.Name, tr.Runtime, tr.Status, tr.Message); err != nil {
			return fmt.Errorf("failed to insert test result: %w", err)
		}
	}
	return nil
}

func (p *PGXTransactor) Commit(ctx context.Context) error {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	return p.tx.Commit(ctx)
}

// Rollback is safe to defer unconditionally: rolling back an already committed
// transaction is a no-op rather than an error worth reporting.
func (p *PGXTransactor) Rollback(ctx context.Context) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	if err := p.tx.Rollback(context.Background()); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		slog.Error("error rolling back transaction", "err", err)
	}
}
