package nat

import (
	"context"
	"testing"

	"github.com/ethereum-optimism/optimism/op-service/testlog"
	"github.com/ethereum/go-ethereum/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNATParameterization(t *testing.T) {
	t.Parallel()

	// Create a test factory that creates a new test instance with its own received params
	makeTest := func() (*Test, *interface{}) {
		var receivedParams interface{}
		testFn := func(ctx context.Context, cfg Config, params interface{}) (bool, error) {
			receivedParams = params
			return true, nil
		}

		test := &Test{
			ID:            "test-with-params",
			DefaultParams: map[string]string{"value": "default"},
			Fn:            testFn,
		}
		return test, &receivedParams
	}

	t.Run("uses default parameters when none provided", func(t *testing.T) {
		test, receivedParams := makeTest()
		cfg := &Config{
			Validators: []Validator{test},
			Log:        log.New(),
		}

		nat, err := New(context.Background(), cfg, "test")
		require.NoError(t, err)
		t.Cleanup(func() {
			err := nat.Stop(context.Background())
			require.NoError(t, err)
		})

		err = nat.Start(context.Background())
		require.NoError(t, err)

		assert.Equal(t, test.DefaultParams, *receivedParams)
	})

	t.Run("uses custom parameters when provided", func(t *testing.T) {
		test, receivedParams := makeTest()
		cfg := &Config{
			Validators: []Validator{test},
			Log:        log.New(),
		}

		nat, err := New(context.Background(), cfg, "test")
		require.NoError(t, err)
		t.Cleanup(func() {
			err := nat.Stop(context.Background())
			require.NoError(t, err)
		})

		customParams := map[string]string{"value": "custom"}
		nat.params = map[string]interface{}{
			test.ID: customParams,
		}

		err = nat.Start(context.Background())
		require.NoError(t, err)

		assert.Equal(t, customParams, *receivedParams)
	})

	t.Run("different test instances can have different parameters", func(t *testing.T) {
		test, receivedParams := makeTest()
		cfg := &Config{
			Validators: []Validator{test},
			Log:        log.New(),
		}

		// Create two instances with different parameters
		nat1, err := New(context.Background(), cfg, "test1")
		require.NoError(t, err)
		nat1.params = map[string]interface{}{
			test.ID: map[string]string{"value": "instance1"},
		}

		nat2, err := New(context.Background(), cfg, "test2")
		require.NoError(t, err)
		nat2.params = map[string]interface{}{
			test.ID: map[string]string{"value": "instance2"},
		}

		t.Cleanup(func() {
			err := nat1.Stop(context.Background())
			require.NoError(t, err)
			err = nat2.Stop(context.Background())
			require.NoError(t, err)
		})

		// Run first instance
		err = nat1.Start(context.Background())
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"value": "instance1"}, *receivedParams)

		// Run second instance
		err = nat2.Start(context.Background())
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"value": "instance2"}, *receivedParams)
	})

	t.Run("results are properly recorded", func(t *testing.T) {
		test, _ := makeTest()
		cfg := &Config{
			Validators: []Validator{test},
			Log:        log.New(),
		}

		nat, err := New(context.Background(), cfg, "test")
		require.NoError(t, err)
		t.Cleanup(func() {
			err := nat.Stop(context.Background())
			require.NoError(t, err)
		})
		nat.params = make(map[string]interface{})

		err = nat.Start(context.Background())
		require.NoError(t, err)

		require.Len(t, nat.results, 1)
		assert.Equal(t, "test-with-params", nat.results[0].ID)
		assert.Equal(t, "Test", nat.results[0].Type)
		assert.Equal(t, ResultPassed, nat.results[0].Result)
	})
}

func TestGateValidatorParameters(t *testing.T) {
	// Create a mock Gate validator that checks its parameters
	mockGate := &mockGateValidator{
		name: "test-gate",
		runFn: func(ctx context.Context, runID string, cfg Config, params interface{}) (ValidatorResult, error) {
			// Verify params are passed correctly
			gateParams, ok := params.(map[string]interface{})
			if !ok {
				t.Fatal("expected params to be map[string]interface{}")
			}

			threshold, ok := gateParams["threshold"].(float64)
			if !ok {
				t.Fatal("expected threshold parameter to be float64")
			}
			if threshold != 0.8 {
				t.Errorf("expected threshold to be 0.8, got %f", threshold)
			}

			return ValidatorResult{
				Type:   "gate",
				ID:     "test-gate",
				Result: ResultPassed,
			}, nil
		},
	}

	// Create NAT config with our mock validator
	cfg := &Config{
		Log:        testlog.Logger(t, log.LvlInfo),
		Validators: []Validator{mockGate},
	}

	// Create NAT instance
	n, err := New(context.Background(), cfg, "test")
	if err != nil {
		t.Fatal(err)
	}

	// Set parameters for the Gate validator
	n.params["test-gate"] = map[string]interface{}{
		"threshold": 0.8,
	}

	// Run NAT
	err = n.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Verify results
	if len(n.results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(n.results))
	}
	if n.results[0].Result != ResultPassed {
		t.Errorf("expected test to pass, got %s", n.results[0].Result)
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
