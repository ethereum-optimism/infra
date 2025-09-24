package registry

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/stretchr/testify/assert"
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
        package: github.com/ethereum-optimism/infra/op-acceptor/validators
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
        package: github.com/ethereum-optimism/infra/op-acceptor/validators
    suites:
      test-suite:
        tests:
          - name: suite-test1
            package: github.com/ethereum-optimism/infra/op-acceptor/validators
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

func TestRegistryGatelessMode(t *testing.T) {
	// Create temporary directory for the test
	tmpDir := t.TempDir()

	// Create test packages structure
	pkg1Dir := filepath.Join(tmpDir, "pkg1")
	pkg2Dir := filepath.Join(tmpDir, "subdir", "pkg2")

	require.NoError(t, os.MkdirAll(pkg1Dir, 0755))
	require.NoError(t, os.MkdirAll(pkg2Dir, 0755))

	// Create test files with proper test function format
	testContent := `package pkg1_test

import "testing"

func TestExample(t *testing.T) {
    t.Log("test running")
}
`
	require.NoError(t, os.WriteFile(filepath.Join(pkg1Dir, "pkg1_test.go"), []byte(testContent), 0644))

	test2Content := `package pkg2_test

import "testing"

func TestExample2(t *testing.T) {
    t.Log("test2 running")
}
`
	require.NoError(t, os.WriteFile(filepath.Join(pkg2Dir, "pkg2_test.go"), []byte(test2Content), 0644))

	// Save current working directory and change to tmpDir for the test
	originalWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmpDir))
	defer func() {
		require.NoError(t, os.Chdir(originalWd))
	}()

	// Create registry in gateless mode using relative path from tmpDir
	registry, err := NewRegistry(Config{
		Log:          log.New(),
		GatelessMode: true,
		TestDir:      ".", // Use current directory (tmpDir)
	})
	require.NoError(t, err)

	// Verify validators were created
	validators := registry.GetValidators()
	require.Len(t, validators, 2)

	// Check that all validators are configured for gateless mode
	for _, validator := range validators {
		assert.Equal(t, "gateless", validator.Gate)
		assert.Empty(t, validator.Suite)
		assert.True(t, validator.RunAll)
		assert.Equal(t, types.ValidatorTypeTest, validator.Type)
	}

	// Check that we can find validators by gate
	gatelessValidators := registry.GetValidatorsByGate("gateless")
	require.Len(t, gatelessValidators, 2)

	// Verify the package paths are correct - should be relative paths
	var packages []string
	for _, validator := range validators {
		packages = append(packages, validator.Package)
	}
	expected := []string{"./pkg1", "./subdir/pkg2"}
	require.ElementsMatch(t, expected, packages)
}

func TestRegistryGatelessModeEmpty(t *testing.T) {
	// Create temporary directory with no test files
	tmpDir := t.TempDir()

	// Create registry in gateless mode
	_, err := NewRegistry(Config{
		Log:          log.New(),
		GatelessMode: true,
		TestDir:      tmpDir,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no test packages found")
}

func TestRegistryGatelessModeInvalidDir(t *testing.T) {
	tmpDir := t.TempDir()
	nonExistentDir := filepath.Join(tmpDir, "nonexistent")

	// Create registry in gateless mode with non-existent directory
	_, err := NewRegistry(Config{
		Log:          log.New(),
		GatelessMode: true,
		TestDir:      nonExistentDir,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not exist")
}
