package runner

import (
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
)

// ResultHierarchyManager handles the creation and management of test result hierarchies
// This consolidates logic shared between serial and parallel execution paths
type ResultHierarchyManager struct{}

// NewResultHierarchyManager creates a new result hierarchy manager
func NewResultHierarchyManager() *ResultHierarchyManager {
	return &ResultHierarchyManager{}
}

// AddTestToResults adds a test result to the appropriate location in the result hierarchy
// This replaces the duplicated logic between serial processTestAndAddToResults and parallel addResultToHierarchy
func (rhm *ResultHierarchyManager) AddTestToResults(
	result *RunnerResult,
	gateID string,
	suiteID string, // Empty for gate-level tests
	testKey string,
	testResult *types.TestResult,
) {
	// Ensure gate exists
	gate := rhm.ensureGateExists(result, gateID)

	if suiteID == "" {
		// Direct gate test
		gate.Tests[testKey] = testResult
		result.updateStats(gate, nil, testResult)
	} else {
		// Suite test
		suite := rhm.ensureSuiteExists(gate, suiteID)
		suite.Tests[testKey] = testResult
		result.updateStats(gate, suite, testResult)
	}
}

// ensureGateExists creates a gate if it doesn't exist and returns it
func (rhm *ResultHierarchyManager) ensureGateExists(result *RunnerResult, gateID string) *GateResult {
	gate, exists := result.Gates[gateID]
	if !exists {
		gate = &GateResult{
			ID:            gateID,
			Tests:         make(map[string]*types.TestResult),
			Suites:        make(map[string]*SuiteResult),
			Stats:         ResultStats{StartTime: time.Now()},
			Duration:      0,
			WallClockTime: 0,
		}
		result.Gates[gateID] = gate
	}
	return gate
}

// ensureSuiteExists creates a suite if it doesn't exist and returns it
func (rhm *ResultHierarchyManager) ensureSuiteExists(gate *GateResult, suiteID string) *SuiteResult {
	suite, exists := gate.Suites[suiteID]
	if !exists {
		suite = &SuiteResult{
			ID:            suiteID,
			Tests:         make(map[string]*types.TestResult),
			Stats:         ResultStats{StartTime: time.Now()},
			Duration:      0,
			WallClockTime: 0,
		}
		gate.Suites[suiteID] = suite
	}
	return suite
}

// FinalizeResults applies final status determination and timing to all results
// This consolidates the finalization logic used in both execution paths
func (rhm *ResultHierarchyManager) FinalizeResults(result *RunnerResult, startTime time.Time) {
	endTime := time.Now()

	// Finalize all gates
	for _, gate := range result.Gates {
		// Finalize all suites in this gate
		for _, suite := range gate.Suites {
			suite.Status = determineSuiteStatus(suite)
			suite.Stats.EndTime = endTime
		}

		// Finalize gate
		gate.Status = determineGateStatus(gate)
		gate.Stats.EndTime = endTime
	}

	// Finalize overall result
	// Note: For parallel execution, Duration should remain as sum of test durations
	// and WallClockTime should be set separately. For serial execution, they are the same.
	if !result.IsParallel {
		result.Duration = time.Since(startTime)
		result.WallClockTime = result.Duration
	}
	// For parallel execution, Duration is already set by updateStats, and WallClockTime
	// should be set by the caller

	result.Status = determineRunnerStatus(result)
	result.Stats.EndTime = endTime
}

// CreateEmptyResult creates a properly initialized empty result
func (rhm *ResultHierarchyManager) CreateEmptyResult(runID string, startTime time.Time) *RunnerResult {
	return &RunnerResult{
		Gates:         make(map[string]*GateResult),
		Stats:         ResultStats{StartTime: startTime},
		RunID:         runID,
		Status:        types.TestStatusSkip,
		Duration:      0,
		WallClockTime: 0,
		IsParallel:    false, // Will be set by caller if needed
	}
}
