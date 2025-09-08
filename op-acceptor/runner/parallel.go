package runner

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/ethereum/go-ethereum/log"
)

// TestWork represents a unit of work that can be executed in parallel
type TestWork struct {
	Validator types.ValidatorMetadata
	GateID    string
	SuiteID   string // Empty for gate-level tests
	ResultKey string // Key to use in the result map (function name or package name)
}

// TestWorkResult contains the result of executing a TestWork
type TestWorkResult struct {
	Work   TestWork
	Result *types.TestResult
	Error  error
}

// ParallelExecutor manages parallel test execution across multiple workers
type ParallelExecutor struct {
	runner      *runner
	concurrency int
	log         log.Logger
	resultMgr   *ResultHierarchyManager
	ui          ProgressIndicator
}

// NewParallelExecutor creates a new parallel test executor with validation
func NewParallelExecutor(runner *runner, concurrency int, ui ProgressIndicator) *ParallelExecutor {
	if runner == nil {
		panic("runner cannot be nil")
	}
	if concurrency < 0 {
		panic("concurrency cannot be negative")
	}

	// Log a warning for unreasonable concurrency values
	if concurrency > 32 {
		runner.log.Warn("Very high concurrency requested", "concurrency", concurrency,
			"recommendation", "Consider using lower values to avoid resource exhaustion")
	}

	return &ParallelExecutor{
		runner:      runner,
		concurrency: concurrency,
		log:         runner.log.New("component", "parallel-executor"),
		resultMgr:   NewResultHierarchyManager(),
		ui:          ui,
	}
}

// ExecuteTests runs the provided test work items in parallel and returns organized results
func (pe *ParallelExecutor) ExecuteTests(ctx context.Context, workItems []TestWork) (*RunnerResult, error) {
	start := time.Now()

	if len(workItems) == 0 {
		pe.log.Debug("No work items to execute")
		// Return empty result for consistency
		result := pe.resultMgr.CreateEmptyResult(pe.runner.runID, start)
		return result, nil
	}

	// Initialize progress tracking if progress indicator is available
	if pe.ui != nil {
		pe.initializeProgressTracking(workItems)
	}

	pe.log.Info("Starting parallel test execution", "totalTests", len(workItems), "concurrency", pe.concurrency)

	// Create channels with conservative buffering to prevent excessive memory usage
	// Buffer size should be reasonable regardless of work item count
	bufferSize := min(pe.concurrency*2, 100) // Conservative buffer: 2x concurrency or 100, whichever is smaller
	workChan := make(chan TestWork, bufferSize)
	resultChan := make(chan TestWorkResult, bufferSize)

	// Start worker goroutines
	var wg sync.WaitGroup
	for i := 0; i < pe.concurrency; i++ {
		wg.Add(1)
		go pe.worker(ctx, &wg, workChan, resultChan)
	}

	// Send work to workers
	go func() {
		defer close(workChan)
		for _, work := range workItems {
			select {
			case workChan <- work:
			case <-ctx.Done():
				pe.log.Debug("Context cancelled while sending work items")
				return
			}
		}
	}()

	// Collect results
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Create result using shared manager
	result := pe.resultMgr.CreateEmptyResult(pe.runner.runID, start)

	// Collect all errors for better error reporting
	var aggregationErrors []error
	successCount := 0

	for workResult := range resultChan {
		if workResult.Error != nil {
			pe.log.Error("Test execution failed", "test", workResult.Work.Validator.ID, "error", workResult.Error)
			aggregationErrors = append(aggregationErrors, fmt.Errorf("test %s failed: %w", workResult.Work.Validator.ID, workResult.Error))
			continue
		}

		successCount++

		// Add result to the appropriate location in the hierarchy using shared logic
		pe.resultMgr.AddTestToResults(
			result,
			workResult.Work.GateID,
			workResult.Work.SuiteID,
			workResult.Work.ResultKey,
			workResult.Result,
		)
	}

	// Return aggregated error if any tests failed
	if len(aggregationErrors) > 0 {
		pe.log.Error("Parallel execution completed with errors",
			"totalErrors", len(aggregationErrors),
			"successfulTests", successCount,
			"totalTests", len(workItems))

		// Create a comprehensive error message
		errorMsg := fmt.Sprintf("parallel execution failed: %d out of %d tests failed", len(aggregationErrors), len(workItems))
		if len(aggregationErrors) <= 3 {
			// Include individual errors if not too many
			for i, err := range aggregationErrors {
				errorMsg += fmt.Sprintf("\n  %d. %v", i+1, err)
			}
		} else {
			// Just show first few errors to avoid overwhelming output
			for i := 0; i < 3; i++ {
				errorMsg += fmt.Sprintf("\n  %d. %v", i+1, aggregationErrors[i])
			}
			errorMsg += fmt.Sprintf("\n  ... and %d more errors", len(aggregationErrors)-3)
		}
		return nil, fmt.Errorf("%s", errorMsg)
	}

	pe.log.Info("Parallel test execution completed successfully",
		"duration", time.Since(start),
		"status", result.Status,
		"totalTests", len(workItems),
		"passed", successCount)

	return result, nil
}

// worker is a goroutine that processes test work items
// It safely handles context cancellation and channel operations
func (pe *ParallelExecutor) worker(ctx context.Context, wg *sync.WaitGroup, workChan <-chan TestWork, resultChan chan<- TestWorkResult) {
	defer wg.Done()

	workerID := fmt.Sprintf("worker-%p", wg) // Simple worker identification for logging
	pe.log.Debug("Worker starting", "workerID", workerID)
	defer pe.log.Debug("Worker exiting", "workerID", workerID)

	for {
		select {
		case work, ok := <-workChan:
			if !ok {
				pe.log.Debug("Work channel closed, worker exiting", "workerID", workerID)
				return // Channel closed, worker should exit
			}

			pe.log.Debug("Worker processing test", "workerID", workerID, "test", work.Validator.ID, "gate", work.GateID, "suite", work.SuiteID)

			// Notify progress indicator that test is starting
			if pe.ui != nil {
				pe.ui.StartTest(work.Validator.GetName())
			}

			// Execute the test with proper error handling
			testResult, err := pe.runner.RunTest(ctx, work.Validator)
			if err != nil {
				pe.log.Error("Test execution failed in worker", "workerID", workerID, "test", work.Validator.ID, "error", err)
			}

			// Notify progress indicator that test completed
			if pe.ui != nil && testResult != nil {
				pe.ui.UpdateTest(work.Validator.GetName(), testResult.Status)
			}

			// Send result back with timeout protection
			select {
			case resultChan <- TestWorkResult{
				Work:   work,
				Result: testResult,
				Error:  err,
			}:
				pe.log.Debug("Worker completed test", "workerID", workerID, "test", work.Validator.ID, "status", func() string {
					if err != nil {
						return "error"
					}
					if testResult != nil {
						return string(testResult.Status)
					}
					return "unknown"
				}())
			case <-ctx.Done():
				pe.log.Debug("Context cancelled while sending result", "workerID", workerID, "test", work.Validator.ID)
				return
			}

		case <-ctx.Done():
			pe.log.Debug("Worker received context cancellation", "workerID", workerID)
			return
		}
	}
}

// collectTestWork gathers all test work items from the runner's validators
func (r *runner) collectTestWork() []TestWork {
	var workItems []TestWork

	// Group validators by gate
	gateValidators := r.groupValidatorsByGate()

	for gateName, validators := range gateValidators {
		// Split validators into suites and direct tests
		suiteValidators, directTests := r.categorizeValidators(validators)

		// Add direct gate tests
		for _, validator := range directTests {
			workItems = append(workItems, TestWork{
				Validator: validator,
				GateID:    gateName,
				SuiteID:   "", // Empty for gate-level tests
				ResultKey: r.getTestKey(validator),
			})
		}

		// Add suite tests
		for suiteName, suiteTests := range suiteValidators {
			for _, validator := range suiteTests {
				workItems = append(workItems, TestWork{
					Validator: validator,
					GateID:    gateName,
					SuiteID:   suiteName,
					ResultKey: r.getTestKey(validator),
				})
			}
		}
	}

	return workItems
}

// initializeProgressTracking sets up data structures to concurrently
// track progress for each gate and suite in the scheduled work items
func (pe *ParallelExecutor) initializeProgressTracking(workItems []TestWork) {
	pe.log.Info("Initializing parallel progress tracking")

	// Group work items by gate
	gateGroups := make(map[string][]TestWork)
	for _, item := range workItems {
		gateGroups[item.GateID] = append(gateGroups[item.GateID], item)
	}

	// Initialize progress for each gate
	for gateName, gateItems := range gateGroups {
		pe.ui.StartGate(gateName, len(gateItems))

		// Group by suite within this gate
		suiteGroups := make(map[string][]TestWork)
		for _, item := range gateItems {
			if item.SuiteID != "" {
				suiteGroups[item.SuiteID] = append(suiteGroups[item.SuiteID], item)
			}
		}

		// Initialize progress for each suite
		for suiteName, suiteItems := range suiteGroups {
			pe.ui.StartSuite(suiteName, len(suiteItems))
		}
	}
}
