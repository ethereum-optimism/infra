package nat

import (
	"context"
	"testing"
	"time"

	"github.com/ethereum-optimism/optimism/op-service/testlog"
	"github.com/ethereum/go-ethereum/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNATParameterization(t *testing.T) {
	t.Parallel()

	// Create a test factory that creates a new test instance with its own channel and received params
	makeTest := func() (*Test, chan interface{}) {
		paramsChan := make(chan interface{}, 2) // Buffered channel to prevent blocking and allow multiple values

		testFn := func(ctx context.Context, cfg Config, params interface{}) (bool, error) {
			select {
			case paramsChan <- params:
				return true, nil
			case <-ctx.Done():
				return false, ctx.Err()
			}
		}

		test := &Test{
			ID:            "test-with-params",
			DefaultParams: map[string]string{"value": "default"},
			Fn:            testFn,
		}
		return test, paramsChan
	}

	t.Run("uses default parameters when none provided", func(t *testing.T) {
		test, paramsChan := makeTest()
		cfg := &Config{
			Validators: []Validator{test},
			Log:        log.New(),
		}

		ctx, cancel := context.WithCancel(context.Background())
		nat, err := New(ctx, cfg, "test")
		require.NoError(t, err)

		t.Cleanup(func() {
			cancel()
			err := nat.Stop(context.Background())
			require.NoError(t, err)
		})

		go func() {
			err := nat.Start(ctx)
			require.NoError(t, err)
		}()

		// Wait for the first test run
		select {
		case params := <-paramsChan:
			assert.Equal(t, test.DefaultParams, params)
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for test execution")
		}
	})

	t.Run("uses custom parameters when provided", func(t *testing.T) {
		test, paramsChan := makeTest()
		cfg := &Config{
			Validators: []Validator{test},
			Log:        log.New(),
		}

		ctx, cancel := context.WithCancel(context.Background())
		nat, err := New(ctx, cfg, "test")
		require.NoError(t, err)

		customParams := map[string]string{"value": "custom"}
		nat.params[test.ID] = customParams

		t.Cleanup(func() {
			cancel()
			err := nat.Stop(context.Background())
			require.NoError(t, err)
		})

		go func() {
			err := nat.Start(ctx)
			require.NoError(t, err)
		}()

		// Wait for the first test run
		select {
		case params := <-paramsChan:
			assert.Equal(t, customParams, params)
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for test execution")
		}
	})

	t.Run("different test instances can have different parameters", func(t *testing.T) {
		test, paramsChan := makeTest()
		cfg := &Config{
			Validators: []Validator{test},
			Log:        log.New(),
		}

		// Create two instances with different parameters
		ctx1, cancel1 := context.WithCancel(context.Background())
		nat1, err := New(ctx1, cfg, "test1")
		require.NoError(t, err)
		nat1.params = map[string]interface{}{
			test.ID: map[string]string{"value": "instance1"},
		}

		ctx2, cancel2 := context.WithCancel(context.Background())
		nat2, err := New(ctx2, cfg, "test2")
		require.NoError(t, err)
		nat2.params = map[string]interface{}{
			test.ID: map[string]string{"value": "instance2"},
		}

		t.Cleanup(func() {
			cancel1()
			cancel2()
			_ = nat1.Stop(context.Background())
			_ = nat2.Stop(context.Background())
		})

		go func() {
			err := nat1.Start(ctx1)
			require.NoError(t, err)
		}()

		go func() {
			err := nat2.Start(ctx2)
			require.NoError(t, err)
		}()

		// Collect both parameter sets with timeout
		var params []interface{}
		for i := 0; i < 2; i++ {
			select {
			case p := <-paramsChan:
				params = append(params, p)
			case <-time.After(5 * time.Second):
				t.Fatal("timeout waiting for test execution")
			}
		}

		// Verify we got two different parameter sets
		require.Len(t, params, 2)
		assert.NotEqual(t, params[0], params[1])
	})
}

func TestGateValidatorParameters(t *testing.T) {
	paramsChan := make(chan map[string]interface{}, 1)

	// Create a mock Gate validator that checks its parameters
	mockGate := &mockGateValidator{
		name: "test-gate",
		runFn: func(ctx context.Context, runID string, cfg Config, params interface{}) (ValidatorResult, error) {
			gateParams, ok := params.(map[string]interface{})
			require.True(t, ok, "expected params to be map[string]interface{}")
			paramsChan <- gateParams
			return ValidatorResult{
				Type:   "gate",
				ID:     "test-gate",
				Result: ResultPassed,
			}, nil
		},
	}

	cfg := &Config{
		Log:        testlog.Logger(t, log.LvlInfo),
		Validators: []Validator{mockGate},
	}

	ctx, cancel := context.WithCancel(context.Background())
	n, err := New(ctx, cfg, "test")
	require.NoError(t, err)

	// Set parameters for the Gate validator
	n.params["test-gate"] = map[string]interface{}{
		"threshold": 0.8,
	}

	t.Cleanup(func() {
		cancel()
		err := n.Stop(context.Background())
		require.NoError(t, err)
	})

	go func() {
		err := n.Start(ctx)
		require.NoError(t, err)
	}()

	// Wait for parameters with timeout
	select {
	case params := <-paramsChan:
		threshold, ok := params["threshold"].(float64)
		require.True(t, ok, "expected threshold parameter to be float64")
		assert.Equal(t, 0.8, threshold)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for test execution")
	}
}

// mockGateValidator implements the Validator interface for testing
type mockGateValidator struct {
	name  string
	runFn func(context.Context, string, Config, interface{}) (ValidatorResult, error)
}

func (m *mockGateValidator) Name() string {
	return m.name
}

func (m *mockGateValidator) Type() string {
	return "gate"
}

func (m *mockGateValidator) Run(ctx context.Context, runID string, cfg Config, params interface{}) (ValidatorResult, error) {
	return m.runFn(ctx, runID, cfg, params)
}
