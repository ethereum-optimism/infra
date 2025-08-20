package nat

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/urfave/cli/v2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"

	"github.com/ethereum-optimism/infra/op-acceptor/addons"
	"github.com/ethereum-optimism/infra/op-acceptor/exitcodes"
	"github.com/ethereum-optimism/infra/op-acceptor/flags"
	"github.com/ethereum-optimism/infra/op-acceptor/logging"
	"github.com/ethereum-optimism/infra/op-acceptor/metrics"
	"github.com/ethereum-optimism/infra/op-acceptor/registry"
	"github.com/ethereum-optimism/infra/op-acceptor/reporting"
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
		Timeout:             config.Timeout,
		GatelessMode:        config.GatelessMode,
		TestDir:             config.TestDir,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create registry: %w", err)
	}

	var devnetEnv *env.DevnetEnv
	var networkName string

	// Handle different orchestrator types
	switch config.Orchestrator {
	case flags.OrchestratorSysext:
		// For sysext, we need DEVNET_ENV_URL
		envURL := config.DevnetEnvURL
		if envURL == "" {
			return nil, fmt.Errorf("devnet environment URL not provided: use --devnet-env-url flag or set DEVNET_ENV_URL environment variable for sysext orchestrator")
		}

		var err error
		devnetEnv, err = env.LoadDevnetFromURL(envURL)
		if err != nil {
			return nil, fmt.Errorf("failed to load devnet environment from %s: %w", envURL, err)
		}

		networkName = extractNetworkName(devnetEnv)
		config.Log.Info("Using sysext orchestrator with devnet environment", "network", networkName, "envURL", envURL)

	case flags.OrchestratorSysgo:
		// For sysgo, we don't need DEVNET_ENV_URL
		devnetEnv = nil
		networkName = "in-memory"
		config.Log.Info("Using sysgo orchestrator (in-memory Go)", "network", networkName)

	default:
		// This should never happen due to CLI validation, but provide a clear error message
		return nil, fmt.Errorf("invalid orchestrator: %s", config.Orchestrator)
	}

	config.Log.Info("Using network name for metrics", "network", networkName)

	// Create runner with registry
	targetGate := config.TargetGate
	if config.GatelessMode {
		targetGate = "gateless"
	}

	// Set working directory for the runner
	workDir := config.TestDir
	if config.GatelessMode {
		// For gateless mode, use the current working directory since package paths
		// are discovered relative to it and should not be adjusted
		var err error
		workDir, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get current working directory: %w", err)
		}
	} else if strings.HasSuffix(workDir, "/...") {
		// For traditional mode with "..." notation, clean the suffix
		workDir = strings.TrimSuffix(workDir, "/...")
	}

	testRunner, err := runner.NewTestRunner(runner.Config{
		Registry:           reg,
		WorkDir:            workDir,
		Log:                config.Log,
		TargetGate:         targetGate,
		GoBinary:           config.GoBinary,
		AllowSkips:         config.AllowSkips,
		OutputRealtimeLogs: config.OutputRealtimeLogs,
		TestLogLevel:       config.TestLogLevel,
		NetworkName:        networkName,
		DevnetEnv:          devnetEnv,
		Serial:             config.Serial,
		Concurrency:        config.Concurrency,
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
	if devnetEnv != nil {
		features := devnetEnv.Env.Features
		if !slices.Contains(features, "faucet") {
			addonsOpts = append(addonsOpts, addons.WithFaucet())
		}
	} else {
		// For sysgo orchestrator, we don't have devnet environment features
		// so we'll use default addons behavior (which may include faucet if needed)
		config.Log.Debug("No devnet environment available (sysgo orchestrator), using default addons configuration")
	}

	addonsManager, err := addons.NewAddonsManager(ctx, devnetEnv, addonsOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create addons manager: %w", err)
	}
	res.addonsManager = addonsManager
	return res, nil
}

// extractNetworkName extracts the network name from the devnet environment.
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
			debug.PrintStack()
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

	reproBlurb := "\nTo reproduce this run, set the following environment variables:\n" + n.runner.ReproducibleEnv().String()

	n.config.Log.Info("Printing results table")
	n.printResultsTable(result.RunID)
	for _, line := range strings.Split(reproBlurb, "\n") {
		n.config.Log.Info(line)
	}

	// Complete the file logging
	if err := n.fileLogger.CompleteWithTiming(result.RunID, n.result.WallClockTime); err != nil {
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

	// Log speedup information in debug mode
	if n.result.IsParallel {
		n.config.Log.Debug("Parallel execution completed",
			"total_test_time", n.result.Duration,
			"wall_clock_time", n.result.WallClockTime,
			"speedup", fmt.Sprintf("%.1fx", n.result.GetSpeedup()),
			"efficiency", n.result.GetEfficiencyDisplayString())
	}

	// Extract test results from the runner result
	testResults := n.extractTestResultsFromRunnerResult(n.result)

	title := fmt.Sprintf("Acceptance Testing Results (network: %s)", n.networkName)
	reporter := reporting.NewTableReporter(title, true)
	err := reporter.PrintTableFromTestResultsWithTiming(testResults, runID, n.networkName, n.config.TargetGate, n.result.WallClockTime)
	if err != nil {
		n.config.Log.Error("Failed to print results table", "error", err)
	}
}

// extractTestResultsFromRunnerResult extracts a flat list of test results from the hierarchical runner result
func (n *nat) extractTestResultsFromRunnerResult(result *runner.RunnerResult) []*types.TestResult {
	testResults := make([]*types.TestResult, 0)
	for _, gate := range result.Gates {
		// Add direct gate tests
		for _, test := range gate.Tests {
			testResults = append(testResults, test)
		}
		// Add suite tests
		for _, suite := range gate.Suites {
			for _, test := range suite.Tests {
				testResults = append(testResults, test)
			}
		}
	}
	return testResults
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
