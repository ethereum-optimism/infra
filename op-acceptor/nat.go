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
	"github.com/urfave/cli/v2"

	"github.com/ethereum-optimism/infra/op-acceptor/metrics"
	"github.com/ethereum-optimism/infra/op-acceptor/registry"
	"github.com/ethereum-optimism/infra/op-acceptor/runner"
	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/ethereum-optimism/optimism/op-service/cliapp"
)

// nat implements the cliapp.Lifecycle interface.
var _ cliapp.Lifecycle = &nat{}

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
	if err != nil {
		// For other errors, return an ExitCoder with exit code 1
		n.config.Log.Error("Error running tests", "error", err)
		return cli.Exit(fmt.Sprintf("Error running tests: %v", err), 2)
	}

	// If in run-once mode, trigger shutdown and return
	if n.config.RunOnce {
		n.config.Log.Info("Tests completed, exiting (run-once mode)")

		// Check if any tests failed and return appropriate exit code
		if n.result != nil && n.result.Status == types.TestStatusFail {
			n.config.Log.Warn("Run-once test run completed with failures, returning exit code 1")
			// return an error with test summary
			return fmt.Errorf("Run-once test run completed with failures: %v", n.result.String())
		}

		n.config.Log.Info("Test run complete with success, returning exit code 0")

		// Only need to call this when we're in run-once mode and all tests passed, otherwise the returned error will trigger shutdown
		go func() {
			n.shutdownCallback(nil)
		}()
		return nil // Success (exit code 0)
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
		return err
	}
	n.result = result

	n.printResultsTable(result.RunID)
	fmt.Println(n.result.String())
	if n.result.Status == types.TestStatusFail {
		printGandalf()
	}
	n.config.Log.Info("Test run completed", "run_id", result.RunID, "status", n.result.Status)
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
