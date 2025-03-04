package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ethereum-optimism/infra/op-acceptor/registry"
	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func initGoModule(t *testing.T, dir string, pkgPath string) {
	t.Helper()
	cmd := exec.Command("go", "mod", "init", pkgPath)
	cmd.Dir = dir
	err := cmd.Run()
	require.NoError(t, err)
}

func setupTestRunner(t *testing.T, testContent, configContent []byte) *runner {
	// Create test directory and config file
	testDir := t.TempDir()

	// Initialize go module in test directory
	initGoModule(t, testDir, "test")

	// Create a test file in the feature directory
	featureDir := filepath.Join(testDir, "feature")
	err := os.MkdirAll(featureDir, 0755)
	require.NoError(t, err)

	// Create a test file with example tests
	err = os.WriteFile(filepath.Join(featureDir, "example_test.go"), testContent, 0644)
	require.NoError(t, err)

	// Create test validator config
	validatorConfigPath := filepath.Join(testDir, "validators.yaml")
	err = os.WriteFile(validatorConfigPath, configContent, 0644)
	require.NoError(t, err)

	// Create registry with correct paths
	reg, err := registry.NewRegistry(registry.Config{
		ValidatorConfigFile: validatorConfigPath,
	})
	require.NoError(t, err)

	r, err := NewTestRunner(Config{
		Registry: reg,
		WorkDir:  testDir,
	})
	require.NoError(t, err)
	return r.(*runner)
}

func setupDefaultTestRunner(t *testing.T) *runner {
	testContent := []byte(`
package feature_test

import "testing"

func TestOne(t *testing.T) {
	t.Log("Test one running")
}

func TestTwo(t *testing.T) {
	t.Log("Test two running")
}
`)

	configContent := []byte(`
gates:
  - id: test-gate
    description: "Test gate"
    suites:
      test-suite:
        description: "Test suite"
        tests:
          - name: TestOne
            package: "./feature"
    tests:
      - name: TestTwo
        package: "./feature"
`)
	return setupTestRunner(t, testContent, configContent)
}

func TestRunTest_SingleTest(t *testing.T) {
	r := setupDefaultTestRunner(t)

	// Create a simple test file in the work directory
	testContent := []byte(`
package main

import "testing"

func TestDirectToGate(t *testing.T) {
	t.Log("Test running")
}
`)
	err := os.WriteFile(filepath.Join(r.workDir, "main_test.go"), testContent, 0644)
	require.NoError(t, err)

	result, err := r.RunTest(types.ValidatorMetadata{
		ID:       "test1",
		Gate:     "test-gate",
		FuncName: "TestDirectToGate",
		Package:  ".",
	})

	require.NoError(t, err)
	assert.Equal(t, types.TestStatusPass, result.Status)
	assert.Nil(t, result.Error)
	assert.Equal(t, "test1", result.Metadata.ID)
	assert.Equal(t, "test-gate", result.Metadata.Gate)
	assert.Equal(t, ".", result.Metadata.Package)
	assert.False(t, result.Metadata.RunAll)
}

func TestRunTest_RunAll(t *testing.T) {
	r := setupDefaultTestRunner(t)

	result, err := r.RunTest(types.ValidatorMetadata{
		ID:      "all-tests",
		Gate:    "test-gate",
		Package: "./feature",
		RunAll:  true,
	})

	require.NoError(t, err)
	assert.Equal(t, types.TestStatusPass, result.Status)
	assert.Empty(t, result.Error)
	assert.Equal(t, "all-tests", result.Metadata.ID)
	assert.Equal(t, "test-gate", result.Metadata.Gate)
	assert.Equal(t, "./feature", result.Metadata.Package)
	assert.True(t, result.Metadata.RunAll)
}

func TestRunAllTests(t *testing.T) {
	r := setupDefaultTestRunner(t)

	// Run all tests
	result, err := r.RunAllTests()
	require.NoError(t, err)
	assert.Equal(t, types.TestStatusPass, result.Status)

	// Verify structure
	require.Contains(t, result.Gates, "test-gate", "should have test-gate")
	gate := result.Gates["test-gate"]
	assert.Equal(t, types.TestStatusPass, gate.Status)

	// Verify gate has both direct tests and suites
	assert.NotEmpty(t, gate.Tests, "should have direct tests")
	assert.NotEmpty(t, gate.Suites, "should have suites")

	// Verify suite structure
	require.Contains(t, gate.Suites, "test-suite", "should have test-suite")
	suite := gate.Suites["test-suite"]
	assert.Equal(t, types.TestStatusPass, suite.Status)
	assert.NotEmpty(t, suite.Tests, "suite should have tests")
}

func TestBuildTestArgs(t *testing.T) {
	r := setupDefaultTestRunner(t)

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
			want: []string{"test", "pkg/foo", "-run", "^TestFoo$", "-count", "1", "-v"},
		},
		{
			name: "run all in package",
			metadata: types.ValidatorMetadata{
				Package: "pkg/foo",
				RunAll:  true,
			},
			want: []string{"test", "pkg/foo", "-count", "1", "-v"},
		},
		{
			name: "no package specified",
			metadata: types.ValidatorMetadata{
				FuncName: "TestFoo",
			},
			want: []string{"test", "./...", "-run", "^TestFoo$", "-count", "1", "-v"},
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
	r := setupDefaultTestRunner(t)

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

func TestGate(t *testing.T) {
	t.Run("gate with direct tests", func(t *testing.T) {
		configContent := []byte(`
gates:
  - id: direct-test-gate
    description: "Gate with direct tests"
    tests:
      - name: TestOne
        package: "./feature"
      - name: TestTwo
        package: "./feature"
`)
		testContent := []byte(`
package feature_test

import "testing"

func TestOne(t *testing.T) {
	t.Log("Test one running")
}

func TestTwo(t *testing.T) {
	t.Log("Test two running")
}
`)
		r := setupTestRunner(t, testContent, configContent)
		result, err := r.RunAllTests()
		require.NoError(t, err)

		// Verify gate structure
		require.Contains(t, result.Gates, "direct-test-gate")
		gate := result.Gates["direct-test-gate"]
		assert.Empty(t, gate.Suites, "should have no suites")
		assert.Len(t, gate.Tests, 2, "should have two direct tests")
	})

	t.Run("gate with inheritance", func(t *testing.T) {
		configContent := []byte(`
gates:
  - id: parent-gate
    description: "Parent gate"
    tests:
      - name: TestParent
        package: "./feature"

  - id: child-gate
    description: "Child gate"
    inherits: ["parent-gate"]
    tests:
      - name: TestChild
        package: "./feature"
`)
		testContent := []byte(`
package feature_test

import "testing"

func TestParent(t *testing.T) {
	t.Log("Parent test running")
}

func TestChild(t *testing.T) {
	t.Log("Child test running")
}
`)
		r := setupTestRunner(t, testContent, configContent)
		result, err := r.RunAllTests()
		require.NoError(t, err)

		// Verify inherited tests are present
		require.Contains(t, result.Gates, "child-gate")
		childGate := result.Gates["child-gate"]
		assert.Len(t, childGate.Tests, 2, "should have both parent and child tests")
	})
}

func TestSuite(t *testing.T) {
	t.Run("suite configuration", func(t *testing.T) {
		configContent := []byte(`
gates:
  - id: suite-test-gate
    description: "Gate with suites"
    suites:
      suite-one:
        description: "First test suite"
        tests:
          - name: TestSuiteOne
            package: "./feature"
      suite-two:
        description: "Second test suite"
        tests:
          - name: TestSuiteTwo
            package: "./feature"
          - name: TestSuiteThree
            package: "./feature"
`)
		testContent := []byte(`
package feature_test

import "testing"

func TestSuiteOne(t *testing.T) {
	t.Log("Suite one test running")
}

func TestSuiteTwo(t *testing.T) {
	t.Log("Suite two test running")
}
	`)

		r := setupTestRunner(t, testContent, configContent)
		result, err := r.RunAllTests()
		require.NoError(t, err)

		// Verify suite structure
		require.Contains(t, result.Gates, "suite-test-gate")
		gate := result.Gates["suite-test-gate"]

		assert.Len(t, gate.Suites, 2, "should have two suites")

		suiteOne := gate.Suites["suite-one"]
		require.NotNil(t, suiteOne)
		assert.Len(t, suiteOne.Tests, 1, "suite-one should have one test")

		suiteTwo := gate.Suites["suite-two"]
		require.NotNil(t, suiteTwo)
		assert.Len(t, suiteTwo.Tests, 2, "suite-two should have two tests")
	})
}

// Add a new test for skipped tests
func TestRunTest_SkippedTest(t *testing.T) {
	r := setupDefaultTestRunner(t)

	// Create a test file with a skipped test
	testContent := []byte(`
package main

import "testing"

func TestSkipped(t *testing.T) {
	t.Skip("Skipping this test")
}
`)
	err := os.WriteFile(filepath.Join(r.workDir, "skip_test.go"), testContent, 0644)
	require.NoError(t, err)

	result, err := r.RunTest(types.ValidatorMetadata{
		ID:       "skip-test",
		Gate:     "test-gate",
		FuncName: "TestSkipped",
		Package:  ".",
	})

	require.NoError(t, err)
	assert.Equal(t, types.TestStatusSkip, result.Status)
	assert.Nil(t, result.Error)
}

func TestStatusDetermination(t *testing.T) {
	tests := []struct {
		name     string
		setup    func() *GateResult
		expected types.TestStatus
	}{
		{
			name: "all tests passed",
			setup: func() *GateResult {
				return &GateResult{
					Tests: map[string]*types.TestResult{
						"test1": {Status: types.TestStatusPass},
						"test2": {Status: types.TestStatusPass},
					},
				}
			},
			expected: types.TestStatusPass,
		},
		{
			name: "all tests skipped",
			setup: func() *GateResult {
				return &GateResult{
					Tests: map[string]*types.TestResult{
						"test1": {Status: types.TestStatusSkip},
						"test2": {Status: types.TestStatusSkip},
					},
				}
			},
			expected: types.TestStatusSkip,
		},
		{
			name: "mixed results with failure",
			setup: func() *GateResult {
				return &GateResult{
					Tests: map[string]*types.TestResult{
						"test1": {Status: types.TestStatusPass},
						"test2": {Status: types.TestStatusFail},
						"test3": {Status: types.TestStatusSkip},
					},
				}
			},
			expected: types.TestStatusFail,
		},
		{
			name: "mixed results without failure",
			setup: func() *GateResult {
				return &GateResult{
					Tests: map[string]*types.TestResult{
						"test1": {Status: types.TestStatusPass},
						"test2": {Status: types.TestStatusSkip},
					},
				}
			},
			expected: types.TestStatusPass,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gate := tt.setup()
			status := determineGateStatus(gate)
			assert.Equal(t, tt.expected, status)
		})
	}
}

func TestSuiteStatusDetermination(t *testing.T) {
	tests := []struct {
		name     string
		setup    func() *SuiteResult
		expected types.TestStatus
	}{
		{
			name: "empty suite",
			setup: func() *SuiteResult {
				return &SuiteResult{
					Tests: make(map[string]*types.TestResult),
				}
			},
			expected: types.TestStatusSkip,
		},
		{
			name: "all tests passed",
			setup: func() *SuiteResult {
				return &SuiteResult{
					Tests: map[string]*types.TestResult{
						"test1": {Status: types.TestStatusPass},
						"test2": {Status: types.TestStatusPass},
					},
				}
			},
			expected: types.TestStatusPass,
		},
		{
			name: "all tests skipped",
			setup: func() *SuiteResult {
				return &SuiteResult{
					Tests: map[string]*types.TestResult{
						"test1": {Status: types.TestStatusSkip},
						"test2": {Status: types.TestStatusSkip},
					},
				}
			},
			expected: types.TestStatusSkip,
		},
		{
			name: "mixed results",
			setup: func() *SuiteResult {
				return &SuiteResult{
					Tests: map[string]*types.TestResult{
						"test1": {Status: types.TestStatusPass},
						"test2": {Status: types.TestStatusSkip},
						"test3": {Status: types.TestStatusFail},
					},
				}
			},
			expected: types.TestStatusFail,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suite := tt.setup()
			status := determineSuiteStatus(suite)
			assert.Equal(t, tt.expected, status)
		})
	}
}
