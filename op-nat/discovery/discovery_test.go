package discovery

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDiscoverTests(t *testing.T) {
	// Create test directory structure
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	require.NoError(t, os.MkdirAll(configDir, 0755))

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
	configPath := filepath.Join(configDir, "validators.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(validConfig), 0644))

	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: Config{
				ConfigFile:   configPath,
				ValidatorDir: tmpDir,
			},
		},
		{
			name: "missing config file",
			cfg: Config{
				ConfigFile:   "nonexistent.yaml",
				ValidatorDir: tmpDir,
			},
			wantErr: true,
		},
		{
			name: "empty config file path",
			cfg: Config{
				ValidatorDir: tmpDir,
			},
			wantErr: true,
		},
		{
			name: "empty validator dir",
			cfg: Config{
				ConfigFile: configPath,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validators, err := DiscoverTests(tt.cfg)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotEmpty(t, validators)

			// Check discovered validators
			if tt.name == "valid config" {
				require.Len(t, validators, 2) // One direct test and one suite test

				// Check direct test
				require.Equal(t, "test1", validators[0].ID)
				require.Equal(t, "test-gate", validators[0].Gate)
				require.Empty(t, validators[0].Suite)

				// Check suite test
				require.Equal(t, "suite-test1", validators[1].ID)
				require.Equal(t, "test-gate", validators[1].Gate)
				require.Equal(t, "test-suite", validators[1].Suite)
			}
		})
	}
}

func TestLoadConfig(t *testing.T) {
	// Create test config file
	tmpDir := t.TempDir()
	validConfig := `
gates:
  - id: test-gate
    tests:
      - name: test1
        package: github.com/ethereum-optimism/infra/op-nat/validators
`
	configPath := filepath.Join(tmpDir, "validators.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(validConfig), 0644))

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Len(t, cfg.Gates, 1)
	require.Equal(t, "test-gate", cfg.Gates[0].ID)
	require.Len(t, cfg.Gates[0].Tests, 1)
	require.Equal(t, "test1", cfg.Gates[0].Tests[0].Name)
}
