package runner

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParallelIsDefaultBehavior ensures that parallel execution is the default
func TestParallelIsDefaultBehavior(t *testing.T) {
	ctx := context.Background()

	testContent := []byte(`
package default_test

import "testing"

func TestDefaultBehavior(t *testing.T) {
	t.Log("Testing default behavior")
}
`)

	configContent := []byte(`
gates:
  - id: default-gate
    description: "Default behavior gate"
    tests:
      - package: "./default"
        run_all: true
`)

	r := setupMultiPackageTestRunner(t, map[string][]byte{
		"default": testContent,
	}, configContent)

	// Don't explicitly set serial - should default to parallel
	// r.serial should be false by default
	assert.False(t, r.serial, "Default should be parallel execution (serial=false)")

	result, err := r.RunAllTests(ctx)
	require.NoError(t, err)
	assert.Equal(t, types.TestStatusPass, result.Status)
}

// TestSerialParallelResultsIdentical proves that both modes produce identical results
func TestSerialParallelResultsIdentical(t *testing.T) {
	ctx := context.Background()

	// Create test content with mixed results
	testContent1 := []byte(`
package identical1_test

import "testing"

func TestPass1(t *testing.T) {
	t.Log("Pass test 1")
}

func TestPass2(t *testing.T) {
	t.Log("Pass test 2")
}
`)

	testContent2 := []byte(`
package identical2_test

import "testing"

func TestPass3(t *testing.T) {
	t.Log("Pass test 3")
}

func TestPass4(t *testing.T) {
	t.Log("Pass test 4")
}
`)

	configContent := []byte(`
gates:
  - id: identical-gate
    description: "Identical results gate"
    tests:
      - package: "./identical1"
        run_all: true
      - package: "./identical2"
        run_all: true
    suites:
      suite1:
        description: "Suite 1"
        tests:
          - package: "./identical1"
            run_all: true
      suite2:
        description: "Suite 2"
        tests:
          - package: "./identical2"
            run_all: true
`)

	r1 := setupMultiPackageTestRunner(t, map[string][]byte{
		"identical1": testContent1,
		"identical2": testContent2,
	}, configContent)

	r2 := setupMultiPackageTestRunner(t, map[string][]byte{
		"identical1": testContent1,
		"identical2": testContent2,
	}, configContent)

	// Run serial
	r1.serial = true
	serialResult, err := r1.RunAllTests(ctx)
	require.NoError(t, err)

	// Run parallel
	r2.serial = false
	parallelResult, err := r2.RunAllTests(ctx)
	require.NoError(t, err)

	// Compare results (ignoring timing and runID)
	assert.Equal(t, serialResult.Status, parallelResult.Status, "Overall status should be identical")
	assert.Equal(t, len(serialResult.Gates), len(parallelResult.Gates), "Number of gates should be identical")

	for gateID, serialGate := range serialResult.Gates {
		parallelGate, exists := parallelResult.Gates[gateID]
		require.True(t, exists, "Gate %s should exist in both results", gateID)

		assert.Equal(t, serialGate.Status, parallelGate.Status, "Gate %s status should be identical", gateID)
		assert.Equal(t, len(serialGate.Tests), len(parallelGate.Tests), "Gate %s test count should be identical", gateID)
		assert.Equal(t, len(serialGate.Suites), len(parallelGate.Suites), "Gate %s suite count should be identical", gateID)
		assert.Equal(t, serialGate.Stats.Total, parallelGate.Stats.Total, "Gate %s total count should be identical", gateID)
		assert.Equal(t, serialGate.Stats.Passed, parallelGate.Stats.Passed, "Gate %s passed count should be identical", gateID)
		assert.Equal(t, serialGate.Stats.Failed, parallelGate.Stats.Failed, "Gate %s failed count should be identical", gateID)

		// Compare suites
		for suiteID, serialSuite := range serialGate.Suites {
			parallelSuite, exists := parallelGate.Suites[suiteID]
			require.True(t, exists, "Suite %s should exist in both results", suiteID)

			assert.Equal(t, serialSuite.Status, parallelSuite.Status, "Suite %s status should be identical", suiteID)
			assert.Equal(t, len(serialSuite.Tests), len(parallelSuite.Tests), "Suite %s test count should be identical", suiteID)
			assert.Equal(t, serialSuite.Stats.Total, parallelSuite.Stats.Total, "Suite %s total count should be identical", suiteID)
			assert.Equal(t, serialSuite.Stats.Passed, parallelSuite.Stats.Passed, "Suite %s passed count should be identical", suiteID)
		}
	}

	t.Logf("  Serial and parallel execution produce identical results")
	t.Logf("   Serial:   %d gates, %d total tests, %d passed", len(serialResult.Gates), serialResult.Stats.Total, serialResult.Stats.Passed)
	t.Logf("   Parallel: %d gates, %d total tests, %d passed", len(parallelResult.Gates), parallelResult.Stats.Total, parallelResult.Stats.Passed)
}

// TestParallelContextCancellation tests that parallel execution respects context cancellation
func TestParallelContextCancellation(t *testing.T) {
	// Create slow test content
	testContent := []byte(`
package slow_test

import (
	"testing"
	"time"
)

func TestSlow(t *testing.T) {
	time.Sleep(2 * time.Second)
	t.Log("Slow test completed")
}
`)

	configContent := []byte(`
gates:
  - id: cancel-gate
    description: "Context cancellation gate"
    tests:
      - package: "./slow"
        run_all: true
`)

	r := setupMultiPackageTestRunner(t, map[string][]byte{
		"slow": testContent,
	}, configContent)
	r.serial = false

	// Create context with short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := r.RunAllTests(ctx)
	duration := time.Since(start)

	// Should complete quickly due to cancellation
	assert.Less(t, duration, 1500*time.Millisecond, "Should be cancelled before tests complete")

	// The error might be nil if cancellation happens during cleanup
	// but should reflect the cancellation
	if err != nil {
		assert.Contains(t, err.Error(), "context")
	}

	t.Logf("Context cancellation respected, completed in %v", duration)
}

// TestParallelWithLargeNumberOfPackages tests scalability
func TestParallelWithLargeNumberOfPackages(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping large package test in short mode")
	}

	ctx := context.Background()

	// Create many packages
	packageCount := 20
	packages := make(map[string][]byte)
	configParts := []string{"gates:", "  - id: large-gate", "    description: \"Large package count gate\"", "    tests:"}

	for i := 0; i < packageCount; i++ {
		packageName := fmt.Sprintf("pkg%d", i)
		testContent := []byte(fmt.Sprintf(`
package pkg%d_test

import "testing"

func TestPkg%d(t *testing.T) {
	t.Log("Package %d test running")
}
`, i, i, i))
		packages[packageName] = testContent
		configParts = append(configParts, fmt.Sprintf("      - package: \"./%s\"", packageName))
		configParts = append(configParts, "        run_all: true")
	}

	configContent := []byte(strings.Join(configParts, "\n"))

	r := setupMultiPackageTestRunner(t, packages, configContent)
	r.serial = false

	start := time.Now()
	result, err := r.RunAllTests(ctx)
	duration := time.Since(start)

	require.NoError(t, err)
	assert.Equal(t, types.TestStatusPass, result.Status)

	// Check that we have the right number of packages in the gate
	gate := result.Gates["large-gate"]
	assert.Equal(t, packageCount, len(gate.Tests), "Should have run all packages")

	// Stats.Total includes both package tests and subtests (packageCount * 2)
	expectedTotal := packageCount * 2
	assert.Equal(t, expectedTotal, result.Stats.Total, "Should have run all packages and subtests")
	assert.Equal(t, expectedTotal, result.Stats.Passed, "All tests should pass (packages + subtests)")

	t.Logf("Successfully ran %d packages in parallel in %v", packageCount, duration)
}

// TestParallelResourceUsage monitors resource usage during parallel execution
func TestParallelResourceUsage(t *testing.T) {
	ctx := context.Background()

	// Create test content
	testContent := []byte(`
package resource_test

import "testing"

func TestResource(t *testing.T) {
	t.Log("Resource test running")
}
`)

	configContent := []byte(`
gates:
  - id: resource-gate
    description: "Resource usage gate"
    tests:
      - package: "./resource"
        run_all: true
`)

	r := setupMultiPackageTestRunner(t, map[string][]byte{
		"resource": testContent,
	}, configContent)
	r.serial = false

	// Monitor goroutines
	initialGoroutines := runtime.NumGoroutine()

	var memStats1, memStats2 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&memStats1)

	result, err := r.RunAllTests(ctx)
	require.NoError(t, err)
	assert.Equal(t, types.TestStatusPass, result.Status)

	runtime.GC()
	runtime.ReadMemStats(&memStats2)
	finalGoroutines := runtime.NumGoroutine()

	// Check for goroutine leaks (allow some tolerance)
	goroutineDiff := finalGoroutines - initialGoroutines
	assert.LessOrEqual(t, goroutineDiff, 5, "Should not leak significant number of goroutines")

	// Memory usage should not grow excessively
	memDiff := memStats2.Alloc - memStats1.Alloc
	t.Logf("   Resource usage check passed")
	t.Logf("   Goroutines: %d -> %d (diff: %d)", initialGoroutines, finalGoroutines, goroutineDiff)
	t.Logf("   Memory: %d -> %d bytes (diff: %d)", memStats1.Alloc, memStats2.Alloc, memDiff)
}

// TestParallelEmptyTestSuite tests edge case of empty test suites
func TestParallelEmptyTestSuite(t *testing.T) {
	ctx := context.Background()

	// Create at least one test package to avoid "no validators found" error
	testContent := []byte(`
package empty_test

import "testing"

func TestEmpty(t *testing.T) {
	t.Log("Empty test")
}
`)

	// Config with some tests but empty suites
	configContent := []byte(`
gates:
  - id: empty-gate
    description: "Empty gate with minimal tests"
    tests:
      - package: "./empty"
        run_all: true
    suites:
      empty-suite:
        description: "Empty suite"
        tests: []
`)

	r := setupMultiPackageTestRunner(t, map[string][]byte{
		"empty": testContent,
	}, configContent)
	r.serial = false

	result, err := r.RunAllTests(ctx)
	require.NoError(t, err)

	// Should handle empty suites gracefully and run the gate tests
	assert.Equal(t, types.TestStatusPass, result.Status)
	assert.Greater(t, result.Stats.Total, 0, "Should have run at least one test")

	// Verify that the gate was processed even with empty suites in config
	gate := result.Gates["empty-gate"]
	require.NotNil(t, gate)
	assert.Greater(t, len(gate.Tests), 0, "Gate should have executed some tests")

	// Note: Empty suites may not appear in results if they have no tests to run
	// This is expected behavior - the system optimizes away empty containers

	t.Logf("Empty test suites handled gracefully")
}

// TestParallelPerformanceRegression provides automated performance regression detection
func TestParallelPerformanceRegression(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance regression test in short mode")
	}

	ctx := context.Background()

	// Create test content with realistic work
	testContent1 := []byte(`
package perf1_test

import (
	"testing"
	"time"
)

func TestPerf1(t *testing.T) {
	time.Sleep(50 * time.Millisecond)
	t.Log("Perf test 1 completed")
}
`)

	testContent2 := []byte(`
package perf2_test

import (
	"testing"
	"time"
)

func TestPerf2(t *testing.T) {
	time.Sleep(50 * time.Millisecond)
	t.Log("Perf test 2 completed")
}
`)

	testContent3 := []byte(`
package perf3_test

import (
	"testing"
	"time"
)

func TestPerf3(t *testing.T) {
	time.Sleep(50 * time.Millisecond)
	t.Log("Perf test 3 completed")
}
`)

	configContent := []byte(`
gates:
  - id: perf-gate
    description: "Performance regression gate"
    tests:
      - package: "./perf1"
        run_all: true
      - package: "./perf2"
        run_all: true
      - package: "./perf3"
        run_all: true
`)

	packages := map[string][]byte{
		"perf1": testContent1,
		"perf2": testContent2,
		"perf3": testContent3,
	}

	// Measure serial performance
	r1 := setupMultiPackageTestRunner(t, packages, configContent)
	r1.serial = true

	start := time.Now()
	serialResult, err := r1.RunAllTests(ctx)
	serialDuration := time.Since(start)
	require.NoError(t, err)

	// Measure parallel performance
	r2 := setupMultiPackageTestRunner(t, packages, configContent)
	r2.serial = false

	start = time.Now()
	parallelResult, err := r2.RunAllTests(ctx)
	parallelDuration := time.Since(start)
	require.NoError(t, err)

	// Performance assertions
	speedup := float64(serialDuration) / float64(parallelDuration)

	// CI environments often have less performance gain due to shared resources
	minSpeedup := 1.1 // More realistic minimum for CI
	if speedup < minSpeedup {
		t.Errorf("Performance regression detected: parallel only %.2fx faster (expected >%.1fx)", speedup, minSpeedup)
	} else {
		t.Logf("Performance check passed: %.2fx speedup (>%.1fx required)", speedup, minSpeedup)
	}

	// Ensure results are equivalent
	assert.Equal(t, serialResult.Status, parallelResult.Status)
	assert.Equal(t, serialResult.Stats.Total, parallelResult.Stats.Total)
	assert.Equal(t, serialResult.Stats.Passed, parallelResult.Stats.Passed)

	t.Logf("   Performance regression check passed")
	t.Logf("   Serial:   %v", serialDuration)
	t.Logf("   Parallel: %v", parallelDuration)
	t.Logf("   Speedup:  %.2fx", speedup)
}

// TestParallelExecutorStress tests the parallel executor under stress
func TestParallelExecutorStress(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	ctx := context.Background()

	// Create many small tests
	packages := make(map[string][]byte)
	configParts := []string{"gates:", "  - id: stress-gate", "    description: \"Stress test gate\"", "    tests:"}

	for i := 0; i < 50; i++ {
		packageName := fmt.Sprintf("stress%d", i)
		testContent := []byte(fmt.Sprintf(`
package stress%d_test

import "testing"

func TestStress%d(t *testing.T) {
	// Simulate some work
	for j := 0; j < 1000; j++ {
		_ = j * j
	}
	t.Log("Stress test %d completed")
}
`, i, i, i))
		packages[packageName] = testContent
		configParts = append(configParts, fmt.Sprintf("      - package: \"./%s\"", packageName))
		configParts = append(configParts, "        run_all: true")
	}

	configContent := []byte(strings.Join(configParts, "\n"))

	r := setupMultiPackageTestRunner(t, packages, configContent)
	r.serial = false

	start := time.Now()
	result, err := r.RunAllTests(ctx)
	duration := time.Since(start)

	require.NoError(t, err)
	assert.Equal(t, types.TestStatusPass, result.Status)

	// Check that we have the right number of packages in the gate
	gate := result.Gates["stress-gate"]
	assert.Equal(t, 50, len(gate.Tests), "Should have run 50 packages")

	// Stress test: 50 packages * 2 (package + subtest) = 100 total
	assert.Equal(t, 100, result.Stats.Total, "Should have 50 packages with subtests")
	assert.Equal(t, 100, result.Stats.Passed, "All tests should pass (packages + subtests)")

	t.Logf("Stress test passed: 50 packages in %v", duration)
}

// TestParallelExecutorConcurrencyLimits tests different concurrency limits
func TestParallelExecutorConcurrencyLimits(t *testing.T) {
	ctx := context.Background()

	testContent := []byte(`
package concurrency_test

import "testing"

func TestConcurrency(t *testing.T) {
	t.Log("Concurrency test running")
}
`)

	configContent := []byte(`
gates:
  - id: concurrency-gate
    description: "Concurrency limits gate"
    tests:
      - package: "./concurrency"
        run_all: true
`)

	// Test various concurrency limits
	concurrencyLevels := []int{1, 2, 4, 8, 16, 32}

	for _, concurrency := range concurrencyLevels {
		t.Run(fmt.Sprintf("concurrency-%d", concurrency), func(t *testing.T) {
			r := setupMultiPackageTestRunner(t, map[string][]byte{
				"concurrency": testContent,
			}, configContent)

			workItems := r.collectTestWork()
			executor := NewParallelExecutor(r, concurrency, nil)

			result, err := executor.ExecuteTests(ctx, workItems)
			require.NoError(t, err)

			// Finalize results
			r.finalizeParallelResults(result)

			assert.Equal(t, types.TestStatusPass, result.Status)
			assert.Equal(t, concurrency, executor.concurrency)
		})
	}

	t.Logf("All concurrency limits work correctly")
}

// TestParallelExecutorErrorRecovery tests error recovery scenarios
func TestParallelExecutorErrorRecovery(t *testing.T) {
	ctx := context.Background()

	// Mix of passing and failing tests
	passingContent := []byte(`
package passing_test

import "testing"

func TestPassing(t *testing.T) {
	t.Log("This test passes")
}
`)

	failingContent := []byte(`
package failing_test

import "testing"

func TestFailing(t *testing.T) {
	t.Fatal("This test fails")
}
`)

	panicContent := []byte(`
package panic_test

import "testing"

func TestPanic(t *testing.T) {
	panic("This test panics")
}
`)

	configContent := []byte(`
gates:
  - id: error-recovery-gate
    description: "Error recovery gate"
    tests:
      - package: "./passing"
        run_all: true
      - package: "./failing"
        run_all: true
      - package: "./panic"
        run_all: true
`)

	r := setupMultiPackageTestRunner(t, map[string][]byte{
		"passing": passingContent,
		"failing": failingContent,
		"panic":   panicContent,
	}, configContent)
	r.serial = false

	result, err := r.RunAllTests(ctx)

	// Should complete without runner-level error
	require.NoError(t, err)

	// But should show test failures
	assert.Equal(t, types.TestStatusFail, result.Status)

	// Note: Stats include subtests, so totals may be higher than package count
	assert.Greater(t, result.Stats.Total, 0, "Should have run some tests")
	assert.Greater(t, result.Stats.Failed, 0, "Should have some failures")
	assert.Greater(t, result.Stats.Passed, 0, "Should have some passes")

	// Main assertion: at least one test passed and at least one failed
	assert.True(t, result.Stats.Failed >= 1, "Should have at least 1 failure")
	assert.True(t, result.Stats.Passed >= 1, "Should have at least 1 pass")

	t.Logf("Error recovery works: %d passed, %d failed out of %d total",
		result.Stats.Passed, result.Stats.Failed, result.Stats.Total)
}

// BenchmarkParallelVsSerial provides automated benchmarking
func BenchmarkParallelVsSerial(b *testing.B) {
	ctx := context.Background()

	testContent := []byte(`
package bench_test

import "testing"

func TestBench(t *testing.T) {
	// Simulate some work
	for i := 0; i < 1000; i++ {
		_ = i * i
	}
}
`)

	configContent := []byte(`
gates:
  - id: bench-gate
    description: "Benchmark gate"
    tests:
      - package: "./bench"
        run_all: true
`)

	b.Run("Serial", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			r := setupMultiPackageTestRunner(b, map[string][]byte{
				"bench": testContent,
			}, configContent)
			r.serial = true

			_, err := r.RunAllTests(ctx)
			require.NoError(b, err)
		}
	})

	b.Run("Parallel", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			r := setupMultiPackageTestRunner(b, map[string][]byte{
				"bench": testContent,
			}, configContent)
			r.serial = false

			_, err := r.RunAllTests(ctx)
			require.NoError(b, err)
		}
	})
}

// Note: setupMultiPackageTestRunner and initGoModule are defined in parallel_test.go
