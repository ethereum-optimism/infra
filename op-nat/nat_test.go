package nat

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum-optimism/optimism/op-service/testlog"
	"github.com/ethereum/go-ethereum/log"
	"github.com/stretchr/testify/require"
)

func setupTestDir(t *testing.T) string {
	tmpDir := t.TempDir()

	// Create test validator config
	validConfig := `
gates:
  - id: test-gate
    tests:
      - name: test1
        package: github.com/ethereum-optimism/infra/op-nat/validators
    suites:
      test-suite:
        tests:
          - name: suite-test1
            package: github.com/ethereum-optimism/infra/op-nat/validators
`
	// Write directly to validators.yaml in the root directory
	require.NoError(t, os.WriteFile(
		filepath.Join(tmpDir, "validators.yaml"),
		[]byte(validConfig),
		0644,
	))

	// Create validators directory
	validatorsDir := filepath.Join(tmpDir, "validators")
	require.NoError(t, os.MkdirAll(validatorsDir, 0755))

	// Create a test file
	testFile := `
package validators

import "testing"

func TestExample(t *testing.T) {
	// This is a passing test
}
`
	require.NoError(t, os.WriteFile(
		filepath.Join(validatorsDir, "example_test.go"),
		[]byte(testFile),
		0644,
	))

	return tmpDir
}

func makeTestConfig(t *testing.T) *Config {
	return &Config{
		TestDir: setupTestDir(t),
		Log:     testlog.Logger(t, log.LvlDebug),
	}
}

func TestNat(t *testing.T) {
	logger := testlog.Logger(t, log.LvlInfo)
	tests := []struct {
		name    string
		config  *Config
		wantErr bool
	}{
		{
			name: "basic test",
			config: &Config{
				TestDir: setupTestDir(t),
				Log:     logger,
			},
		},
		{
			name: "missing config dir",
			config: &Config{
				TestDir: t.TempDir(), // Empty directory
				Log:     logger,
			},
			wantErr: true,
		},
		{
			name: "nonexistent dir",
			config: &Config{
				TestDir: "/nonexistent",
				Log:     logger,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, err := New(context.Background(), tt.config, "test")
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			err = n.Start(context.Background())
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, n.result, "Expected result to be set")
			require.True(t, n.result.Passed, "Expected tests to pass")
		})
	}
}

func TestNat_Stop(t *testing.T) {
	cfg := makeTestConfig(t)
	n, err := New(context.Background(), cfg, "test")
	require.NoError(t, err)

	// Test Stop
	err = n.Stop(context.Background())
	require.NoError(t, err)
	require.False(t, n.running.Load(), "Expected running to be false after stop")
}

// Remove TestNATParameterization and TestGateValidatorParameters as they're testing
// the old implementation. We'll need to create new tests that work with the registry-based
// architecture.
