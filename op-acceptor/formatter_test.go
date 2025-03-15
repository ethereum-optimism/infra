package nat

import (
	"errors"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/stretchr/testify/assert"

	"github.com/ethereum-optimism/infra/op-acceptor/runner"
	"github.com/ethereum-optimism/infra/op-acceptor/types"
)

// TestConsoleResultFormatter_FormatResults tests the basic functionality of the formatter
func TestConsoleResultFormatter_FormatResults(t *testing.T) {
	// Create a sample result
	result := createSampleResult()

	// Create logger
	logger := log.New()

	// Create formatter
	formatter := &ConsoleResultFormatter{
		logger: logger,
	}

	// Format results - this is mostly a visual test, so we're just checking it doesn't error
	err := formatter.FormatResults(result)

	// Check assertions
	assert.NoError(t, err)
}

// TestConsoleResultFormatter_FormatResults_EmptyResult tests formatting an empty result
func TestConsoleResultFormatter_FormatResults_EmptyResult(t *testing.T) {
	// Create an empty result
	result := &runner.RunnerResult{
		RunID:    "empty-run",
		Status:   types.TestStatusPass,
		Duration: 100 * time.Millisecond,
		Gates:    make(map[string]*runner.GateResult),
		Stats: runner.ResultStats{
			Total:  0,
			Passed: 0,
			Failed: 0,
		},
	}

	// Create logger
	logger := log.New()

	// Create formatter
	formatter := &ConsoleResultFormatter{
		logger: logger,
	}

	// Format results - this is mostly a visual test, so we're just checking it doesn't error
	err := formatter.FormatResults(result)

	// Check assertions
	assert.NoError(t, err)
}

// Helper function to create a sample test result for formatting
func createSampleResult() *runner.RunnerResult {
	// Create a test result with some sample data
	testResult1 := &types.TestResult{
		Status:   types.TestStatusPass,
		Duration: 50 * time.Millisecond,
		Metadata: types.ValidatorMetadata{
			ID:      "test1",
			Package: "github.com/example/test1",
		},
	}

	testResult2 := &types.TestResult{
		Status:   types.TestStatusFail,
		Duration: 75 * time.Millisecond,
		Error:    errors.New("test failed with error"),
		Metadata: types.ValidatorMetadata{
			ID:      "test2",
			Package: "github.com/example/test2",
		},
	}

	testResult3 := &types.TestResult{
		Status:   types.TestStatusSkip,
		Duration: 10 * time.Millisecond,
		Metadata: types.ValidatorMetadata{
			ID:      "test3",
			Package: "github.com/example/test3",
		},
	}

	// Create a suite result
	suiteResult := &runner.SuiteResult{
		ID:       "test-suite",
		Tests:    map[string]*types.TestResult{"test1": testResult1, "test2": testResult2},
		Status:   types.TestStatusFail, // Fail because one test failed
		Duration: 125 * time.Millisecond,
		Stats: runner.ResultStats{
			Total:   2,
			Passed:  1,
			Failed:  1,
			Skipped: 0,
		},
	}

	// Create a gate result
	gateResult := &runner.GateResult{
		ID:       "test-gate",
		Tests:    map[string]*types.TestResult{"test3": testResult3},
		Suites:   map[string]*runner.SuiteResult{"test-suite": suiteResult},
		Status:   types.TestStatusFail, // Fail because the suite failed
		Duration: 135 * time.Millisecond,
		Stats: runner.ResultStats{
			Total:   3,
			Passed:  1,
			Failed:  1,
			Skipped: 1,
		},
	}

	// Create the runner result
	runnerResult := &runner.RunnerResult{
		RunID:    "test-run-1",
		Gates:    map[string]*runner.GateResult{"test-gate": gateResult},
		Status:   types.TestStatusFail, // Fail because the gate failed
		Duration: 135 * time.Millisecond,
		Stats: runner.ResultStats{
			Total:   3,
			Passed:  1,
			Failed:  1,
			Skipped: 1,
		},
	}

	return runnerResult
}
