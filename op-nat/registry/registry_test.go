package registry

import (
	"os"
	"path/filepath"
	"testing"

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
		ValidatorConfigFile: configPath,
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
					ValidatorConfigFile: "nonexistent.yaml",
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
				}
			})
		}
	})
}

func TestLoadConfig(t *testing.T) {
	// Create test config file
	tmpDir := t.TempDir()
	validConfig := `
gates:
  - id: test-gate
    tests:
      - name: TestNATFortyTwo
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
	require.Equal(t, "TestNATFortyTwo", cfg.Gates[0].Tests[0].Name)
}

func TestGateInheritance(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name      string
		config    string
		wantError string
	}{
		{
			name: "valid inheritance",
			config: `
gates:
  - id: parent
    tests:
      - name: parentTest
        package: ./pkg
  - id: child
    inherits: [parent]
    tests:
      - name: childTest
        package: ./pkg
`,
			wantError: "",
		},
		{
			name: "circular inheritance",
			config: `
gates:
  - id: gate1
    inherits: [gate2]
    tests:
      - name: test1
        package: ./pkg
  - id: gate2
    inherits: [gate1]
    tests:
      - name: test2
        package: ./pkg
`,
			wantError: "circular inheritance detected",
		},
		{
			name: "self inheritance",
			config: `
gates:
  - id: gate1
    inherits: [gate1]
    tests:
      - name: test1
        package: ./pkg
`,
			wantError: "circular inheritance detected",
		},
		{
			name: "non-existent gate",
			config: `
gates:
  - id: gate1
    inherits: [nonexistent]
    tests:
      - name: test1
        package: ./pkg
`,
			wantError: "inherits from non-existent gate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath := filepath.Join(tmpDir, "validators.yaml")
			err := os.WriteFile(configPath, []byte(tt.config), 0644)
			require.NoError(t, err)

			r, err := NewRegistry(Config{
				ValidatorConfigFile: configPath,
			})

			if tt.wantError != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantError)
			} else {
				require.NoError(t, err)
				require.NotNil(t, r)
			}
		})
	}
}

func TestDiscoverTests(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "validators.yaml")

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
	err := os.WriteFile(configPath, []byte(validConfig), 0644)
	require.NoError(t, err)

	reg, err := NewRegistry(Config{
		ValidatorConfigFile: configPath,
	})
	require.NoError(t, err)

	validators := reg.GetValidators()
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
