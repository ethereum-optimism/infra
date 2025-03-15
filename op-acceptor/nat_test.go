package nat

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/ethereum-optimism/infra/op-acceptor/registry"
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

	// Create service with the mock
	service := &nat{
		ctx: ctx,
		config: &Config{
			Log:         logger,
			RunInterval: 25 * time.Millisecond, // Short interval for testing
		},
		runner: mockRunner,
		done:   make(chan struct{}),
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

func TestNAT_Start_RunsTestsImmediately(t *testing.T) {
	// ARRANGE
	cfg := getTestConfig(t, false)
	mockRunner := &mockTestRunner{}
	testNAT := getNAT(t, cfg, mockRunner, nil)

	// ACT
	err := testNAT.Start(context.Background())

	// ASSERT
	require.NoError(t, err)
	assert.Equal(t, 1, mockRunner.runCount, "Tests should be run immediately on startup")
}

func TestNAT_Start_RunsTestsPeriodically(t *testing.T) {
	// ARRANGE
	cfg := getTestConfig(t, false)
	cfg.RunInterval = 100 * time.Millisecond
	mockRunner := &mockTestRunner{}
	testNAT := getNAT(t, cfg, mockRunner, nil)

	// ACT
	err := testNAT.Start(context.Background())

	// ASSERT
	require.NoError(t, err)
	assert.Equal(t, 1, mockRunner.runCount, "Tests should be run immediately on startup")

	// Wait for at least one periodic run (plus some buffer time)
	time.Sleep(150 * time.Millisecond)
	assert.GreaterOrEqual(t, mockRunner.runCount, 2, "Tests should be run periodically")
}

func TestNAT_Stop_CleansUpResources(t *testing.T) {
	// ARRANGE
	cfg := getTestConfig(t, false)
	mockRunner := &mockTestRunner{}
	testNAT := getNAT(t, cfg, mockRunner, nil)

	// Start the service
	ctx, cancel := context.WithCancel(context.Background())
	err := testNAT.Start(ctx)
	require.NoError(t, err)

	// ACT
	cancel() // Cancel the context
	err = testNAT.Stop(ctx)

	// ASSERT
	require.NoError(t, err)
	assert.False(t, testNAT.running.Load(), "NAT should not be running after stop")
}

func TestNAT_Context_Cancellation(t *testing.T) {
	// ARRANGE
	cfg := getTestConfig(t, false)
	cfg.RunInterval = 10 * time.Millisecond
	mockRunner := &mockTestRunner{}
	testNAT := getNAT(t, cfg, mockRunner, nil)

	// Start the service with a cancellable context
	ctx, cancel := context.WithCancel(context.Background())
	err := testNAT.Start(ctx)
	require.NoError(t, err)

	// ACT
	// Wait for at least one periodic run
	time.Sleep(20 * time.Millisecond)
	cancel() // Cancel the context
	time.Sleep(20 * time.Millisecond)

	// ASSERT
	assert.False(t, testNAT.running.Load(), "NAT should stop running when context is canceled")
}

func TestNAT_RunOnceMode(t *testing.T) {
	// ARRANGE
	cfg := getTestConfig(t, true) // Run once mode
	mockRunner := &mockTestRunner{}

	// Create a channel to signal when shutdown is called
	shutdownCh := make(chan error, 1)
	shutdownCallback := func(err error) {
		shutdownCh <- err
	}

	testNAT := getNAT(t, cfg, mockRunner, shutdownCallback)

	// ACT
	err := testNAT.Start(context.Background())

	// ASSERT
	require.NoError(t, err)
	assert.Equal(t, 1, mockRunner.runCount, "Tests should be run immediately on startup")

	// Wait for shutdown to be called with a timeout
	select {
	case err := <-shutdownCh:
		// No error should be passed when tests pass
		assert.Nil(t, err, "No error should be passed when tests pass")
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Shutdown callback was not called within the timeout period")
	}
}

func TestNAT_RunOnceMode_WithFailedTests(t *testing.T) {
	// ARRANGE
	cfg := getTestConfig(t, true) // Run once mode
	mockRunner := &mockTestRunner{
		failTests: true, // Simulate failed tests
	}

	// Create a channel to signal when shutdown is called
	shutdownCh := make(chan error, 1)
	shutdownCallback := func(err error) {
		shutdownCh <- err
	}

	testNAT := getNAT(t, cfg, mockRunner, shutdownCallback)

	// ACT
	err := testNAT.Start(context.Background())

	// ASSERT
	require.NoError(t, err)
	assert.Equal(t, 1, mockRunner.runCount, "Tests should be run immediately on startup")

	// Wait for shutdown to be called with a timeout
	var capturedError error
	select {
	case capturedError = <-shutdownCh:
		// Good, the callback was called
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Shutdown callback was not called within the timeout period")
	}

	// Verify we received an error related to test failure
	require.NotNil(t, capturedError, "Expected an error to be passed to shutdown callback")
	assert.Contains(t, capturedError.Error(), "fail", "Error should mention test failure")
}

func TestGetExitCode(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected int
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: ExitCodeSuccess,
		},
		{
			name:     "standard error",
			err:      errors.New("some error"),
			expected: ExitCodeSystemError,
		},
		{
			name:     "custom error with exit code",
			err:      &TestFailureError{msg: "test failed", exitCode: ExitCodeTestFailure},
			expected: ExitCodeTestFailure,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			code := GetExitCode(test.err)
			assert.Equal(t, test.expected, code, "Exit code should match expected value")
		})
	}
}

func TestTestFailureError(t *testing.T) {
	tests := []struct {
		name         string
		status       types.TestStatus
		expectedCode int
		expectedMsg  string
	}{
		{
			name:         "pass status",
			status:       types.TestStatusPass,
			expectedCode: ExitCodeSuccess,
			expectedMsg:  "tests completed with status: pass",
		},
		{
			name:         "skip status",
			status:       types.TestStatusSkip,
			expectedCode: ExitCodeSuccess,
			expectedMsg:  "tests completed with status: skip",
		},
		{
			name:         "fail status",
			status:       types.TestStatusFail,
			expectedCode: ExitCodeTestFailure,
			expectedMsg:  "tests completed with status: fail",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := NewTestFailureError(test.status)
			assert.Equal(t, test.expectedCode, err.ExitCode(), "Exit code should match expected value")
			assert.Equal(t, test.expectedMsg, err.Error(), "Error message should match expected value")
		})
	}
}

func TestNAT_GetExitCode(t *testing.T) {
	// Create test cases for different result states
	testCases := []struct {
		name       string
		resultNil  bool
		testStatus types.TestStatus
		expected   int
	}{
		{
			name:      "nil result",
			resultNil: true,
			expected:  ExitCodeSystemError,
		},
		{
			name:       "passing tests",
			testStatus: types.TestStatusPass,
			expected:   ExitCodeSuccess,
		},
		{
			name:       "skipped tests",
			testStatus: types.TestStatusSkip,
			expected:   ExitCodeSuccess,
		},
		{
			name:       "failed tests",
			testStatus: types.TestStatusFail,
			expected:   ExitCodeTestFailure,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a new nat instance with a mock runner
			cfg := getTestConfig(t, false)
			testNAT := getNAT(t, cfg, &mockTestRunner{}, nil)

			// Set up the result based on test case
			if tc.resultNil {
				testNAT.result = nil
			} else {
				testNAT.result = &runner.RunnerResult{
					Status: tc.testStatus,
				}
			}

			// Get the exit code
			code := testNAT.GetExitCode()

			// Verify the exit code matches expectations
			assert.Equal(t, tc.expected, code, "Exit code should match expected value")
		})
	}
}

func TestNewRunnerError(t *testing.T) {
	// Create a basic error to wrap
	baseErr := errors.New("underlying error")

	// Create a runner error
	runnerErr := NewRunnerError(baseErr)

	// Verify its properties
	assert.Equal(t, ExitCodeSystemError, runnerErr.ExitCode(), "Runner error should have system error exit code")
	assert.Equal(t, types.TestStatusFail, runnerErr.Status(), "Runner error should have fail status")
	assert.Contains(t, runnerErr.Error(), "test runner error", "Error message should indicate test runner error")
	assert.ErrorIs(t, runnerErr, baseErr, "Underlying error should be preserved")
}

func TestErrorPropagationInRunOnceMode(t *testing.T) {
	// ARRANGE
	// Create error scenarios to test
	testCases := []struct {
		name             string
		setupRunner      func(*mockTestRunner)
		expectStartError bool // Expect error from Start
		expectCallback   bool // Expect callback to be called
		expectedCode     int  // Expected exit code
	}{
		{
			name: "runner returns error",
			setupRunner: func(m *mockTestRunner) {
				m.runError = errors.New("runner execution error")
			},
			expectStartError: true,  // Runner errors are returned from Start
			expectCallback:   false, // No callback for runner errors
			expectedCode:     ExitCodeSystemError,
		},
		{
			name: "tests pass",
			setupRunner: func(m *mockTestRunner) {
				m.failTests = false
			},
			expectStartError: false, // No error from Start
			expectCallback:   true,  // Callback called with nil
			expectedCode:     ExitCodeSuccess,
		},
		{
			name: "tests fail",
			setupRunner: func(m *mockTestRunner) {
				m.failTests = true
			},
			expectStartError: false, // No error from Start
			expectCallback:   true,  // Callback with test failure error
			expectedCode:     ExitCodeTestFailure,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create config for run-once mode
			cfg := getTestConfig(t, true)
			mockRunner := &mockTestRunner{}

			// Apply test-specific setup
			tc.setupRunner(mockRunner)

			// Create a channel to signal when shutdown is called
			shutdownCh := make(chan error, 1)
			shutdownCallback := func(err error) {
				shutdownCh <- err
			}

			testNAT := getNAT(t, cfg, mockRunner, shutdownCallback)

			// ACT
			err := testNAT.Start(context.Background())

			// ASSERT
			if tc.expectStartError {
				// For runner errors, Start should return an error
				require.Error(t, err, "Start should return runner errors")

				// Verify the exit code
				var exitCode int
				if coder, ok := err.(interface{ ExitCode() int }); ok {
					exitCode = coder.ExitCode()
				} else {
					exitCode = ExitCodeSystemError
				}
				assert.Equal(t, tc.expectedCode, exitCode, "Error should have expected exit code")

				// No callback should be called for runner errors
				select {
				case callbackErr := <-shutdownCh:
					t.Fatalf("Unexpected shutdown callback with error: %v", callbackErr)
				case <-time.After(50 * time.Millisecond):
					// Expected - no callback
				}

				return
			}

			// For test results (pass/fail), Start should not return an error
			require.NoError(t, err, "Start should not return error for test pass/fail")

			// Verify callback was called with expected error
			if tc.expectCallback {
				var callbackErr error
				select {
				case callbackErr = <-shutdownCh:
					// Good, callback was called
				case <-time.After(100 * time.Millisecond):
					t.Fatal("Shutdown callback not called within timeout")
				}

				if tc.name == "tests fail" {
					// For failing tests, verify the error
					require.NotNil(t, callbackErr, "Expected error for failed tests")

					// Check exit code
					var exitCode int
					if coder, ok := callbackErr.(interface{ ExitCode() int }); ok {
						exitCode = coder.ExitCode()
					} else {
						exitCode = ExitCodeSystemError
					}
					assert.Equal(t, tc.expectedCode, exitCode, "Error should have expected exit code")
				} else {
					// For passing tests, no error
					assert.Nil(t, callbackErr, "No error expected for passing tests")
				}
			}
		})
	}
}

// Helper functions

func getTestConfig(t *testing.T, runOnce bool) *Config {
	tmpDir, err := os.MkdirTemp("", "nat-test-*")
	require.NoError(t, err)
	t.Cleanup(func() {
		os.RemoveAll(tmpDir)
	})

	return &Config{
		TestDir:         tmpDir,
		ValidatorConfig: "test-validators.yaml",
		TargetGate:      "test-gate",
		GoBinary:        "go",
		RunInterval:     1 * time.Second,
		RunOnce:         runOnce,
		Log:             log.New(),
	}
}

func getNAT(t *testing.T, cfg *Config, mockRunner *mockTestRunner, shutdownCallback func(error)) *nat {
	testNAT := &nat{
		ctx:              context.Background(),
		config:           cfg,
		version:          "test-version",
		registry:         &registry.Registry{},
		runner:           mockRunner,
		result:           &runner.RunnerResult{Status: types.TestStatusPass},
		done:             make(chan struct{}),
		shutdownCallback: shutdownCallback,
	}
	mockRunner.result = testNAT.result
	return testNAT
}

// Mock test runner for testing
type mockTestRunner struct {
	runCount  int
	failTests bool
	result    *runner.RunnerResult
	runError  error // Added to simulate runner errors
}

func (m *mockTestRunner) Run(gate string) error {
	m.runCount++
	if m.failTests {
		m.result.Status = types.TestStatusFail
		return fmt.Errorf("tests failed")
	}
	m.result.Status = types.TestStatusPass
	return nil
}

func (m *mockTestRunner) RunAllTests() (*runner.RunnerResult, error) {
	m.runCount++

	// If runError is set, return that error
	if m.runError != nil {
		return nil, m.runError
	}

	if m.failTests {
		m.result.Status = types.TestStatusFail
		return m.result, nil
	}
	m.result.Status = types.TestStatusPass
	return m.result, nil
}

func (m *mockTestRunner) RunTest(metadata types.ValidatorMetadata) (*types.TestResult, error) {
	m.runCount++
	if m.failTests {
		return &types.TestResult{
			Status: types.TestStatusFail,
		}, nil
	}
	return &types.TestResult{
		Status: types.TestStatusPass,
	}, nil
}
