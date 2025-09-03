package runner

import (
	"testing"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResultHierarchyManager_AddTestToResults(t *testing.T) {
	rhm := NewResultHierarchyManager()
	startTime := time.Now()
	result := rhm.CreateEmptyResult("test-run-id", startTime)

	// Create test result
	testResult := &types.TestResult{
		Metadata: types.ValidatorMetadata{
			FuncName: "TestExample",
			Package:  "./example",
		},
		Status:   types.TestStatusPass,
		Duration: 100 * time.Millisecond,
		SubTests: make(map[string]*types.TestResult),
	}

	// Test 1: Add direct gate test
	rhm.AddTestToResults(result, "test-gate", "", "TestExample", testResult)

	// Verify gate was created and test was added
	require.Contains(t, result.Gates, "test-gate")
	gate := result.Gates["test-gate"]
	assert.Equal(t, "test-gate", gate.ID)
	require.Contains(t, gate.Tests, "TestExample")
	assert.Equal(t, testResult, gate.Tests["TestExample"])

	// Verify statistics were updated
	assert.Equal(t, 1, result.Stats.Total)
	assert.Equal(t, 1, result.Stats.Passed)
	assert.Equal(t, 0, result.Stats.Failed)
	assert.Equal(t, 1, gate.Stats.Total)
	assert.Equal(t, 1, gate.Stats.Passed)

	// Test 2: Add suite test
	suiteTestResult := &types.TestResult{
		Metadata: types.ValidatorMetadata{
			FuncName: "TestSuiteExample",
			Package:  "./suite",
		},
		Status:   types.TestStatusFail,
		Duration: 200 * time.Millisecond,
		SubTests: make(map[string]*types.TestResult),
	}

	rhm.AddTestToResults(result, "test-gate", "test-suite", "TestSuiteExample", suiteTestResult)

	// Verify suite was created and test was added
	require.Contains(t, gate.Suites, "test-suite")
	suite := gate.Suites["test-suite"]
	assert.Equal(t, "test-suite", suite.ID)
	require.Contains(t, suite.Tests, "TestSuiteExample")
	assert.Equal(t, suiteTestResult, suite.Tests["TestSuiteExample"])

	// Verify statistics were updated for suite, gate, and overall
	assert.Equal(t, 2, result.Stats.Total)
	assert.Equal(t, 1, result.Stats.Passed)
	assert.Equal(t, 1, result.Stats.Failed)
	assert.Equal(t, 2, gate.Stats.Total)
	assert.Equal(t, 1, gate.Stats.Failed)
	assert.Equal(t, 1, suite.Stats.Total)
	assert.Equal(t, 1, suite.Stats.Failed)
}

func TestResultHierarchyManager_AddTestWithSubTests(t *testing.T) {
	rhm := NewResultHierarchyManager()
	startTime := time.Now()
	result := rhm.CreateEmptyResult("test-run-id", startTime)

	// Create test result with subtests
	subTest1 := &types.TestResult{
		Status:   types.TestStatusPass,
		Duration: 50 * time.Millisecond,
	}
	subTest2 := &types.TestResult{
		Status:   types.TestStatusFail,
		Duration: 75 * time.Millisecond,
	}

	testResult := &types.TestResult{
		Metadata: types.ValidatorMetadata{
			FuncName: "TestWithSubtests",
			Package:  "./example",
		},
		Status:   types.TestStatusFail, // Failed because one subtest failed
		Duration: 125 * time.Millisecond,
		SubTests: map[string]*types.TestResult{
			"SubTest1": subTest1,
			"SubTest2": subTest2,
		},
	}

	rhm.AddTestToResults(result, "test-gate", "", "TestWithSubtests", testResult)

	// Verify statistics include subtests
	// Main test: 1 failed
	// SubTest1: 1 passed
	// SubTest2: 1 failed
	// Total: 3 tests (1 main + 2 sub)
	assert.Equal(t, 3, result.Stats.Total)
	assert.Equal(t, 1, result.Stats.Passed)
	assert.Equal(t, 2, result.Stats.Failed) // Main test + SubTest2
	assert.Equal(t, 0, result.Stats.Skipped)
}

func TestResultHierarchyManager_FinalizeResults(t *testing.T) {
	rhm := NewResultHierarchyManager()
	startTime := time.Now()
	result := rhm.CreateEmptyResult("test-run-id", startTime)

	// Add some test results
	passResult := &types.TestResult{
		Status:   types.TestStatusPass,
		Duration: 100 * time.Millisecond,
		SubTests: make(map[string]*types.TestResult),
	}
	failResult := &types.TestResult{
		Status:   types.TestStatusFail,
		Duration: 200 * time.Millisecond,
		SubTests: make(map[string]*types.TestResult),
	}

	rhm.AddTestToResults(result, "gate1", "", "TestPass", passResult)
	rhm.AddTestToResults(result, "gate1", "suite1", "TestFail", failResult)
	rhm.AddTestToResults(result, "gate2", "", "TestPass2", passResult)

	// Sleep a bit to ensure timing differences
	time.Sleep(10 * time.Millisecond)

	// Finalize results
	rhm.FinalizeResults(result, startTime)

	// Verify overall status determination
	assert.Equal(t, types.TestStatusFail, result.Status) // Has failures

	// Verify gate statuses
	gate1 := result.Gates["gate1"]
	assert.Equal(t, types.TestStatusFail, gate1.Status) // Has failure in suite
	gate2 := result.Gates["gate2"]
	assert.Equal(t, types.TestStatusPass, gate2.Status) // Only passing tests

	// Verify suite status
	suite1 := gate1.Suites["suite1"]
	assert.Equal(t, types.TestStatusFail, suite1.Status) // Has failing test

	// Verify timing is set
	assert.True(t, result.Duration > 0)
	assert.False(t, result.Stats.EndTime.IsZero())
	assert.False(t, gate1.Stats.EndTime.IsZero())
	assert.False(t, suite1.Stats.EndTime.IsZero())
}

func TestResultHierarchyManager_CreateEmptyResult(t *testing.T) {
	rhm := NewResultHierarchyManager()
	startTime := time.Now()
	runID := "test-run-123"

	result := rhm.CreateEmptyResult(runID, startTime)

	assert.Equal(t, runID, result.RunID)
	assert.Equal(t, startTime, result.Stats.StartTime)
	assert.Equal(t, types.TestStatusSkip, result.Status)
	assert.NotNil(t, result.Gates)
	assert.Len(t, result.Gates, 0)
	assert.Equal(t, 0, result.Stats.Total)
}

func TestResultHierarchyManager_EnsureGateExists(t *testing.T) {
	rhm := NewResultHierarchyManager()
	result := rhm.CreateEmptyResult("test-run", time.Now())

	// Test creating new gate
	gate1 := rhm.ensureGateExists(result, "new-gate")
	assert.Equal(t, "new-gate", gate1.ID)
	assert.NotNil(t, gate1.Tests)
	assert.NotNil(t, gate1.Suites)
	assert.Contains(t, result.Gates, "new-gate")

	// Test getting existing gate
	gate2 := rhm.ensureGateExists(result, "new-gate")
	assert.Equal(t, gate1, gate2) // Should be the same instance
}

func TestResultHierarchyManager_EnsureSuiteExists(t *testing.T) {
	rhm := NewResultHierarchyManager()
	result := rhm.CreateEmptyResult("test-run", time.Now())
	gate := rhm.ensureGateExists(result, "test-gate")

	// Test creating new suite
	suite1 := rhm.ensureSuiteExists(gate, "new-suite")
	assert.Equal(t, "new-suite", suite1.ID)
	assert.NotNil(t, suite1.Tests)
	assert.Contains(t, gate.Suites, "new-suite")

	// Test getting existing suite
	suite2 := rhm.ensureSuiteExists(gate, "new-suite")
	assert.Equal(t, suite1, suite2) // Should be the same instance
}

func TestResultHierarchyManager_ReusesBetweenSerialAndParallel(t *testing.T) {
	// This test demonstrates that the same logic works for both execution paths
	rhm := NewResultHierarchyManager()

	// Simulate serial-style usage
	serialResult := rhm.CreateEmptyResult("serial-run", time.Now())
	testResult1 := &types.TestResult{
		Status:   types.TestStatusPass,
		Duration: 100 * time.Millisecond,
		SubTests: make(map[string]*types.TestResult),
	}
	rhm.AddTestToResults(serialResult, "gate1", "suite1", "Test1", testResult1)

	// Simulate parallel-style usage with the same manager
	parallelResult := rhm.CreateEmptyResult("parallel-run", time.Now())
	testResult2 := &types.TestResult{
		Status:   types.TestStatusPass,
		Duration: 150 * time.Millisecond,
		SubTests: make(map[string]*types.TestResult),
	}
	rhm.AddTestToResults(parallelResult, "gate1", "suite1", "Test2", testResult2)

	// Both should have the same structure
	assert.Contains(t, serialResult.Gates, "gate1")
	assert.Contains(t, parallelResult.Gates, "gate1")
	assert.Contains(t, serialResult.Gates["gate1"].Suites, "suite1")
	assert.Contains(t, parallelResult.Gates["gate1"].Suites, "suite1")

	// But different test contents
	assert.Contains(t, serialResult.Gates["gate1"].Suites["suite1"].Tests, "Test1")
	assert.Contains(t, parallelResult.Gates["gate1"].Suites["suite1"].Tests, "Test2")
	assert.NotContains(t, serialResult.Gates["gate1"].Suites["suite1"].Tests, "Test2")
	assert.NotContains(t, parallelResult.Gates["gate1"].Suites["suite1"].Tests, "Test1")
}
