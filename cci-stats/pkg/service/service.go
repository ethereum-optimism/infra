package service

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/axelKingsley/go-circleci"
	"github.com/ethereum-optimism/infra/cci-stats/pkg/config"
	"github.com/ethereum-optimism/infra/cci-stats/pkg/db"
	"github.com/sourcegraph/conc/pool"
)

func GenerateReport(ctx context.Context, config config.Config, client *circleci.Client, dbConn db.Connection) error {
	lastPipeline, err := dbConn.LastPipeline(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch last pipeline: %w", err)
	}
	cutoff := time.Now().Add(-time.Duration(config.FetchLimitDays) * 24 * time.Hour)
	if lastPipeline != nil {
		cutoff = lastPipeline.CreatedAt
	}

	pipelines, err := fetchPipelines(ctx, config, cutoff, client)
	if err != nil {
		return fmt.Errorf("failed to fetch pipelines: %w", err)
	}

	var completed int32
	plinePool := pool.New().
		WithErrors().
		WithFirstError().
		WithMaxGoroutines(config.MaxConcurrentFetchJobs).
		WithContext(ctx).
		WithCancelOnError()
	for _, pline := range pipelines {
		pline := pline
		plinePool.Go(func(ctx context.Context) error {
			if err := processPipeline(ctx, config, client, dbConn, pline); err != nil {
				return fmt.Errorf("failed to process pipeline: %w", err)
			}

			cmpl := atomic.AddInt32(&completed, 1)
			slog.Info(
				"completed pipeline",
				"pipeline", pline.ID,
				"number", pline.Number,
				"total", len(pipelines),
				"completed", cmpl,
			)
			return nil
		})
	}
	if err := plinePool.Wait(); err != nil {
		return fmt.Errorf("failed to ingest pipelines: %w", err)
	}
	return nil
}

func processPipeline(ctx context.Context, config config.Config, client *circleci.Client, dbConn db.Connection, pline *circleci.Pipeline) error {
	workflows, err := fetchWorkflows(ctx, config, client, pline.ID)
	if err != nil {
		return fmt.Errorf("failed to fetch workflows: %w", err)
	}

	if len(workflows) == 0 {
		slog.Info("skipping pipeline with no workflows", "pipeline", pline.ID, "number", pline.Number)
		return nil
	}

	workflowsJobs := make(map[string][]*circleci.WorkflowJob)
	workflowsByID := make(map[string]*circleci.Workflow)

	slog.Info("fetching jobs", "pipeline", pline.ID, "number", pline.Number)
	for _, wf := range workflows {
		if wf.Status == "running" ||
			wf.Status == "not_run" ||
			wf.Status == "failing" ||
			wf.Status == "on_hold" ||
			wf.Status == "canceled" ||
			wf.Status == "unauthorized" {
			return nil
		}

		jobs, err := fetchJobs(ctx, client, wf.ID)
		if err != nil {
			return fmt.Errorf("failed to fetch jobs: %w", err)
		}
		workflowsJobs[wf.ID] = jobs
		workflowsByID[wf.ID] = wf
	}

	type testMetadata struct {
		JobID    string
		WFID     string
		Metadata []*circleci.TestMetadata
	}

	slog.Debug("fetching test metadata", "pipeline", pline.ID, "number", pline.Number)
	testPool := pool.NewWithResults[testMetadata]().
		WithErrors().
		WithFirstError().
		WithMaxGoroutines(config.MaxConcurrentFetchJobs).
		WithContext(ctx).
		WithCancelOnError()
	for wfID, jobs := range workflowsJobs {
		for _, job := range jobs {
			testPool.Go(func(ctx context.Context) (testMetadata, error) {
				tm, err := fetchTestMetadata(ctx, config, client, job)
				if err != nil {
					return testMetadata{}, fmt.Errorf("failed to fetch test metadata: %w", err)
				}
				return testMetadata{
					JobID:    job.ID,
					WFID:     wfID,
					Metadata: tm,
				}, nil
			})
		}
	}
	metadata, err := testPool.Wait()
	if err != nil {
		return fmt.Errorf("failed to fetch test metadata: %w", err)
	}

	tx, err := dbConn.Begin()
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}
	defer func() {
		if err != nil {
			tx.Rollback(ctx)
		}
	}()

	slog.Info("inserting into DB", "pipeline", pline.ID, "number", pline.Number)
	if err := tx.InsertPipeline(ctx, db.Pipeline{
		ID:        pline.ID,
		Number:    pline.Number,
		Commit:    pline.Vcs.Revision,
		Branch:    pline.Vcs.Branch,
		CreatedAt: pline.CreatedAt,
	}); err != nil {
		return fmt.Errorf("failed to insert pipeline: %w", err)
	}

	slog.Info("inserting jobs", "pipeline", pline.ID)
	for wfID, jobs := range workflowsJobs {
		wf := workflowsByID[wfID]
		for _, job := range jobs {
			if err := tx.InsertJob(ctx, db.Job{
				ID:           fmt.Sprintf("%s/%s", wf.ID, job.ID),
				PipelineID:   pline.ID,
				WorkflowID:   wfID,
				WorkflowName: wf.Name,
				Number:       job.JobNumber,
				Name:         job.Name,
				Status:       job.Status,
				StartedAt:    job.StartedAt,
				StoppedAt:    job.StoppedAt,
			}); err != nil {
				return fmt.Errorf("failed to insert workflow: %w", err)
			}
		}
	}

	slog.Info("inserting test metadata", "pipeline", pline.ID, "number", pline.Number)
	for _, tm := range metadata {
		for _, m := range tm.Metadata {
			if _, err := tx.InsertTestResult(ctx, db.TestResult{
				JobID:   fmt.Sprintf("%s/%s", tm.WFID, tm.JobID),
				Name:    m.Name,
				Runtime: m.RunTime,
				Status:  m.Result,
				Message: m.Message,
			}); err != nil {
				return fmt.Errorf("failed to insert test result: %w", err)
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

func fetchPipelines(ctx context.Context, config config.Config, cutoff time.Time, client *circleci.Client) ([]*circleci.Pipeline, error) {
	var res []*circleci.Pipeline
	var pageToken string
	opts := circleci.ProjectListPipelinesOptions{}

	for {
		if pageToken != "" {
			opts.PageToken = &pageToken
		}

		pipelines, err := client.Projects.ListPipelines(ctx, config.ProjectSlug, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to list pipelines: %w", err)
		}

		var done bool
		for _, p := range pipelines.Items {
			if !config.BranchPatternRegex.MatchString(p.Vcs.Branch) {
				continue
			}
			if p.CreatedAt.Before(cutoff) {
				done = true
				break
			}
			res = append(res, p)
		}

		if len(res) > 0 {
			slog.Info("fetched pipelines", "count", len(res), "last", res[len(res)-1].CreatedAt)
		}

		if done || pipelines.NextPageToken == "" {
			break
		}
		pageToken = pipelines.NextPageToken
	}

	return res, nil
}

func fetchWorkflows(ctx context.Context, config config.Config, client *circleci.Client, pipelineID string) ([]*circleci.Workflow, error) {
	var res []*circleci.Workflow
	opts := circleci.PipelineListWorkflowsOptions{}

	workflows, err := client.Pipelines.ListWorkflows(ctx, pipelineID, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to list workflows: %w", err)
	}

	for _, wf := range workflows.Items {
		if !config.WorkflowPatternRegex.MatchString(wf.Name) {
			continue
		}
		res = append(res, wf)
	}

	slog.Debug("fetched workflows", "count", len(res), "pipeline", pipelineID)

	return res, nil
}

func fetchJobs(ctx context.Context, client *circleci.Client, workflowID string) ([]*circleci.WorkflowJob, error) {
	jobs, err := client.Workflows.ListWorkflowJobs(ctx, workflowID)
	if err != nil {
		return nil, fmt.Errorf("failed to list jobs: %w", err)
	}
	slog.Debug("fetched jobs", "count", len(jobs.Items), "workflow", workflowID)
	return jobs.Items, nil
}

func fetchTestMetadata(ctx context.Context, config config.Config, client *circleci.Client, job *circleci.WorkflowJob) ([]*circleci.TestMetadata, error) {
	md, err := client.Jobs.ListTestMetadata(ctx, job.ProjectSlug, fmt.Sprintf("%d", job.JobNumber))
	if err != nil {
		return nil, fmt.Errorf("failed to list test metadata: %w", err)
	}

	var out []*circleci.TestMetadata
	for _, m := range md.Items {
		if m.Result == "skipped" {
			continue
		}

		if m.Result == "success" && config.SlowTestThresholdSeconds > m.RunTime {
			continue
		}

		out = append(out, m)
	}
	slog.Debug("fetched test metadata", "count", len(out), "job", job.ID)
	return out, nil
}
