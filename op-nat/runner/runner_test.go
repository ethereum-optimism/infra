package runner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum-optimism/infra/op-nat/registry"
	"github.com/ethereum-optimism/infra/op-nat/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGate(t *testing.T) {}

func TestSuite(t *testing.T) {}

func TestDirectToGate(t *testing.T) {
	assert.True(t, true, "this test should pass")
}

func TestInSuite(t *testing.T) {
	assert.True(t, true, "this test should pass")
}

func setupTestRunner(t *testing.T) *runner {
	testDir := t.TempDir()
	configPath := filepath.Join(testDir, "validators.yaml")

	// Create test validator config
	configContent := []byte(`
gates:
  - id: test-gate
    description: "Test gate"
    tests:
      - name: TestOne
        package: "./testdata/package"
`)
	err := os.WriteFile(configPath, configContent, 0644)
	require.NoError(t, err)

	reg, err := registry.NewRegistry(registry.Config{
		Source: types.SourceConfig{
			Location:   testDir,
			ConfigPath: configPath,
		},
		WorkDir: ".",
	})
	require.NoError(t, err)

	r, err := NewTestRunner(reg)
	require.NoError(t, err)
	return r.(*runner)
}

func TestRunTest_SingleTest(t *testing.T) {
	r := setupTestRunner(t)

	result, err := r.RunTest(types.ValidatorMetadata{
		ID:       "test1",
		Gate:     "test-gate",
		FuncName: "TestDirectToGate",
		Package:  ".",
	})

	require.NoError(t, err)
	assert.True(t, result.Passed)
	assert.Empty(t, result.Error)
}

func TestRunTest_RunAll(t *testing.T) {
	r := setupTestRunner(t)

	result, err := r.RunTest(types.ValidatorMetadata{
		ID:      "all-tests",
		Gate:    "test-gate",
		Package: "./testdata/package",
		RunAll:  true,
	})

	require.NoError(t, err)
	assert.True(t, result.Passed)
	assert.Empty(t, result.Error)
}

func TestRunAllTests(t *testing.T) {
	// Create a test runner with known validators
	testDir := t.TempDir()
	configPath := filepath.Join(testDir, "validators.yaml")

	// Create test validator config with both tests and suites
	configContent := []byte(`
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
`)
	err := os.WriteFile(configPath, configContent, 0644)
	require.NoError(t, err)

	reg, err := registry.NewRegistry(registry.Config{
		Source: types.SourceConfig{
			Location:   testDir,
			ConfigPath: configPath,
		},
		WorkDir: ".",
	})
	require.NoError(t, err)

	r, err := NewTestRunner(reg)
	require.NoError(t, err)

	// Run all tests
	result, err := r.RunAllTests()
	require.NoError(t, err)
	assert.True(t, result.Passed)

	// Verify structure
	require.Contains(t, result.Gates, "test-gate", "should have test-gate")
	gate := result.Gates["test-gate"]
	assert.True(t, gate.Passed)

	// Verify gate has both direct tests and suites
	assert.NotEmpty(t, gate.Tests, "should have direct tests")
	assert.NotEmpty(t, gate.Suites, "should have suites")

	// Verify suite structure
	require.Contains(t, gate.Suites, "test-suite", "should have test-suite")
	suite := gate.Suites["test-suite"]
	assert.True(t, suite.Passed)
	assert.NotEmpty(t, suite.Tests, "suite should have tests")
}

func TestBuildTestArgs(t *testing.T) {
	r := setupTestRunner(t)

	tests := []struct {
		name     string
		metadata types.ValidatorMetadata
		want     []string
	}{
		{
			name: "specific test",
			metadata: types.ValidatorMetadata{
				FuncName: "TestFoo",
				Package:  "pkg/foo",
			},
			want: []string{"test", "pkg/foo", "-run", "^TestFoo$", "-v"},
		},
		{
			name: "run all in package",
			metadata: types.ValidatorMetadata{
				Package: "pkg/foo",
				RunAll:  true,
			},
			want: []string{"test", "pkg/foo", "-v"},
		},
		{
			name: "no package specified",
			metadata: types.ValidatorMetadata{
				FuncName: "TestFoo",
			},
			want: []string{"test", "./...", "-run", "^TestFoo$", "-v"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.buildTestArgs(tt.metadata)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsValidTestName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"TestFoo", true},
		{"", false},
		{"ok", false},
		{"?   pkg/foo", false},
		{"TestBar_SubTest", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidTestName(tt.name)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFormatErrors(t *testing.T) {
	r := setupTestRunner(t)

	tests := []struct {
		name   string
		errors []string
		want   string
	}{
		{
			name:   "no errors",
			errors: nil,
			want:   "",
		},
		{
			name:   "single error",
			errors: []string{"test failed"},
			want:   "Failed tests:\ntest failed",
		},
		{
			name:   "multiple errors",
			errors: []string{"test1 failed", "test2 failed"},
			want:   "Failed tests:\ntest1 failed\ntest2 failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.formatErrors(tt.errors)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRunTests(t *testing.T) {
	// Reset validators for testing
	validators = []types.ValidatorMetadata{
		{
			ID:   "test-gate",
			Type: types.ValidatorTypeGate,
		},
		{
			ID:   "test-suite",
			Type: types.ValidatorTypeSuite,
			Gate: "test-gate",
		},
		{
			ID:       "test1",
			Type:     types.ValidatorTypeTest,
			Gate:     "test-gate",
			FuncName: "TestDirectToGate",
		},
		{
			ID:       "test2",
			Type:     types.ValidatorTypeTest,
			Gate:     "test-gate",
			Suite:    "test-suite",
			FuncName: "TestInSuite",
		},
	}

	// Initialize results
	results = &RunnerResult{
		Gates:  make(map[string]*GateResult),
		Passed: true,
	}
	initializeResults()

	// Run tests
	RunTests(t)

	// Verify results were collected
	assert.True(t, results.Passed, "all tests should pass")
	assert.Len(t, results.Gates, 1, "should have one gate")
}

func TestUpdateResults(t *testing.T) {
	// Reset results for testing
	results = &RunnerResult{
		Gates:  make(map[string]*GateResult),
		Passed: true,
	}

	// Initialize with a test gate and suite
	gateID := "test-gate"
	suiteID := "test-suite"

	results.Gates[gateID] = &GateResult{
		ID: gateID,
		Suites: map[string]*SuiteResult{
			suiteID: {
				ID:     suiteID,
				Tests:  make([]*TestResult, 0),
				Passed: true,
			},
		},
		Tests:  make([]*TestResult, 0),
		Passed: true,
	}

	tests := []struct {
		name     string
		result   TestResult
		wantPass bool
	}{
		{
			name: "direct gate test pass",
			result: TestResult{
				Metadata: types.ValidatorMetadata{
					ID:   "direct-test",
					Type: types.ValidatorTypeTest,
					Gate: gateID,
				},
				Passed: true,
			},
			wantPass: true,
		},
		{
			name: "suite test fail",
			result: TestResult{
				Metadata: types.ValidatorMetadata{
					ID:    "suite-test",
					Type:  types.ValidatorTypeTest,
					Gate:  gateID,
					Suite: suiteID,
				},
				Passed: false,
				Error:  "test failed",
			},
			wantPass: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			updateResults(tt.result)

			// Find the result in our structure
			gate := results.Gates[tt.result.Metadata.Gate]
			require.NotNil(t, gate, "gate should exist")

			if tt.result.Metadata.Suite == "" {
				// Direct gate test
				found := false
				for _, test := range gate.Tests {
					if test.Metadata.ID == tt.result.Metadata.ID {
						assert.Equal(t, tt.wantPass, test.Passed)
						found = true
						break
					}
				}
				assert.True(t, found, "test result not found in gate")
			} else {
				// Suite test
				suite := gate.Suites[tt.result.Metadata.Suite]
				require.NotNil(t, suite, "suite should exist")
				found := false
				for _, test := range suite.Tests {
					if test.Metadata.ID == tt.result.Metadata.ID {
						assert.Equal(t, tt.wantPass, test.Passed)
						found = true
						break
					}
				}
				assert.True(t, found, "test result not found in suite")
			}
		})
	}
}

func TestInitializeResults(t *testing.T) {
	// Reset validators and results
	validators = []types.ValidatorMetadata{
		{
			ID:   "test-gate",
			Type: types.ValidatorTypeGate,
		},
		{
			ID:   "test-suite",
			Type: types.ValidatorTypeSuite,
			Gate: "test-gate",
		},
	}
	results = &RunnerResult{
		Gates:  make(map[string]*GateResult),
		Passed: true,
	}

	initializeResults()

	assert.Len(t, results.Gates, 1, "should have one gate")
	gate := results.Gates["test-gate"]
	assert.Equal(t, "test-gate", gate.ID)
	assert.Len(t, gate.Suites, 1, "gate should have one suite")
	assert.Equal(t, "test-suite", gate.Suites["test-suite"].ID)
}

// Add a helper function to create a temporary test file
func createTempTestFile(t *testing.T) string {
	dir := t.TempDir()
	content := []byte(`
package test

import "testing"

func TestOne(t *testing.T) {
	t.Log("Test one running")
}

func TestTwo(t *testing.T) {
	t.Log("Test two running")
}
`)
	testFile := filepath.Join(dir, "temp_test.go")
	err := os.WriteFile(testFile, content, 0644)
	require.NoError(t, err)
	return dir
}
