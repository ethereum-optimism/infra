package nat

import (
	"errors"
	"testing"

	"github.com/ethereum/go-ethereum/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/ethereum-optimism/infra/op-acceptor/runner"
	"github.com/ethereum-optimism/infra/op-acceptor/types"
)

// MockTestRunnerForExecutor is a mock implementation of runner.TestRunner
type MockTestRunnerForExecutor struct {
	mock.Mock
}

func (m *MockTestRunnerForExecutor) RunAllTests() (*runner.RunnerResult, error) {
	args := m.Called()
	result := args.Get(0)
	err := args.Error(1)
	if result == nil {
		return nil, err
	}
	return result.(*runner.RunnerResult), err
}

func (m *MockTestRunnerForExecutor) RunTest(metadata types.ValidatorMetadata) (*types.TestResult, error) {
	args := m.Called(metadata)
	result := args.Get(0)
	err := args.Error(1)
	if result == nil {
		return nil, err
	}
	return result.(*types.TestResult), err
}

// TestDefaultTestExecutor_RunTests_Success tests the successful execution path
func TestDefaultTestExecutor_RunTests_Success(t *testing.T) {
	// Create mock runner
	mockRunner := new(MockTestRunnerForExecutor)

	// Create a sample successful result
	expectedResult := &runner.RunnerResult{
		RunID:  "test-run-1",
		Status: types.TestStatusPass,
		Stats: runner.ResultStats{
			Total:   5,
			Passed:  5,
			Failed:  0,
			Skipped: 0,
		},
	}

	// Set up expectation - RunAllTests should be called once and return our expected result
	mockRunner.On("RunAllTests").Return(expectedResult, nil)

	// Create logger
	logger := log.New()

	// Create the executor with our mock runner
	executor := &DefaultTestExecutor{
		runner: mockRunner,
		logger: logger,
	}

	// Call RunTests method
	result, err := executor.RunTests()

	// Verify expectations
	mockRunner.AssertExpectations(t)

	// Check assertions
	assert.NoError(t, err)
	assert.Equal(t, expectedResult, result)
}

// TestDefaultTestExecutor_RunTests_Error tests the error handling path
func TestDefaultTestExecutor_RunTests_Error(t *testing.T) {
	// Create mock runner
	mockRunner := new(MockTestRunnerForExecutor)

	// Create an expected error
	expectedError := errors.New("test runner error")

	// Set up expectation - RunAllTests should be called once and return an error
	mockRunner.On("RunAllTests").Return(nil, expectedError)

	// Create logger
	logger := log.New()

	// Create the executor with our mock runner
	executor := &DefaultTestExecutor{
		runner: mockRunner,
		logger: logger,
	}

	// Call RunTests method
	result, err := executor.RunTests()

	// Verify expectations
	mockRunner.AssertExpectations(t)

	// Check assertions
	assert.Error(t, err)
	assert.Equal(t, expectedError, err)
	assert.Nil(t, result)
}
