package nat

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"

	"github.com/ethereum-optimism/infra/op-nat/metrics"
	"github.com/ethereum-optimism/infra/op-nat/registry"
	"github.com/ethereum-optimism/infra/op-nat/runner"
	"github.com/ethereum-optimism/infra/op-nat/types"
	"github.com/ethereum-optimism/optimism/op-service/cliapp"
)

// nat implements the cliapp.Lifecycle interface.
var _ cliapp.Lifecycle = &nat{}

type nat struct {
	ctx      context.Context
	config   *Config
	version  string
	registry *registry.Registry
	runner   runner.TestRunner
	result   *runner.RunnerResult

	running atomic.Bool
}

func New(ctx context.Context, config *Config, version string) (*nat, error) {
	if config == nil {
		return nil, errors.New("config is required")
	}

	config.Log.Debug("Creating NAT with config",
		"testDir", config.TestDir,
		"validatorConfig", config.ValidatorConfig)

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
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create test runner: %w", err)
	}
	config.Log.Info("nat.New: created registry and test runner")

	return &nat{
		ctx:      ctx,
		config:   config,
		version:  version,
		registry: reg,
		runner:   testRunner,
	}, nil
}

// Start runs the acceptance tests and returns true if the tests pass.
// Start implements the cliapp.Lifecycle interface.
func (n *nat) Start(ctx context.Context) error {
	n.config.Log.Info("Starting OpNAT")
	n.ctx = ctx
	n.running.Store(true)

	// Add detailed debug logging for paths
	n.config.Log.Debug("NAT config paths",
		"config.TestDir", n.config.TestDir,
		"config.ValidatorConfig", n.config.ValidatorConfig)

	// Run all tests
	n.config.Log.Info("Running all tests[n.runner.RunAllTests()]...")
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
	n.config.Log.Info("OpNAT finished", "run_id", result.RunID)

	return nil
}

// Stop stops the OpNAT service.
// Stop implements the cliapp.Lifecycle interface.
func (n *nat) Stop(ctx context.Context) error {
	n.running.Store(false)
	n.config.Log.Info("OpNAT stopped")
	return nil
}

// Stopped returns true if the OpNAT service is stopped.
// Stopped implements the cliapp.Lifecycle interface.
func (n *nat) Stopped() bool {
	return n.running.Load()
}

// printResultsTable prints the results of the acceptance tests to the console.
func (n *nat) printResultsTable(runID string) {
	n.config.Log.Info("Printing results...")
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetTitle(fmt.Sprintf("NAT Results (%s)", formatDuration(n.result.Duration)))

	// Configure columns
	t.AppendHeader(table.Row{
		"Type", "ID", "Duration", "Tests", "Passed", "Failed", "Skipped", "Status", "Error",
	})

	// Set column configurations for better readability
	t.SetColumnConfigs([]table.ColumnConfig{
		{Name: "Type", AutoMerge: true},
		{Name: "ID", WidthMax: 50},
		{Name: "Duration", Align: text.AlignRight},
		{Name: "Tests", Align: text.AlignRight},
		{Name: "Passed", Align: text.AlignRight},
		{Name: "Failed", Align: text.AlignRight},
		{Name: "Skipped", Align: text.AlignRight},
	})

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
				t.AppendRow(table.Row{
					"Test",
					fmt.Sprintf("%s %s", prefix, testName),
					formatDuration(test.Duration),
					"1", // Count actual test
					boolToInt(test.Status == types.TestStatusPass),
					boolToInt(test.Status == types.TestStatusFail),
					boolToInt(test.Status == types.TestStatusSkip),
					getResultString(test.Status),
					test.Error,
				})
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
			t.AppendRow(table.Row{
				"Test",
				fmt.Sprintf("%s %s", prefix, testName),
				formatDuration(test.Duration),
				"1", // Count actual test
				boolToInt(test.Status == types.TestStatusPass),
				boolToInt(test.Status == types.TestStatusFail),
				boolToInt(test.Status == types.TestStatusSkip),
				getResultString(test.Status),
				test.Error,
			})
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
