package service

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/axelKingsley/go-circleci"
	"github.com/ethereum-optimism/infra/cci-stats/pkg/config"
	"github.com/ethereum-optimism/infra/cci-stats/pkg/db"
)

// fakeProjects, fakePipelines, fakeWorkflows and fakeJobs embed the client's
// interfaces so only the calls under test need implementing.

type fakeProjects struct {
	circleci.Projects
	pipelines []*circleci.Pipeline
}

func (f *fakeProjects) ListPipelines(_ context.Context, _ string, _ circleci.ProjectListPipelinesOptions) (*circleci.PipelineList, error) {
	return &circleci.PipelineList{Items: f.pipelines}, nil
}

type fakePipelines struct {
	circleci.Pipelines
	mtx sync.Mutex
	// states, workflows and the error maps are keyed by pipeline ID.
	states    map[string]string
	workflows map[string][]*circleci.Workflow
	getErr    map[string]error
	listErr   map[string]error
	getCalls  int
	// pageSize splits ListWorkflows across pages when positive.
	pageSize    int
	pagesServed int
}

func (f *fakePipelines) Get(_ context.Context, id string) (*circleci.Pipeline, error) {
	f.mtx.Lock()
	defer f.mtx.Unlock()
	f.getCalls++
	if err := f.getErr[id]; err != nil {
		return nil, err
	}
	return &circleci.Pipeline{State: f.states[id]}, nil
}

func (f *fakePipelines) ListWorkflows(_ context.Context, id string, opts circleci.PipelineListWorkflowsOptions) (*circleci.WorkflowList, error) {
	f.mtx.Lock()
	defer f.mtx.Unlock()
	if err := f.listErr[id]; err != nil {
		return nil, err
	}
	f.pagesServed++

	all := f.workflows[id]
	if f.pageSize <= 0 {
		return &circleci.WorkflowList{Items: all}, nil
	}

	offset := 0
	if opts.PageToken != nil {
		var err error
		if offset, err = strconv.Atoi(*opts.PageToken); err != nil {
			return nil, err
		}
	}
	end := min(offset+f.pageSize, len(all))
	page := &circleci.WorkflowList{Items: all[offset:end]}
	if end < len(all) {
		page.NextPageToken = strconv.Itoa(end)
	}
	return page, nil
}

type fakeWorkflows struct {
	circleci.Workflows
	jobs map[string][]*circleci.WorkflowJob
}

func (f *fakeWorkflows) ListWorkflowJobs(_ context.Context, id string) (*circleci.WorkflowJobList, error) {
	return &circleci.WorkflowJobList{Items: f.jobs[id]}, nil
}

type fakeJobs struct {
	circleci.Jobs
	mtx sync.Mutex
	// metadata is keyed by job number, so a job's results can be changed
	// between runs independently of the others.
	metadata map[string][]*circleci.TestMetadata
}

func (f *fakeJobs) ListTestMetadata(_ context.Context, _ string, jobNumber string) (*circleci.TestMetadataList, error) {
	f.mtx.Lock()
	defer f.mtx.Unlock()
	return &circleci.TestMetadataList{Items: f.metadata[jobNumber]}, nil
}

// fakeDB is an in-memory stand-in for the Postgres connection.
type fakeDB struct {
	mtx          sync.Mutex
	pipelines    map[string]db.Pipeline
	order        []string
	jobs         map[string]db.Job
	testResults  map[string][]db.TestResult
	replaceCalls map[string]int
}

func newFakeDB() *fakeDB {
	return &fakeDB{
		pipelines:    map[string]db.Pipeline{},
		jobs:         map[string]db.Job{},
		testResults:  map[string][]db.TestResult{},
		replaceCalls: map[string]int{},
	}
}

func (f *fakeDB) LastPipeline(context.Context) (*db.Pipeline, error) {
	f.mtx.Lock()
	defer f.mtx.Unlock()
	var last *db.Pipeline
	for _, p := range f.pipelines {
		if last == nil || p.CreatedAt.After(last.CreatedAt) {
			cp := p
			last = &cp
		}
	}
	return last, nil
}

func (f *fakeDB) IncompletePipelines(_ context.Context, since time.Time) ([]db.Pipeline, error) {
	f.mtx.Lock()
	defer f.mtx.Unlock()
	var res []db.Pipeline
	for _, id := range f.order {
		p := f.pipelines[id]
		if !p.Complete && !p.CreatedAt.Before(since) {
			res = append(res, p)
		}
	}
	return res, nil
}

func (f *fakeDB) RecordPending(_ context.Context, pipelines []db.Pipeline) error {
	f.mtx.Lock()
	defer f.mtx.Unlock()
	for _, p := range pipelines {
		if _, ok := f.pipelines[p.ID]; ok {
			continue
		}
		p.Complete = false
		f.pipelines[p.ID] = p
		f.order = append(f.order, p.ID)
	}
	return nil
}

func (f *fakeDB) Begin() (db.Transactor, error) { return &fakeTx{db: f}, nil }
func (f *fakeDB) Close() error                  { return nil }

type fakeTx struct {
	db *fakeDB
}

func (t *fakeTx) InsertPipeline(_ context.Context, p db.Pipeline) error {
	t.db.mtx.Lock()
	defer t.db.mtx.Unlock()
	if _, ok := t.db.pipelines[p.ID]; !ok {
		t.db.order = append(t.db.order, p.ID)
	}
	t.db.pipelines[p.ID] = p
	return nil
}

func (t *fakeTx) InsertJob(_ context.Context, j db.Job) error {
	t.db.mtx.Lock()
	defer t.db.mtx.Unlock()
	t.db.jobs[j.ID] = j
	return nil
}

// ReplaceTestResults mirrors the real implementation, which refuses to clear a
// job's rows when handed nothing to put back.
func (t *fakeTx) ReplaceTestResults(_ context.Context, jobID string, results []db.TestResult) error {
	t.db.mtx.Lock()
	defer t.db.mtx.Unlock()
	t.db.replaceCalls[jobID]++
	if len(results) == 0 {
		return errors.New("refusing to replace test results with none")
	}
	t.db.testResults[jobID] = results
	return nil
}

func (t *fakeTx) Commit(context.Context) error { return nil }
func (t *fakeTx) Rollback(context.Context)     {}

func testConfig() config.Config { return testConfigMatching("^main$") }

func testConfigMatching(workflowPattern string) config.Config {
	return config.Config{
		ProjectSlug:              "gh/acme/repo",
		BranchPatternRegex:       regexp.MustCompile(".*"),
		WorkflowPatternRegex:     regexp.MustCompile(workflowPattern),
		FetchLimitDays:           5,
		MaxConcurrentFetchJobs:   1,
		SlowTestThresholdSeconds: 0,
	}
}

func testPipeline(id string, number int64) *circleci.Pipeline {
	return &circleci.Pipeline{
		ID:        id,
		Number:    number,
		State:     "created",
		CreatedAt: time.Now().UTC().Add(-time.Hour),
		Vcs:       &circleci.VCS{Revision: "abc123", Branch: "develop"},
	}
}

// TestGenerateReport_RetriesUnfinishedPipeline covers the cycle this package
// exists for: a pipeline seen while a workflow was still running is recorded as
// incomplete, picked up again on the next run, and only then marked complete.
func TestGenerateReport_RetriesUnfinishedPipeline(t *testing.T) {
	pipeline := testPipeline("pipeline-1", 42)

	pipelinesAPI := &fakePipelines{
		states: map[string]string{"pipeline-1": "created"},
		workflows: map[string][]*circleci.Workflow{"pipeline-1": {
			{ID: "wf-setup", Name: "setup", Status: "success"},
			{ID: "wf-main", Name: "main", Status: "running"},
		}},
	}
	workflowsAPI := &fakeWorkflows{jobs: map[string][]*circleci.WorkflowJob{
		"wf-main": {{ID: "job-1", JobNumber: 7, Name: "build", Status: "success"}},
	}}
	client := &circleci.Client{
		Projects:  &fakeProjects{pipelines: []*circleci.Pipeline{pipeline}},
		Pipelines: pipelinesAPI,
		Workflows: workflowsAPI,
		Jobs:      &fakeJobs{metadata: map[string][]*circleci.TestMetadata{"7": {{Name: "TestFoo", Result: "failed", RunTime: 1.5}}}},
	}
	conn := newFakeDB()

	// Run one: main is still running, so nothing of it is recorded yet.
	if err := GenerateReport(context.Background(), testConfig(), client, conn); err != nil {
		t.Fatalf("first run: %v", err)
	}
	got, ok := conn.pipelines["pipeline-1"]
	if !ok {
		t.Fatal("first run did not record the pipeline, so it can never be retried")
	}
	if got.Complete {
		t.Error("pipeline marked complete while a matched workflow was still running")
	}
	if len(conn.jobs) != 0 {
		t.Errorf("recorded %d jobs for an unfinished workflow, want 0", len(conn.jobs))
	}
	if got.Commit != "abc123" || got.Branch != "develop" || got.Number != 42 {
		t.Errorf("pipeline recorded with wrong details: %+v", got)
	}

	// Run two: main has finished. The pipeline is no longer newly listed, so it
	// can only be reached through the retry path.
	client.Projects = &fakeProjects{}
	pipelinesAPI.workflows["pipeline-1"] = []*circleci.Workflow{
		{ID: "wf-setup", Name: "setup", Status: "success"},
		{ID: "wf-main", Name: "main", Status: "success"},
	}

	if err := GenerateReport(context.Background(), testConfig(), client, conn); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if pipelinesAPI.getCalls == 0 {
		t.Error("retried pipeline did not have its state refreshed")
	}
	got = conn.pipelines["pipeline-1"]
	if !got.Complete {
		t.Error("pipeline still incomplete after every matched workflow finished")
	}
	if len(conn.jobs) != 1 {
		t.Fatalf("recorded %d jobs, want 1", len(conn.jobs))
	}
	if results := conn.testResults["wf-main/job-1"]; len(results) != 1 || results[0].Name != "TestFoo" {
		t.Errorf("test results = %+v, want one TestFoo row", results)
	}
}

// TestGenerateReport_KeepsTestResultsWhenMetadataMissing covers the wipe that
// reindexing would otherwise cause: a job's stored results must survive a later
// run whose metadata fetch comes back empty, because a spurious not found is
// indistinguishable from a job that genuinely has no tests.
func TestGenerateReport_KeepsTestResultsWhenMetadataMissing(t *testing.T) {
	pipeline := testPipeline("pipeline-1", 42)
	pipelinesAPI := &fakePipelines{
		states: map[string]string{"pipeline-1": "created"},
		workflows: map[string][]*circleci.Workflow{"pipeline-1": {
			{ID: "wf-main", Name: "main", Status: "success"},
			{ID: "wf-extra", Name: "extra", Status: "running"},
		}},
	}
	jobsAPI := &fakeJobs{metadata: map[string][]*circleci.TestMetadata{
		"7": {{Name: "TestFoo", Result: "failed", RunTime: 1.5}},
	}}
	client := &circleci.Client{
		Projects:  &fakeProjects{pipelines: []*circleci.Pipeline{pipeline}},
		Pipelines: pipelinesAPI,
		Workflows: &fakeWorkflows{jobs: map[string][]*circleci.WorkflowJob{
			"wf-main": {{ID: "job-1", JobNumber: 7, Name: "build", Status: "success"}},
		}},
		Jobs: jobsAPI,
	}
	conn := newFakeDB()
	cfg := testConfigMatching("^(main|extra)$")

	// Run one records the job and its results; extra is still running, so the
	// pipeline stays on the retry list.
	if err := GenerateReport(context.Background(), cfg, client, conn); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if len(conn.testResults["wf-main/job-1"]) != 1 {
		t.Fatalf("first run stored %d test results, want 1", len(conn.testResults["wf-main/job-1"]))
	}

	// Run two reindexes the same job, but its metadata now comes back empty.
	client.Projects = &fakeProjects{}
	jobsAPI.metadata["7"] = nil
	pipelinesAPI.workflows["pipeline-1"] = []*circleci.Workflow{
		{ID: "wf-main", Name: "main", Status: "success"},
		{ID: "wf-extra", Name: "extra", Status: "success"},
	}

	before := conn.replaceCalls["wf-main/job-1"]
	if err := GenerateReport(context.Background(), cfg, client, conn); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if got := conn.testResults["wf-main/job-1"]; len(got) != 1 || got[0].Name != "TestFoo" {
		t.Errorf("reindexing wiped stored test results: %+v", got)
	}
	// The storage layer also refuses an empty replace, so assert the caller did
	// not even attempt one. Otherwise this passes on the lower guard alone.
	if got := conn.replaceCalls["wf-main/job-1"] - before; got != 0 {
		t.Errorf("attempted %d replacements with no metadata, want 0", got)
	}
}

// TestGenerateReport_SkipsFailedRetry covers a retried pipeline that cannot be
// fetched. It must neither fail the run nor stop other pipelines being indexed,
// or a single stuck row would block everything until it ages out.
func TestGenerateReport_SkipsFailedRetry(t *testing.T) {
	conn := newFakeDB()
	conn.pipelines["stuck"] = db.Pipeline{ID: "stuck", CreatedAt: time.Now().UTC().Add(-2 * time.Hour)}
	conn.order = []string{"stuck"}

	healthy := testPipeline("healthy", 1)
	client := &circleci.Client{
		Projects: &fakeProjects{pipelines: []*circleci.Pipeline{healthy}},
		Pipelines: &fakePipelines{
			states:    map[string]string{"healthy": "created"},
			workflows: map[string][]*circleci.Workflow{"healthy": {{ID: "wf-main", Name: "main", Status: "success"}}},
			getErr:    map[string]error{"stuck": errors.New("404 not found")},
		},
		Workflows: &fakeWorkflows{jobs: map[string][]*circleci.WorkflowJob{
			"wf-main": {{ID: "job-1", JobNumber: 7, Name: "build", Status: "success"}},
		}},
		Jobs: &fakeJobs{},
	}

	if err := GenerateReport(context.Background(), testConfig(), client, conn); err != nil {
		t.Fatalf("a retried pipeline that cannot be fetched failed the whole run: %v", err)
	}
	if got := conn.pipelines["healthy"]; !got.Complete {
		t.Error("a stuck retry stopped a healthy pipeline being indexed")
	}
	if len(conn.jobs) != 1 {
		t.Errorf("recorded %d jobs for the healthy pipeline, want 1", len(conn.jobs))
	}
}

// TestGenerateReport_ClaimsPipelinesBeforeIndexing covers what RecordPending is
// for: a run that stops part way must leave the pipelines it listed on the retry
// list, not behind the high water mark.
func TestGenerateReport_ClaimsPipelinesBeforeIndexing(t *testing.T) {
	// The failing pipeline is listed first, so with one worker the second is
	// never processed.
	bad, good := testPipeline("bad", 1), testPipeline("good", 2)
	client := &circleci.Client{
		Projects: &fakeProjects{pipelines: []*circleci.Pipeline{bad, good}},
		Pipelines: &fakePipelines{
			states:    map[string]string{"bad": "created", "good": "created"},
			workflows: map[string][]*circleci.Workflow{"good": {{ID: "wf-main", Name: "main", Status: "success"}}},
			listErr:   map[string]error{"bad": errors.New("boom")},
		},
		Workflows: &fakeWorkflows{},
		Jobs:      &fakeJobs{},
	}
	conn := newFakeDB()

	if err := GenerateReport(context.Background(), testConfig(), client, conn); err == nil {
		t.Fatal("expected the run to fail on the listed pipeline that could not be indexed")
	}

	// Every listed pipeline must have a row. Without the up-front claim, one the
	// run never got to has none at all, yet the mark has already moved past it.
	// Whether the pool got to "good" before cancelling is not deterministic, so
	// it may legitimately be either complete or still on the retry list.
	for _, id := range []string{"bad", "good"} {
		if _, ok := conn.pipelines[id]; !ok {
			t.Errorf("pipeline %q was listed but has no row, so it is behind the mark and lost", id)
		}
	}
	if conn.pipelines["bad"].Complete {
		t.Error("a pipeline that failed to index was marked complete")
	}

	retryable, err := conn.IncompletePipelines(context.Background(), time.Now().UTC().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("IncompletePipelines: %v", err)
	}
	var found bool
	for _, p := range retryable {
		found = found || p.ID == "bad"
	}
	if !found {
		t.Error("a pipeline that failed to index is not on the retry list")
	}
}

// TestGenerateReport_SkipsFailedRetryThatIsAlsoListed covers the pipeline
// holding the high water mark. The cutoff is inclusive, so it is listed again
// every run while also being on the retry list, and it is the pipeline most
// likely to still be running. Failing to index it must not fail the run.
func TestGenerateReport_SkipsFailedRetryThatIsAlsoListed(t *testing.T) {
	stuck := testPipeline("stuck", 1)
	conn := newFakeDB()
	conn.pipelines["stuck"] = db.Pipeline{ID: "stuck", Number: 1, CreatedAt: stuck.CreatedAt}
	conn.order = []string{"stuck"}

	client := &circleci.Client{
		// Still listed, because the cutoff is its own created_at.
		Projects: &fakeProjects{pipelines: []*circleci.Pipeline{stuck}},
		Pipelines: &fakePipelines{
			states:  map[string]string{"stuck": "created"},
			listErr: map[string]error{"stuck": errors.New("boom")},
		},
		Workflows: &fakeWorkflows{},
		Jobs:      &fakeJobs{},
	}

	if err := GenerateReport(context.Background(), testConfig(), client, conn); err != nil {
		t.Fatalf("a pipeline already on the retry list failed the whole run: %v", err)
	}
	if conn.pipelines["stuck"].Complete {
		t.Error("a pipeline that failed to index was marked complete")
	}
}

// TestGenerateReport_DoesNotClaimBeyondTheHorizon covers an indexer that has
// been down for longer than FETCH_LIMIT_DAYS. The mark is then older than the
// retry window, and anything claimed past the window could never be reindexed.
func TestGenerateReport_DoesNotClaimBeyondTheHorizon(t *testing.T) {
	conn := newFakeDB()
	// FetchLimitDays is 5, so the mark sits well behind the horizon.
	conn.pipelines["old"] = db.Pipeline{
		ID:        "old",
		CreatedAt: time.Now().UTC().Add(-10 * 24 * time.Hour),
		Complete:  true,
	}
	conn.order = []string{"old"}

	stale := testPipeline("stale", 2)
	stale.CreatedAt = time.Now().UTC().Add(-7 * 24 * time.Hour)
	client := &circleci.Client{
		Projects:  &fakeProjects{pipelines: []*circleci.Pipeline{stale}},
		Pipelines: &fakePipelines{states: map[string]string{"stale": "created"}},
		Workflows: &fakeWorkflows{},
		Jobs:      &fakeJobs{},
	}

	if err := GenerateReport(context.Background(), testConfig(), client, conn); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, ok := conn.pipelines["stale"]; ok {
		t.Error("claimed a pipeline older than the retry window, so it can never be reindexed")
	}
}

// TestGenerateReport_PaginatesWorkflows covers the workflow list being split
// across pages. The count feeds the permanence decision, so a truncated list
// could mark a pipeline complete on a partial set.
func TestGenerateReport_PaginatesWorkflows(t *testing.T) {
	pipeline := testPipeline("pipeline-1", 42)
	pipelinesAPI := &fakePipelines{
		pageSize: 1,
		states:   map[string]string{"pipeline-1": "created"},
		workflows: map[string][]*circleci.Workflow{"pipeline-1": {
			{ID: "wf-setup", Name: "setup", Status: "success"},
			{ID: "wf-main", Name: "main", Status: "running"},
		}},
	}
	client := &circleci.Client{
		Projects:  &fakeProjects{pipelines: []*circleci.Pipeline{pipeline}},
		Pipelines: pipelinesAPI,
		Workflows: &fakeWorkflows{},
		Jobs:      &fakeJobs{},
	}
	conn := newFakeDB()

	if err := GenerateReport(context.Background(), testConfig(), client, conn); err != nil {
		t.Fatalf("run: %v", err)
	}
	if pipelinesAPI.pagesServed != 2 {
		t.Errorf("served %d workflow pages, want 2", pipelinesAPI.pagesServed)
	}
	// main is on the second page and still running, so a truncated list would
	// have marked this complete.
	if conn.pipelines["pipeline-1"].Complete {
		t.Error("pipeline marked complete from a truncated workflow list")
	}
}

// TestGenerateReport_PipelineWithNoMatchingWorkflows records the pipeline so the
// high water mark can pass it, and does not leave it on the retry list.
func TestGenerateReport_PipelineWithNoMatchingWorkflows(t *testing.T) {
	pipeline := testPipeline("tag-pipeline", 9)
	client := &circleci.Client{
		Projects: &fakeProjects{pipelines: []*circleci.Pipeline{pipeline}},
		Pipelines: &fakePipelines{
			states:    map[string]string{"tag-pipeline": "created"},
			workflows: map[string][]*circleci.Workflow{"tag-pipeline": {{ID: "wf-rel", Name: "release", Status: "success"}}},
		},
		Workflows: &fakeWorkflows{},
		Jobs:      &fakeJobs{},
	}
	conn := newFakeDB()

	if err := GenerateReport(context.Background(), testConfig(), client, conn); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := conn.pipelines["tag-pipeline"]
	if !got.Complete {
		t.Error("pipeline whose workflows cannot match was left on the retry list")
	}
	if len(conn.jobs) != 0 {
		t.Errorf("recorded %d jobs, want 0", len(conn.jobs))
	}
}
