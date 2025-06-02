package nat

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/urfave/cli/v2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"

	"github.com/ethereum-optimism/infra/op-acceptor/addons"
	"github.com/ethereum-optimism/infra/op-acceptor/exitcodes"
	"github.com/ethereum-optimism/infra/op-acceptor/logging"
	"github.com/ethereum-optimism/infra/op-acceptor/metrics"
	"github.com/ethereum-optimism/infra/op-acceptor/registry"
	"github.com/ethereum-optimism/infra/op-acceptor/runner"
	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/ethereum-optimism/optimism/devnet-sdk/shell/env"
	"github.com/ethereum-optimism/optimism/op-service/cliapp"
	"github.com/google/uuid"
)

// nat implements the cliapp.Lifecycle interface.
var _ cliapp.Lifecycle = &nat{}

// nat is a Network Acceptance Tester that runs tests.
type nat struct {
	ctx         context.Context
	config      *Config
	version     string
	registry    *registry.Registry
	runner      runner.TestRunner
	result      *runner.RunnerResult
	fileLogger  *logging.FileLogger
	networkName string

	running atomic.Bool
	done    chan struct{}
	wg      sync.WaitGroup

	addonsManager *addons.AddonsManager

	tracer           trace.Tracer
	shutdownCallback func(error) // Callback to signal application shutdown
}

func New(ctx context.Context, config *Config, version string, shutdownCallback func(error)) (*nat, error) {
	if config == nil {
		return nil, errors.New("config is required")
	}

	reg, err := registry.NewRegistry(registry.Config{
		Log:                 config.Log,
		ValidatorConfigFile: config.ValidatorConfig,
		DefaultTimeout:      config.DefaultTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create registry: %w", err)
	}

	envURL := os.Getenv(env.EnvURLVar)
	if envURL == "" {
		return nil, fmt.Errorf("devnet environment URL not provided: %s environment variable is required", env.EnvURLVar)
	}

	devnetEnv, err := env.LoadDevnetFromURL(envURL)
	if err != nil {
		return nil, fmt.Errorf("failed to load devnet environment from %s: %w", envURL, err)
	}

	networkName := extractNetworkName(devnetEnv)
	config.Log.Info("Using network name for metrics", "network", networkName)

	// Create runner with registry
	testRunner, err := runner.NewTestRunner(runner.Config{
		Registry:           reg,
		WorkDir:            config.TestDir,
		Log:                config.Log,
		TargetGate:         config.TargetGate,
		GoBinary:           config.GoBinary,
		AllowSkips:         config.AllowSkips,
		OutputRealtimeLogs: config.OutputRealtimeLogs,
		TestLogLevel:       config.TestLogLevel,
		NetworkName:        networkName,
		DevnetEnv:          devnetEnv,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create test runner: %w", err)
	}

	// FileLogger will be created when we have a valid runID
	// It's initialized during the first test run

	config.Log.Debug("Created NAT with config",
		"testDir", config.TestDir,
		"validatorConfig", config.ValidatorConfig,
		"targetGate", config.TargetGate,
		"runInterval", config.RunInterval,
		"runOnce", config.RunOnce,
		"allowSkips", config.AllowSkips,
		"goBinary", config.GoBinary,
		"logDir", config.LogDir,
		"network", networkName,
	)

	res := &nat{
		ctx:              ctx,
		config:           config,
		version:          version,
		registry:         reg,
		runner:           testRunner,
		done:             make(chan struct{}),
		shutdownCallback: shutdownCallback,
		networkName:      networkName,
		tracer:           otel.Tracer("op-acceptor"),
	}

	// Create addons manager
	addonsOpts := []addons.Option{}
	features := devnetEnv.Env.Features
	if !slices.Contains(features, "faucet") {
		addonsOpts = append(addonsOpts, addons.WithFaucet())
	}

	addonsManager, err := addons.NewAddonsManager(ctx, devnetEnv, addonsOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create addons manager: %w", err)
	}
	res.addonsManager = addonsManager
	return res, nil
}

// extractNetworkName extracts the network name from the DEVNET_ENV_URL.
func extractNetworkName(env *env.DevnetEnv) string {
	fallbackName := "unknown"
	if env == nil {
		return fallbackName
	}

	// Extract name from the devnet environment
	if env.Env.Name != "" {
		return env.Env.Name
	}

	// If name is empty in the environment, return unknown
	log.Debug("Devnet environment has empty name")
	return fallbackName
}

// Start runs the acceptance tests periodically at the configured interval.
// Start implements the cliapp.Lifecycle interface.
func (n *nat) Start(ctx context.Context) error {
	// Set up panic recovery to ensure we exit with code 2 for runtime errors
	defer func() {
		if r := recover(); r != nil {
			n.config.Log.Error("Runtime error occurred", "error", r)
			os.Exit(exitcodes.RuntimeErr)
		}
	}()

	ctx, span := n.tracer.Start(ctx, "acceptance tests")
	defer span.End()

	if err := n.addonsManager.Start(ctx); err != nil {
		return fmt.Errorf("failed to start addons: %w", err)
	}

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
		"config.ValidatorConfig", n.config.ValidatorConfig,
		"config.LogDir", n.config.LogDir)

	// Run tests immediately on startup
	err := n.runTests(ctx)
	if err != nil {
		// For runtime errors (like panics or configuration issues), return exit code 2
		n.config.Log.Error("Runtime error running tests", "error", err)
		return cli.Exit(err.Error(), 2)
	}

	// If in run-once mode, trigger shutdown and return
	if n.config.RunOnce {
		n.config.Log.Debug("Tests completed, exiting (run-once mode)")

		// Check if any tests failed and return appropriate exit code
		if n.result != nil && n.result.Status == types.TestStatusFail {
			n.config.Log.Warn("Run-once test run completed with failures")
			return NewTestFailureError("Run-once test run completed with failures")
		}

		// Only need to call this when we're in run-once mode and all tests passed
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
				if err := n.runTests(ctx); err != nil {
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
func (n *nat) runTests(ctx context.Context) error {
	n.config.Log.Info("Running all tests...")

	// Generate a runID for this test run
	runID := uuid.New().String()
	n.config.Log.Info("Generated new runID for test run", "runID", runID)

	// Create a new file logger with the runID
	fileLogger, err := logging.NewFileLogger(n.config.LogDir, runID, n.networkName, n.config.TargetGate)
	if err != nil {
		n.config.Log.Error("Error creating file logger", "error", err)
		return fmt.Errorf("failed to create file logger: %w", err)
	}

	// Save the new logger
	n.fileLogger = fileLogger

	// Update the runner with the new file logger
	if n.runner != nil {
		if fileLoggerRunner, ok := n.runner.(runner.TestRunnerWithFileLogger); ok {
			fileLoggerRunner.SetFileLogger(n.fileLogger)
		} else {
			n.config.Log.Error("Runner does not implement TestRunnerWithFileLogger interface")
		}
	}

	// Run the tests with our new logger
	result, err := n.runner.RunAllTests(ctx)
	if err != nil {
		// This is a runtime error (not a test failure)
		n.config.Log.Error("Runtime error running tests", "error", err)
		return NewRuntimeError(err)
	}
	n.result = result

	// We should have the same runID from the test run result
	if result.RunID != runID {
		n.config.Log.Warn("RunID from result doesn't match expected runID",
			"expected", runID, "actual", result.RunID)
	}

	// Save the test results to files
	err = n.saveTestResults(result)
	if err != nil {
		n.config.Log.Error("Error saving test results to files", "error", err)
		// Continue execution despite file saving errors
	}

	reproBlurb := "\nTo reproduce this run, set the following environment variables:\n" + n.runner.ReproducibleEnv().String()

	n.config.Log.Info("Printing results table")
	n.printResultsTable(result.RunID)
	for _, line := range strings.Split(reproBlurb, "\n") {
		n.config.Log.Info(line)
	}

	// Complete the file logging
	if err := n.fileLogger.Complete(result.RunID); err != nil {
		n.config.Log.Error("Error completing file logging", "error", err)
	}

	// Save the original detailed summary to the all.log file
	resultSummary := n.result.String() + reproBlurb

	// Get the all.log file path
	allLogsFile, err := n.fileLogger.GetAllLogsFileForRunID(result.RunID)
	if err != nil {
		n.config.Log.Error("Error getting all.log file path", "error", err)
	} else {
		// Write the complete detailed summary to all.log
		// We don't need the separate detailed-summary.log file anymore
		if err := os.WriteFile(allLogsFile, []byte(resultSummary), 0644); err != nil {
			n.config.Log.Error("Error saving detailed summary to all.log file", "error", err)
		}
	}

	// Get the raw_go_events.log file path
	rawEventsFile, err := n.fileLogger.GetRawEventsFileForRunID(result.RunID)
	if err != nil {
		n.config.Log.Error("Error getting raw_go_events.log file path", "error", err)
	} else {
		n.config.Log.Info("Raw Go test events saved", "file", rawEventsFile)
	}

	if n.result.Status == types.TestStatusFail && result.Stats.Failed > result.Stats.Passed {
		printGandalf()
	}

	// Get log directory for this run
	logDir, err := n.fileLogger.GetDirectoryForRunID(result.RunID)
	if err != nil {
		n.config.Log.Error("Error getting log directory path", "error", err)
		// Use default base directory as fallback
		logDir = n.fileLogger.GetBaseDir()
	}

	// Record metrics for the test run
	metrics.RecordAcceptance(
		n.networkName,
		result.RunID,
		string(n.result.Status),
		n.result.Stats.Total,
		n.result.Stats.Passed,
		n.result.Stats.Failed,
		n.result.Duration,
	)

	// Record metrics for individual tests
	for _, gate := range n.result.Gates {
		// Record direct gate tests
		for testName, test := range gate.Tests {
			metrics.RecordIndividualTest(
				n.networkName,
				result.RunID,
				testName,
				gate.ID,
				"", // No suite for direct gate tests
				test.Status,
				test.Duration,
			)

			// Record subtests if present
			for subTestName, subTest := range test.SubTests {
				metrics.RecordIndividualTest(
					n.networkName,
					result.RunID,
					subTestName,
					gate.ID,
					"", // No suite for direct gate tests
					subTest.Status,
					subTest.Duration,
				)
			}
		}

		// Record suite tests
		for suiteName, suite := range gate.Suites {
			for testName, test := range suite.Tests {
				metrics.RecordIndividualTest(
					n.networkName,
					result.RunID,
					testName,
					gate.ID,
					suiteName,
					test.Status,
					test.Duration,
				)

				// Record subtests if present
				for subTestName, subTest := range test.SubTests {
					metrics.RecordIndividualTest(
						n.networkName,
						result.RunID,
						subTestName,
						gate.ID,
						suiteName,
						subTest.Status,
						subTest.Duration,
					)
				}
			}
		}
	}

	n.config.Log.Info("Test run completed",
		"run_id", result.RunID,
		"status", n.result.Status,
		"log_dir", logDir,
		"results_html", filepath.Join(logDir, logging.HTMLResultsFilename),
	)
	return nil
}

// saveTestResults saves the test results to files
func (n *nat) saveTestResults(result *runner.RunnerResult) error {
	// Process each gate
	for _, gate := range result.Gates {
		// Process direct tests for each gate
		for _, test := range gate.Tests {
			if err := n.fileLogger.LogTestResult(test, result.RunID); err != nil {
				return fmt.Errorf("failed to save test result for %s: %w", test.Metadata.FuncName, err)
			}
		}

		// Process suite tests for each gate
		for _, suite := range gate.Suites {
			for _, test := range suite.Tests {
				if err := n.fileLogger.LogTestResult(test, result.RunID); err != nil {
					return fmt.Errorf("failed to save test result for %s: %w", test.Metadata.FuncName, err)
				}
			}
		}
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

	n.config.Log.Debug("Stopping addons")
	if err := n.addonsManager.Stop(ctx); err != nil {
		return fmt.Errorf("failed to stop addons: %w", err)
	}

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
	t.SetTitle(fmt.Sprintf("Acceptance Testing Results (network: %s)", n.networkName))

	// Configure columns
	t.AppendHeader(table.Row{
		"Type", "ID", "Duration", "Tests", "Passed", "Failed", "Skipped", "Status",
	})

	// Set column configurations for better readability
	t.SetColumnConfigs([]table.ColumnConfig{
		{Name: "Type", AutoMerge: true},
		{Name: "ID", WidthMax: 200, WidthMaxEnforcer: text.WrapSoft},
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
			"", // No stdout for gates
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
				"", // No stdout for suites
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
	})

	t.Render()
}

// Helper function to convert bool to int
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// getResultString returns a human-readable string for a test status
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

// formatDuration formats a duration in a readable format
func formatDuration(d time.Duration) string {
	return d.Round(time.Second).String()
}

// WaitForShutdown waits for all goroutines to finish
func (n *nat) WaitForShutdown(ctx context.Context) error {
	timeout := time.NewTimer(time.Second * 5)
	defer timeout.Stop()

	doneCh := make(chan struct{})
	go func() {
		n.wg.Wait()
		close(doneCh)
	}()

	select {
	case <-doneCh:
		return nil
	case <-timeout.C:
		return errors.New("timed out waiting for nat to shutdown")
	case <-ctx.Done():
		return ctx.Err()
	}
}
