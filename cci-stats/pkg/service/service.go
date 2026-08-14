package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/axelKingsley/go-circleci"
	"github.com/ethereum-optimism/infra/cci-stats/pkg/config"
	"github.com/ethereum-optimism/infra/cci-stats/pkg/db"
	"github.com/sourcegraph/conc/pool"
)

// pipelineWork is a pipeline queued for indexing, from either the CircleCI
// listing or the set of pipelines held over from an earlier run.
type pipelineWork struct {
	pipeline db.Pipeline
	// state is the CircleCI pipeline state, carried over from the listing. It is
	// unset for a pipeline known only from the database, which reloads it.
	state string
	// retry marks a pipeline already recorded as incomplete by an earlier run.
	// It stays on the retry list either way, so failing to index it is not fatal
	// to the run. A pipeline claimed for the first time by this run is not a
	// retry: a failure there is still worth surfacing.
	retry bool
}

func GenerateReport(ctx context.Context, config config.Config, client *circleci.Client, dbConn db.Connection) error {
	lastPipeline, err := dbConn.LastPipeline(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch last pipeline: %w", err)
	}
	// UTC because pgx reinterprets a non-UTC wall clock as UTC when writing a
	// timestamp without time zone, which would shift the window by the offset.
	horizon := time.Now().UTC().Add(-time.Duration(config.FetchLimitDays) * 24 * time.Hour)
	cutoff := horizon
	// Never reach back past the horizon, even when the mark is older because the
	// indexer has been down for longer than FETCH_LIMIT_DAYS. Anything claimed
	// beyond it would fall outside the retry window and so could never be
	// reindexed.
	if lastPipeline != nil && lastPipeline.CreatedAt.After(horizon) {
		cutoff = lastPipeline.CreatedAt
	}

	newPipelines, err := fetchPipelines(ctx, config, cutoff, client)
	if err != nil {
		return fmt.Errorf("failed to fetch pipelines: %w", err)
	}

	// Pipelines are not indexed in creation order, so the mark routinely moves
	// past one that was unfinished when last seen. See db.Pipeline.Complete.
	incomplete, err := dbConn.IncompletePipelines(ctx, horizon)
	if err != nil {
		return fmt.Errorf("failed to fetch incomplete pipelines: %w", err)
	}
	retries := make([]pipelineWork, 0, len(incomplete))
	for _, p := range incomplete {
		retries = append(retries, pipelineWork{pipeline: p, retry: true})
	}

	pipelines := mergePipelines(newPipelines, retries)
	slog.Info("indexing pipelines", "new", len(newPipelines), "retried", len(retries), "total", len(pipelines))

	// Each pipeline commits its own transaction, so a run that stops part way
	// would otherwise leave the mark past pipelines it never wrote.
	claimed := make([]db.Pipeline, 0, len(newPipelines))
	for _, work := range newPipelines {
		claimed = append(claimed, work.pipeline)
	}
	if err := dbConn.RecordPending(ctx, claimed); err != nil {
		return fmt.Errorf("failed to record pending pipelines: %w", err)
	}

	var completed int32
	plinePool := pool.New().
		WithErrors().
		WithFirstError().
		WithMaxGoroutines(config.MaxConcurrentFetchJobs).
		WithContext(ctx).
		WithCancelOnError()
	for _, work := range pipelines {
		plinePool.Go(func(ctx context.Context) error {
			if err := processPipeline(ctx, config, client, dbConn, work); err != nil {
				if work.retry {
					// A retried pipeline stays on the retry list, so failing the
					// run here would repeat every run until it ages out and
					// would stop anything at all being recorded meanwhile.
					slog.Error(
						"skipping retried pipeline that failed to index",
						"pipeline", work.pipeline.ID,
						"number", work.pipeline.Number,
						"err", err,
					)
					return nil
				}
				return fmt.Errorf("failed to process pipeline: %w", err)
			}

			cmpl := atomic.AddInt32(&completed, 1)
			slog.Info(
				"completed pipeline",
				"pipeline", work.pipeline.ID,
				"number", work.pipeline.Number,
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

func processPipeline(ctx context.Context, config config.Config, client *circleci.Client, dbConn db.Connection, work pipelineWork) error {
	pline := work.pipeline

	state := work.state
	if state == "" {
		// Reloaded from the database, which does not record the state.
		fresh, err := client.Pipelines.Get(ctx, pline.ID)
		if err != nil {
			return fmt.Errorf("failed to fetch pipeline: %w", err)
		}
		state = fresh.State
	}

	allWorkflows, err := fetchWorkflows(ctx, client, pline.ID)
	if err != nil {
		return fmt.Errorf("failed to fetch workflows: %w", err)
	}

	type matchedWorkflow struct {
		workflow *circleci.Workflow
		status   string
	}
	var matched []matchedWorkflow
	var statuses []string
	for _, wf := range allWorkflows {
		if config.WorkflowPatternRegex.MatchString(wf.Name) {
			status := workflowStatus(wf)
			matched = append(matched, matchedWorkflow{workflow: wf, status: status})
			statuses = append(statuses, status)
		}
	}
	pline.Complete = pipelineComplete(state, len(allWorkflows), statuses)

	workflowsJobs := make(map[string][]*circleci.WorkflowJob)
	workflowsByID := make(map[string]*circleci.Workflow)

	slog.Info("fetching jobs", "pipeline", pline.ID, "number", pline.Number, "state", state, "complete", pline.Complete)
	for _, m := range matched {
		// Nothing worth recording yet. The pipeline is still written, marked
		// incomplete, so a later run picks the workflow up once it finishes.
		if !isTerminalStatus(m.status) {
			continue
		}

		jobs, err := fetchJobs(ctx, client, m.workflow.ID)
		if err != nil {
			return fmt.Errorf("failed to fetch jobs: %w", err)
		}
		workflowsJobs[m.workflow.ID] = jobs
		workflowsByID[m.workflow.ID] = m.workflow
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
	defer tx.Rollback(ctx)

	slog.Info("inserting into DB", "pipeline", pline.ID, "number", pline.Number)
	if err := tx.InsertPipeline(ctx, pline); err != nil {
		return fmt.Errorf("failed to insert pipeline: %w", err)
	}

	slog.Info("inserting jobs", "pipeline", pline.ID)
	for wfID, jobs := range workflowsJobs {
		wf := workflowsByID[wfID]
		for _, job := range jobs {
			// StartedAt and StoppedAt are the zero time for a blocked job, which
			// CircleCI reports without timestamps. Stored as 0001-01-01, so a
			// query computing wall clock has to exclude them.
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
		// Leave the stored results alone when the fetch produced nothing. A job
		// that genuinely has no metadata is indistinguishable from one whose
		// fetch returned not found, and clearing on the latter would be
		// permanent once the pipeline is marked complete.
		if len(tm.Metadata) == 0 {
			continue
		}

		results := make([]db.TestResult, 0, len(tm.Metadata))
		for _, m := range tm.Metadata {
			results = append(results, db.TestResult{
				Name:    m.Name,
				Runtime: m.RunTime,
				Status:  m.Result,
				Message: m.Message,
			})
		}
		if err := tx.ReplaceTestResults(ctx, fmt.Sprintf("%s/%s", tm.WFID, tm.JobID), results); err != nil {
			return fmt.Errorf("failed to replace test results: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

func fetchPipelines(ctx context.Context, config config.Config, cutoff time.Time, client *circleci.Client) ([]pipelineWork, error) {
	var res []pipelineWork
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
			if p.Vcs == nil || !config.BranchPatternRegex.MatchString(p.Vcs.Branch) {
				continue
			}
			if p.CreatedAt.Before(cutoff) {
				done = true
				break
			}
			res = append(res, pipelineWork{
				pipeline: db.Pipeline{
					ID:        p.ID,
					Number:    p.Number,
					Commit:    p.Vcs.Revision,
					Branch:    p.Vcs.Branch,
					CreatedAt: p.CreatedAt,
				},
				state: p.State,
			})
		}

		if len(res) > 0 {
			slog.Info("fetched pipelines", "count", len(res), "last", res[len(res)-1].pipeline.CreatedAt)
		}

		if done || pipelines.NextPageToken == "" {
			break
		}
		pageToken = pipelines.NextPageToken
	}

	return res, nil
}

// fetchWorkflows returns every workflow on the pipeline. The caller filters by
// WORKFLOW_PATTERN, since it also needs the unfiltered count.
//
// The list has to be exhausted: a truncated page would understate the workflows
// and could mark a pipeline complete on a partial set.
func fetchWorkflows(ctx context.Context, client *circleci.Client, pipelineID string) ([]*circleci.Workflow, error) {
	var res []*circleci.Workflow
	var pageToken string
	opts := circleci.PipelineListWorkflowsOptions{}

	for {
		if pageToken != "" {
			opts.PageToken = &pageToken
		}

		workflows, err := client.Pipelines.ListWorkflows(ctx, pipelineID, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to list workflows: %w", err)
		}
		res = append(res, workflows.Items...)

		if workflows.NextPageToken == "" {
			break
		}
		pageToken = workflows.NextPageToken
	}

	slog.Debug("fetched workflows", "count", len(res), "pipeline", pipelineID)

	return res, nil
}

// fetchJobs cannot follow NextPageToken the way its siblings do: the client's
// ListWorkflowJobs takes no options argument, so there is nowhere to pass a page
// token. A workflow with more jobs than CircleCI's page size is therefore
// truncated.
// TODO(#710): paginate once the client can express it.
func fetchJobs(ctx context.Context, client *circleci.Client, workflowID string) ([]*circleci.WorkflowJob, error) {
	jobs, err := client.Workflows.ListWorkflowJobs(ctx, workflowID)
	if err != nil {
		return nil, fmt.Errorf("failed to list jobs: %w", err)
	}
	slog.Debug("fetched jobs", "count", len(jobs.Items), "workflow", workflowID)
	return jobs.Items, nil
}

// terminalWorkflowStatuses are the CircleCI workflow statuses that will not
// change again. Everything else, notably on_hold, is still in progress.
var terminalWorkflowStatuses = map[string]bool{
	"success":      true,
	"failed":       true,
	"error":        true,
	"canceled":     true,
	"unauthorized": true,
	"not_run":      true,
}

func isTerminalStatus(status string) bool {
	return terminalWorkflowStatuses[status]
}

// workflowStatus reads the status off a workflow, which the client models as an
// untyped field. A non-string reads as unknown, and so as still in progress,
// because retrying is the safe direction.
func workflowStatus(wf *circleci.Workflow) string {
	s, ok := wf.Status.(string)
	if !ok {
		slog.Warn("workflow has non-string status", "workflow", wf.ID, "status", wf.Status)
		return ""
	}
	return s
}

const (
	pipelineStateCreated = "created"
	pipelineStateErrored = "errored"
)

// pipelineWorkflowsFinal reports whether a pipeline will create no further
// workflows.
//
// The state has to be consulted, because under dynamic config generation the
// workflows worth recording are created by the continuation request, and setup
// reaching success is not ordered against that: measured over 20 monorepo
// pipelines the gap ran from -1s to +2s. Judging on the workflow list alone can
// catch a pipeline looking finished a second before its real workflows exist
// and write it off for good. CircleCI holds it in setup or pending until the
// continuation lands, so neither reads as final here.
func pipelineWorkflowsFinal(state string, totalWorkflows int) bool {
	switch state {
	case pipelineStateErrored:
		// Never the initial state, so it needs no workflow guard.
		return true
	case pipelineStateCreated:
		// Also reported before setup starts, so require evidence of workflows.
		return totalWorkflows > 0
	default:
		return false
	}
}

// pipelineComplete reports whether a pipeline is done producing data.
func pipelineComplete(state string, totalWorkflows int, matchedStatuses []string) bool {
	if !pipelineWorkflowsFinal(state, totalWorkflows) {
		return false
	}
	for _, s := range matchedStatuses {
		if !isTerminalStatus(s) {
			return false
		}
	}
	return true
}

// mergePipelines combines newly discovered pipelines with those being retried.
// A pipeline in both keeps the freshly fetched copy, which already carries a
// current state, but stays marked as a retry so that failing to index it is not
// fatal. The pipeline holding the high water mark is listed again every run, so
// without this the commonest retry would be the one losing that protection.
func mergePipelines(fetched []pipelineWork, retries []pipelineWork) []pipelineWork {
	retrying := make(map[string]bool, len(retries))
	for _, p := range retries {
		retrying[p.pipeline.ID] = true
	}

	seen := make(map[string]bool, len(fetched))
	res := make([]pipelineWork, 0, len(fetched)+len(retries))
	for _, p := range fetched {
		seen[p.pipeline.ID] = true
		p.retry = retrying[p.pipeline.ID]
		res = append(res, p)
	}
	for _, p := range retries {
		if seen[p.pipeline.ID] {
			continue
		}
		res = append(res, p)
	}
	return res
}

// mapFlakyStatus maps CircleCI test result status based on flaky markers
// in the skip message. FLAKY_FAIL becomes "flaky_fail", FLAKY_PASS becomes "flaky_pass",
// and all other statuses pass through unchanged.
func mapFlakyStatus(result, message string) string {
	if result == "skipped" {
		if strings.Contains(message, "FLAKY_FAIL") {
			return "flaky_fail"
		}
		if strings.Contains(message, "FLAKY_PASS") {
			return "flaky_pass"
		}
	}
	return result
}

func isNotFoundError(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "not found")
}

func fetchTestMetadata(ctx context.Context, config config.Config, client *circleci.Client, job *circleci.WorkflowJob) ([]*circleci.TestMetadata, error) {
	md, err := client.Jobs.ListTestMetadata(ctx, job.ProjectSlug, fmt.Sprintf("%d", job.JobNumber))
	if err != nil {
		// Some jobs don't have test metadata in certain states (e.g., running, queued, not_run, on_hold)
		// CircleCI API returns "not found" for these cases, which is expected and not an error
		if isNotFoundError(err) {
			slog.Debug(
				"job has no test metadata ",
				"job", job.ID,
				"jobName", job.Name,
				"jobNumber", job.JobNumber,
				"projectSlug", job.ProjectSlug,
			)
			return []*circleci.TestMetadata{}, nil
		}
		return nil, fmt.Errorf("failed to list test metadata: %w", err)
	}

	var out []*circleci.TestMetadata
	for _, m := range md.Items {
		m.Result = mapFlakyStatus(m.Result, m.Message)

		if m.Result == "success" && config.SlowTestThresholdSeconds > m.RunTime {
			continue
		}

		out = append(out, m)
	}
	slog.Debug("fetched test metadata", "count", len(out), "job", job.ID)
	return out, nil
}
