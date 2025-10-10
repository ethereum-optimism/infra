package runner

import (
	"runtime"
	"testing"

	"github.com/ethereum/go-ethereum/log"
	"github.com/stretchr/testify/assert"
)

// TestDetermineConcurrency tests the intelligent concurrency determination logic
func TestDetermineConcurrency(t *testing.T) {
	// Create a minimal runner for testing
	r := &runner{
		log: log.NewLogger(log.DiscardHandler()),
	}

	tests := []struct {
		name               string
		userConcurrency    int
		numWorkItems       int
		expectedRange      [2]int // [min, max] expected range
		expectUserOverride bool
	}{
		{
			name:            "Auto-determine with 4 work items",
			userConcurrency: 0, // auto-determine
			numWorkItems:    4,
			expectedRange:   [2]int{1, 4}, // Should not exceed work items
		},
		{
			name:            "Auto-determine with many work items",
			userConcurrency: 0, // auto-determine
			numWorkItems:    20,
			expectedRange:   [2]int{1, MaxReasonableConcurrency}, // Should cap at reasonable max
		},
		{
			name:               "User override within work items",
			userConcurrency:    3,
			numWorkItems:       10,
			expectedRange:      [2]int{3, 3}, // Exact user preference
			expectUserOverride: true,
		},
		{
			name:               "User override exceeds work items",
			userConcurrency:    8,
			numWorkItems:       3,
			expectedRange:      [2]int{3, 3}, // Capped at work items
			expectUserOverride: true,
		},
		{
			name:            "Single work item",
			userConcurrency: 0,
			numWorkItems:    1,
			expectedRange:   [2]int{1, 1}, // Can't exceed 1
		},
		{
			name:               "User requests high concurrency",
			userConcurrency:    25,
			numWorkItems:       30,
			expectedRange:      [2]int{25, 25}, // Should honor user preference
			expectUserOverride: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r.concurrency = tt.userConcurrency

			actualConcurrency := r.determineConcurrency(tt.numWorkItems)

			// Verify concurrency is within expected range
			assert.GreaterOrEqual(t, actualConcurrency, tt.expectedRange[0],
				"Concurrency should be at least %d", tt.expectedRange[0])
			assert.LessOrEqual(t, actualConcurrency, tt.expectedRange[1],
				"Concurrency should not exceed %d", tt.expectedRange[1])

			// Verify concurrency never exceeds work items
			assert.LessOrEqual(t, actualConcurrency, tt.numWorkItems,
				"Concurrency should never exceed number of work items")

			// Verify minimum concurrency
			assert.GreaterOrEqual(t, actualConcurrency, 1,
				"Concurrency should be at least 1")

			t.Logf("Concurrency determination: user=%d, workItems=%d, actual=%d",
				tt.userConcurrency, tt.numWorkItems, actualConcurrency)
		})
	}
}

// TestConcurrencyHeuristics tests the auto-determination heuristics
func TestConcurrencyHeuristics(t *testing.T) {
	r := &runner{
		log:         log.NewLogger(log.DiscardHandler()),
		concurrency: 0, // auto-determine
	}

	numCPU := runtime.NumCPU()

	// Test with sufficient work items to not be constrained
	numWorkItems := 20

	actualConcurrency := r.determineConcurrency(numWorkItems)

	// Verify heuristics based on CPU count
	if numCPU <= 2 {
		// Low-core systems should be conservative
		assert.LessOrEqual(t, actualConcurrency, numCPU,
			"Low-core systems should not exceed CPU count")
	} else if numCPU <= 4 {
		// Mid-range systems should have modest increase
		expectedMax := int(float64(numCPU) * 1.25)
		assert.LessOrEqual(t, actualConcurrency, expectedMax+1, // +1 for rounding
			"Mid-range systems should have modest increase")
	} else {
		// High-core systems can be more aggressive
		expectedMax := int(float64(numCPU) * 1.5)
		assert.LessOrEqual(t, actualConcurrency, expectedMax+1, // +1 for rounding
			"High-core systems can be more aggressive")
	}

	// General constraints
	assert.GreaterOrEqual(t, actualConcurrency, 1, "Should have at least 1 worker")
	assert.LessOrEqual(t, actualConcurrency, MaxReasonableConcurrency, "Should cap at reasonable maximum")
	assert.LessOrEqual(t, actualConcurrency, numWorkItems, "Should not exceed work items")

	t.Logf("CPU cores: %d, determined concurrency: %d", numCPU, actualConcurrency)
}

// TestConcurrencyEdgeCases tests edge cases in concurrency determination
func TestConcurrencyEdgeCases(t *testing.T) {
	r := &runner{
		log: log.NewLogger(log.DiscardHandler()),
	}

	t.Run("Zero work items", func(t *testing.T) {
		r.concurrency = 0
		actualConcurrency := r.determineConcurrency(0)
		assert.Equal(t, 0, actualConcurrency, "Zero work items should result in zero concurrency")
	})

	t.Run("User requests zero concurrency", func(t *testing.T) {
		r.concurrency = 0
		actualConcurrency := r.determineConcurrency(5)
		assert.GreaterOrEqual(t, actualConcurrency, 1, "Auto-determination should provide at least 1")
		assert.LessOrEqual(t, actualConcurrency, 5, "Should not exceed work items")
	})

	t.Run("User requests negative concurrency", func(t *testing.T) {
		r.concurrency = -1
		actualConcurrency := r.determineConcurrency(5)
		assert.GreaterOrEqual(t, actualConcurrency, 1, "Negative user input should fall back to auto-determination")
		assert.LessOrEqual(t, actualConcurrency, 5, "Should not exceed work items")
	})

	t.Run("Very high user concurrency", func(t *testing.T) {
		r.concurrency = 1000
		actualConcurrency := r.determineConcurrency(5)
		assert.Equal(t, 5, actualConcurrency, "Should cap at work items even with very high user request")
	})

	t.Run("User concurrency exceeds reasonable limit", func(t *testing.T) {
		r.concurrency = 50
		actualConcurrency := r.determineConcurrency(100)
		assert.Equal(t, 50, actualConcurrency, "Should respect user preference when within reasonable bounds")
	})

	t.Run("Auto-determination constraint order", func(t *testing.T) {
		r.concurrency = 0
		// Test that constraints are applied in correct order
		actualConcurrency := r.determineConcurrency(1)
		assert.Equal(t, 1, actualConcurrency, "Single work item should result in 1 worker")
	})
}

// BenchmarkDetermineConcurrency benchmarks the concurrency determination performance
func BenchmarkDetermineConcurrency(b *testing.B) {
	r := &runner{
		log:         log.NewLogger(log.DiscardHandler()),
		concurrency: 0, // auto-determine
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = r.determineConcurrency(10)
	}
}

// TestConcurrencyIntegration tests concurrency determination in context of full runner
func TestConcurrencyIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Test with different concurrency settings
	testCases := []struct {
		name        string
		concurrency int
		description string
	}{
		{
			name:        "AutoDetermine",
			concurrency: 0,
			description: "Auto-determined concurrency",
		},
		{
			name:        "Manual2",
			concurrency: 2,
			description: "User-specified concurrency of 2",
		},
		{
			name:        "Manual1",
			concurrency: 1,
			description: "User-specified concurrency of 1",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create test content
			testContent := []byte(`
package concurrency_test

import (
	"testing"
	"time"
)

func TestConcurrencyIntegration(t *testing.T) {
	time.Sleep(50 * time.Millisecond)
	t.Log("Concurrency integration test completed")
}
`)

			configContent := []byte(`
gates:
  - id: concurrency-gate
    description: "Concurrency integration test"
    tests:
      - package: "./concurrency"
        run_all: true
`)

			r := setupMultiPackageTestRunner(t, map[string][]byte{
				"concurrency": testContent,
			}, configContent)

			// Set the concurrency
			r.concurrency = tc.concurrency
			r.serial = false // Ensure parallel mode

			// Test concurrency determination without full execution
			workItems := r.collectTestWork()
			determinedConcurrency := r.determineConcurrency(len(workItems))

			t.Logf("%s: workItems=%d, determinedConcurrency=%d",
				tc.description, len(workItems), determinedConcurrency)

			// Validate concurrency makes sense
			assert.GreaterOrEqual(t, determinedConcurrency, 1, "Should have at least 1 worker")
			assert.LessOrEqual(t, determinedConcurrency, len(workItems), "Should not exceed work items")

			if tc.concurrency > 0 {
				expectedConcurrency := tc.concurrency
				if expectedConcurrency > len(workItems) {
					expectedConcurrency = len(workItems)
				}
				assert.Equal(t, expectedConcurrency, determinedConcurrency,
					"Should respect user-specified concurrency (capped at work items)")
			}
		})
	}
}
