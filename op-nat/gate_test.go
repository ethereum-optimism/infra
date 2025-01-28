package nat

import (
	"context"
	"testing"

	"github.com/ethereum/go-ethereum/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGate(t *testing.T) {
	t.Run("passes when all validators pass", func(t *testing.T) {
		gate := &Gate{
			Validators: []Validator{
				&Test{
					ID: "test1",
					Fn: func(ctx context.Context, log log.Logger, cfg Config, params interface{}) (bool, error) {
						return true, nil
					},
				},
				&Test{
					ID: "test2",
					Fn: func(ctx context.Context, log log.Logger, cfg Config, params interface{}) (bool, error) {
						return true, nil
					},
				},
			},
		}

		result, err := gate.Run(context.Background(), log.New(), "run1", Config{}, nil)

		require.NoError(t, err)
		assert.Equal(t, ResultPassed, result.Result)
	})

	t.Run("fails if any validator fails", func(t *testing.T) {
		gate := &Gate{
			Validators: []Validator{
				&Test{
					ID: "test1",
					Fn: func(ctx context.Context, log log.Logger, cfg Config, params interface{}) (bool, error) {
						return true, nil
					},
				},
				&Test{
					ID: "test2",
					Fn: func(ctx context.Context, log log.Logger, cfg Config, params interface{}) (bool, error) {
						return false, nil
					},
				},
			},
		}

		result, err := gate.Run(context.Background(), log.New(), "run1", Config{}, nil)

		require.NoError(t, err)
		assert.Equal(t, ResultFailed, result.Result)
	})

	t.Run("doesnt stop on validator failure", func(t *testing.T) {
		executionOrder := []string{}

		gate := &Gate{
			Validators: []Validator{
				&Test{
					ID: "test1",
					Fn: func(ctx context.Context, log log.Logger, cfg Config, params interface{}) (bool, error) {
						executionOrder = append(executionOrder, "test1")
						return true, nil
					},
				},
				&Test{
					ID: "test2",
					Fn: func(ctx context.Context, log log.Logger, cfg Config, params interface{}) (bool, error) {
						executionOrder = append(executionOrder, "test2")
						return false, nil
					},
				},
				&Test{
					ID: "test3",
					Fn: func(ctx context.Context, log log.Logger, cfg Config, params interface{}) (bool, error) {
						executionOrder = append(executionOrder, "test3")
						return true, nil
					},
				},
			},
		}

		result, err := gate.Run(context.Background(), log.New(), "run1", Config{}, nil)

		require.NoError(t, err)
		assert.Equal(t, ResultFailed, result.Result)
		assert.Equal(t, []string{"test1", "test2", "test3"}, executionOrder, "shouldnt stop on validator failure")
	})
}
