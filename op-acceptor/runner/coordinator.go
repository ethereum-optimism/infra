package runner

import (
	"context"
	"fmt"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/google/uuid"
)

// ParallelRunner interface for parallel execution
type ParallelRunner interface {
	RunParallel(ctx context.Context, validators []types.ValidatorMetadata, result *RunnerResult) error
	GetConcurrency() int
}

// TestCoordinator orchestrates test execution
type TestCoordinator interface {
	// Run all tests with the given validators
	Run(ctx context.Context, validators []types.ValidatorMetadata, isParallel bool) (*RunnerResult, error)

	// Run tests for a specific gate
	RunGate(ctx context.Context, gateName string, validators []types.ValidatorMetadata) (*GateResult, error)
	GetUI() ProgressIndicator
}

// testCoordinator implements TestCoordinator
type testCoordinator struct {
	executor       TestExecutor
	collector      ResultCollector
	parallelRunner ParallelRunner
	ui             ProgressIndicator
}

// NewTestCoordinator creates a new test coordinator
func NewTestCoordinator(executor TestExecutor, collector ResultCollector, parallelRunner ParallelRunner, ui ProgressIndicator) TestCoordinator {
	return &testCoordinator{
		executor:       executor,
		collector:      collector,
		parallelRunner: parallelRunner,
		ui:             ui,
	}
}

// GetUI returns the progress indicator
func (c *testCoordinator) GetUI() ProgressIndicator {
	return c.ui
}

// Run orchestrates the execution of all tests
func (c *testCoordinator) Run(ctx context.Context, validators []types.ValidatorMetadata, isParallel bool) (*RunnerResult, error) {
	runID := uuid.New().String()
	result := c.collector.NewRunResult(runID, isParallel)

	if isParallel {
		return c.runParallel(ctx, validators, result)
	}
	return c.runSerial(ctx, validators, result)
}

// RunGate runs tests for a specific gate
func (c *testCoordinator) RunGate(ctx context.Context, gateName string, validators []types.ValidatorMetadata) (*GateResult, error) {
	gateResult := &GateResult{
		ID:          gateName,
		Description: gateName,
		Tests:       make(map[string]*types.TestResult),
		Suites:      make(map[string]*SuiteResult),
		Status:      types.TestStatusPass,
	}

	// Categorize validators into suites and direct tests
	suiteValidators, directTests := c.categorizeValidators(validators)

	// Process suite tests
	for suiteName, suiteTests := range suiteValidators {
		suite := &SuiteResult{
			ID:          suiteName,
			Description: suiteName,
			Tests:       make(map[string]*types.TestResult),
			Status:      types.TestStatusPass,
		}

		for _, validator := range suiteTests {
			// Notify progress indicator that test is starting
			if c.ui != nil {
				c.ui.StartTest(validator.GetName())
			}

			testResult, err := c.executor.Execute(ctx, validator)
			if err != nil {
				log.Error("Test execution failed", "test", validator.GetName(), "error", err)
				testResult = c.newFailedTestResult(validator, err)
			}

			testKey := c.getTestKey(validator)
			suite.Tests[testKey] = testResult

			// Update suite status
			if testResult.Status == types.TestStatusFail {
				suite.Status = types.TestStatusFail
			} else if testResult.Status == types.TestStatusSkip && suite.Status != types.TestStatusFail {
				suite.Status = types.TestStatusSkip
			}
		}

		gateResult.Suites[suiteName] = suite
	}

	// Process direct tests
	for _, validator := range directTests {
		// Notify progress indicator that test is starting
		if c.ui != nil {
			c.ui.StartTest(validator.GetName())
		}

		testResult, err := c.executor.Execute(ctx, validator)
		if err != nil {
			log.Error("Test execution failed", "test", validator.GetName(), "error", err)
			testResult = c.newFailedTestResult(validator, err)
		}

		testKey := c.getTestKey(validator)
		gateResult.Tests[testKey] = testResult

		// Update gate status
		if testResult.Status == types.TestStatusFail {
			gateResult.Status = types.TestStatusFail
		} else if testResult.Status == types.TestStatusSkip && gateResult.Status != types.TestStatusFail {
			gateResult.Status = types.TestStatusSkip
		}
	}

	return gateResult, nil
}

func (c *testCoordinator) runSerial(ctx context.Context, validators []types.ValidatorMetadata, result *RunnerResult) (*RunnerResult, error) {
	// Group validators by gate
	gateGroups := c.groupValidatorsByGate(validators)

	// Process each gate
	for gateName, gateValidators := range gateGroups {
		if err := c.processGate(ctx, gateName, gateValidators, result); err != nil {
			return nil, err
		}
	}

	// Finalize results
	c.collector.FinalizeResults(result)
	return result, nil
}

func (c *testCoordinator) runParallel(ctx context.Context, validators []types.ValidatorMetadata, result *RunnerResult) (*RunnerResult, error) {
	// Use parallel runner to execute tests
	err := c.parallelRunner.RunParallel(ctx, validators, result)
	if err != nil {
		return nil, err
	}

	// Finalize results
	c.collector.FinalizeResults(result)
	return result, nil
}

func (c *testCoordinator) processGate(ctx context.Context, gateName string, validators []types.ValidatorMetadata, result *RunnerResult) error {
	// Initialize progress if UI is available
	if c.ui != nil {
		c.ui.StartGate(gateName, len(validators))
	}

	// Categorize validators
	suiteValidators, directTests := c.categorizeValidators(validators)

	// Process suites
	for suiteName, suiteTests := range suiteValidators {
		if c.ui != nil {
			c.ui.StartSuite(suiteName, len(suiteTests))
		}

		for _, validator := range suiteTests {
			// Notify progress indicator that test is starting
			if c.ui != nil {
				c.ui.StartTest(validator.GetName())
			}

			testResult, err := c.executor.Execute(ctx, validator)
			if err != nil {
				log.Error("Test execution failed", "test", validator.GetName(), "error", err)
				testResult = c.newFailedTestResult(validator, err)
			}

			c.collector.AddTestResult(result, testResult, gateName, suiteName)

			if c.ui != nil {
				c.ui.UpdateTest(validator.GetName(), testResult.Status)
			}
		}

		if c.ui != nil {
			c.ui.CompleteSuite(suiteName)
		}
	}

	// Process direct tests
	for _, validator := range directTests {
		// Notify progress indicator that test is starting
		if c.ui != nil {
			c.ui.StartTest(validator.GetName())
		}

		testResult, err := c.executor.Execute(ctx, validator)
		if err != nil {
			log.Error("Test execution failed", "test", validator.GetName(), "error", err)
			testResult = c.newFailedTestResult(validator, err)
		}

		c.collector.AddTestResult(result, testResult, gateName, "")

		if c.ui != nil {
			c.ui.UpdateTest(validator.GetName(), testResult.Status)
		}
	}

	if c.ui != nil {
		c.ui.CompleteGate(gateName)
	}

	return nil
}

func (c *testCoordinator) groupValidatorsByGate(validators []types.ValidatorMetadata) map[string][]types.ValidatorMetadata {
	groups := make(map[string][]types.ValidatorMetadata)
	for _, v := range validators {
		gateName := v.Gate
		if gateName == "" {
			gateName = "default"
		}
		groups[gateName] = append(groups[gateName], v)
	}
	return groups
}

func (c *testCoordinator) categorizeValidators(validators []types.ValidatorMetadata) (map[string][]types.ValidatorMetadata, []types.ValidatorMetadata) {
	suiteValidators := make(map[string][]types.ValidatorMetadata)
	var directTests []types.ValidatorMetadata

	for _, v := range validators {
		if v.Suite != "" {
			suiteValidators[v.Suite] = append(suiteValidators[v.Suite], v)
		} else {
			directTests = append(directTests, v)
		}
	}

	return suiteValidators, directTests
}

func (c *testCoordinator) getTestKey(validator types.ValidatorMetadata) string {
	if validator.GetName() != "" {
		return fmt.Sprintf("%s::%s", validator.Package, validator.GetName())
	}
	return validator.Package
}

func (c *testCoordinator) newFailedTestResult(metadata types.ValidatorMetadata, err error) *types.TestResult {
	return &types.TestResult{
		Metadata: metadata,
		Status:   types.TestStatusFail,
		Error:    err,
	}
}
