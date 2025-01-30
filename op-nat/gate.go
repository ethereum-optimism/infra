package nat

import (
	"context"
	"errors"

	"github.com/ethereum-optimism/infra/op-nat/metrics"
)

var _ Validator = &Gate{}

// A Gate is a collection of suites and/or tests.
type Gate struct {
	ID         string
	Validators []Validator // Validators can be Suites or Tests
	Params     map[string]interface{}
}

// Run runs all the tests in the gate.
// Returns the overall result of the gate and an error if any of the tests failed.
// Gate-specific params are passed in as `_` because we haven't implemented them yet.
func (g Gate) Run(ctx context.Context, runID string, config Config, _ interface{}) (ValidatorResult, error) {
	config.Log.Info("", "type", g.Type(), "id", g.Name())
	var overallResult ResultType = ResultPassed
	hasSkipped := false
	results := []ValidatorResult{}
	var allErrors error
	for _, validator := range g.Validators {
		// We don't want Gates to have Gates
		if validator == nil || validator.Type() == "Gate" {
			continue
		}
		// Get params
		params := g.Params[validator.Name()]

		res, err := validator.Run(ctx, runID, config, params)
		allErrors = errors.Join(allErrors, err)
		if res.Result == ResultFailed {
			overallResult = ResultFailed
		}
		if res.Result == ResultSkipped {
			hasSkipped = true
		}
		results = append(results, res)
	}
	if overallResult == ResultPassed && hasSkipped {
		overallResult = ResultSkipped
	}
	config.Log.Info("", "type", g.Type(), "id", g.Name(), "result", overallResult, "error", allErrors)
	metrics.RecordValidation("todo", runID, g.Name(), g.Type(), overallResult.String())
	return ValidatorResult{
		ID:         g.ID,
		Type:       g.Type(),
		Result:     overallResult,
		Error:      allErrors,
		SubResults: results,
	}, nil
}

// Type returns the type name of the gate.
func (g Gate) Type() string {
	return "Gate"
}

// Name returns the id of the gate.
func (g Gate) Name() string {
	return g.ID
}
