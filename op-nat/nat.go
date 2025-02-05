package nat

import (
	"context"
	"errors"
	"os"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/jedib0t/go-pretty/v6/table"

	"github.com/ethereum-optimism/infra/op-nat/metrics"
	"github.com/ethereum-optimism/optimism/op-service/cliapp"
)

// nat implements the cliapp.Lifecycle interface.
var _ cliapp.Lifecycle = &nat{}

type nat struct {
	ctx     context.Context
	config  *Config
	params  map[string]interface{}
	version string
	results []ValidatorResult

	running atomic.Bool
}

func New(ctx context.Context, config *Config, version string) (*nat, error) {
	if config == nil {
		return nil, errors.New("config is required")
	}

	return &nat{
		ctx:     ctx,
		config:  config,
		params:  map[string]interface{}{},
		version: version,
	}, nil
}

// Start runs the acceptance tests and returns true if the tests pass.
// Start implements the cliapp.Lifecycle interface.
func (n *nat) Start(ctx context.Context) error {
	n.config.Log.Info("Starting OpNAT")
	n.ctx = ctx
	n.running.Store(true)
	runID := uuid.New().String()

	n.results = []ValidatorResult{}
	for _, validator := range n.config.Validators {
		n.config.Log.Info("Running acceptance tests...", "run_id", runID)

		// Get test-specific parameters if they exist
		params := n.params[validator.Name()]

		result, err := validator.Run(ctx, runID, *n.config, params)
		n.config.Log.Info("Completed validator", "validator", validator.Name(), "type", validator.Type(), "result", result.Result.String(), "error", err)
		if err != nil {
			n.config.Log.Error("Error running validator", "validator", validator.Name(), "error", err)
		}
		n.results = append(n.results, result)
	}
	n.printResultsTable(runID)

	n.config.Log.Info("OpNAT finished", "run_id", runID)
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

	var overallResult ResultType = ResultPassed
	var hasSkipped = false
	var overallErr error
	for _, res := range n.results {
		appendResultRows(t, res)
		overallErr = errors.Join(overallErr)
		if res.Result == ResultFailed {
			overallResult = ResultFailed
		}
		if res.Result == ResultSkipped {
			hasSkipped = true
		}
		t.AppendSeparator()
	}
	if overallResult == ResultPassed && hasSkipped {
		overallResult = ResultSkipped
	}
	t.AppendFooter([]interface{}{"SUMMARY", "", overallResult.String(), ""})
	if overallResult == ResultFailed {
		t.SetStyle(table.StyleColoredBlackOnRedWhite)
	}
	t.Render()

	// Emit metrics
	// TODO: This shouldn't be here; needs a refactor
	// TODO: don't hardcode the network name
	metrics.RecordAcceptance("todo", runID, overallResult.String())
}

func appendResultRows(t table.Writer, result ValidatorResult) {
	resultRows := []table.Row{}
	resultRows = append(resultRows, table.Row{result.Type, result.ID, result.Result.String(), result.Error})
	t.AppendRows(resultRows)
	for _, subResult := range result.SubResults {
		appendResultRows(t, subResult)
	}
}
