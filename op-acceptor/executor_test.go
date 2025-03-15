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

// MockExecutorRunner is a mock implementation of the TestRunner interface for testing the executor
type MockExecutorRunner struct {
	mock.Mock
}

func (m *MockExecutorRunner) RunAllTests() (*runner.RunnerResult, error) {
	args := m.Called()
	result := args.Get(0)
	err := args.Error(1)
	if result == nil {
		return nil, err
	}
	return result.(*runner.RunnerResult), err
}

func (m *MockExecutorRunner) RunTest(metadata types.ValidatorMetadata) (*types.TestResult, error) {
	args := m.Called(metadata)
	result := args.Get(0)
	err := args.Error(1)
	if result == nil {
		return nil, err
	}
	return result.(*types.TestResult), err
}

// TestDefaultTestExecutor_RunTests_Success tests the success path of the DefaultTestExecutor
func TestDefaultTestExecutor_RunTests_Success_Standalone(t *testing.T) {
	// Create mock runner
	mockRunner := new(MockExecutorRunner)

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

// TestDefaultTestExecutor_RunTests_Error tests the error handling path of the DefaultTestExecutor
func TestDefaultTestExecutor_RunTests_Error_Standalone(t *testing.T) {
	// Create mock runner
	mockRunner := new(MockExecutorRunner)

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
