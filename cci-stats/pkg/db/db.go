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
	ID      int
	JobID   string
	Name    string
	Runtime float64
	Status  string
	Message string
}

type Connection interface {
	LastPipeline(ctx context.Context) (*Pipeline, error)

	Begin() (Transactor, error)
	Close() error
}

type Transactor interface {
	InsertPipeline(ctx context.Context, p Pipeline) error
	InsertJob(ctx context.Context, j Job) error
	InsertTestResult(ctx context.Context, tr TestResult) (int, error)
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
SELECT id, number, commit, branch, created_at
FROM pipelines ORDER BY created_at DESC LIMIT 1
`

	row := p.conn.QueryRow(ctx, sql)
	var pl Pipeline
	if err := row.Scan(&pl.ID, &pl.Number, &pl.Commit, &pl.Branch, &pl.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}

		return nil, fmt.Errorf("failed to get last pipeline: %w", err)
	}
	return &pl, nil
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

	sql := `
INSERT INTO pipelines (id, number, commit, branch, created_at)
VALUES ($1, $2, $3, $4, $5) ON CONFLICT DO NOTHING
`

	if _, err := p.tx.Exec(ctx,
		sql,
		pl.ID,
		pl.Number,
		pl.Commit,
		pl.Branch,
		pl.CreatedAt,
	); err != nil {
		return fmt.Errorf("failed to insert pipeline: %w", err)
	}
	return nil
}

func (p *PGXTransactor) InsertJob(ctx context.Context, j Job) error {
	p.mtx.Lock()
	defer p.mtx.Unlock()

	type queryPkg struct {
		query string
		args  []any
	}

	queries := []queryPkg{
		{
			"DELETE FROM test_results WHERE job_id = $1",
			[]any{j.ID},
		},
		{
			`INSERT INTO jobs (id, pipeline_id, workflow_id, workflow_name, number, name, status, started_at, stopped_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) ON CONFLICT DO NOTHING`,
			[]any{
				j.ID,
				j.PipelineID,
				j.WorkflowID,
				j.WorkflowName,
				j.Number,
				j.Name,
				j.Status,
				j.StartedAt,
				j.StoppedAt,
			},
		},
	}

	for i, q := range queries {
		if _, err := p.tx.Exec(ctx,
			q.query,
			q.args...,
		); err != nil {
			return fmt.Errorf("failed to insert job: query %d: %w", i, err)
		}
	}

	return nil
}

func (p *PGXTransactor) InsertTestResult(ctx context.Context, tr TestResult) (int, error) {
	p.mtx.Lock()
	defer p.mtx.Unlock()

	sql := `
INSERT INTO test_results (job_id, name, runtime, status, message)
VALUES ($1, $2, $3, $4, $5) RETURNING id
`

	row := p.tx.QueryRow(ctx,
		sql,
		tr.JobID,
		tr.Name,
		tr.Runtime,
		tr.Status,
		tr.Message,
	)
	var id int
	if err := row.Scan(&id); err != nil {
		return 0, fmt.Errorf("failed to insert test result: %w", err)
	}
	return id, nil
}

func (p *PGXTransactor) Commit(ctx context.Context) error {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	return p.tx.Commit(ctx)
}

func (p *PGXTransactor) Rollback(ctx context.Context) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	if err := p.tx.Rollback(context.Background()); err != nil {
		slog.Error("error rolling back transaction", "err", err)
	}
}
