package nat

import (
	"context"
	"fmt"
	"strconv"

	"github.com/ethereum-optimism/infra/op-nat/metrics"
	"github.com/ethereum/go-ethereum/log"
)

var _ Validator = &Test{}

type Test struct {
	ID            string
	DefaultParams interface{}
	Fn            func(ctx context.Context, log log.Logger, cfg Config, params interface{}) (bool, error)
}

func (t Test) Run(ctx context.Context, log log.Logger, cfg Config, params interface{}) (ValidatorResult, error) {
	if t.Fn == nil {
		return ValidatorResult{
			Result: ResultFailed,
		}, fmt.Errorf("test function is nil")
	}

	// Use default params if none provided
	testParams := t.DefaultParams
	if params != nil {
		testParams = params
	}

	log.Info("", "type", t.Type(), "id", t.Name(), "params", testParams)
	res, err := t.Fn(ctx, log, cfg, testParams)
	log.Info("", "type", t.Type(), "id", t.Name(), "params", testParams, "result", strconv.FormatBool(res), "error", err)

	metrics.RecordValidation("todo", t.Name(), t.Type(), strconv.FormatBool(res), err)

	return ValidatorResult{
		ID:     t.ID,
		Type:   t.Type(),
		Error:  err,
		Result: ResultTypeFromBool(res),
	}, err
}

// Name returns the id of the test.
func (t Test) Name() string {
	return t.ID
}

// Type returns the type name of the test.
func (t Test) Type() string {
	return "Test"
}
