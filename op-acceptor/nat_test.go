package nat

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/ethereum-optimism/infra/op-acceptor/logging"
	"github.com/ethereum-optimism/infra/op-acceptor/runner"
	"github.com/ethereum-optimism/infra/op-acceptor/types"
)

// trackedMockRunner is a mock runner that counts executions and provides synchronization
type trackedMockRunner struct {
	mock.Mock
	execCount atomic.Int32  // Count of RunAllTests executions
	execCh    chan struct{} // Channel for signaling on each execution
}

func newTrackedMockRunner() *trackedMockRunner {
	return &trackedMockRunner{
		execCh: make(chan struct{}, 50),
	}
}

func (m *trackedMockRunner) RunTest(metadata types.ValidatorMetadata) (*types.TestResult, error) {
	args := m.Called(metadata)
	return args.Get(0).(*types.TestResult), args.Error(1)
}

func (m *trackedMockRunner) RunAllTests() (*runner.RunnerResult, error) {
	args := m.Called()

	// Track execution and signal on channel
	m.execCount.Add(1)
	select {
	case m.execCh <- struct{}{}:
	default: // Don't block if channel buffer is full
	}

	return args.Get(0).(*runner.RunnerResult), args.Error(1)
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

	// Set up a mock file logger
	mockFileLogger, err := logging.NewFileLogger(t.TempDir(), "test-run-id")
	require.NoError(t, err)

	// Create service with the mock
	service := &nat{
		ctx: ctx,
		config: &Config{
			Log:         logger,
			RunInterval: 25 * time.Millisecond, // Short interval for testing
			LogDir:      t.TempDir(),
		},
		runner:     mockRunner,
		fileLogger: mockFileLogger,
		done:       make(chan struct{}),
	}

	return mockRunner, service, ctx, cancel
}

// teardownTest ensures the service is fully stopped before test completion
func teardownTest(t *testing.T, service *nat, cancel context.CancelFunc) {
	t.Helper()

	// Cancel the context to ensure any hanging goroutines exit
	cancel()

	// Only attempt cleanup if the service was actually created
	if service == nil {
		return
	}

	// Call Stop directly to ensure proper cleanup
	err := service.Stop(context.Background())
	if err != nil {
		t.Logf("Warning: error stopping service during teardown: %v", err)
	}

	// Create a timeout for shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer shutdownCancel()

	// Wait for shutdown with timeout
	err = service.WaitForShutdown(shutdownCtx)
	if err != nil {
		t.Logf("Warning: service shutdown timed out: %v", err)
	}
}

// TestNAT_Start_RunsTestsImmediately tests that NAT runs tests immediately when started
func TestNAT_Start_RunsTestsImmediately(t *testing.T) {
	// Setup
	mockRunner, service, ctx, cancel := setupTest(t)
	defer teardownTest(t, service, cancel)

	// Create expected result
	result := &runner.RunnerResult{
		Status: types.TestStatusPass,
		RunID:  "test-run-id",
	}

	// Expect RunAllTests to be called once
	mockRunner.On("RunAllTests").Return(result, nil).Maybe()

	// Start the service
	err := service.Start(ctx)
	require.NoError(t, err)

	// Verify immediate execution
	assert.True(t, mockRunner.waitForExecutions(ctx, 1),
		"Expected immediate test execution")

	// Stop the service
	err = service.Stop(ctx)
	require.NoError(t, err)

	// Verify exactly one execution occurred
	assert.Equal(t, int32(1), mockRunner.execCount.Load(),
		"Expected exactly one test execution")
}

// TestNAT_Start_RunsTestsPeriodically tests that NAT runs tests periodically
func TestNAT_Start_RunsTestsPeriodically(t *testing.T) {
	// Setup
	mockRunner, service, ctx, cancel := setupTest(t)
	defer teardownTest(t, service, cancel)

	// Create expected result
	result := &runner.RunnerResult{
		Status: types.TestStatusPass,
		RunID:  "test-run-id",
	}

	// Configure mock for any number of calls
	mockRunner.On("RunAllTests").Return(result, nil).Maybe()

	// Start the service
	err := service.Start(ctx)
	require.NoError(t, err)

	// Wait for at least 3 total executions (initial + at least 2 periodic)
	assert.True(t, mockRunner.waitForExecutions(ctx, 3),
		"Expected at least 1 periodic test execution after initial run")

	// Stop the service
	err = service.Stop(ctx)
	require.NoError(t, err)

	// Log the final execution count for diagnostics
	execCount := mockRunner.execCount.Load()
	t.Logf("Test executed %d times", execCount)
	assert.GreaterOrEqual(t, execCount, int32(3),
		"Expected at least 3 test executions (1 initial + 2 periodic)")
}

// TestNAT_Stop_CleansUpResources tests that the NAT service properly stops
func TestNAT_Stop_CleansUpResources(t *testing.T) {
	// Setup
	mockRunner, service, ctx, cancel := setupTest(t)
	defer teardownTest(t, service, cancel)

	// Create expected result
	result := &runner.RunnerResult{
		Status: types.TestStatusPass,
		RunID:  "test-run-id",
	}

	// Configure mock for any number of calls
	mockRunner.On("RunAllTests").Return(result, nil).Maybe()

	// Start the service
	err := service.Start(ctx)
	require.NoError(t, err)

	// Wait for the initial execution
	assert.True(t, mockRunner.waitForExecutions(ctx, 1),
		"Expected at least one test execution")

	// Verify service is running
	assert.False(t, service.Stopped())

	// Stop the service
	err = service.Stop(ctx)
	require.NoError(t, err)

	// Verify service is stopped
	assert.True(t, service.Stopped())

	// Record the execution count after stopping
	execCountAfterStop := mockRunner.execCount.Load()

	// Wait 3 intervals to ensure no more tests run after stopping
	// This gives sufficient time for any in-flight operations to complete
	time.Sleep(3 * service.config.RunInterval)

	// Verify no additional executions occurred after stopping
	assert.Equal(t, execCountAfterStop, mockRunner.execCount.Load(),
		"No additional test executions should occur after stopping the service")
}

// TestNAT_Context_Cancellation tests that the NAT service properly handles
// context cancellation
func TestNAT_Context_Cancellation(t *testing.T) {
	// Setup
	mockRunner, service, ctx, cancel := setupTest(t)
	defer teardownTest(t, service, cancel)

	// Create expected result
	result := &runner.RunnerResult{
		Status: types.TestStatusPass,
		RunID:  "test-run-id",
	}

	// Configure mock for any number of calls
	mockRunner.On("RunAllTests").Return(result, nil).Maybe()

	// Start the service
	err := service.Start(ctx)
	require.NoError(t, err)

	// Wait for the initial execution
	assert.True(t, mockRunner.waitForExecutions(ctx, 1),
		"Expected immediate test execution")

	// Verify service is running
	assert.False(t, service.Stopped())

	// Record the execution count before cancellation
	execCountBeforeCancel := mockRunner.execCount.Load()
	t.Logf("Test executed %d times before cancellation", execCountBeforeCancel)

	// Cancel the context
	cancel()

	// Wait a small amount of time for cancellation to propagate
	// Sleep a minimum of 20ms to allow context cancellation to be processed
	time.Sleep(20 * time.Millisecond)

	// Verify service is stopped after context cancellation
	assert.True(t, service.Stopped(), "Service should be stopped after context cancellation")

	// Wait 3 intervals to ensure no more tests run after cancellation
	// This gives sufficient time for any in-flight operations to complete
	time.Sleep(3 * service.config.RunInterval)

	// Verify no additional executions occurred after cancellation
	assert.Equal(t, execCountBeforeCancel, mockRunner.execCount.Load(),
		"No additional test executions should occur after context cancellation")
}

// TestNAT_RunOnceMode tests that NAT runs once and triggers shutdown in run-once mode
func TestNAT_RunOnceMode(t *testing.T) {
	// Setup
	mockRunner, service, ctx, cancel := setupTest(t)
	defer teardownTest(t, service, cancel)

	// Set run-once mode
	service.config.RunOnce = true

	// Create expected result
	result := &runner.RunnerResult{
		Status: types.TestStatusPass,
		RunID:  "test-run-id",
	}

	// Monitor for shutdown signal
	shutdownCalled := false
	service.shutdownCallback = func(err error) {
		shutdownCalled = true
	}

	// Expect RunAllTests to be called once
	mockRunner.On("RunAllTests").Return(result, nil).Once()

	// Start the service
	err := service.Start(ctx)
	require.NoError(t, err)

	// Allow time for the delayed shutdown to occur
	time.Sleep(200 * time.Millisecond)

	// Verify exactly one execution occurred and shutdown was called
	assert.Equal(t, int32(1), mockRunner.execCount.Load(),
		"Expected exactly one test execution")
	assert.True(t, shutdownCalled,
		"Expected shutdown to be called in run-once mode")
}
