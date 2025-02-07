package nat

import (
	"context"
	"errors"
	"os"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/jedib0t/go-pretty/v6/table"

	"github.com/ethereum-optimism/infra/op-nat/discovery"
	"github.com/ethereum-optimism/infra/op-nat/metrics"
	"github.com/ethereum-optimism/infra/op-nat/runner"
	"github.com/ethereum-optimism/optimism/op-service/cliapp"
)

// nat implements the cliapp.Lifecycle interface.
var _ cliapp.Lifecycle = &nat{}

type nat struct {
	ctx     context.Context
	config  *Config
	version string
	runner  runner.TestRunner
	result  *runner.RunnerResult

	running atomic.Bool
}

func New(ctx context.Context, config *Config, version string) (*nat, error) {
	if config == nil {
		return nil, errors.New("config is required")
	}

	testRunner, err := runner.NewTestRunner(config.TestDir)
	if err != nil {
		return nil, err
	}

	return &nat{
		ctx:     ctx,
		config:  config,
		version: version,
		runner:  testRunner,
	}, nil
}

// Start runs the acceptance tests and returns true if the tests pass.
// Start implements the cliapp.Lifecycle interface.
func (n *nat) Start(ctx context.Context) error {
	n.config.Log.Info("Starting OpNAT")
	n.ctx = ctx
	n.running.Store(true)
	runID := uuid.New().String()

	// Discovered tests
	validators, err := discovery.DiscoverTests(n.config.TestDir)
	if err != nil {
		n.config.Log.Error("Error discovering tests", "error", err)
		return err
	}
	n.config.Log.Debug("Discovered test structure", "structure", discovery.ValidatorHierarchyString(validators))

	n.config.Log.Info("Running acceptance tests...", "run_id", runID)

	result, err := n.runner.RunAllTests()
	if err != nil {
		n.config.Log.Error("Error running tests", "error", err)
		return err
	}
	n.result = result

	n.printResultsTable(runID)
	n.config.Log.Info("OpNAT finished", "run_id", runID)

	if !result.Passed {
		return errors.New("one or more tests failed")
	}
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
	t.SetStyle(table.StyleColoredBlackOnGreenWhite)
	t.SetOutputMirror(os.Stdout)
	t.SetTitle("NAT Results")
	t.AppendHeader(table.Row{"Type", "ID", "Result", "Error"})
	colConfigAutoMerge := table.ColumnConfig{AutoMerge: true}
	t.SetColumnConfigs([]table.ColumnConfig{colConfigAutoMerge})

	// Print gates and their results
	for _, gate := range n.result.Gates {
		t.AppendRow(table.Row{"Gate", gate.Metadata.ID, getResultString(gate.Passed), ""})

		// Print suites in this gate
		for _, suite := range gate.Suites {
			t.AppendRow(table.Row{"Suite", suite.Metadata.ID, getResultString(suite.Passed), ""})

			// Print tests in this suite
			for _, test := range suite.Tests {
				t.AppendRow(table.Row{"Test", test.Metadata.ID, getResultString(test.Passed), test.Error})
			}
		}

		// Print direct gate tests
		for _, test := range gate.Tests {
			t.AppendRow(table.Row{"Test", test.Metadata.ID, getResultString(test.Passed), test.Error})
		}

		t.AppendSeparator()
	}

	// Set overall style based on result
	if !n.result.Passed {
		t.SetStyle(table.StyleColoredBlackOnRedWhite)
	}

	t.AppendFooter(table.Row{"SUMMARY", "", getResultString(n.result.Passed), ""})
	t.Render()

	// Emit metrics
	metrics.RecordAcceptance("todo", runID, getResultString(n.result.Passed))
}

func getResultString(passed bool) string {
	if passed {
		return "PASSED"
	}
	return "FAILED"
}
