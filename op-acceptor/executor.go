package nat

import (
	"github.com/ethereum/go-ethereum/log"

	"github.com/ethereum-optimism/infra/op-acceptor/runner"
)

// TestExecutor is responsible for running tests.
type TestExecutor interface {
	RunTests() (*runner.RunnerResult, error)
}

// DefaultTestExecutor implements the TestExecutor interface.
type DefaultTestExecutor struct {
	runner runner.TestRunner
	logger log.Logger
}

// NewDefaultTestExecutor creates a new DefaultTestExecutor.
func NewDefaultTestExecutor(runner runner.TestRunner, logger log.Logger) *DefaultTestExecutor {
	return &DefaultTestExecutor{
		runner: runner,
		logger: logger,
	}
}

// RunTests runs all tests and returns the results.
func (e *DefaultTestExecutor) RunTests() (*runner.RunnerResult, error) {
	e.logger.Info("Running all tests...")
	result, err := e.runner.RunAllTests()
	if err != nil {
		e.logger.Error("Error running tests", "error", err)
		return nil, err
	}
	e.logger.Info("Test run completed", "run_id", result.RunID, "status", result.Status)
	return result, nil
}
