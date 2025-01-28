package nat

import (
	"context"

	"github.com/ethereum/go-ethereum/log"
)

type Validator interface {
	Run(ctx context.Context, log log.Logger, runID string, cfg Config, params interface{}) (ValidatorResult, error)
	Name() string
	Type() string
}

type ResultType int

const (
	ResultFailed ResultType = iota
	ResultPassed
	ResultSkipped
)

// TODO: Temporary until test.Fn's return ValidatorResult
func ResultTypeFromBool(b bool) ResultType {
	if b {
		return ResultPassed
	}
	return ResultFailed
}

// String provides a string representation of ResultType
func (r ResultType) String() string {
	switch r {
	case ResultPassed:
		return "pass"
	case ResultFailed:
		return "fail"
	case ResultSkipped:
		return "skip"
	default:
		return "unknown"
	}
}

type ValidatorResult struct {
	ID         string
	Type       string
	Result     ResultType
	Error      error
	RunID      string
	SubResults []ValidatorResult
}
