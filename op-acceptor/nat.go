package nat

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"

	"github.com/ethereum-optimism/infra/op-acceptor/metrics"
	"github.com/ethereum-optimism/infra/op-acceptor/registry"
	"github.com/ethereum-optimism/infra/op-acceptor/runner"
	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/ethereum-optimism/optimism/op-service/cliapp"
)

// nat implements the cliapp.Lifecycle interface.
var _ cliapp.Lifecycle = &nat{}

// Exit codes
const (
	ExitCodeSuccess     = 0 // Tests passed or skipped
	ExitCodeTestFailure = 1 // At least one test failed
	ExitCodeSystemError = 2 // An error occurred in the application
)

// nat is a Network Acceptance Tester that runs tests.
type nat struct {
	ctx      context.Context
	config   *Config
	version  string
	registry *registry.Registry
	runner   runner.TestRunner
	result   *runner.RunnerResult

	running atomic.Bool
	done    chan struct{}
	wg      sync.WaitGroup

	shutdownCallback func(error) // Callback to signal application shutdown
}

// TestFailureError is a custom error type that includes an exit code
type TestFailureError struct {
	msg      string
	exitCode int
	status   types.TestStatus
	cause    error // Underlying error that caused the failure
}

// Error implements the error interface
func (e *TestFailureError) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %v", e.msg, e.cause)
	}
	return e.msg
}

// ExitCode returns the exit code to use
func (e *TestFailureError) ExitCode() int {
	return e.exitCode
}

// Status returns the test status
func (e *TestFailureError) Status() types.TestStatus {
	return e.status
}

// Unwrap implements the errors.Unwrap interface for error chains
func (e *TestFailureError) Unwrap() error {
	return e.cause
}

// NewTestFailureError creates a new test failure error from a test status
func NewTestFailureError(status types.TestStatus) *TestFailureError {
	exitCode := ExitCodeSuccess
	if status == types.TestStatusFail {
		exitCode = ExitCodeTestFailure
	}

	return &TestFailureError{
		msg:      fmt.Sprintf("tests completed with status: %s", status),
		exitCode: exitCode,
		status:   status,
	}
}

// NewRunnerError creates an error for when the test runner fails
func NewRunnerError(err error) *TestFailureError {
	return &TestFailureError{
		msg:      "test runner error",
		exitCode: ExitCodeSystemError,
		status:   types.TestStatusFail,
		cause:    err,
	}
}

func New(ctx context.Context, config *Config, version string, shutdownCallback func(error)) (*nat, error) {
	if config == nil {
		return nil, errors.New("config is required")
	}

	config.Log.Debug("Creating NAT with config",
		"testDir", config.TestDir,
		"validatorConfig", config.ValidatorConfig,
		"runInterval", config.RunInterval,
		"runOnce", config.RunOnce)

	reg, err := registry.NewRegistry(registry.Config{
		Log:                 config.Log,
		ValidatorConfigFile: config.ValidatorConfig,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create registry: %w", err)
	}

	// Create runner with registry
	testRunner, err := runner.NewTestRunner(runner.Config{
		Registry:   reg,
		WorkDir:    config.TestDir,
		Log:        config.Log,
		TargetGate: config.TargetGate,
		GoBinary:   config.GoBinary,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create test runner: %w", err)
	}
	config.Log.Info("nat.New: created registry and test runner")

	return &nat{
		ctx:              ctx,
		config:           config,
		version:          version,
		registry:         reg,
		runner:           testRunner,
		done:             make(chan struct{}),
		shutdownCallback: shutdownCallback,
	}, nil
}

// Start runs the acceptance tests periodically at the configured interval.
// Start implements the cliapp.Lifecycle interface.
func (n *nat) Start(ctx context.Context) error {
	n.ctx = ctx
	n.done = make(chan struct{})
	n.running.Store(true)

	if n.config.RunOnce {
		n.config.Log.Info("Starting op-acceptor in run-once mode")
	} else {
		n.config.Log.Info("Starting op-acceptor in continuous mode", "interval", n.config.RunInterval)
	}

	n.config.Log.Debug("NAT config paths",
		"config.TestDir", n.config.TestDir,
		"config.ValidatorConfig", n.config.ValidatorConfig)

	// Run tests immediately on startup
	err := n.runTests()

	// If there was a runner error, return it immediately
	var runnerErr *TestFailureError
	if err != nil && errors.As(err, &runnerErr) && runnerErr.cause != nil {
		// This is a runner error (not a test failure)
		return err
	}

	// If in run-once mode, trigger shutdown and return
	if n.config.RunOnce {
		n.config.Log.Info("Tests completed, exiting (run-once mode)")

		// Use a goroutine to avoid blocking in Start()
		go func() {
			// For test failures, pass the error to the shutdown callback
			n.shutdownCallback(err)
		}()

		// Always return nil from Start in run-once mode when it's a test failure
		// as we're handling it via the shutdown callback
		return nil
	}

	// In continuous mode, log errors but continue running
	if err != nil {
		n.config.Log.Error("Initial test run failed", "error", err)
		// We don't return the error here as we want to continue
		// running tests periodically in continuous mode
	}

	// Start a goroutine for periodic test execution
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		n.config.Log.Debug("Starting periodic test runner goroutine", "interval", n.config.RunInterval)

		for {
			select {
			case <-time.After(n.config.RunInterval):
				// Check if we should still be running
				if !n.running.Load() {
					n.config.Log.Debug("Service stopped, exiting periodic test runner")
					return
				}

				// Run tests
				n.config.Log.Info("Running periodic tests")
				if err := n.runTests(); err != nil {
					n.config.Log.Error("Error running periodic tests", "error", err)
				}
				n.config.Log.Info("Test run interval", "interval", n.config.RunInterval)

			case <-n.done:
				n.config.Log.Debug("Done signal received, stopping periodic test runner")
				return

			case <-ctx.Done():
				n.config.Log.Debug("Context canceled, stopping periodic test runner")
				n.running.Store(false)
				return
			}
		}
	}()
	n.config.Log.Debug("op-acceptor started successfully")
	return nil
}

// runTests runs all tests and processes the results
func (n *nat) runTests() error {
	n.config.Log.Info("Running all tests...")
	result, err := n.runner.RunAllTests()
	if err != nil {
		n.config.Log.Error("Error running tests", "error", err)
		// Create a proper runner error and propagate it
		return NewRunnerError(err)
	}

	n.result = result

	n.printResultsTable(result.RunID)
	fmt.Println(n.result.String())
	if n.result.Status == types.TestStatusFail {
		printGandalf()
	}

	n.config.Log.Info("Test run completed", "run_id", result.RunID, "status", n.result.Status)

	// If tests failed, return an appropriate error
	if n.result.Status == types.TestStatusFail {
		return NewTestFailureError(n.result.Status)
	}

	return nil
}

// Stop stops the op-acceptor service.
// Stop implements the cliapp.Lifecycle interface.
func (n *nat) Stop(ctx context.Context) error {
	n.config.Log.Info("Stopping op-acceptor")

	// Check if we're already stopped
	if !n.running.Load() {
		n.config.Log.Debug("Service already stopped, nothing to do")
		return nil
	}

	// Update running state first to prevent new test runs
	n.running.Store(false)

	// Signal goroutines to exit
	n.config.Log.Debug("Sending done signal to goroutines")
	close(n.done)

	n.config.Log.Info("op-acceptor stopped successfully")
	return nil
}

// Stopped returns true if the op-acceptor service is stopped.
// Stopped implements the cliapp.Lifecycle interface.
func (n *nat) Stopped() bool {
	return !n.running.Load()
}

// printResultsTable prints the results of the acceptance tests to the console.
func (n *nat) printResultsTable(runID string) {
	n.config.Log.Info("Printing results...")
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetTitle(fmt.Sprintf("Acceptance Testing Results (%s)", formatDuration(n.result.Duration)))

	// Configure columns
	t.AppendHeader(table.Row{
		"Type", "ID", "Duration", "Tests", "Passed", "Failed", "Skipped", "Status", "Error",
	})

	// Set column configurations for better readability
	t.SetColumnConfigs([]table.ColumnConfig{
		{Name: "Type", AutoMerge: true},
		{Name: "ID", WidthMax: 50, WidthMaxEnforcer: text.WrapSoft},
		{Name: "Duration", Align: text.AlignRight},
		{Name: "Tests", Align: text.AlignRight},
		{Name: "Passed", Align: text.AlignRight},
		{Name: "Failed", Align: text.AlignRight},
		{Name: "Skipped", Align: text.AlignRight},
	})

	// Add flag to show individual tests for packages
	showIndividualTests := true

	// Print gates and their results
	for _, gate := range n.result.Gates {
		// Gate row - show test counts but no "1" in Tests column
		t.AppendRow(table.Row{
			"Gate",
			gate.ID,
			formatDuration(gate.Duration),
			"-", // Don't count gate as a test
			gate.Stats.Passed,
			gate.Stats.Failed,
			gate.Stats.Skipped,
			getResultString(gate.Status),
			"",
		})

		// Print suites in this gate
		for suiteName, suite := range gate.Suites {
			t.AppendRow(table.Row{
				"Suite",
				fmt.Sprintf("├── %s", suiteName),
				formatDuration(suite.Duration),
				"-", // Don't count suite as a test
				suite.Stats.Passed,
				suite.Stats.Failed,
				suite.Stats.Skipped,
				getResultString(suite.Status),
				"",
			})

			// Print tests in this suite
			i := 0
			for testName, test := range suite.Tests {
				prefix := "│   ├──"
				if i == len(suite.Tests)-1 {
					prefix = "│   └──"
				}

				// Get a display name for the test
				displayName := types.GetTestDisplayName(testName, test.Metadata)

				// Display the test result
				t.AppendRow(table.Row{
					"Test",
					fmt.Sprintf("%s %s", prefix, displayName),
					formatDuration(test.Duration),
					"1", // Count actual test
					boolToInt(test.Status == types.TestStatusPass),
					boolToInt(test.Status == types.TestStatusFail),
					boolToInt(test.Status == types.TestStatusSkip),
					getResultString(test.Status),
					test.Error,
				})

				// Display individual sub-tests if present (for package tests)
				if len(test.SubTests) > 0 && showIndividualTests {
					j := 0
					for subTestName, subTest := range test.SubTests {
						subPrefix := "│   │   ├──"
						if j == len(test.SubTests)-1 {
							subPrefix = "│   │   └──"
						}

						t.AppendRow(table.Row{
							"",
							fmt.Sprintf("%s %s", subPrefix, subTestName),
							formatDuration(subTest.Duration),
							"1", // Count actual test
							boolToInt(subTest.Status == types.TestStatusPass),
							boolToInt(subTest.Status == types.TestStatusFail),
							boolToInt(subTest.Status == types.TestStatusSkip),
							getResultString(subTest.Status),
							subTest.Error,
						})
						j++
					}
				}

				i++
			}
		}

		// Print direct gate tests
		i := 0
		for testName, test := range gate.Tests {
			prefix := "├──"
			if i == len(gate.Tests)-1 && len(gate.Suites) == 0 {
				prefix = "└──"
			}

			// Get a display name for the test
			displayName := types.GetTestDisplayName(testName, test.Metadata)

			// Display the test result
			t.AppendRow(table.Row{
				"Test",
				fmt.Sprintf("%s %s", prefix, displayName),
				formatDuration(test.Duration),
				"1", // Count actual test
				boolToInt(test.Status == types.TestStatusPass),
				boolToInt(test.Status == types.TestStatusFail),
				boolToInt(test.Status == types.TestStatusSkip),
				getResultString(test.Status),
				test.Error,
			})

			// Display individual sub-tests if present (for package tests)
			if len(test.SubTests) > 0 && showIndividualTests {
				j := 0
				for subTestName, subTest := range test.SubTests {
					subPrefix := "    ├──"
					if j == len(test.SubTests)-1 {
						subPrefix = "    └──"
					}

					t.AppendRow(table.Row{
						"",
						fmt.Sprintf("%s %s", subPrefix, subTestName),
						formatDuration(subTest.Duration),
						"1", // Count actual test
						boolToInt(subTest.Status == types.TestStatusPass),
						boolToInt(subTest.Status == types.TestStatusFail),
						boolToInt(subTest.Status == types.TestStatusSkip),
						getResultString(subTest.Status),
						subTest.Error,
					})
					j++
				}
			}

			i++
		}

		t.AppendSeparator()
	}

	// Update the table style setting based on result status
	if n.result.Status == types.TestStatusPass {
		t.SetStyle(table.StyleColoredBlackOnGreenWhite)
	} else if n.result.Status == types.TestStatusSkip {
		t.SetStyle(table.StyleColoredBlackOnYellowWhite)
	} else {
		t.SetStyle(table.StyleColoredBlackOnRedWhite)
	}

	// Add summary footer
	t.AppendFooter(table.Row{
		"TOTAL",
		"",
		formatDuration(n.result.Duration),
		n.result.Stats.Total, // Show total number of actual tests
		n.result.Stats.Passed,
		n.result.Stats.Failed,
		n.result.Stats.Skipped,
		getResultString(n.result.Status),
		"",
	})

	t.Render()

	// Emit metrics
	metrics.RecordAcceptance(
		"todo",
		runID,
		string(n.result.Status),
		n.result.Stats.Total,
		n.result.Stats.Passed,
		n.result.Stats.Failed,
		n.result.Duration,
	)
}

// Helper function to convert bool to int
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// getResultString returns a colored string representing the test result
func getResultString(status types.TestStatus) string {
	switch status {
	case types.TestStatusPass:
		return "✓ pass"
	case types.TestStatusSkip:
		return "- skip"
	default:
		return "✗ fail"
	}
}

// Helper function to format duration to seconds with 1 decimal place
func formatDuration(d time.Duration) string {
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// WaitForShutdown blocks until all goroutines have terminated.
// This is useful in tests to ensure complete cleanup before moving to the next test.
func (n *nat) WaitForShutdown(ctx context.Context) error {
	n.config.Log.Debug("Waiting for all goroutines to terminate")

	// Create a channel that will be closed when the WaitGroup is done
	done := make(chan struct{})
	go func() {
		n.wg.Wait()
		close(done)
	}()

	// Wait for either WaitGroup completion or context expiration
	select {
	case <-done:
		n.config.Log.Debug("All goroutines terminated successfully")
		return nil
	case <-ctx.Done():
		n.config.Log.Warn("Timed out waiting for goroutines to terminate", "error", ctx.Err())
		return ctx.Err()
	}
}

// GetExitCode returns the appropriate exit code based on test results
func (n *nat) GetExitCode() int {
	if n.result == nil {
		// No result means we had an error running tests
		return ExitCodeSystemError
	}

	if n.result.Status == types.TestStatusFail {
		// Tests failed
		return ExitCodeTestFailure
	}

	// Tests passed or were skipped
	return ExitCodeSuccess
}

// GetExitCode extracts the exit code from an error
func GetExitCode(err error) int {
	if err == nil {
		return ExitCodeSuccess
	}

	// Try to extract the exit code using errors.As for type safety
	var testErr *TestFailureError
	if errors.As(err, &testErr) {
		return testErr.ExitCode()
	}

	// Check for other error types implementing ExitCode() int
	type ExitCoder interface {
		ExitCode() int
	}
	if coder, ok := err.(ExitCoder); ok {
		return coder.ExitCode()
	}

	// Default to system error for standard errors
	return ExitCodeSystemError
}
