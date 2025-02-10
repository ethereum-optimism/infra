package discovery

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func setupTestData(t *testing.T) string {
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
	require.NoError(t, os.WriteFile(
		filepath.Join(tmpDir, "validators.yaml"),
		[]byte(validConfig),
		0644,
	))

	return tmpDir
}

func TestPrintValidatorHierarchy(t *testing.T) {
	testDir := setupTestData(t)

	validators, err := DiscoverTests(Config{
		ConfigFile:   filepath.Join(testDir, "validators.yaml"),
		ValidatorDir: testDir,
	})
	require.NoError(t, err)

	// Just verify it doesn't panic
	ValidatorHierarchyString(validators)
}

func TestValidatorHierarchyString(t *testing.T) {
	testDir := setupTestData(t)

	validators, err := DiscoverTests(Config{
		ConfigFile:   filepath.Join(testDir, "validators.yaml"),
		ValidatorDir: testDir,
	})
	require.NoError(t, err)

	result := ValidatorHierarchyString(validators)
	require.NotEmpty(t, result)

	expected := `Validator Hierarchy:
└── Gate: test-gate
    ├── Direct Tests:
    │   └── test1
    └── Suites:
        └── test-suite
            └── suite-test1
`
	require.Equal(t, expected, result)
}
