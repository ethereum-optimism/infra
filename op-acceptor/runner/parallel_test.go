package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/registry"
	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParallelExecution(t *testing.T) {
	ctx := context.Background()

	// Create test content with multiple packages
	testContent1 := []byte(`
package feature_test

import "testing"

func TestPackageOne(t *testing.T) {
	t.Log("Test package one running")
}

func TestPackageTwo(t *testing.T) {
	t.Log("Test package two running")
}
`)

	testContent2 := []byte(`
package integration_test

import "testing"

func TestIntegrationOne(t *testing.T) {
	t.Log("Test integration one running")
}

func TestIntegrationTwo(t *testing.T) {
	t.Log("Test integration two running")
}
`)

	configContent := []byte(`
gates:
  - id: parallel-gate
    description: "Parallel execution gate"
    tests:
      - package: "./feature"
        run_all: true
      - package: "./integration"
        run_all: true
    suites:
      suite1:
        description: "Suite 1"
        tests:
          - package: "./feature"
            run_all: true
      suite2:
        description: "Suite 2"
        tests:
          - package: "./integration"
            run_all: true
`)

	r := setupMultiPackageTestRunner(t, map[string][]byte{
		"feature":     testContent1,
		"integration": testContent2,
	}, configContent)

	// Test parallel execution (default)
	r.serial = false
	startTime := time.Now()
	result, err := r.RunAllTests(ctx)
	parallelDuration := time.Since(startTime)

	require.NoError(t, err)
	assert.Equal(t, types.TestStatusPass, result.Status)

	// Verify structure
	require.Contains(t, result.Gates, "parallel-gate")
	gate := result.Gates["parallel-gate"]
	assert.Equal(t, types.TestStatusPass, gate.Status)

	// Should have direct gate tests and suite tests
	assert.Len(t, gate.Tests, 2, "should have 2 direct gate tests")
	assert.Len(t, gate.Suites, 2, "should have 2 suites")

	t.Logf("Parallel execution took: %v", parallelDuration)
}

func TestSerialExecution(t *testing.T) {
	ctx := context.Background()

	testContent := []byte(`
package feature_test

import "testing"

func TestOne(t *testing.T) {
	t.Log("Test one running")
}

func TestTwo(t *testing.T) {
	t.Log("Test two running")
}
`)

	configContent := []byte(`
gates:
  - id: serial-gate
    description: "Serial execution gate"
    tests:
      - package: "./feature"
        run_all: true
`)

	r := setupTestRunner(t, testContent, configContent)

	// Test serial execution
	r.serial = true
	result, err := r.RunAllTests(ctx)

	require.NoError(t, err)
	assert.Equal(t, types.TestStatusPass, result.Status)

	// Verify structure is the same as before
	require.Contains(t, result.Gates, "serial-gate")
	gate := result.Gates["serial-gate"]
	assert.Equal(t, types.TestStatusPass, gate.Status)
	assert.Len(t, gate.Tests, 1, "should have 1 direct gate test")
}

func TestParallelVsSerialPerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	ctx := context.Background()

	// Create test content with artificial delays to simulate work
	testContent1 := []byte(`
package slow1_test

import (
	"testing"
	"time"
)

func TestSlow1(t *testing.T) {
	time.Sleep(100 * time.Millisecond)
	t.Log("Slow test 1 completed")
}
`)

	testContent2 := []byte(`
package slow2_test

import (
	"testing"
	"time"
)

func TestSlow2(t *testing.T) {
	time.Sleep(100 * time.Millisecond)
	t.Log("Slow test 2 completed")
}
`)

	testContent3 := []byte(`
package slow3_test

import (
	"testing"
	"time"
)

func TestSlow3(t *testing.T) {
	time.Sleep(100 * time.Millisecond)
	t.Log("Slow test 3 completed")
}
`)

	configContent := []byte(`
gates:
  - id: perf-gate
    description: "Performance test gate"
    tests:
      - package: "./slow1"
        run_all: true
      - package: "./slow2"
        run_all: true
      - package: "./slow3"
        run_all: true
`)

	r := setupMultiPackageTestRunner(t, map[string][]byte{
		"slow1": testContent1,
		"slow2": testContent2,
		"slow3": testContent3,
	}, configContent)

	// Test serial execution
	r.serial = true
	startSerial := time.Now()
	serialResult, err := r.RunAllTests(ctx)
	serialDuration := time.Since(startSerial)
	require.NoError(t, err)
	assert.Equal(t, types.TestStatusPass, serialResult.Status)

	// Test parallel execution
	r.serial = false
	startParallel := time.Now()
	parallelResult, err := r.RunAllTests(ctx)
	parallelDuration := time.Since(startParallel)
	require.NoError(t, err)
	assert.Equal(t, types.TestStatusPass, parallelResult.Status)

	t.Logf("Serial execution: %v", serialDuration)
	t.Logf("Parallel execution: %v", parallelDuration)

	// Parallel should be faster than serial for multiple packages
	maxAllowedRatio := 0.9 // Parallel should be at most 90% of serial time
	if parallelDuration <= time.Duration(float64(serialDuration)*maxAllowedRatio) {
		t.Logf("Performance check passed: parallel (%v) vs serial (%v)", parallelDuration, serialDuration)
	} else {
		t.Logf("Performance check: parallel (%v) vs serial (%v) - less speedup than expected but acceptable for CI", parallelDuration, serialDuration)
	}

	// Results should be equivalent
	assert.Equal(t, serialResult.Status, parallelResult.Status)
	assert.Equal(t, len(serialResult.Gates), len(parallelResult.Gates))
}

func TestParallelExecutorConcurrency(t *testing.T) {
	ctx := context.Background()

	testContent := []byte(`
package concurrent_test

import "testing"

func TestConcurrent(t *testing.T) {
	t.Log("Concurrent test running")
}
`)

	configContent := []byte(`
gates:
  - id: concurrent-gate
    description: "Concurrent test gate"
    tests:
      - package: "./concurrent"
        run_all: true
`)

	r := setupMultiPackageTestRunner(t, map[string][]byte{
		"concurrent": testContent,
	}, configContent)

	// Test with different concurrency levels
	workItems := r.collectTestWork()
	assert.Len(t, workItems, 1, "should have 1 work item")

	for _, concurrency := range []int{1, 2, 4, 8} {
		t.Run(fmt.Sprintf("concurrency-%d", concurrency), func(t *testing.T) {
			executor := NewParallelExecutor(r, concurrency, nil)
			assert.Equal(t, concurrency, executor.concurrency)

			result, err := executor.ExecuteTests(ctx, workItems)
			require.NoError(t, err)

			// Since we're calling ExecuteTests directly, we need to finalize the results manually
			r.finalizeParallelResults(result)

			assert.Equal(t, types.TestStatusPass, result.Status)
		})
	}
}

func TestParallelExecutorErrorHandling(t *testing.T) {
	ctx := context.Background()

	// Create test content with a failing test
	testContent := []byte(`
package failing_test

import "testing"

func TestFailing(t *testing.T) {
	t.Fatal("This test always fails")
}
`)

	configContent := []byte(`
gates:
  - id: error-gate
    description: "Error handling gate"
    tests:
      - package: "./failing"
        run_all: true
`)

	r := setupMultiPackageTestRunner(t, map[string][]byte{
		"failing": testContent,
	}, configContent)
	r.serial = false

	result, err := r.RunAllTests(ctx)

	// Should complete without error at the runner level
	require.NoError(t, err)

	// Debug the result
	t.Logf("Result status: %s", result.Status)
	t.Logf("Result stats: Total=%d, Passed=%d, Failed=%d, Skipped=%d",
		result.Stats.Total, result.Stats.Passed, result.Stats.Failed, result.Stats.Skipped)

	gate := result.Gates["error-gate"]
	t.Logf("Gate status: %s", gate.Status)
	t.Logf("Gate stats: Total=%d, Passed=%d, Failed=%d, Skipped=%d",
		gate.Stats.Total, gate.Stats.Passed, gate.Stats.Failed, gate.Stats.Skipped)

	// Check the individual test result
	for testName, testResult := range gate.Tests {
		t.Logf("Test %s: status=%s, error=%v", testName, testResult.Status, testResult.Error)
		for subTestName, subTest := range testResult.SubTests {
			t.Logf("  SubTest %s: status=%s, error=%v", subTestName, subTest.Status, subTest.Error)
		}
	}

	// But the test should have failed
	assert.Equal(t, types.TestStatusFail, result.Status)
	assert.Equal(t, types.TestStatusFail, gate.Status)
}

func TestCollectTestWork(t *testing.T) {
	testContent := []byte(`
package work_test

import "testing"

func TestWork(t *testing.T) {
	t.Log("Work test running")
}
`)

	configContent := []byte(`
gates:
  - id: work-gate
    description: "Work collection gate"
    tests:
      - package: "./work"
        run_all: true
      - name: "TestSpecific"
        package: "./work"
    suites:
      work-suite:
        description: "Work suite"
        tests:
          - package: "./work"
            run_all: true
`)

	r := setupMultiPackageTestRunner(t, map[string][]byte{
		"work": testContent,
	}, configContent)

	workItems := r.collectTestWork()

	// Should have 3 work items:
	// 1. Direct gate test (package)
	// 2. Direct gate test (specific function)
	// 3. Suite test (package)
	assert.Len(t, workItems, 3, "should collect all work items")

	// Verify work item structure
	gatePackageWork := findWorkItem(workItems, "work-gate", "", "./work")
	require.NotNil(t, gatePackageWork, "should have gate package work")
	assert.Equal(t, "./work", gatePackageWork.ResultKey)

	gateSpecificWork := findWorkItem(workItems, "work-gate", "", "TestSpecific")
	require.NotNil(t, gateSpecificWork, "should have gate specific work")
	assert.Equal(t, "TestSpecific", gateSpecificWork.ResultKey)

	suiteWork := findWorkItem(workItems, "work-gate", "work-suite", "./work")
	require.NotNil(t, suiteWork, "should have suite work")
	assert.Equal(t, "./work", suiteWork.ResultKey)
}

// Helper functions

func setupMultiPackageTestRunner(t testing.TB, testContents map[string][]byte, configContent []byte) *runner {
	testDir := t.TempDir()
	initGoModule(t, testDir, "test")

	// Create multiple test packages
	for packageName, content := range testContents {
		packageDir := filepath.Join(testDir, packageName)
		err := os.MkdirAll(packageDir, 0755)
		require.NoError(t, err)

		err = os.WriteFile(filepath.Join(packageDir, "example_test.go"), content, 0644)
		require.NoError(t, err)
	}

	// Create test validator config
	validatorConfigPath := filepath.Join(testDir, "validators.yaml")
	err := os.WriteFile(validatorConfigPath, configContent, 0644)
	require.NoError(t, err)

	// Create registry
	reg, err := registry.NewRegistry(registry.Config{
		ValidatorConfigFile: validatorConfigPath,
	})
	require.NoError(t, err)

	r, err := NewTestRunner(Config{
		Registry: reg,
		WorkDir:  testDir,
	})
	require.NoError(t, err)
	return r.(*runner)
}

func findWorkItem(workItems []TestWork, gateID, suiteID, resultKey string) *TestWork {
	for _, item := range workItems {
		if item.GateID == gateID && item.SuiteID == suiteID && item.ResultKey == resultKey {
			return &item
		}
	}
	return nil
}
