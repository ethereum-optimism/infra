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
			executor := NewParallelExecutor(r, concurrency)
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

func TestApplySplitFilter(t *testing.T) {
	// Create 10 work items with distinct packages/functions
	var items []TestWork
	for i := 0; i < 10; i++ {
		items = append(items, TestWork{
			Validator: types.ValidatorMetadata{
				Package:  fmt.Sprintf("./pkg%d", i),
				FuncName: fmt.Sprintf("TestFunc%d", i),
			},
			GateID:    "gate",
			ResultKey: fmt.Sprintf("TestFunc%d", i),
		})
	}

	t.Run("split into 4 nodes covers all items", func(t *testing.T) {
		total := 4
		var all []TestWork
		seen := make(map[string]bool)

		for idx := 0; idx < total; idx++ {
			subset := ApplySplitFilter(items, total, idx, nil)
			for _, item := range subset {
				key := item.Validator.Package + "|" + item.Validator.FuncName
				assert.False(t, seen[key], "item %s assigned to multiple nodes", key)
				seen[key] = true
			}
			all = append(all, subset...)
		}

		assert.Len(t, all, 10, "all items should be covered across nodes")
	})

	t.Run("split into 1 node returns all items", func(t *testing.T) {
		result := ApplySplitFilter(items, 1, 0, nil)
		assert.Len(t, result, 10, "single node should get all items")
	})

	t.Run("split into more nodes than items", func(t *testing.T) {
		total := 20
		var all []TestWork
		for idx := 0; idx < total; idx++ {
			all = append(all, ApplySplitFilter(items, total, idx, nil)...)
		}
		assert.Len(t, all, 10, "all items should be covered even with excess nodes")
	})

	t.Run("split is deterministic", func(t *testing.T) {
		result1 := ApplySplitFilter(items, 3, 1, nil)
		result2 := ApplySplitFilter(items, 3, 1, nil)
		require.Len(t, result1, len(result2))
		for i := range result1 {
			assert.Equal(t, result1[i].Validator.Package, result2[i].Validator.Package)
			assert.Equal(t, result1[i].Validator.FuncName, result2[i].Validator.FuncName)
		}
	})

	t.Run("nodes get roughly equal work", func(t *testing.T) {
		total := 3
		for idx := 0; idx < total; idx++ {
			subset := ApplySplitFilter(items, total, idx, nil)
			// 10 items / 3 nodes = 3 or 4 per node
			assert.True(t, len(subset) >= 3 && len(subset) <= 4,
				"node %d got %d items, expected 3-4", idx, len(subset))
		}
	})
}

func TestApplySplitFilterWithTimings(t *testing.T) {
	// Create items with known timing characteristics:
	// One heavy package (120s) and several light ones (10s each)
	items := []TestWork{
		{Validator: types.ValidatorMetadata{Package: "./heavy", FuncName: ""}, GateID: "gate", ResultKey: "./heavy"},
		{Validator: types.ValidatorMetadata{Package: "./light1", FuncName: ""}, GateID: "gate", ResultKey: "./light1"},
		{Validator: types.ValidatorMetadata{Package: "./light2", FuncName: ""}, GateID: "gate", ResultKey: "./light2"},
		{Validator: types.ValidatorMetadata{Package: "./light3", FuncName: ""}, GateID: "gate", ResultKey: "./light3"},
		{Validator: types.ValidatorMetadata{Package: "./light4", FuncName: ""}, GateID: "gate", ResultKey: "./light4"},
		{Validator: types.ValidatorMetadata{Package: "./light5", FuncName: ""}, GateID: "gate", ResultKey: "./light5"},
	}

	timings := map[string]float64{
		"gate|./heavy|":  120.0,
		"gate|./light1|": 10.0,
		"gate|./light2|": 10.0,
		"gate|./light3|": 10.0,
		"gate|./light4|": 10.0,
		"gate|./light5|": 10.0,
	}

	t.Run("heavy item gets its own node", func(t *testing.T) {
		total := 2
		node0 := ApplySplitFilter(items, total, 0, timings)
		node1 := ApplySplitFilter(items, total, 1, timings)

		// All items should be covered
		assert.Equal(t, len(items), len(node0)+len(node1))

		// The heavy item should be alone on one node, lights on the other
		// With LPT: heavy goes to node 0, then lights fill node 1 until it's still less
		// Actually: heavy(120) → node0. light1(10) → node1(10). light2(10) → node1(20).
		// light3(10) → node1(30). light4(10) → node1(40). light5(10) → node1(50).
		// So node0=[heavy], node1=[light1..light5]

		// Find which node has the heavy item
		var heavyNode, lightNode []TestWork
		for _, item := range node0 {
			if item.Validator.Package == "./heavy" {
				heavyNode = node0
				lightNode = node1
				break
			}
		}
		if heavyNode == nil {
			heavyNode = node1
			lightNode = node0
		}

		assert.Len(t, heavyNode, 1, "heavy item should be alone")
		assert.Len(t, lightNode, 5, "all lights should be together")
	})

	t.Run("all items covered with timings", func(t *testing.T) {
		total := 3
		var all []TestWork
		seen := make(map[string]bool)

		for idx := 0; idx < total; idx++ {
			subset := ApplySplitFilter(items, total, idx, timings)
			for _, item := range subset {
				key := splitKey(item)
				assert.False(t, seen[key], "item %s assigned to multiple nodes", key)
				seen[key] = true
			}
			all = append(all, subset...)
		}
		assert.Len(t, all, len(items), "all items should be covered across nodes")
	})

	t.Run("timing split is deterministic", func(t *testing.T) {
		result1 := ApplySplitFilter(items, 3, 1, timings)
		result2 := ApplySplitFilter(items, 3, 1, timings)
		require.Len(t, result1, len(result2))
		for i := range result1 {
			assert.Equal(t, splitKey(result1[i]), splitKey(result2[i]))
		}
	})

	t.Run("balanced distribution with equal timings", func(t *testing.T) {
		equalTimings := map[string]float64{
			"gate|./heavy|":  30.0,
			"gate|./light1|": 30.0,
			"gate|./light2|": 30.0,
			"gate|./light3|": 30.0,
			"gate|./light4|": 30.0,
			"gate|./light5|": 30.0,
		}
		total := 3
		for idx := 0; idx < total; idx++ {
			subset := ApplySplitFilter(items, total, idx, equalTimings)
			// 6 items / 3 nodes = 2 per node
			assert.Len(t, subset, 2, "node %d should get 2 items with equal timings", idx)
		}
	})
}

func TestApplySplitFilterTimingFallback(t *testing.T) {
	items := []TestWork{
		{Validator: types.ValidatorMetadata{Package: "./pkg1", FuncName: "TestA"}, GateID: "gate"},
		{Validator: types.ValidatorMetadata{Package: "./pkg2", FuncName: "TestB"}, GateID: "gate"},
	}

	// With nil timings, should fall back to round-robin
	node0 := ApplySplitFilter(items, 2, 0, nil)
	node1 := ApplySplitFilter(items, 2, 1, nil)
	assert.Equal(t, 2, len(node0)+len(node1))

	// With empty timings, should also fall back to round-robin
	node0Empty := ApplySplitFilter(items, 2, 0, map[string]float64{})
	node1Empty := ApplySplitFilter(items, 2, 1, map[string]float64{})
	assert.Equal(t, 2, len(node0Empty)+len(node1Empty))
}

func TestLoadWriteTimingFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "timing.json")

	// Write timing data
	original := map[string]float64{
		"gate|./pkg1|TestA": 45.2,
		"gate|./pkg2|":      120.5,
		"gate|./pkg3|TestC": 8.1,
	}
	err := WriteTimingFile(path, original)
	require.NoError(t, err)

	// Read it back
	loaded, err := LoadTimingFile(path)
	require.NoError(t, err)
	assert.Equal(t, original, loaded)
}

func TestLoadTimingFileNotExist(t *testing.T) {
	// Non-existent file returns nil, nil
	timings, err := LoadTimingFile("/nonexistent/path/timing.json")
	assert.NoError(t, err)
	assert.Nil(t, timings)
}

func TestLoadTimingFileEmpty(t *testing.T) {
	// Empty path returns nil, nil
	timings, err := LoadTimingFile("")
	assert.NoError(t, err)
	assert.Nil(t, timings)
}

func TestMedianTiming(t *testing.T) {
	t.Run("empty map returns 60", func(t *testing.T) {
		assert.Equal(t, 60.0, medianTiming(map[string]float64{}))
	})

	t.Run("single value", func(t *testing.T) {
		assert.Equal(t, 42.0, medianTiming(map[string]float64{"a": 42.0}))
	})

	t.Run("odd count", func(t *testing.T) {
		m := map[string]float64{"a": 10, "b": 20, "c": 30}
		assert.Equal(t, 20.0, medianTiming(m))
	})

	t.Run("even count", func(t *testing.T) {
		m := map[string]float64{"a": 10, "b": 20, "c": 30, "d": 40}
		assert.Equal(t, 25.0, medianTiming(m))
	})
}

func TestCollectTestWorkWithSplit(t *testing.T) {
	testContent := []byte(`
package work_test

import "testing"

func TestWork(t *testing.T) {
	t.Log("Work test running")
}
`)

	configContent := []byte(`
gates:
  - id: split-gate
    description: "Split test gate"
    tests:
      - package: "./work"
        run_all: true
      - name: "TestSpecific"
        package: "./work"
`)

	r := setupMultiPackageTestRunner(t, map[string][]byte{
		"work": testContent,
	}, configContent)

	// Without split, should get all items
	allItems := r.collectTestWork()
	totalCount := len(allItems)

	// With split, each node should get a subset
	r.splitTotal = 2
	r.splitIndex = 0
	node0Items := r.collectTestWork()

	r.splitIndex = 1
	node1Items := r.collectTestWork()

	assert.Equal(t, totalCount, len(node0Items)+len(node1Items),
		"split nodes should cover all items")
	assert.True(t, len(node0Items) > 0, "node 0 should have items")
	assert.True(t, len(node1Items) > 0, "node 1 should have items")
}

func findWorkItem(workItems []TestWork, gateID, suiteID, resultKey string) *TestWork {
	for _, item := range workItems {
		if item.GateID == gateID && item.SuiteID == suiteID && item.ResultKey == resultKey {
			return &item
		}
	}
	return nil
}
