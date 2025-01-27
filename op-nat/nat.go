package nat

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"

	"github.com/ethereum/go-ethereum/log"
	"github.com/jedib0t/go-pretty/v6/table"

	"github.com/ethereum-optimism/infra/op-nat/metrics"
	"github.com/ethereum-optimism/optimism/op-service/cliapp"
)

var _ cliapp.Lifecycle = &nat{}

type nat struct {
	ctx     context.Context
	log     log.Logger
	config  *Config
	params  map[string]interface{}
	version string
	results []ValidatorResult

	running atomic.Bool
}

func New(ctx context.Context, config *Config, log log.Logger, version string) (*nat, error) {
	if config == nil {
		return nil, errors.New("config is required")
	}
	if err := config.Check(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &nat{
		ctx:     ctx,
		config:  config,
		params:  map[string]interface{}{},
		log:     log,
		version: version,
	}, nil
}

// Run runs the acceptance tests and returns true if the tests pass
func (n *nat) Start(ctx context.Context) error {
	n.log.Info("Starting OpNAT")
	n.ctx = ctx
	n.running.Store(true)

	n.results = []ValidatorResult{}
	for _, validator := range n.config.Validators {
		n.log.Info("Running acceptance tests...")

		// Get test-specific parameters if they exist
		params := n.params[validator.Name()]

		result, err := validator.Run(ctx, n.log, *n.config, params)
		n.log.Info("Completed validator", "validator", validator.Name(), "type", validator.Type(), "result", result.Result.String(), "error", err)
		if err != nil {
			n.log.Error("Error running validator", "validator", validator.Name(), "error", err)
		}
		n.results = append(n.results, result)
	}
	n.printResultsTable()

	n.log.Info("OpNAT finished")
	return nil
}

func (n *nat) Stop(ctx context.Context) error {
	n.running.Store(false)
	n.log.Info("OpNAT stopped")
	return nil
}

func (n *nat) Stopped() bool {
	return n.running.Load()
}

func (n *nat) printResultsTable() {
	n.log.Info("Printing results...")
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
	metrics.RecordAcceptance("op-kurtosis-1", overallResult.String(), overallErr)
}

func appendResultRows(t table.Writer, result ValidatorResult) {
	resultRows := []table.Row{}
	resultRows = append(resultRows, table.Row{result.Type, result.ID, result.Result.String(), result.Error})
	t.AppendRows(resultRows)
	for _, subResult := range result.SubResults {
		appendResultRows(t, subResult)
	}
}
