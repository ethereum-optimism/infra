package nat

import (
	"context"
	"errors"
	"fmt"

	"github.com/ethereum-optimism/infra/op-acceptor/registry"
	"github.com/ethereum-optimism/infra/op-acceptor/runner"
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

	// Modular components
	executor  TestExecutor
	scheduler TestScheduler
	formatter ResultFormatter
	reporter  MetricsReporter

	result           *runner.RunnerResult
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

	// Create the modular components
	executor := NewDefaultTestExecutor(testRunner, config.Log)
	scheduler := NewDefaultTestScheduler(config.RunInterval, config.RunOnce, config.Log)
	formatter := NewConsoleResultFormatter(config.Log)
	reporter := NewDefaultMetricsReporter()

	return &nat{
		ctx:              ctx,
		config:           config,
		version:          version,
		registry:         reg,
		executor:         executor,
		scheduler:        scheduler,
		formatter:        formatter,
		reporter:         reporter,
		shutdownCallback: shutdownCallback,
	}, nil
}

// Start runs the acceptance tests periodically at the configured interval.
// Start implements the cliapp.Lifecycle interface.
func (n *nat) Start(ctx context.Context) error {
	n.ctx = ctx

	// Register the test execution callback with the scheduler
	n.scheduler.RegisterCallback(func() error {
		// Run tests
		result, err := n.executor.RunTests()
		if err != nil {
			return err
		}
		n.result = result

		// Format and display results
		if err := n.formatter.FormatResults(result); err != nil {
			n.config.Log.Error("Error formatting results", "error", err)
		}

		// Report metrics
		n.reporter.ReportResults(result.RunID, result)

		return nil
	})

	// Start the scheduler
	if err := n.scheduler.Start(ctx); err != nil {
		return err
	}

	// If in run-once mode, trigger shutdown
	if n.config.RunOnce {
		n.config.Log.Info("Tests completed, exiting (run-once mode)")

		// Use a goroutine to avoid blocking in Start()
		go func() {
			n.shutdownCallback(nil)
		}()
	}

	n.config.Log.Debug("op-acceptor started successfully")
	return nil
}

// Stop stops the op-acceptor service.
// Stop implements the cliapp.Lifecycle interface.
func (n *nat) Stop(ctx context.Context) error {
	n.config.Log.Info("Stopping op-acceptor")

	// Stop the scheduler
	if err := n.scheduler.Stop(); err != nil {
		return err
	}

	n.config.Log.Info("op-acceptor stopped successfully")
	return nil
}

// Stopped returns true if the op-acceptor service is stopped.
// Stopped implements the cliapp.Lifecycle interface.
func (n *nat) Stopped() bool {
	// Delegate to the scheduler
	return n.scheduler.Stopped()
}

// WaitForShutdown blocks until all goroutines have terminated.
func (n *nat) WaitForShutdown(ctx context.Context) error {
	return n.scheduler.WaitForShutdown(ctx)
}
