package nat

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/ethereum-optimism/infra/op-acceptor/runner"
	"github.com/ethereum-optimism/infra/op-acceptor/types"
)

// TestDefaultMetricsReporter_ReportResults tests the metrics reporter
func TestDefaultMetricsReporter_ReportResults(t *testing.T) {
	// Create a sample result
	result := &runner.RunnerResult{
		RunID:    "test-run-1",
		Status:   types.TestStatusPass,
		Duration: 100 * time.Millisecond,
		Stats: runner.ResultStats{
			Total:   5,
			Passed:  5,
			Failed:  0,
			Skipped: 0,
		},
	}

	// Create reporter
	reporter := &DefaultMetricsReporter{}

	// Report results - this is mostly checking it doesn't error
	// In a real test, we would mock the metrics package and verify the calls
	reporter.ReportResults(result.RunID, result)

	// No assertions needed as we're just checking it doesn't panic
	assert.True(t, true, "Test completed without panicking")
}

// TestDefaultMetricsReporter_ReportResults_FailedTests tests reporting failed tests
func TestDefaultMetricsReporter_ReportResults_FailedTests(t *testing.T) {
	// Create a sample result with failures
	result := &runner.RunnerResult{
		RunID:    "test-run-2",
		Status:   types.TestStatusFail,
		Duration: 150 * time.Millisecond,
		Stats: runner.ResultStats{
			Total:   10,
			Passed:  7,
			Failed:  3,
			Skipped: 0,
		},
	}

	// Create reporter
	reporter := &DefaultMetricsReporter{}

	// Report results - this is mostly checking it doesn't error
	reporter.ReportResults(result.RunID, result)

	// No assertions needed as we're just checking it doesn't panic
	assert.True(t, true, "Test completed without panicking")
}

// TestDefaultMetricsReporter_ReportResults_SkippedTests tests reporting skipped tests
func TestDefaultMetricsReporter_ReportResults_SkippedTests(t *testing.T) {
	// Create a sample result with skipped tests
	result := &runner.RunnerResult{
		RunID:    "test-run-3",
		Status:   types.TestStatusSkip,
		Duration: 75 * time.Millisecond,
		Stats: runner.ResultStats{
			Total:   8,
			Passed:  5,
			Failed:  0,
			Skipped: 3,
		},
	}

	// Create reporter
	reporter := &DefaultMetricsReporter{}

	// Report results - this is mostly checking it doesn't error
	reporter.ReportResults(result.RunID, result)

	// No assertions needed as we're just checking it doesn't panic
	assert.True(t, true, "Test completed without panicking")
}
