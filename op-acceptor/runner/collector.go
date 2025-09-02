package runner

import (
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
)

var _ ResultCollector = (*resultCollector)(nil)

// ResultCollector handles aggregation of test results
type ResultCollector interface {
	// Initialize a new run result
	NewRunResult(runID string, isParallel bool) *RunnerResult

	// Add test result to appropriate gate and suite
	AddTestResult(result *RunnerResult, test *types.TestResult, gateName string, suiteName string)

	// Update statistics for a test result
	UpdateStats(result *RunnerResult, gate *GateResult, suite *SuiteResult, test *types.TestResult)

	// Finalize results and calculate statuses
	FinalizeResults(result *RunnerResult)
}

// resultCollector implements ResultCollector
type resultCollector struct{}

// NewResultCollector creates a new result collector
func NewResultCollector() ResultCollector {
	return &resultCollector{}
}

// NewRunResult initializes a new run result
func (c *resultCollector) NewRunResult(runID string, isParallel bool) *RunnerResult {
	return &RunnerResult{
		Gates:      make(map[string]*GateResult),
		Status:     types.TestStatusFail,
		RunID:      runID,
		IsParallel: isParallel,
		Stats: ResultStats{
			StartTime: time.Now(),
		},
	}
}

// AddTestResult adds a test result to the appropriate gate and suite
func (c *resultCollector) AddTestResult(result *RunnerResult, test *types.TestResult, gateName string, suiteName string) {
	if result == nil {
		panic("result cannot be nil")
	}
	if test == nil {
		panic("test cannot be nil")
	}
	if gateName == "" {
		gateName = "default"
	}
	// Get or create gate
	gate, exists := result.Gates[gateName]
	if !exists {
		gate = &GateResult{
			ID:          gateName,
			Description: gateName,
			Tests:       make(map[string]*types.TestResult),
			Suites:      make(map[string]*SuiteResult),
			Status:      types.TestStatusFail, // Default to FAIL for safety - will be recalculated in FinalizeResults
			Stats: ResultStats{
				StartTime: time.Now(),
			},
		}
		result.Gates[gateName] = gate
	}

	// If suite is specified, add to suite
	if suiteName != "" {
		suite, exists := gate.Suites[suiteName]
		if !exists {
			suite = &SuiteResult{
				ID:          suiteName,
				Description: suiteName,
				Tests:       make(map[string]*types.TestResult),
				Status:      types.TestStatusFail, // Default to FAIL for safety - will be recalculated in FinalizeResults
				Stats: ResultStats{
					StartTime: time.Now(),
				},
			}
			gate.Suites[suiteName] = suite
		}

		testKey := c.getTestKey(test.Metadata)
		suite.Tests[testKey] = test
		c.UpdateStats(result, gate, suite, test)
	} else {
		// Add directly to gate
		testKey := c.getTestKey(test.Metadata)
		gate.Tests[testKey] = test
		c.UpdateStats(result, gate, nil, test)
	}
}

// UpdateStats updates statistics for the result hierarchy
func (c *resultCollector) UpdateStats(result *RunnerResult, gate *GateResult, suite *SuiteResult, test *types.TestResult) {
	// Update test counts and durations
	if suite != nil {
		c.updateStatsForContainer(&suite.Stats, &suite.Duration, suite.Status, test.Duration)
		suite.Stats.Total++
		c.updateStatusCounts(&suite.Stats, test.Status)
	}

	c.updateStatsForContainer(&gate.Stats, &gate.Duration, gate.Status, test.Duration)
	gate.Stats.Total++
	c.updateStatusCounts(&gate.Stats, test.Status)

	c.updateStatsForContainer(&result.Stats, &result.Duration, result.Status, test.Duration)
	result.Stats.Total++
	c.updateStatusCounts(&result.Stats, test.Status)
}

// FinalizeResults calculates final statuses and wall clock times
func (c *resultCollector) FinalizeResults(result *RunnerResult) {
	// Calculate wall clock time for the entire run
	result.Stats.EndTime = time.Now()
	result.WallClockTime = result.Stats.EndTime.Sub(result.Stats.StartTime)

	// Process each gate
	for _, gate := range result.Gates {
		// Process suites within gate
		for _, suite := range gate.Suites {
			suite.Stats.EndTime = time.Now()
			suite.WallClockTime = suite.Stats.EndTime.Sub(suite.Stats.StartTime)
			suite.Status = c.determineSuiteStatus(suite)
		}

		// Calculate gate status and wall clock time
		gate.Stats.EndTime = time.Now()
		gate.WallClockTime = gate.Stats.EndTime.Sub(gate.Stats.StartTime)
		gate.Status = c.determineGateStatus(gate)
	}

	// Calculate overall run status
	result.Status = c.determineRunnerStatus(result)
}

func (c *resultCollector) updateStatsForContainer(stats *ResultStats, duration *time.Duration, status types.TestStatus, testDuration time.Duration) {
	*duration += testDuration
}

func (c *resultCollector) updateStatusCounts(stats *ResultStats, status types.TestStatus) {
	switch status {
	case types.TestStatusPass:
		stats.Passed++
	case types.TestStatusFail:
		stats.Failed++
	case types.TestStatusSkip:
		stats.Skipped++
	}
}

func (c *resultCollector) determineGateStatus(gate *GateResult) types.TestStatus {
	allSkipped := true
	anyFailed := false

	// Check direct tests
	for _, test := range gate.Tests {
		if test.Status != types.TestStatusSkip {
			allSkipped = false
		}
		if test.Status == types.TestStatusFail {
			anyFailed = true
		}
	}

	// Check suite tests
	for _, suite := range gate.Suites {
		if suite.Status != types.TestStatusSkip {
			allSkipped = false
		}
		if suite.Status == types.TestStatusFail {
			anyFailed = true
		}
	}

	return determineStatusFromFlags(allSkipped, anyFailed)
}

func (c *resultCollector) determineRunnerStatus(result *RunnerResult) types.TestStatus {
	allSkipped := true
	anyFailed := false

	for _, gate := range result.Gates {
		if gate.Status != types.TestStatusSkip {
			allSkipped = false
		}
		if gate.Status == types.TestStatusFail {
			anyFailed = true
		}
	}

	return determineStatusFromFlags(allSkipped, anyFailed)
}

// determineStatusFromFlags returns a status based on test results.
// It prioritizes failures over skips - if any test failed, the overall status is fail.
func determineStatusFromFlags(allSkipped, anyFailed bool) types.TestStatus {
	if anyFailed {
		return types.TestStatusFail
	}
	if allSkipped {
		return types.TestStatusSkip
	}
	return types.TestStatusPass
}

func (c *resultCollector) determineSuiteStatus(suite *SuiteResult) types.TestStatus {
	allSkipped := true
	anyFailed := false

	for _, test := range suite.Tests {
		if test.Status != types.TestStatusSkip {
			allSkipped = false
		}
		if test.Status == types.TestStatusFail {
			anyFailed = true
		}
	}

	return determineStatusFromFlags(allSkipped, anyFailed)
}

func (c *resultCollector) getTestKey(metadata types.ValidatorMetadata) string {
	if metadata.FuncName != "" {
		return metadata.Package + "::" + metadata.FuncName
	}
	return metadata.Package
}
