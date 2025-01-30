package nat

import (
	"context"
	"errors"

	"github.com/ethereum-optimism/infra/op-nat/metrics"
)

var _ Validator = &Suite{}

// A Suite is a collection of tests.
type Suite struct {
	ID          string
	Tests       []Test
	TestsParams map[string]interface{}
}

// Run runs all the tests in the suite.
// Returns the overall result of the suite and an error if any of the tests failed.
// Suite-specific params are passed in as `_` because we haven't implemented them yet.
func (s Suite) Run(ctx context.Context, runID string, cfg Config, _ interface{}) (ValidatorResult, error) {
	cfg.Log.Info("", "type", s.Type(), "id", s.Name())
	var overallResult ResultType = ResultPassed
	hasSkipped := false
	results := []ValidatorResult{}
	var allErrors error
	for _, test := range s.Tests {
		// Get test-specific params
		params := s.TestsParams[test.ID]

		res, err := test.Run(ctx, runID, cfg, params)
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
	cfg.Log.Info("", "type", s.Type(), "id", s.Name(), "result", overallResult, "error", allErrors)
	metrics.RecordValidation("todo", runID, s.Name(), s.Type(), overallResult.String())
	return ValidatorResult{
		ID:         s.ID,
		Type:       s.Type(),
		Result:     overallResult,
		Error:      allErrors,
		SubResults: results,
	}, nil
}

// Name returns the id of the suite.
func (s Suite) Name() string {
	return s.ID
}

// Type returns the type name of the suite.
func (s Suite) Type() string {
	return "Suite"
}
