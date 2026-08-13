package service

import (
	"errors"
	"slices"
	"testing"

	"github.com/axelKingsley/go-circleci"
	"github.com/ethereum-optimism/infra/cci-stats/pkg/db"
)

func TestIsNotFoundError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "not found - lowercase",
			err:      errors.New("not found"),
			expected: true,
		},
		{
			name:     "not found - mixed case",
			err:      errors.New("Not Found"),
			expected: true,
		},
		{
			name:     "not found - uppercase",
			err:      errors.New("NOT FOUND"),
			expected: true,
		},
		{
			name:     "not found - with prefix",
			err:      errors.New("failed to list test metadata: not found"),
			expected: true,
		},
		{
			name:     "not found - with suffix",
			err:      errors.New("resource not found"),
			expected: true,
		},
		{
			name:     "other error - rate limit",
			err:      errors.New("rate limit exceeded"),
			expected: false,
		},
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isNotFoundError(tt.err)
			if got != tt.expected {
				t.Errorf("isNotFoundError() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestFlakyStatusMapping(t *testing.T) {
	tests := []struct {
		name     string
		result   string
		message  string
		expected string
	}{
		{
			name:     "flaky fail becomes flaky_fail",
			result:   "skipped",
			message:  "FLAKY_FAIL: test-reason: assertion failed",
			expected: "flaky_fail",
		},
		{
			name:     "flaky pass becomes flaky_pass",
			result:   "skipped",
			message:  "FLAKY_PASS: test-reason",
			expected: "flaky_pass",
		},
		{
			name:     "flaky pass with log prefix",
			result:   "skipped",
			message:  "=== RUN TestFoo\ntestlog.go:151: writing test log\ntesting.go:259: FLAKY_PASS: tracked as flaky\n--- SKIP: TestFoo (0.00s)",
			expected: "flaky_pass",
		},
		{
			name:     "flaky pass without reason",
			result:   "skipped",
			message:  "=== RUN TestFoo\ntestlog.go:151: writing test log\ntesting.go:259: FLAKY_PASS\n--- SKIP: TestFoo (0.00s)",
			expected: "flaky_pass",
		},
		{
			name:     "flaky fail with log prefix",
			result:   "skipped",
			message:  "=== RUN TestFoo\ntestlog.go:151: writing test log\ntesting.go:259: FLAKY_FAIL: assertion failed\n--- SKIP: TestFoo (0.00s)",
			expected: "flaky_fail",
		},
		{
			name:     "regular skip stays skipped",
			result:   "skipped",
			message:  "precondition not met",
			expected: "skipped",
		},
		{
			name:     "empty skip stays skipped",
			result:   "skipped",
			message:  "",
			expected: "skipped",
		},
		{
			name:     "regular failure unchanged",
			result:   "failed",
			message:  "test failed",
			expected: "failed",
		},
		{
			name:     "success unchanged",
			result:   "success",
			message:  "",
			expected: "success",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapFlakyStatus(tt.result, tt.message)
			if got != tt.expected {
				t.Errorf("mapFlakyStatus(%q, %q) = %q, want %q", tt.result, tt.message, got, tt.expected)
			}
		})
	}
}

func TestIsTerminalStatus(t *testing.T) {
	tests := []struct {
		status   string
		expected bool
	}{
		{"success", true},
		{"failed", true},
		{"error", true},
		{"canceled", true},
		{"unauthorized", true},
		{"not_run", true},
		{"running", false},
		{"failing", false},
		{"on_hold", false},
		{"something_new", false},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			if got := isTerminalStatus(tt.status); got != tt.expected {
				t.Errorf("isTerminalStatus(%q) = %v, want %v", tt.status, got, tt.expected)
			}
		})
	}
}

func TestPipelineComplete(t *testing.T) {
	tests := []struct {
		name     string
		state    string
		total    int
		statuses []string
		expected bool
	}{
		{
			name:     "all matched workflows terminal",
			state:    "created",
			total:    2,
			statuses: []string{"success", "failed"},
			expected: true,
		},
		{
			name:     "terminal but not successful still completes",
			state:    "created",
			total:    3,
			statuses: []string{"canceled", "not_run", "unauthorized"},
			expected: true,
		},
		{
			name:     "one workflow still running",
			state:    "created",
			total:    2,
			statuses: []string{"success", "running"},
			expected: false,
		},
		{
			name:     "awaiting approval",
			state:    "created",
			total:    1,
			statuses: []string{"on_hold"},
			expected: false,
		},
		{
			name:     "failing is not terminal",
			state:    "created",
			total:    1,
			statuses: []string{"failing"},
			expected: false,
		},
		{
			// The workflow list is final, so a pipeline whose workflows match
			// nothing is done with. Retrying it every run would be pure waste.
			name:     "no matched workflows but list is final",
			state:    "created",
			total:    3,
			statuses: nil,
			expected: true,
		},
		{
			// The window the state gate exists for.
			name:     "setup succeeded but continuation has not landed",
			state:    "pending",
			total:    1,
			statuses: nil,
			expected: false,
		},
		{
			name:     "setup workflow still running",
			state:    "setup",
			total:    1,
			statuses: nil,
			expected: false,
		},
		{
			name:     "setup not started",
			state:    "setup-pending",
			total:    0,
			statuses: nil,
			expected: false,
		},
		{
			name:     "created with no workflows yet",
			state:    "created",
			total:    0,
			statuses: nil,
			expected: false,
		},
		{
			// Terminal, so it needs no workflow guard.
			name:     "errored before any workflow existed",
			state:    "errored",
			total:    0,
			statuses: nil,
			expected: true,
		},
		{
			name:     "errored after setup failed",
			state:    "errored",
			total:    1,
			statuses: nil,
			expected: true,
		},
		{
			name:     "errored with a matched workflow still running",
			state:    "errored",
			total:    2,
			statuses: []string{"running"},
			expected: false,
		},
		{
			name:     "unrecognised status is treated as in progress",
			state:    "created",
			total:    1,
			statuses: []string{""},
			expected: false,
		},
		{
			name:     "unrecognised state is treated as in progress",
			state:    "some-new-state",
			total:    2,
			statuses: []string{"success"},
			expected: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pipelineComplete(tt.state, tt.total, tt.statuses); got != tt.expected {
				t.Errorf("pipelineComplete(%q, %d, %v) = %v, want %v", tt.state, tt.total, tt.statuses, got, tt.expected)
			}
		})
	}
}

func TestMergePipelines(t *testing.T) {
	fetched := []pipelineWork{
		{pipeline: db.Pipeline{ID: "a"}, state: "created"},
		{pipeline: db.Pipeline{ID: "b"}, state: "created"},
	}
	retries := []pipelineWork{
		{pipeline: db.Pipeline{ID: "b"}},
		{pipeline: db.Pipeline{ID: "c"}},
	}

	got := mergePipelines(fetched, retries)

	var ids []string
	for _, p := range got {
		ids = append(ids, p.pipeline.ID)
	}
	want := []string{"a", "b", "c"}
	if !slices.Equal(ids, want) {
		t.Errorf("mergePipelines() ids = %v, want %v", ids, want)
	}
	// The fetched copy wins, so the duplicate keeps its already-current state
	// instead of forcing a redundant refetch.
	if got[1].state != "created" {
		t.Errorf("merged duplicate state = %q, want %q", got[1].state, "created")
	}
	// It must still count as a retry, or the pipeline holding the high water
	// mark loses the protection every run, since it is always listed again.
	if !got[1].retry {
		t.Error("a pipeline on the retry list lost its retry flag by also being listed")
	}
	if got[0].retry {
		t.Error("a pipeline only this run has listed was marked as a retry")
	}
}

func TestWorkflowStatus(t *testing.T) {
	if got := workflowStatus(&circleci.Workflow{Status: "success"}); got != "success" {
		t.Errorf("workflowStatus() = %q, want %q", got, "success")
	}
	// The client models status as an untyped field, so a non-string must not
	// panic and must not be mistaken for a terminal state.
	if got := workflowStatus(&circleci.Workflow{Status: 42}); got != "" {
		t.Errorf("workflowStatus() = %q, want empty", got)
	}
}
