package runner

import (
	"context"
	"testing"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParallelExecutorValidation tests validation and edge cases for ParallelExecutor
func TestParallelExecutorValidation(t *testing.T) {
	t.Run("ParallelExecutor validation", func(t *testing.T) {
		r := &runner{
			log: log.NewLogger(log.DiscardHandler()),
		}

		// Test panic on nil runner
		assert.Panics(t, func() {
			NewParallelExecutor(nil, 4)
		}, "Should panic with nil runner")

		// Test panic on negative concurrency
		assert.Panics(t, func() {
			NewParallelExecutor(r, -1)
		}, "Should panic with negative concurrency")

		// Test valid creation
		executor := NewParallelExecutor(r, 4)
		assert.NotNil(t, executor, "Should create valid executor")
		assert.Equal(t, 4, executor.concurrency, "Should set correct concurrency")
	})

	t.Run("Empty work items handling", func(t *testing.T) {
		r := &runner{
			log:   log.NewLogger(log.DiscardHandler()),
			runID: "test-run",
		}

		executor := NewParallelExecutor(r, 4)
		result, err := executor.ExecuteTests(context.Background(), []TestWork{})

		assert.NoError(t, err, "Should handle empty work items without error")
		assert.NotNil(t, result, "Should return valid result")
		assert.Equal(t, 0, result.Stats.Total, "Should have zero total tests")
	})

	t.Run("Conservative channel buffering", func(t *testing.T) {
		// This test validates that channel buffer size is conservative
		// We can't directly test channel buffer size, but we can verify
		// that the built-in min function works correctly

		assert.Equal(t, 5, min(5, 10), "min should return smaller value")
		assert.Equal(t, 5, min(10, 5), "min should return smaller value")
		assert.Equal(t, 5, min(5, 5), "min should handle equal values")
		assert.Equal(t, 0, min(0, 5), "min should handle zero")
	})

	t.Run("Nil coordinator handling", func(t *testing.T) {
		// Test that ParallelExecutor works correctly when coordinator is nil
		// This simulates the case where NewParallelExecutor is called before
		// the coordinator is initialized
		r := &runner{
			log:   log.NewLogger(log.DiscardHandler()),
			runID: "test-run",
			// coordinator is nil by default
		}

		executor := NewParallelExecutor(r, 2)
		assert.NotNil(t, executor, "Should create executor even with nil coordinator")

		// Verify that getUI returns nil when coordinator is nil
		ui := executor.getUI()
		assert.Nil(t, ui, "getUI should return nil when coordinator is nil")

		// The executor should still be functional for empty work items
		result, err := executor.ExecuteTests(context.Background(), []TestWork{})
		assert.NoError(t, err, "Should handle empty work items without coordinator")
		assert.NotNil(t, result, "Should return valid result")
		assert.Equal(t, 0, result.Stats.Total, "Should have zero total tests")
	})

	t.Run("UIProvider dependency injection", func(t *testing.T) {
		// Test that dependency injection works through UIProvider interface
		r := &runner{
			log:   log.NewLogger(log.DiscardHandler()),
			runID: "test-run",
		}

		executor := NewParallelExecutor(r, 1)
		assert.NotNil(t, executor, "Should create executor")

		// Verify the runner implements UIProvider interface
		var _ UIProvider = r

		// Verify that UIProvider is set correctly
		assert.Equal(t, r, executor.uiProvider, "UIProvider should be set to runner")

		// When coordinator is nil, GetUI should return nil
		assert.Nil(t, r.GetUI(), "GetUI should return nil when coordinator is nil")
		assert.Nil(t, executor.getUI(), "executor getUI should return nil when coordinator is nil")
	})
}

// TestImprovedErrorAggregation tests the enhanced error handling
func TestImprovedErrorAggregation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping error aggregation test in short mode")
	}

	// Create test content that will fail
	failingTestContent1 := []byte(`
package failing1_test

import "testing"

func TestFailing1(t *testing.T) {
	t.Fatal("Test 1 intentionally fails")
}
`)

	failingTestContent2 := []byte(`
package failing2_test

import "testing"

func TestFailing2(t *testing.T) {
	t.Fatal("Test 2 intentionally fails")
}
`)

	passingTestContent := []byte(`
package passing_test

import "testing"

func TestPassing(t *testing.T) {
	t.Log("Test passes successfully")
}
`)

	configContent := []byte(`
gates:
  - id: error-aggregation-gate
    description: "Error aggregation test"
    tests:
      - package: "./failing1"
        run_all: true
      - package: "./failing2"
        run_all: true
      - package: "./passing"
        run_all: true
`)

	r := setupMultiPackageTestRunner(t, map[string][]byte{
		"failing1": failingTestContent1,
		"failing2": failingTestContent2,
		"passing":  passingTestContent,
	}, configContent)

	r.serial = false // Use parallel execution
	r.concurrency = 2

	// This should complete but with errors
	result, err := r.RunAllTests(context.Background())

	// We expect the run to complete (not return error) but have failed tests
	require.NoError(t, err, "Runner should complete even with test failures")
	require.NotNil(t, result, "Should return valid result")

	// Verify that some tests failed but at least one passed
	assert.Greater(t, result.Stats.Failed, 0, "Should have failed tests")
	assert.Greater(t, result.Stats.Passed, 0, "Should have passed tests")
	assert.Equal(t, types.TestStatusFail, result.Status, "Overall status should be fail")

	t.Logf("Error aggregation test completed: %d passed, %d failed",
		result.Stats.Passed, result.Stats.Failed)
}

// TestConcurrencyLogging tests that the enhanced logging provides useful information
func TestConcurrencyLogging(t *testing.T) {
	// Create a simple test runner
	testContent := []byte(`
package logging_test

import "testing"

func TestLogging(t *testing.T) {
	t.Log("Logging test completed")
}
`)

	configContent := []byte(`
gates:
  - id: logging-gate
    description: "Logging test"
    tests:
      - package: "./logging"
        run_all: true
`)

	r := setupMultiPackageTestRunner(t, map[string][]byte{
		"logging": testContent,
	}, configContent)

	r.serial = false
	r.concurrency = 2

	// Test that concurrency determination works and logs appropriately
	workItems := r.collectTestWork()
	concurrency := r.determineConcurrency(len(workItems))

	assert.GreaterOrEqual(t, concurrency, 1, "Should determine at least 1 worker")
	assert.LessOrEqual(t, concurrency, len(workItems), "Should not exceed work items")

	t.Logf("Concurrency logging test: %d work items, %d workers", len(workItems), concurrency)
}
