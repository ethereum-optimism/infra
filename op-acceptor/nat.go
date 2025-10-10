package nat

import (
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
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

// buildEffectiveSnapshot builds a snapshot of the effective configuration for logging and artifacts
func (n *nat) buildEffectiveSnapshot(runID string) types.EffectiveConfigSnapshot {
	workDir := strings.TrimSuffix(n.config.TestDir, "/...")

	return types.EffectiveConfigSnapshot{
		Runner: types.RunnerConfigSnapshot{
			AllowSkips:       n.config.AllowSkips,
			DefaultTimeout:   n.config.DefaultTimeout,
			Timeout:          n.config.Timeout,
			Serial:           n.config.Serial,
			Concurrency:      n.config.Concurrency,
			ShowProgress:     n.config.ShowProgress,
			ProgressInterval: n.config.ProgressInterval,
		},
		Orchestration: types.OrchestrationConfigSnapshot{
			Orchestrator: n.config.Orchestrator.String(),
			DevnetEnvURL: n.config.DevnetEnvURL,
		},
		Logging: types.LoggingConfigSnapshot{
			TestLogLevel:       n.config.TestLogLevel,
			OutputRealtimeLogs: n.config.OutputRealtimeLogs,
		},
		Execution: types.ExecutionConfigSnapshot{
			RunInterval: n.config.RunInterval,
			RunOnce:     n.config.RunOnce,
			GoBinary:    n.config.GoBinary,
			TargetGate:  n.config.TargetGate,
			Gateless:    n.config.GatelessMode,
		},
		Paths: types.PathsConfigSnapshot{
			TestDir:         n.config.TestDir,
			ValidatorConfig: n.config.ValidatorConfig,
			LogDir:          n.config.LogDir,
			WorkDir:         workDir,
		},
		NetworkName: n.networkName,
		RunID:       runID,
	}
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
		ExcludeGates:        config.ExcludeGates,
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

	config.Log.Debug("Using network name for metrics", "network", networkName)

	// Create runner with registry
	targetGate := config.TargetGate
	if config.GatelessMode {
		targetGate = "gateless"
	}

	// Set working directory for the runner
	workDir := strings.TrimSuffix(config.TestDir, "/...")

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
		ShowProgress:       config.ShowProgress,
		ProgressInterval:   config.ProgressInterval,
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

	// Create addons manager (skip in dry-run)
	if true {
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
	}
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

	if n.addonsManager != nil {
		if err := n.addonsManager.Start(ctx); err != nil {
			return fmt.Errorf("failed to start addons: %w", err)
		}
	}

	n.ctx = ctx
	n.done = make(chan struct{})
	n.running.Store(true)

	if n.config.RunOnce {
		n.config.Log.Info("Starting op-acceptor in run-once mode")
	} else {
		n.config.Log.Info("Starting op-acceptor in continuous mode", "interval", n.config.RunInterval)
	}

	// Log Effective Configuration summary (INFO)
	{
		snap := n.buildEffectiveSnapshot("")
		n.config.Log.Info("Effective Configuration",
			"orchestrator", snap.Orchestration.Orchestrator,
			"devnet_env_url", snap.Orchestration.DevnetEnvURL,
			"testdir", snap.Paths.TestDir,
			"validator_config", snap.Paths.ValidatorConfig,
			"logdir", snap.Paths.LogDir,
			"workdir", snap.Paths.WorkDir,
			"go_binary", snap.Execution.GoBinary,
			"target_gate", snap.Execution.TargetGate,
			"gateless", snap.Execution.Gateless,
			"run_interval", snap.Execution.RunInterval,
			"run_once", snap.Execution.RunOnce,
			"allow_skips", snap.Runner.AllowSkips,
			"default_timeout", snap.Runner.DefaultTimeout,
			"timeout", snap.Runner.Timeout,
			"serial", snap.Runner.Serial,
			"concurrency", snap.Runner.Concurrency,
			"show_progress", snap.Runner.ShowProgress,
			"progress_interval", snap.Runner.ProgressInterval,
			"test_log_level", snap.Logging.TestLogLevel,
			"output_realtime_logs", snap.Logging.OutputRealtimeLogs,
			"network", snap.NetworkName,
		)
	}

	n.config.Log.Debug("NAT config paths",
		"config.TestDir", n.config.TestDir,
		"config.ValidatorConfig", n.config.ValidatorConfig,
		"config.LogDir", n.config.LogDir)

	// Run tests immediately on startup
	err := n.runTests(ctx)
	if err != nil {
		// Check the error type and return appropriate error for exit code handling
		// Also check the error message as a fallback in case type information is lost
		if IsTestFailureError(err) || strings.HasPrefix(err.Error(), "test failure:") {
			// Test failures should use exit code 1
			return cli.Exit(err.Error(), 1)
		}
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
	n.config.Log.Debug("Generated new runID for test run", "runID", runID)

	// Create a new file logger with the runID
	fileLogger, err := logging.NewFileLogger(n.config.LogDir, runID, n.networkName, n.config.TargetGate)
	if err != nil {
		n.config.Log.Error("Error creating file logger", "error", err)
		return fmt.Errorf("failed to create file logger: %w", err)
	}

	// Save the new logger
	n.fileLogger = fileLogger

	// Provide effective configuration snapshot to HTML sink for this run
	if sink, ok := n.fileLogger.GetSinkByType("ReportingHTMLSink"); ok {
		if htmlSink, ok := sink.(*reporting.ReportingHTMLSink); ok {
			snap := n.buildEffectiveSnapshot(runID)
			htmlSink.SetConfigSnapshot(runID, &snap)
		}
	}

	// Update the runner with the new file logger
	if n.runner != nil {
		if fileLoggerRunner, ok := n.runner.(runner.TestRunnerWithFileLogger); ok {
			fileLoggerRunner.SetFileLogger(n.fileLogger)
		} else {
			n.config.Log.Error("Runner does not implement TestRunnerWithFileLogger interface")
		}
	}

	// Run the tests - either flake-shake mode or regular mode
	// Optional flake-shake report artifact paths for final consolidated log
	var flakeReportHTML string
	var flakeReportJSON string

	if n.config.FlakeShake {
		// Run in flake-shake mode
		n.config.Log.Info("Running in flake-shake mode", "iterations", n.config.FlakeShakeIterations)

		flakeShakeRunner := runner.NewFlakeShakeRunner(n.runner, n.config.FlakeShakeIterations, n.config.Log)
		// Ensure gate is set to "gateless" when running in gateless mode
		gateForReport := n.config.TargetGate
		if n.config.GatelessMode || gateForReport == "" {
			gateForReport = "gateless"
		}
		flakeShakeReport, err := flakeShakeRunner.RunFlakeShake(ctx, gateForReport)
		if err != nil {
			n.config.Log.Error("Flake-shake analysis failed", "error", err)
			return NewRuntimeError(err)
		}

		// Save flake-shake reports (both JSON and HTML) in this run's directory
		// Set the RunID on the report for traceability
		flakeShakeReport.RunID = runID
		runDir, getDirErr := n.fileLogger.GetDirectoryForRunID(runID)
		if getDirErr != nil || runDir == "" {
			// Fallback to default layout if for some reason run dir isn't available
			runDir = filepath.Join(n.config.LogDir, "testrun-"+runID)
			_ = os.MkdirAll(runDir, 0755)
		}
		savedFiles, err := runner.SaveFlakeShakeReport(flakeShakeReport, runDir)
		if err != nil {
			n.config.Log.Error("Failed to save flake-shake reports", "error", err)
		}
		// Capture flake report filepaths
		for _, file := range savedFiles {
			if strings.HasSuffix(file, ".html") {
				flakeReportHTML = file
			} else if strings.HasSuffix(file, ".json") {
				flakeReportJSON = file
			}
		}

		// Create a summary result for display purposes
		n.result = &runner.RunnerResult{
			RunID:         runID,
			Status:        types.TestStatusPass,
			WallClockTime: time.Since(time.Now()),
			Stats: runner.ResultStats{
				Total: len(flakeShakeReport.Tests),
			},
		}

		// Print flake-shake summary
		n.printFlakeShakeSummary(flakeShakeReport)

	} else {
		// Regular test execution
		result, err := n.runner.RunAllTests(ctx)
		if err != nil {
			// Check if this is a test-related error (e.g., module resolution) vs a runtime error
			if strings.Contains(err.Error(), "is not in module") {
				// Module resolution errors should be treated as test failures, not runtime errors
				n.config.Log.Error("Test execution failed", "error", err)
				return NewTestFailureError(err.Error())
			}
			// This is a runtime error (not a test failure)
			n.config.Log.Error("Runtime error running tests", "error", err)
			return NewRuntimeError(err)
		}
		n.result = result
	}

	// We should have the same runID from the test run result (skip for flake-shake mode)
	if !n.config.FlakeShake && n.result.RunID != runID {
		n.config.Log.Warn("RunID from result doesn't match expected runID",
			"expected", runID, "actual", n.result.RunID)
	}

	// Skip regular result printing for flake-shake mode
	if !n.config.FlakeShake {
		reproBlurb := "\nTo reproduce this run, set the following environment variables:\n" + n.runner.ReproducibleEnv().String()

		n.config.Log.Debug("Printing results table")
		n.printResultsTable(n.result.RunID)
		for _, line := range strings.Split(reproBlurb, "\n") {
			n.config.Log.Info(line)
		}
	}

	// Complete the file logging
	if err := n.fileLogger.CompleteWithTiming(n.result.RunID, n.result.WallClockTime); err != nil {
		n.config.Log.Error("Error completing file logging", "error", err)
	}

	// Save the original detailed summary to the all.log file
	var resultSummary string
	if n.config.FlakeShake {
		resultSummary = "Flake-Shake Analysis Complete\n"
	} else {
		reproBlurb := "\nTo reproduce this run, set the following environment variables:\n" + n.runner.ReproducibleEnv().String()
		resultSummary = n.result.String() + reproBlurb
	}

	// Get the all.log file path
	allLogsFile, err := n.fileLogger.GetAllLogsFileForRunID(n.result.RunID)
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
	rawEventsFile, err := n.fileLogger.GetRawEventsFileForRunID(n.result.RunID)
	if err != nil {
		n.config.Log.Error("Error getting raw_go_events.log file path", "error", err)
	} else {
		n.config.Log.Info("Raw Go test events saved", "file", rawEventsFile)
	}

	if n.result.Status == types.TestStatusFail && n.result.Stats.Failed > n.result.Stats.Passed {
		printGandalf()
	}

	// Get log directory for this run
	logDir, err := n.fileLogger.GetDirectoryForRunID(n.result.RunID)
	if err != nil {
		n.config.Log.Error("Error getting log directory path", "error", err)
		// Use default base directory as fallback
		logDir = n.fileLogger.GetBaseDir()
	}

	// Write artifacts for this run
	if err := n.writeRunArtifacts(n.result.RunID); err != nil {
		n.config.Log.Error("Error writing run artifacts", "error", err)
	}

	// Record metrics for the test run
	metrics.RecordAcceptance(
		n.networkName,
		n.result.RunID,
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
				n.result.RunID,
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
					n.result.RunID,
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
					n.result.RunID,
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
						n.result.RunID,
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

	// Build consolidated fields for final log line
	fields := []interface{}{
		"run_id", n.result.RunID,
		"status", n.result.Status,
		"log_dir", logDir,
		"results_html", filepath.Join(logDir, logging.HTMLResultsFilename),
	}
	// Include flake-shake report paths if available (flake-shake mode)
	if n.config.FlakeShake {
		if flakeReportHTML != "" {
			fields = append(fields, "flake_report_html", flakeReportHTML)
		}
		if flakeReportJSON != "" {
			fields = append(fields, "flake_report_json", flakeReportJSON)
		}
	}

	n.config.Log.Info("Test run completed", fields...)
	return nil
}

// writeRunArtifacts writes basic artifacts describing the run configuration and reproduction env
func (n *nat) writeRunArtifacts(runID string) error {
	if n.fileLogger == nil {
		return nil
	}
	dir, err := n.fileLogger.GetDirectoryForRunID(runID)
	if err != nil {
		return err
	}
	// reproducible-env.txt
	envPath := filepath.Join(dir, "reproducible-env.txt")
	repro := n.runner.ReproducibleEnv().String()
	if err := os.WriteFile(envPath, []byte(repro+"\n"), 0644); err != nil {
		return fmt.Errorf("failed to write reproducible-env.txt: %w", err)
	}
	// config.json (effective snapshot)
	snap := n.buildEffectiveSnapshot(runID)
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), b, 0644); err != nil {
		return fmt.Errorf("failed to write config.json: %w", err)
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

	if n.addonsManager != nil {
		n.config.Log.Debug("Stopping addons")
		if err := n.addonsManager.Stop(ctx); err != nil {
			return fmt.Errorf("failed to stop addons: %w", err)
		}
	}

	n.config.Log.Info("op-acceptor stopped successfully")

	return nil
}

// Stopped returns true if the op-acceptor service is stopped.
// Stopped implements the cliapp.Lifecycle interface.
func (n *nat) Stopped() bool {
	return !n.running.Load()
}

// printFlakeShakeSummary prints the flake-shake analysis results to the console.
func (n *nat) printFlakeShakeSummary(report *runner.FlakeShakeReport) {
	n.config.Log.Info("Printing flake-shake results...")

	// Print the formatted table
	tableStr := n.formatFlakeShakeTable(report)
	fmt.Print(tableStr)
}

// formatFlakeShakeTable formats the flake-shake report as a table
func (n *nat) formatFlakeShakeTable(report *runner.FlakeShakeReport) string {
	var buf bytes.Buffer

	// Create table writer
	t := table.NewWriter()
	t.SetOutputMirror(&buf)
	t.SetTitle(fmt.Sprintf("Flake-Shake Analysis Results (gate: %s, iterations: %d)", report.Gate, report.Iterations))

	// Set headers
	t.AppendHeader(table.Row{"TEST NAME", "PACKAGE", "RUNS", "PASS RATE", "AVG DURATION", "RECOMMENDATION", "STATUS"})

	// Configure columns
	t.SetColumnConfigs([]table.ColumnConfig{
		{Name: "TEST NAME"},
		{Name: "PACKAGE"},
		{Name: "RUNS", Align: text.AlignRight},
		{Name: "PASS RATE", Align: text.AlignRight},
		{Name: "AVG DURATION", Align: text.AlignRight},
		{Name: "RECOMMENDATION", Align: text.AlignCenter},
		{Name: "STATUS", Align: text.AlignCenter},
	})

	// Count statistics
	stable := 0
	unstable := 0

	// Add test rows
	for _, test := range report.Tests {
		status := "✓"
		if test.PassRate < 100 {
			status = "✗"
		}

		// Format test name - extract actual test name from package if empty
		testName := test.TestName
		packageName := test.Package
		if testName == "" && packageName != "" {
			// Try to extract test name from package string
			parts := strings.Split(packageName, "::")
			if len(parts) > 1 {
				packageName = parts[0]
				// Take last part after last /
				pkgParts := strings.Split(parts[len(parts)-1], "/")
				testName = pkgParts[len(pkgParts)-1]
			} else {
				// Extract last part of package path as test name
				pkgParts := strings.Split(packageName, "/")
				testName = pkgParts[len(pkgParts)-1]
			}
		}

		// Determine color for status
		var statusColor text.Color
		if test.Recommendation == "STABLE" {
			statusColor = text.FgGreen
			stable++
		} else {
			statusColor = text.FgRed
			unstable++
		}

		t.AppendRow(table.Row{
			testName,
			packageName,
			fmt.Sprintf("%d/%d", test.Passes, test.TotalRuns),
			fmt.Sprintf("%.1f%%", test.PassRate),
			test.AvgDuration.Round(time.Millisecond).String(),
			statusColor.Sprint(test.Recommendation),
			status,
		})
	}

	// Add summary footer
	t.AppendFooter(table.Row{
		"TOTAL",
		fmt.Sprintf("%d tests", len(report.Tests)),
		"",
		"",
		"",
		fmt.Sprintf("Stable: %d | Unstable: %d", stable, unstable),
		"",
	})

	// Set style based on overall results
	if unstable > 0 {
		t.SetStyle(table.StyleColoredBlackOnRedWhite)
	} else if stable == len(report.Tests) && len(report.Tests) > 0 {
		t.SetStyle(table.StyleColoredBlackOnGreenWhite)
	} else {
		t.SetStyle(table.StyleDefault)
	}

	t.Render()
	return buf.String()
}

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
