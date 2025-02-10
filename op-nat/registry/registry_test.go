package registry

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum-optimism/infra/op-nat/types"
	"github.com/stretchr/testify/require"
)

func TestRegistry(t *testing.T) {
	// Create test directory structure
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "validators.yaml")

	// Create test validator config
	validConfig := `
gates:
  - id: test-gate
    description: "Test gate"
    suites:
      test-suite:
        description: "Test suite"
        tests:
          - name: TestOne
            package: "./testdata/package"
    tests:
      - name: TestTwo
        package: "./testdata/package"
`
	err := os.WriteFile(configPath, []byte(validConfig), 0644)
	require.NoError(t, err)

	baseConfig := Config{
		Source: types.SourceConfig{
			Location:   tmpDir,            // Directory containing the config
			ConfigPath: "validators.yaml", // Just the filename
		},
		WorkDir: tmpDir,
	}

	t.Run("source loading", func(t *testing.T) {
		tests := []struct {
			name    string
			cfg     Config
			wantErr bool
		}{
			{
				name:    "valid local source",
				cfg:     baseConfig,
				wantErr: false,
			},
			{
				name: "invalid config path",
				cfg: Config{
					Source: types.SourceConfig{
						Location:   tmpDir,
						ConfigPath: "nonexistent.yaml",
					},
					WorkDir: tmpDir,
				},
				wantErr: true,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				r, err := NewRegistry(tt.cfg)
				if (err != nil) != tt.wantErr {
					t.Errorf("NewRegistry() error = %v, wantErr %v", err, tt.wantErr)
					return
				}
				if err == nil {
					require.NotNil(t, r.GetConfig(), "config should be loaded")
					require.NotNil(t, r.GetGate("test-gate"), "gate should be loaded")
				}
			})
		}
	})

	t.Run("gate operations", func(t *testing.T) {
		r, err := NewRegistry(baseConfig)
		require.NoError(t, err)

		t.Run("get existing gate", func(t *testing.T) {
			gate := r.GetGate("test-gate")
			require.NotNil(t, gate, "expected to find test-gate")
			require.Equal(t, "test-gate", gate.ID)
		})

		t.Run("get non-existent gate", func(t *testing.T) {
			gate := r.GetGate("nonexistent-gate")
			require.Nil(t, gate, "expected nil for non-existent gate")
		})
	})

	t.Run("validate", func(t *testing.T) {
		r, err := NewRegistry(baseConfig)
		require.NoError(t, err)

		err = r.Validate()
		require.NoError(t, err)
	})

	t.Run("get config", func(t *testing.T) {
		cfg := baseConfig
		cfg.Gate = "test-gate"

		r, err := NewRegistry(cfg)
		require.NoError(t, err)

		gotCfg := r.GetConfig()
		require.Equal(t, cfg.Gate, gotCfg.Gate)
		require.Equal(t, cfg.WorkDir, gotCfg.WorkDir)
	})
}
