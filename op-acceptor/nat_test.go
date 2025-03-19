package nat

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/ethereum-optimism/infra/op-acceptor/runner"
	"github.com/ethereum-optimism/infra/op-acceptor/types"
)

// trackedMockRunner is a mock runner that counts executions and provides synchronization
type trackedMockRunner struct {
	mock.Mock
	execCount atomic.Int32  // Count of RunAllTests executions
	execCh    chan struct{} // Channel for signaling on each execution
}

// newTrackedMockRunner creates a new runner with execution tracking
func newTrackedMockRunner() *trackedMockRunner {
	mock := &trackedMockRunner{
		execCh: make(chan struct{}, 100), // Buffer to prevent blocking
	}

	// Set up default expectation for Finalize (can be overridden in tests)
	mock.On("Finalize").Return().Maybe()

	return mock
}

func (m *trackedMockRunner) RunTest(metadata types.ValidatorMetadata) (*types.TestResult, error) {
	args := m.Called(metadata)
	return args.Get(0).(*types.TestResult), args.Error(1)
}

// RunAllTests implements the runner.TestRunner interface
func (m *trackedMockRunner) RunAllTests() (*runner.RunnerResult, error) {
	count := m.execCount.Add(1)
	args := m.Called()

	// Signal that an execution has happened
	select {
	case m.execCh <- struct{}{}:
	default:
		// Non-blocking send, just in case no one is listening
	}

	// Return based on count to make different results possible
	if count%2 == 0 {
		return args.Get(0).(*runner.RunnerResult), args.Error(1)
	}
	return args.Get(0).(*runner.RunnerResult), args.Error(1)
}

// Finalize implements the runner.TestRunner interface
func (m *trackedMockRunner) Finalize() {
	m.Called()
}

// waitForExecutions waits for a specific number of executions with timeout
func (m *trackedMockRunner) waitForExecutions(ctx context.Context, count int32) bool {
	// Create a timeout context
	timeoutCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	// Use a ticker to periodically check the execution count
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		// Check if we've reached the desired count
		if m.execCount.Load() >= count {
			return true
		}

		// Wait for either a new execution, ticker, or timeout
		select {
		case <-m.execCh:
			// An execution signal received, immediately recheck the count
			continue
		case <-ticker.C:
			// Periodic check, loop back to check the count again
			continue
		case <-timeoutCtx.Done():
			// Timeout expired
			return false
		}
	}
}

// setupTest creates a test service with a tracked mock runner
func setupTest(t *testing.T) (*trackedMockRunner, *nat, context.Context, context.CancelFunc) {
	t.Helper()

	// Create a clean context for each test with a generous timeout to prevent hangs
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

	// Create a tracked mock runner
	mockRunner := newTrackedMockRunner()

	// Create a basic logger
	logger := log.New()

	// Create service with the mock
	service := &nat{
		ctx: ctx,
		config: &Config{
			Log:         logger,
			RunInterval: 25 * time.Millisecond, // Short interval for testing
		},
		runner: mockRunner,
		done:   make(chan struct{}),
		// Add a no-op shutdown callback for tests
		shutdownCallback: func(error) {},
	}

	return mockRunner, service, ctx, cancel
}

// teardownTest ensures the service is fully stopped before test completion
func teardownTest(t *testing.T, service *nat, cancel context.CancelFunc) {
	t.Helper()

	// Cancel context first to stop background activities
	cancel()

	// Then properly stop the service
	if !service.Stopped() {
		err := service.Stop(context.Background())
		assert.NoError(t, err, "Service should stop cleanly during teardown")
	}

	// Ensure all goroutines have terminated
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := service.WaitForShutdown(ctx)
	if err != nil {
		t.Logf("Warning: Service did not shut down cleanly in teardown: %v", err)
	}
}

// TestNAT_Start_RunsTestsImmediately tests that NAT runs tests immediately when started
func TestNAT_Start_RunsTestsImmediately(t *testing.T) {
	// Setup
	mockRunner, service, ctx, cancel := setupTest(t)
	defer teardownTest(t, service, cancel)

	// Configure mock to return success
	result := &runner.RunnerResult{
		Status: types.TestStatusPass,
	}
	mockRunner.On("RunAllTests").Return(result, nil)
	mockRunner.On("Finalize").Return()

	// Start the service
	err := service.Start(ctx)
	require.NoError(t, err)

	// Wait for first execution to complete
	execCompleted := mockRunner.waitForExecutions(ctx, 1)
	require.True(t, execCompleted, "First execution should have completed")

	// Verify the runner was called once
	mockRunner.AssertNumberOfCalls(t, "RunAllTests", 1)
}

// TestNAT_Start_RunsTestsPeriodically tests that NAT runs tests periodically
func TestNAT_Start_RunsTestsPeriodically(t *testing.T) {
	// Setup
	mockRunner, service, ctx, cancel := setupTest(t)
	defer teardownTest(t, service, cancel)

	// Configure mock to return success
	result := &runner.RunnerResult{
		Status: types.TestStatusPass,
	}
	mockRunner.On("RunAllTests").Return(result, nil)
	mockRunner.On("Finalize").Return()

	// Start the service
	err := service.Start(ctx)
	require.NoError(t, err)

	// Wait for multiple executions (at least 3)
	execCompleted := mockRunner.waitForExecutions(ctx, 3)
	require.True(t, execCompleted, "Multiple executions should have completed")

	// Verify the runner was called multiple times
	callCount := mockRunner.execCount.Load()
	assert.GreaterOrEqual(t, callCount, int32(3), "Runner should be called at least 3 times")
}

// TestNAT_Context_Cancellation tests that the NAT service properly handles
// context cancellation
func TestNAT_Context_Cancellation(t *testing.T) {
	// Setup
	mockRunner, service, ctx, cancel := setupTest(t)
	defer teardownTest(t, service, cancel)

	// Configure mock to return success
	result := &runner.RunnerResult{
		Status: types.TestStatusPass,
	}
	mockRunner.On("RunAllTests").Return(result, nil)
	mockRunner.On("Finalize").Return()

	// Start the service
	err := service.Start(ctx)
	require.NoError(t, err)

	// Wait for first execution to complete
	execCompleted := mockRunner.waitForExecutions(ctx, 1)
	require.True(t, execCompleted, "First execution should have completed")

	// Record the execution count before cancellation
	execCountBeforeCancel := mockRunner.execCount.Load()

	// Cancel the context
	cancel()

	// Wait a moment for the cancellation to propagate
	time.Sleep(50 * time.Millisecond)

	// Verify service is stopped
	assert.True(t, service.Stopped(), "Service should be stopped after context cancellation")

	// Wait more time to ensure no more tests run after stopping
	time.Sleep(3 * service.config.RunInterval)

	// Verify no additional executions occurred after cancellation
	assert.Equal(t, execCountBeforeCancel, mockRunner.execCount.Load(),
		"No additional test executions should occur after context cancellation")
}

// TestNAT_RunOnceMode tests that NAT runs once and triggers shutdown in run-once mode
func TestNAT_RunOnceMode(t *testing.T) {
	// Setup
	mockRunner, service, ctx, cancel := setupTest(t)
	defer cancel()

	// Set run-once mode
	service.config.RunOnce = true

	// Configure mock for 1 call
	passResult := &runner.RunnerResult{
		Status: types.TestStatusPass,
	}
	mockRunner.On("RunAllTests").Return(passResult, nil).Once()
	mockRunner.On("Finalize").Return()

	// Start the service
	err := service.Start(ctx)
	require.NoError(t, err)

	// Wait for execution to complete
	execCompleted := mockRunner.waitForExecutions(ctx, 1)
	require.True(t, execCompleted, "Execution should have completed")

	// Verify the runner was called exactly once and doesn't continue running
	time.Sleep(3 * service.config.RunInterval)
	mockRunner.AssertNumberOfCalls(t, "RunAllTests", 1)
}

// TestExtractKeyErrorMessage tests the error message extraction functionality
func TestExtractKeyErrorMessage(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: "",
		},
		{
			name:     "precondition not met error",
			err:      fmt.Errorf("exit status 1\nsystest.go:185: precondition not met: no available wallet with balance of at least of 1000000000000000000"),
			expected: "precondition not met: no available wallet with balance of at least of 1000000000000000000",
		},
		{
			name:     "assertion failure",
			err:      fmt.Errorf("exit status 1\ntest.go:42: assertion failed: expected 5 but got 4"),
			expected: "assertion failed: expected 5 but got 4",
		},
		{
			name:     "panic error",
			err:      fmt.Errorf("exit status 2\npanic: runtime error: index out of range [10] with length 5"),
			expected: "panic: runtime error: index out of range [10] with length 5",
		},
		{
			name:     "expected vs got error",
			err:      fmt.Errorf("exit status 1\nsome_test.go:123: expected \"success\", got: \"failure\""),
			expected: "some_test.go:123: expected \"success\", got: \"failure\"",
		},
		{
			name:     "simple error",
			err:      fmt.Errorf("simple error message"),
			expected: "simple error message",
		},
		{
			name:     "multiline error without specific pattern",
			err:      fmt.Errorf("first line\nsecond line\nthird line"),
			expected: "first line",
		},
		{
			name:     "long error without newlines",
			err:      fmt.Errorf("this is a very long error message that should be truncated because it exceeds the maximum length that we want to display in our formatted output table"),
			expected: "this is a very long error message that should be truncated because it ...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractKeyErrorMessage(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}
