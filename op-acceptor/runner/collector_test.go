package runner

import (
	"testing"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewResultCollector(t *testing.T) {
	collector := NewResultCollector()
	assert.NotNil(t, collector, "NewResultCollector should return non-nil collector")
}

func TestResultCollector_NewRunResult(t *testing.T) {
	collector := NewResultCollector()

	runID := "test-run-123"
	isParallel := true

	result := collector.NewRunResult(runID, isParallel)

	require.NotNil(t, result, "NewRunResult should return non-nil result")
	assert.Equal(t, runID, result.RunID, "RunID should match")
	assert.Equal(t, isParallel, result.IsParallel, "IsParallel should match")
	assert.Equal(t, types.TestStatusPass, result.Status, "Initial status should be pass")
	assert.NotNil(t, result.Gates, "Gates map should be initialized")
	assert.Empty(t, result.Gates, "Gates map should be empty initially")
	assert.False(t, result.Stats.StartTime.IsZero(), "StartTime should be set")
}

func TestResultCollector_AddTestResult(t *testing.T) {
	collector := NewResultCollector()
	result := collector.NewRunResult("test-run", false)

	testResult := &types.TestResult{
		Metadata: types.ValidatorMetadata{
			FuncName: "TestExample",
			Package:  "example/pkg",
		},
		Status:   types.TestStatusPass,
		Duration: 100 * time.Millisecond,
	}

	tests := []struct {
		name       string
		gateName   string
		suiteName  string
		wantGates  int
		wantSuites int
	}{
		{
			name:       "add to new gate without suite",
			gateName:   "gate1",
			suiteName:  "",
			wantGates:  1,
			wantSuites: 0,
		},
		{
			name:       "add to new gate with suite",
			gateName:   "gate2",
			suiteName:  "suite1",
			wantGates:  2,
			wantSuites: 1,
		},
		{
			name:       "add to existing gate with new suite",
			gateName:   "gate2",
			suiteName:  "suite2",
			wantGates:  2,
			wantSuites: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			collector.AddTestResult(result, testResult, tt.gateName, tt.suiteName)

			assert.Len(t, result.Gates, tt.wantGates, "Should have expected number of gates")

			gate, exists := result.Gates[tt.gateName]
			require.True(t, exists, "Gate should exist")

			if tt.suiteName != "" {
				assert.Len(t, gate.Suites, tt.wantSuites, "Should have expected number of suites in gate")
				suite, suiteExists := gate.Suites[tt.suiteName]
				require.True(t, suiteExists, "Suite should exist")
				assert.Len(t, suite.Tests, 1, "Suite should have one test")
			} else {
				assert.Len(t, gate.Tests, 1, "Gate should have one direct test")
			}
		})
	}
}

func TestResultCollector_AddTestResult_Validation(t *testing.T) {
	collector := NewResultCollector()

	tests := []struct {
		name        string
		result      *RunnerResult
		test        *types.TestResult
		shouldPanic bool
	}{
		{
			name:        "nil result should panic",
			result:      nil,
			test:        &types.TestResult{},
			shouldPanic: true,
		},
		{
			name:        "nil test should panic",
			result:      &RunnerResult{Gates: make(map[string]*GateResult)},
			test:        nil,
			shouldPanic: true,
		},
		{
			name:        "valid inputs should not panic",
			result:      &RunnerResult{Gates: make(map[string]*GateResult)},
			test:        &types.TestResult{Metadata: types.ValidatorMetadata{Package: "test"}},
			shouldPanic: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.shouldPanic {
				assert.Panics(t, func() {
					collector.AddTestResult(tt.result, tt.test, "gate", "")
				})
			} else {
				assert.NotPanics(t, func() {
					collector.AddTestResult(tt.result, tt.test, "gate", "")
				})
			}
		})
	}
}

func TestResultCollector_UpdateStats(t *testing.T) {
	collector := NewResultCollector()
	result := collector.NewRunResult("test-run", false)

	// Create gate and suite
	gate := &GateResult{
		ID:     "gate1",
		Tests:  make(map[string]*types.TestResult),
		Suites: make(map[string]*SuiteResult),
		Stats:  ResultStats{},
	}
	result.Gates["gate1"] = gate

	suite := &SuiteResult{
		ID:    "suite1",
		Tests: make(map[string]*types.TestResult),
		Stats: ResultStats{},
	}
	gate.Suites["suite1"] = suite

	testResult := &types.TestResult{
		Status:   types.TestStatusPass,
		Duration: 200 * time.Millisecond,
	}

	// Update stats
	collector.UpdateStats(result, gate, suite, testResult)

	// Verify stats were updated at all levels
	assert.Equal(t, 1, suite.Stats.Total, "Suite should have 1 total test")
	assert.Equal(t, 1, suite.Stats.Passed, "Suite should have 1 passed test")
	assert.Equal(t, 1, gate.Stats.Total, "Gate should have 1 total test")
	assert.Equal(t, 1, gate.Stats.Passed, "Gate should have 1 passed test")
	assert.Equal(t, 1, result.Stats.Total, "Result should have 1 total test")
	assert.Equal(t, 1, result.Stats.Passed, "Result should have 1 passed test")

	// Verify durations
	assert.Equal(t, 200*time.Millisecond, suite.Duration, "Suite duration should match test duration")
	assert.Equal(t, 200*time.Millisecond, gate.Duration, "Gate duration should match test duration")
	assert.Equal(t, 200*time.Millisecond, result.Duration, "Result duration should match test duration")
}

func TestResultCollector_DetermineStatusFromFlags(t *testing.T) {
	collector := &resultCollector{}

	tests := []struct {
		name       string
		allSkipped bool
		anyFailed  bool
		want       types.TestStatus
	}{
		{
			name:       "all passed",
			allSkipped: false,
			anyFailed:  false,
			want:       types.TestStatusPass,
		},
		{
			name:       "some failed",
			allSkipped: false,
			anyFailed:  true,
			want:       types.TestStatusFail,
		},
		{
			name:       "all skipped",
			allSkipped: true,
			anyFailed:  false,
			want:       types.TestStatusSkip,
		},
		{
			name:       "failed takes priority over skipped",
			allSkipped: true,
			anyFailed:  true,
			want:       types.TestStatusFail,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collector.determineStatusFromFlags(tt.allSkipped, tt.anyFailed)
			assert.Equal(t, tt.want, got, "Status should match expected")
		})
	}
}

func TestResultCollector_GetTestKey(t *testing.T) {
	collector := &resultCollector{}

	tests := []struct {
		name     string
		metadata types.ValidatorMetadata
		want     string
	}{
		{
			name: "with function name",
			metadata: types.ValidatorMetadata{
				FuncName: "TestExample",
				Package:  "example/pkg",
			},
			want: "example/pkg::TestExample",
		},
		{
			name: "without function name",
			metadata: types.ValidatorMetadata{
				Package: "example/pkg",
			},
			want: "example/pkg",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collector.getTestKey(tt.metadata)
			assert.Equal(t, tt.want, got, "Test key should match expected format")
		})
	}
}
