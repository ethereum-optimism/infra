package runner

import (
	"context"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
)

// parallelRunnerAdapter adapts the existing ParallelExecutor to the ParallelRunner interface
type parallelRunnerAdapter struct {
	executor *ParallelExecutor
}

// NewParallelRunnerAdapter creates an adapter for the existing ParallelExecutor
func NewParallelRunnerAdapter(executor *ParallelExecutor) ParallelRunner {
	if executor == nil {
		return nil
	}
	return &parallelRunnerAdapter{
		executor: executor,
	}
}

// RunParallel adapts the existing ExecuteTests method to the interface
func (a *parallelRunnerAdapter) RunParallel(ctx context.Context, validators []types.ValidatorMetadata, result *RunnerResult) error {
	// Convert validators to work items
	var workItems []TestWork
	for _, validator := range validators {
		gateName := validator.Gate
		if gateName == "" {
			gateName = "default"
		}

		workItems = append(workItems, TestWork{
			Validator: validator,
			GateID:    gateName,
			SuiteID:   validator.Suite,
			ResultKey: getTestKeyFromValidator(validator),
		})
	}

	// Execute using the existing parallel executor
	executorResult, err := a.executor.ExecuteTests(ctx, workItems)
	if err != nil {
		return err
	}

	// Copy results from executor result to the provided result
	copyResults(executorResult, result)
	return nil
}

// GetConcurrency returns the current concurrency level
func (a *parallelRunnerAdapter) GetConcurrency() int {
	return a.executor.concurrency
}

// Helper functions

func getTestKeyFromValidator(validator types.ValidatorMetadata) string {
	if validator.FuncName != "" {
		return validator.Package + "::" + validator.FuncName
	}
	return validator.Package
}

func copyResults(from *RunnerResult, to *RunnerResult) {
	if from == nil || to == nil {
		return
	}

	// Copy basic fields
	to.Status = from.Status
	to.Duration = from.Duration
	to.WallClockTime = from.WallClockTime
	to.Stats = from.Stats

	// Copy gates
	if to.Gates == nil {
		to.Gates = make(map[string]*GateResult)
	}

	for gateID, gate := range from.Gates {
		to.Gates[gateID] = gate
	}
}
