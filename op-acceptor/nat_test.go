package nat

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/logging"
	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"

	"github.com/ethereum-optimism/infra/op-acceptor/runner"
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

func (m *trackedMockRunner) RunTest(_ context.Context, metadata types.ValidatorMetadata) (*types.TestResult, error) {
	args := m.Called(metadata)
	return args.Get(0).(*types.TestResult), args.Error(1)
}

func (m *trackedMockRunner) RunAllTests(_ context.Context) (*runner.RunnerResult, error) {
	args := m.Called()

	// Track execution and signal on channel
	m.execCount.Add(1)
	select {
	case m.execCh <- struct{}{}:
	default: // Don't block if channel buffer is full
	}

	return args.Get(0).(*runner.RunnerResult), args.Error(1)
}

func (m *trackedMockRunner) ReproducibleEnv() runner.Env {
	return runner.Env{}
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
	mockFileLogger, err := logging.NewFileLogger(t.TempDir(), "test-run-id", "test-network", "test-gate")
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
		tracer:     otel.Tracer("test"),
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

// TestNAT_New_FastFailOnDevnetEnvErrors tests that New fails fast when devnet environment cannot be loaded
func TestNAT_New_FastFailOnDevnetEnvErrors(t *testing.T) {
	// Save and restore original environment variable
	originalEnv := os.Getenv("DEVNET_ENV_URL")
	defer func() {
		if originalEnv != "" {
			os.Setenv("DEVNET_ENV_URL", originalEnv)
		} else {
			os.Unsetenv("DEVNET_ENV_URL")
		}
	}()

	// Helper function to create a valid validator config file
	createValidatorConfig := func(t *testing.T) string {
		validatorConfigDir := t.TempDir()
		validatorConfigFile := validatorConfigDir + "/validators.yaml"
		validatorConfig := `
gates:
  - id: test-gate
    description: "Test gate"
    tests:
      - name: TestExample
        package: "./example"
`
		err := os.WriteFile(validatorConfigFile, []byte(validatorConfig), 0644)
		require.NoError(t, err)
		return validatorConfigFile
	}

	// Helper function to create config
	createConfig := func(t *testing.T, validatorConfigFile string) *Config {
		logger := log.New()
		return &Config{
			Log:             logger,
			ValidatorConfig: validatorConfigFile,
			TestDir:         t.TempDir(),
			TargetGate:      "test-gate",
		}
	}

	t.Run("missing environment variable", func(t *testing.T) {
		// Unset the environment variable
		os.Unsetenv("DEVNET_ENV_URL")

		validatorConfigFile := createValidatorConfig(t)
		config := createConfig(t, validatorConfigFile)
		ctx := context.Background()

		// This should fail with a specific error about missing environment variable
		_, err := New(ctx, config, "test-version", func(error) {})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "devnet environment URL not provided")
		assert.Contains(t, err.Error(), "environment variable is required")
	})

	t.Run("non-existent file (absolute path)", func(t *testing.T) {
		// Set environment variable to a non-existent file path
		nonExistentFile := "/path/to/non/existent/file.json"
		os.Setenv("DEVNET_ENV_URL", nonExistentFile)

		validatorConfigFile := createValidatorConfig(t)
		config := createConfig(t, validatorConfigFile)
		ctx := context.Background()

		// This should fail with a specific error about failing to load devnet environment
		_, err := New(ctx, config, "test-version", func(error) {})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to load devnet environment from")
		assert.Contains(t, err.Error(), nonExistentFile)
	})

	t.Run("non-existent file (relative path)", func(t *testing.T) {
		// Set environment variable to a relative path that doesn't exist
		nonExistentRelativeFile := "./does/not/exist.json"
		os.Setenv("DEVNET_ENV_URL", nonExistentRelativeFile)

		validatorConfigFile := createValidatorConfig(t)
		config := createConfig(t, validatorConfigFile)
		ctx := context.Background()

		// This should fail with a specific error about failing to load devnet environment
		_, err := New(ctx, config, "test-version", func(error) {})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to load devnet environment from")
		assert.Contains(t, err.Error(), nonExistentRelativeFile)
	})
}

// TestNAT_New_FastFailOnInvalidDevnetFile tests that New fails when devnet file is invalid
func TestNAT_New_FastFailOnInvalidDevnetFile(t *testing.T) {
	// Save and restore original environment variable
	originalEnv := os.Getenv("DEVNET_ENV_URL")
	defer func() {
		if originalEnv != "" {
			os.Setenv("DEVNET_ENV_URL", originalEnv)
		} else {
			os.Unsetenv("DEVNET_ENV_URL")
		}
	}()

	// Create a temporary invalid devnet file
	tempDir := t.TempDir()
	invalidFile := tempDir + "/invalid-devnet.json"
	err := os.WriteFile(invalidFile, []byte("invalid json content"), 0644)
	require.NoError(t, err)

	// Set environment variable to the invalid file
	os.Setenv("DEVNET_ENV_URL", invalidFile)

	// Create a valid validator config file
	validatorConfigDir := t.TempDir()
	validatorConfigFile := validatorConfigDir + "/validators.yaml"
	validatorConfig := `
gates:
  - id: test-gate
    description: "Test gate"
    tests:
      - name: TestExample
        package: "./example"
`
	err = os.WriteFile(validatorConfigFile, []byte(validatorConfig), 0644)
	require.NoError(t, err)

	ctx := context.Background()
	logger := log.New()

	config := &Config{
		Log:             logger,
		ValidatorConfig: validatorConfigFile,
		TestDir:         t.TempDir(),
		TargetGate:      "test-gate",
	}

	// This should fail with a specific error about failing to load devnet environment
	_, err = New(ctx, config, "test-version", func(error) {})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load devnet environment from")
	assert.Contains(t, err.Error(), invalidFile)
}

// TestNAT_New_SucceedsWithValidDevnetFile tests that New succeeds when devnet file is valid
func TestNAT_New_SucceedsWithValidDevnetFile(t *testing.T) {
	// Save and restore original environment variable
	originalEnv := os.Getenv("DEVNET_ENV_URL")
	defer func() {
		if originalEnv != "" {
			os.Setenv("DEVNET_ENV_URL", originalEnv)
		} else {
			os.Unsetenv("DEVNET_ENV_URL")
		}
	}()

	// Create a temporary valid devnet file
	tempDir := t.TempDir()
	validFile := tempDir + "/valid-devnet.json"
	validContent := `{
		"name": "test-network",
		"l1": {
			"name": "test-l1",
			"id": "1",
			"nodes": [],
			"addresses": {},
			"wallets": {}
		},
		"l2": []
	}`
	err := os.WriteFile(validFile, []byte(validContent), 0644)
	require.NoError(t, err)

	// Set environment variable to the valid file
	os.Setenv("DEVNET_ENV_URL", validFile)

	// Create a valid validator config file
	validatorConfigDir := t.TempDir()
	validatorConfigFile := validatorConfigDir + "/validators.yaml"
	validatorConfig := `
gates:
  - id: test-gate
    description: "Test gate"
    tests:
      - name: TestExample
        package: "./example"
`
	err = os.WriteFile(validatorConfigFile, []byte(validatorConfig), 0644)
	require.NoError(t, err)

	ctx := context.Background()
	logger := log.New()

	config := &Config{
		Log:             logger,
		ValidatorConfig: validatorConfigFile,
		TestDir:         t.TempDir(),
		TargetGate:      "test-gate",
	}

	// This should succeed
	nat, err := New(ctx, config, "test-version", func(error) {})
	require.NoError(t, err)
	require.NotNil(t, nat)

	// Check the network name
	assert.Equal(t, "test-network", nat.networkName)

	// Clean up
	_ = nat.Stop(ctx)
}
