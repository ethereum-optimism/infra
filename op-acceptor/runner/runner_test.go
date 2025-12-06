package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/flags"
	"github.com/ethereum-optimism/infra/op-acceptor/registry"
	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/ethereum-optimism/infra/op-acceptor/ui"
	"github.com/ethereum-optimism/optimism/devnet-sdk/shell/env"
	"github.com/ethereum-optimism/optimism/op-service/testlog"
	"github.com/ethereum/go-ethereum/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func initGoModule(t testing.TB, dir string, pkgPath string) {
	t.Helper()
	cmd := exec.Command("go", "mod", "init", pkgPath)
	cmd.Dir = dir
	err := cmd.Run()
	require.NoError(t, err)
}

type TestRunnerOption func(*Config)

func WithShowProgress(enabled bool) TestRunnerOption {
	return func(c *Config) {
		c.ShowProgress = enabled
	}
}

func WithProgressInterval(interval time.Duration) TestRunnerOption {
	return func(c *Config) {
		c.ProgressInterval = interval
	}
}

func WithLogger(logger log.Logger) TestRunnerOption {
	return func(c *Config) {
		c.Log = logger
	}
}

func WithSerial(serial bool) TestRunnerOption {
	return func(c *Config) {
		c.Serial = serial
	}
}

func setupTestRunnerWithGates(t *testing.T, testContent, configContent []byte, targetGates []string) *runner {
	testDir := t.TempDir()

	initGoModule(t, testDir, "test")

	featureDir := filepath.Join(testDir, "feature")
	err := os.MkdirAll(featureDir, 0755)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(featureDir, "example_test.go"), testContent, 0644)
	require.NoError(t, err)

	validatorConfigPath := filepath.Join(testDir, "validators.yaml")
	err = os.WriteFile(validatorConfigPath, configContent, 0644)
	require.NoError(t, err)

	reg, err := registry.NewRegistry(registry.Config{
		ValidatorConfigFile: validatorConfigPath,
	})
	require.NoError(t, err)

	lgr := testlog.Logger(t, slog.LevelDebug)

	config := Config{
		Registry:   reg,
		TargetGate: targetGates,
		WorkDir:    testDir,
		Log:        lgr,
	}

	r, err := NewTestRunner(config)
	require.NoError(t, err)
	return r.(*runner)
}

func setupTestRunner(t *testing.T, testContent, configContent []byte, opts ...TestRunnerOption) *runner {
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

	lgr := testlog.Logger(t, slog.LevelDebug)

	// Start with default config
	config := Config{
		Registry: reg,
		WorkDir:  testDir,
		Log:      lgr,
	}

	// Apply all options
	for _, opt := range opts {
		opt(&config)
	}

	r, err := NewTestRunner(config)
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
	return setupTestRunner(t, testContent, configContent, WithShowProgress(true), WithProgressInterval(100*time.Millisecond))
}

func TestRunTest_SingleTest(t *testing.T) {
	ctx := context.Background()
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

	result, err := r.RunTest(ctx, types.ValidatorMetadata{
		ID:       "test1",
		Gate:     "test-gate",
		FuncName: "TestDirectToGate",
		Package:  ".",
	})

	require.NoError(t, err)
	assert.Equal(t, types.TestStatusPass, result.Status)
	assert.Equal(t, "test1", result.Metadata.ID)
	assert.Equal(t, "test-gate", result.Metadata.Gate)
	assert.Equal(t, ".", result.Metadata.Package)
	assert.False(t, result.Metadata.RunAll)
}

func TestRunTest_RunAll(t *testing.T) {
	ctx := context.Background()
	r := setupDefaultTestRunner(t)

	result, err := r.RunTest(ctx, types.ValidatorMetadata{
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
	ctx := context.Background()
	r := setupDefaultTestRunner(t)

	// Run all tests
	result, err := r.RunAllTests(ctx)
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
				Timeout:  10 * time.Second,
			},
			want: []string{"test", "pkg/foo", "-run", "^TestFoo$", "-count", "1", "-timeout", "10s", "-v", "-json"},
		},
		{
			name: "run all in package",
			metadata: types.ValidatorMetadata{
				Package: "pkg/foo",
				RunAll:  true,
			},
			want: []string{"test", "pkg/foo", "-count", "1", "-timeout", "10m0s", "-v", "-json"},
		},
		{
			name: "no package specified",
			metadata: types.ValidatorMetadata{
				FuncName: "TestFoo",
			},
			want: []string{"test", "./...", "-run", "^TestFoo$", "-count", "1", "-v", "-json"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.buildTestArgs(tt.metadata)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGate(t *testing.T) {
	ctx := context.Background()
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
		result, err := r.RunAllTests(ctx)
		require.NoError(t, err)

		// Verify gate structure
		require.Contains(t, result.Gates, "direct-test-gate")
		gate := result.Gates["direct-test-gate"]
		assert.Empty(t, gate.Suites, "should have no suites")
		assert.Len(t, gate.Tests, 2, "should have two direct tests")
	})

	// Separate test to verify all-excluded becomes a no-op
	t.Run("all tests excluded becomes noop", func(t *testing.T) {
		cfgContent := []byte(`
gates:
  - id: base
    tests:
      - name: TestT1
        package: "./feature"
`)
		testContent2 := []byte(`
package feature_test

import "testing"

func TestT1(t *testing.T) { t.Log("T1 running") }
`)
		_ = setupTestRunner(t, testContent2, cfgContent)
		// Write a temp validators file
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "validators.yaml")
		require.NoError(t, os.WriteFile(cfgPath, cfgContent, 0644))

		reg, err := registry.NewRegistry(registry.Config{ValidatorConfigFile: cfgPath, ExcludeGates: []string{"base"}})
		require.NoError(t, err)

		rr, err := NewTestRunner(Config{Registry: reg, WorkDir: t.TempDir(), Log: log.New()})
		require.NoError(t, err)

		res, err := rr.RunAllTests(ctx)
		require.NoError(t, err)
		assert.Equal(t, 0, res.Stats.Total)
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
		result, err := r.RunAllTests(ctx)
		require.NoError(t, err)

		// Verify both gates are present (parent-gate is shown separately because it has validators)
		require.Contains(t, result.Gates, "parent-gate")
		require.Contains(t, result.Gates, "child-gate")
		parentGate := result.Gates["parent-gate"]
		childGate := result.Gates["child-gate"]
		assert.Len(t, parentGate.Tests, 1, "parent-gate should have its own test")
		assert.Len(t, childGate.Tests, 1, "child-gate should have its own test (parent-gate is shown separately)")
	})
}

func TestSuite(t *testing.T) {
	ctx := context.Background()
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
		result, err := r.RunAllTests(ctx)
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
	ctx := context.Background()
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

	result, err := r.RunTest(ctx, types.ValidatorMetadata{
		ID:       "skip-test",
		Gate:     "test-gate",
		FuncName: "TestSkipped",
		Package:  ".",
	})

	require.NoError(t, err)
	assert.Equal(t, types.TestStatusSkip, result.Status)
	// With JSON output, we may capture test output even for skipped tests
	// assert.Nil(t, result.Error)
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

func TestRunPackageTests(t *testing.T) {
	ctx := context.Background()
	// Setup test with multiple tests in a package
	testContent := []byte(`
package feature_test

import "testing"

func TestPackageOne(t *testing.T) {
	t.Log("Test package one running")
}

func TestPackageTwo(t *testing.T) {
	t.Log("Test package two running")
}

func TestPackageThree(t *testing.T) {
	t.Log("Test package three running")
}

func TestPackageFour(t *testing.T) {
	t.Log("Test package four running")
}
`)

	configContent := []byte(`
gates:
  - id: package-gate
    description: "Package gate"
    suites:
      package-suite:
        description: "Package suite"
        tests:
          - package: "./feature"
            run_all: true
`)
	r := setupTestRunner(t, testContent, configContent)

	// Run all tests
	result, err := r.RunAllTests(ctx)
	require.NoError(t, err)
	assert.Equal(t, types.TestStatusPass, result.Status)

	// Verify structure
	require.Contains(t, result.Gates, "package-gate", "should have package-gate")
	gate := result.Gates["package-gate"]
	assert.Equal(t, types.TestStatusPass, gate.Status)

	// Verify suite structure
	require.Contains(t, gate.Suites, "package-suite", "should have package-suite")
	suite := gate.Suites["package-suite"]
	assert.Equal(t, types.TestStatusPass, suite.Status)

	// Verify tests in the suite
	assert.Len(t, suite.Tests, 1, "should have one test (the package)")

	// Get the package test
	var packageTest *types.TestResult
	for _, test := range suite.Tests {
		packageTest = test
		break
	}
	require.NotNil(t, packageTest, "package test should exist")

	// Verify the package test has subtests
	assert.NotEmpty(t, packageTest.SubTests, "package test should have subtests")
	assert.Len(t, packageTest.SubTests, 4, "should have found all 4 tests in the package")

	// Verify each subtest exists and passed
	subTestNames := []string{"TestPackageOne", "TestPackageTwo", "TestPackageThree", "TestPackageFour"}
	for _, name := range subTestNames {
		assert.Contains(t, packageTest.SubTests, name, "should have subtest "+name)
		assert.Equal(t, types.TestStatusPass, packageTest.SubTests[name].Status, name+" should be passing")
	}

	// Verify stats include all subtests
	assert.Equal(t, 5, result.Stats.Total, "stats should include all tests (1 package + 4 subtests)")
	assert.Equal(t, 5, result.Stats.Passed, "all tests should be passing")
	assert.Equal(t, 0, result.Stats.Failed, "no tests should be failing")
	assert.Equal(t, 0, result.Stats.Skipped, "no tests should be skipped")

	// Verify gate stats
	assert.Equal(t, 5, gate.Stats.Total, "gate stats should include all tests")
	assert.Equal(t, 5, gate.Stats.Passed, "all gate tests should be passing")

	// Verify suite stats
	assert.Equal(t, 5, suite.Stats.Total, "suite stats should include all tests")
	assert.Equal(t, 5, suite.Stats.Passed, "all suite tests should be passing")
}

func TestRunPackageWithFailingTests(t *testing.T) {
	ctx := context.Background()
	// Setup test with a failing test in a package
	testContent := []byte(`
package feature_test

import "testing"

func TestFailing(t *testing.T) {
	t.Error("This test fails")
}
`)

	configContent := []byte(`
gates:
  - id: failing-gate
    description: "Gate with a failing test"
    suites:
      failing-suite:
        description: "Suite with a failing test"
        tests:
          - package: "./feature"
            run_all: true
`)
	r := setupTestRunner(t, testContent, configContent)

	// Run all tests
	result, err := r.RunAllTests(ctx)
	require.NoError(t, err)
	assert.Equal(t, types.TestStatusFail, result.Status, "overall result should be failure when any test fails")

	// Verify structure
	require.Contains(t, result.Gates, "failing-gate", "should have failing-gate")
	gate := result.Gates["failing-gate"]
	assert.Equal(t, types.TestStatusFail, gate.Status, "gate status should be failure")

	// Verify suite structure
	require.Contains(t, gate.Suites, "failing-suite", "should have failing-suite")
	suite := gate.Suites["failing-suite"]
	assert.Equal(t, types.TestStatusFail, suite.Status, "suite status should be failure")

	// Verify tests in the suite
	assert.Len(t, suite.Tests, 1, "should have one test (the package)")

	// Get the package test
	var packageTest *types.TestResult
	for _, test := range suite.Tests {
		packageTest = test
		break
	}
	require.NotNil(t, packageTest, "package test should exist")

	// Verify the package test failed
	assert.Equal(t, types.TestStatusFail, packageTest.Status, "package test should be marked as failing")
	assert.NotNil(t, packageTest.Error, "package test should have an error")

	// Verify the package test has subtests
	assert.NotEmpty(t, packageTest.SubTests, "package test should have subtests")
	assert.Len(t, packageTest.SubTests, 1, "should have found the failing test")

	// Verify the subtest has the correct status
	subTest := packageTest.SubTests["TestFailing"]
	require.NotNil(t, subTest, "should have the TestFailing subtest")
	assert.Equal(t, types.TestStatusFail, subTest.Status, "subtest should be failing")

	// Verify stats are accurate
	assert.Equal(t, 2, result.Stats.Total, "stats should include all tests (1 package + 1 subtest)")
	assert.Equal(t, 0, result.Stats.Passed, "no tests should pass")
	assert.Equal(t, 2, result.Stats.Failed, "1 subtest and parent package should fail")
	assert.Equal(t, 0, result.Stats.Skipped, "no tests should be skipped")

	// Verify gate stats
	assert.Equal(t, 2, gate.Stats.Total, "gate stats should include all tests")
	assert.Equal(t, 0, gate.Stats.Passed, "no tests should pass")
	assert.Equal(t, 2, gate.Stats.Failed, "all tests should fail")
	assert.Equal(t, 0, gate.Stats.Skipped, "no tests should be skipped")

	// Verify suite stats
	assert.Equal(t, 2, suite.Stats.Total, "suite stats should include all tests")
	assert.Equal(t, 0, suite.Stats.Passed, "no tests should pass")
	assert.Equal(t, 2, suite.Stats.Failed, "all tests should fail")
	assert.Equal(t, 0, suite.Stats.Skipped, "no tests should be skipped")
}

func TestMultiplePackageTests(t *testing.T) {
	ctx := context.Background()
	// Setup tests in two different packages
	packageOneContent := []byte(`
package pkg1_test

import "testing"

func TestPkg1One(t *testing.T) {
	t.Log("Test pkg1 one running")
}

func TestPkg1Two(t *testing.T) {
	t.Log("Test pkg1 two running")
}
`)

	packageTwoContent := []byte(`
package pkg2_test

import "testing"

func TestPkg2One(t *testing.T) {
	t.Log("Test pkg2 one running")
}

func TestPkg2Two(t *testing.T) {
	t.Log("Test pkg2 two running")
}
`)

	configContent := []byte(`
gates:
  - id: multi-package-gate
    description: "Gate with multiple package tests"
    suites:
      multi-package-suite:
        description: "Suite with multiple package tests"
        tests:
          - package: "./pkg1"
            run_all: true
          - package: "./pkg2"
            run_all: true
`)

	// Setup the test runner with multiple packages
	r := setupTestRunner(t, nil, configContent) // Using nil for the default package

	// Create directories for each package
	pkg1Dir := filepath.Join(r.workDir, "pkg1")
	pkg2Dir := filepath.Join(r.workDir, "pkg2")

	err := os.Mkdir(pkg1Dir, 0755)
	require.NoError(t, err)
	err = os.Mkdir(pkg2Dir, 0755)
	require.NoError(t, err)

	// Write the test files
	err = os.WriteFile(filepath.Join(pkg1Dir, "pkg1_test.go"), packageOneContent, 0644)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(pkg2Dir, "pkg2_test.go"), packageTwoContent, 0644)
	require.NoError(t, err)

	// Run all tests
	result, err := r.RunAllTests(ctx)
	require.NoError(t, err)

	// Verify structure
	require.Contains(t, result.Gates, "multi-package-gate", "should have multi-package-gate")
	gate := result.Gates["multi-package-gate"]
	assert.Equal(t, types.TestStatusPass, gate.Status, "gate status should be pass")

	// Verify suite structure
	require.Contains(t, gate.Suites, "multi-package-suite", "should have multi-package-suite")
	suite := gate.Suites["multi-package-suite"]
	assert.Equal(t, types.TestStatusPass, suite.Status, "suite status should be pass")

	// Verify tests in the suite
	assert.Len(t, suite.Tests, 2, "should have two tests (one for each package)")

	// Verify each package test has its own subtests
	var pkg1Test, pkg2Test *types.TestResult
	for _, test := range suite.Tests {
		if strings.Contains(test.Metadata.Package, "pkg1") {
			pkg1Test = test
		} else if strings.Contains(test.Metadata.Package, "pkg2") {
			pkg2Test = test
		}
	}

	require.NotNil(t, pkg1Test, "pkg1 test should exist")
	require.NotNil(t, pkg2Test, "pkg2 test should exist")

	// Verify each package test has subtests
	assert.Len(t, pkg1Test.SubTests, 2, "pkg1 should have 2 subtests")
	assert.Len(t, pkg2Test.SubTests, 2, "pkg2 should have 2 subtests")

	// Verify subtests in pkg1
	assert.Contains(t, pkg1Test.SubTests, "TestPkg1One", "should have TestPkg1One subtest")
	assert.Contains(t, pkg1Test.SubTests, "TestPkg1Two", "should have TestPkg1Two subtest")

	// Verify subtests in pkg2
	assert.Contains(t, pkg2Test.SubTests, "TestPkg2One", "should have TestPkg2One subtest")
	assert.Contains(t, pkg2Test.SubTests, "TestPkg2Two", "should have TestPkg2Two subtest")

	// Verify the stats
	assert.Equal(t, 6, result.Stats.Total, "stats should include all tests (2 packages + 4 subtests)")
	assert.Equal(t, 6, result.Stats.Passed, "all tests should be passing")
}

func TestMultiplePackageTestsInGate(t *testing.T) {
	ctx := context.Background()
	// Setup tests in two different packages
	packageOneContent := []byte(`
package pkg1_test

import "testing"

func TestPkg1One(t *testing.T) {
	t.Log("Test pkg1 one running")
}

func TestPkg1Two(t *testing.T) {
	t.Log("Test pkg1 two running")
}
`)

	packageTwoContent := []byte(`
package pkg2_test

import "testing"

func TestPkg2One(t *testing.T) {
	t.Log("Test pkg2 one running")
}

func TestPkg2Two(t *testing.T) {
	t.Log("Test pkg2 two running")
}
`)

	configContent := []byte(`
gates:
  - id: direct-package-gate
    description: "Gate with multiple package tests as direct tests"
    tests:
      - package: "./pkg1"
        run_all: true
      - package: "./pkg2"
        run_all: true
`)

	// Setup the test runner with multiple packages
	r := setupTestRunner(t, nil, configContent) // Using nil for the default package

	// Create directories for each package
	pkg1Dir := filepath.Join(r.workDir, "pkg1")
	pkg2Dir := filepath.Join(r.workDir, "pkg2")

	err := os.Mkdir(pkg1Dir, 0755)
	require.NoError(t, err)
	err = os.Mkdir(pkg2Dir, 0755)
	require.NoError(t, err)

	// Write the test files
	err = os.WriteFile(filepath.Join(pkg1Dir, "pkg1_test.go"), packageOneContent, 0644)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(pkg2Dir, "pkg2_test.go"), packageTwoContent, 0644)
	require.NoError(t, err)

	// Run all tests
	result, err := r.RunAllTests(ctx)
	require.NoError(t, err)

	// Verify structure
	require.Contains(t, result.Gates, "direct-package-gate", "should have direct-package-gate")
	gate := result.Gates["direct-package-gate"]
	assert.Equal(t, types.TestStatusPass, gate.Status, "gate status should be pass")

	// Verify tests in the gate
	assert.Len(t, gate.Tests, 2, "should have two tests (one for each package)")
	assert.Empty(t, gate.Suites, "should not have any suites")

	// Verify each package test has its own subtests
	var pkg1Test, pkg2Test *types.TestResult
	for _, test := range gate.Tests {
		if strings.Contains(test.Metadata.Package, "pkg1") {
			pkg1Test = test
		} else if strings.Contains(test.Metadata.Package, "pkg2") {
			pkg2Test = test
		}
	}

	require.NotNil(t, pkg1Test, "pkg1 test should exist")
	require.NotNil(t, pkg2Test, "pkg2 test should exist")

	// Verify each package test has subtests
	assert.Len(t, pkg1Test.SubTests, 2, "pkg1 should have 2 subtests")
	assert.Len(t, pkg2Test.SubTests, 2, "pkg2 should have 2 subtests")

	// Verify subtests in pkg1
	assert.Contains(t, pkg1Test.SubTests, "TestPkg1One", "should have TestPkg1One subtest")
	assert.Contains(t, pkg1Test.SubTests, "TestPkg1Two", "should have TestPkg1Two subtest")

	// Verify subtests in pkg2
	assert.Contains(t, pkg2Test.SubTests, "TestPkg2One", "should have TestPkg2One subtest")
	assert.Contains(t, pkg2Test.SubTests, "TestPkg2Two", "should have TestPkg2Two subtest")

	// Verify the stats
	assert.Equal(t, 6, result.Stats.Total, "stats should include all tests (2 packages + 4 subtests)")
	assert.Equal(t, 6, result.Stats.Passed, "all tests should be passing")
}

// TestRunTest_PanicRecovery verifies that the RunTest method properly handles and recovers from panics
func TestRunTest_PanicRecovery(t *testing.T) {
	ctx := context.Background()
	// Create a test runner with a test file that will panic when executed
	testContent := []byte(`
package feature_test

import "testing"

func TestPanic(t *testing.T) {
	// This test will deliberately panic
	panic("deliberate test panic")
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
          - name: TestPanic
            package: "./feature"
`)
	r := setupTestRunner(t, testContent, configContent)

	// Run a test that will panic
	metadata := types.ValidatorMetadata{
		ID:       "panic-test",
		Gate:     "test-gate",
		FuncName: "TestPanic",
		Package:  "./feature",
	}

	// The panic should be caught and converted to a test failure
	result, err := r.RunTest(ctx, metadata)

	// The RunTest method actually returns the result but not an error for panics
	// because the Go test command captures the panic and returns a failed test result
	require.NotNil(t, result, "Result should not be nil despite panic")
	assert.Nil(t, err, "RunTest should not return an error for a test panic")

	// Instead, the test should be marked as failed
	assert.Equal(t, types.TestStatusFail, result.Status, "Test status should be fail")
	assert.NotNil(t, result.Error, "Result should have an error")

	// The error should indicate a test failure or contain panic information
	// With JSON output, we may not get the exact error message format we used to expect
	assert.Contains(t, result.Error.Error(), "panic", "Error should indicate a panic occurred")

	// Verify the metadata was preserved
	assert.Equal(t, metadata.ID, result.Metadata.ID)
	assert.Equal(t, metadata.Gate, result.Metadata.Gate)
	assert.Equal(t, metadata.FuncName, result.Metadata.FuncName)
	assert.Equal(t, metadata.Package, result.Metadata.Package)
}

// TestRunAllTests_PanicHandling verifies that the RunAllTests method properly handles tests that panic
func TestRunAllTests_MultipleGates(t *testing.T) {
	testContent := []byte(`
package feature_test

import "testing"

func TestShared(t *testing.T) {
	t.Log("Shared test running")
}

func TestGateA(t *testing.T) {
	t.Log("Gate A specific test")
}

func TestGateB(t *testing.T) {
	t.Log("Gate B specific test")
}
`)

	configContent := []byte(`
gates:
  - id: gate-a
    description: "Gate A"
    tests:
      - name: TestShared
        package: "./feature"
      - name: TestGateA
        package: "./feature"
  - id: gate-b
    description: "Gate B"
    tests:
      - name: TestShared
        package: "./feature"
      - name: TestGateB
        package: "./feature"
`)

	r := setupTestRunnerWithGates(t, testContent, configContent, []string{"gate-a", "gate-b"})

	ctx := context.Background()
	result, err := r.RunAllTests(ctx)
	require.NoError(t, err)

	// Should have separate gates, not a combined gate
	require.Contains(t, result.Gates, "gate-a", "should have gate-a")
	require.Contains(t, result.Gates, "gate-b", "should have gate-b")
	assert.NotContains(t, result.Gates, "gate-a+gate-b", "should NOT have combined gate")

	gateA := result.Gates["gate-a"]
	assert.Equal(t, types.TestStatusPass, gateA.Status)
	assert.Len(t, gateA.Tests, 2, "gate-a should have 2 tests")
	assert.Contains(t, gateA.Tests, "TestShared", "gate-a should contain TestShared")
	assert.Contains(t, gateA.Tests, "TestGateA", "gate-a should contain TestGateA")

	gateB := result.Gates["gate-b"]
	assert.Equal(t, types.TestStatusPass, gateB.Status)
	assert.Len(t, gateB.Tests, 2, "gate-b should have 2 tests")
	assert.Contains(t, gateB.Tests, "TestShared", "gate-b should contain TestShared")
	assert.Contains(t, gateB.Tests, "TestGateB", "gate-b should contain TestGateB")
}

func TestRunAllTests_PanicHandling(t *testing.T) {
	ctx := context.Background()
	// Create a test runner with a mix of normal and panicking tests
	testContent := []byte(`
package feature_test

import "testing"

func TestNormal(t *testing.T) {
	t.Log("This test runs normally")
}

func TestPanic(t *testing.T) {
	// This test will deliberately panic
	panic("deliberate panic in RunAllTests")
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
          - name: TestNormal
            package: "./feature"
          - name: TestPanic
            package: "./feature"
`)
	r := setupTestRunner(t, testContent, configContent)

	// Run all tests - the runner should handle the panic and continue
	result, err := r.RunAllTests(ctx)

	// There should be no error at the top level because the runner handles test panics
	require.NoError(t, err, "RunAllTests should not return an error even with panicking tests")
	require.NotNil(t, result, "Result should not be nil")

	// The overall run status should be fail
	assert.Equal(t, types.TestStatusFail, result.Status, "Run status should be fail when tests panic")

	// Verify gate and suite structure
	require.Contains(t, result.Gates, "test-gate", "Result should contain test-gate")
	gate := result.Gates["test-gate"]
	require.Contains(t, gate.Suites, "test-suite", "Gate should contain test-suite")
	suite := gate.Suites["test-suite"]

	// Verify test results - there should be a normal test and a failing test (the one that panicked)
	require.Equal(t, 2, len(suite.Tests), "Suite should contain 2 tests")

	// Find the normal and panicking tests
	var normalTest, panicTest *types.TestResult
	for _, test := range suite.Tests {
		switch test.Metadata.FuncName {
		case "TestNormal":
			normalTest = test
		case "TestPanic":
			panicTest = test
		}
	}

	// Verify normal test passed
	require.NotNil(t, normalTest, "Normal test should be in results")
	assert.Equal(t, types.TestStatusPass, normalTest.Status, "Normal test should pass")

	// Verify panic test failed
	require.NotNil(t, panicTest, "Panic test should be in results")
	assert.Equal(t, types.TestStatusFail, panicTest.Status, "Panic test should fail")
	assert.NotNil(t, panicTest.Error, "Panic test should have an error")
}

// TestAllowSkipsFlag verifies that the allowSkips flag correctly controls whether
// the DEVNET_EXPECT_PRECONDITIONS_MET environment variable is set
func TestAllowSkipsFlag(t *testing.T) {
	// Create a test that checks its environment variables and outputs it in a predictable format
	testContent := []byte(`
package env_test

import (
	"os"
	"testing"
)

func TestEnvVarCheck(t *testing.T) {
	// Check if DEVNET_EXPECT_PRECONDITIONS_MET is set
	val, exists := os.LookupEnv("DEVNET_EXPECT_PRECONDITIONS_MET")

	// Use a consistent message format that we can check for in the test output
	if exists {
		t.Logf("ENV_VAR_CHECK: DEVNET_EXPECT_PRECONDITIONS_MET=%s", val)
	} else {
		t.Log("ENV_VAR_CHECK: DEVNET_EXPECT_PRECONDITIONS_MET is not set")
	}
}
`)

	configContent := []byte(`
gates:
  - id: test-gate
    description: "Test gate"
    tests:
      - name: TestEnvVarCheck
        package: "./env"
`)

	testCases := []struct {
		name       string
		allowSkips bool
	}{
		{
			name:       "With allowSkips=false, environment variable should be set to true",
			allowSkips: false,
		},
		{
			name:       "With allowSkips=true, environment variable should not be set",
			allowSkips: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create test directory and config file
			testDir := t.TempDir()

			// Initialize go module in test directory
			initGoModule(t, testDir, "test")

			// Create a test file in the env directory
			envDir := filepath.Join(testDir, "env")
			err := os.MkdirAll(envDir, 0755)
			require.NoError(t, err)

			// Create a test file
			err = os.WriteFile(filepath.Join(envDir, "env_test.go"), testContent, 0644)
			require.NoError(t, err)

			// Create test validator config
			validatorConfigPath := filepath.Join(testDir, "validators.yaml")
			err = os.WriteFile(validatorConfigPath, configContent, 0644)
			require.NoError(t, err)

			// Run a direct Go test command to capture the actual environment variables
			// that would be set based on the allowSkips flag
			args := []string{"test", "./env", "-run", "TestEnvVarCheck", "-v"}
			cmd := exec.Command("go", args...)
			cmd.Dir = testDir

			// Set up a runner and use ReproducibleEnv to get the correct environment variables
			r := &runner{
				allowSkips: tc.allowSkips,
				runID:      "test-run-id",
			}

			// Use the actual ReproducibleEnv method to get environment variables
			env := os.Environ()
			env = append(env, r.ReproducibleEnv()...)
			cmd.Env = env

			var stdout bytes.Buffer
			cmd.Stdout = &stdout

			err = cmd.Run()
			require.NoError(t, err)

			output := stdout.String()

			// Verify the correct environment variable behavior based on allowSkips
			if tc.allowSkips {
				assert.Contains(t, output, "ENV_VAR_CHECK: DEVNET_EXPECT_PRECONDITIONS_MET is not set",
					"DEVNET_EXPECT_PRECONDITIONS_MET should not be set when allowSkips=true")
			} else {
				assert.Contains(t, output, "ENV_VAR_CHECK: DEVNET_EXPECT_PRECONDITIONS_MET=true",
					"DEVNET_EXPECT_PRECONDITIONS_MET should be set to 'true' when allowSkips=false")
			}
		})
	}
}

func TestParseTestOutput(t *testing.T) {
	r := setupDefaultTestRunner(t)

	// Helper function to convert TestEvent slices to JSON
	eventToJSON := func(events []TestEvent) string {
		var lines []string
		for _, event := range events {
			data, err := json.Marshal(event)
			require.NoError(t, err)
			lines = append(lines, string(data))
		}
		return strings.Join(lines, "\n")
	}

	// Time values for test events
	baseTime := time.Date(2023, 5, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name            string
		events          []TestEvent
		metadata        types.ValidatorMetadata
		wantStatus      types.TestStatus
		wantError       bool
		wantSubTests    int
		wantSubTestDurs map[string]time.Duration // Added field for expected subtest durations
	}{
		{
			name: "passing test",
			events: []TestEvent{
				{
					Time:    baseTime,
					Action:  ActionStart,
					Package: "pkg/foo",
					Test:    "TestFoo",
				},
				{
					Time:    baseTime.Add(1 * time.Second),
					Action:  ActionPass,
					Package: "pkg/foo",
					Test:    "TestFoo",
					Elapsed: 1.0,
				},
			},
			metadata: types.ValidatorMetadata{
				FuncName: "TestFoo",
				Package:  "pkg/foo",
			},
			wantStatus:   types.TestStatusPass,
			wantError:    false,
			wantSubTests: 0,
		},
		{
			name: "failing test with output",
			events: []TestEvent{
				{
					Time:    baseTime,
					Action:  ActionStart,
					Package: "pkg/foo",
					Test:    "TestFoo",
				},
				{
					Time:    baseTime.Add(100 * time.Millisecond),
					Action:  ActionOutput,
					Package: "pkg/foo",
					Test:    "TestFoo",
					Output:  "Some error occurred\n",
				},
				{
					Time:    baseTime.Add(1 * time.Second),
					Action:  ActionFail,
					Package: "pkg/foo",
					Test:    "TestFoo",
					Elapsed: 1.0,
				},
			},
			metadata: types.ValidatorMetadata{
				FuncName: "TestFoo",
				Package:  "pkg/foo",
			},
			wantStatus:   types.TestStatusFail,
			wantError:    true,
			wantSubTests: 0,
		},
		{
			name: "skipped test",
			events: []TestEvent{
				{
					Time:    baseTime,
					Action:  ActionStart,
					Package: "pkg/foo",
					Test:    "TestFoo",
				},
				{
					Time:    baseTime.Add(100 * time.Millisecond),
					Action:  ActionSkip,
					Package: "pkg/foo",
					Test:    "TestFoo",
					Elapsed: 0.1,
				},
			},
			metadata: types.ValidatorMetadata{
				FuncName: "TestFoo",
				Package:  "pkg/foo",
			},
			wantStatus:   types.TestStatusSkip,
			wantError:    false,
			wantSubTests: 0,
		},
		{
			name: "test with subtests",
			events: []TestEvent{
				{
					Time:    baseTime,
					Action:  ActionStart,
					Package: "pkg/foo",
					Test:    "TestFoo",
				},
				{
					Time:    baseTime.Add(100 * time.Millisecond),
					Action:  ActionStart,
					Package: "pkg/foo",
					Test:    "TestFoo/SubTest1",
				},
				{
					Time:    baseTime.Add(200 * time.Millisecond),
					Action:  ActionPass,
					Package: "pkg/foo",
					Test:    "TestFoo/SubTest1",
					Elapsed: 0.1,
				},
				{
					Time:    baseTime.Add(300 * time.Millisecond),
					Action:  ActionStart,
					Package: "pkg/foo",
					Test:    "TestFoo/SubTest2",
				},
				{
					Time:    baseTime.Add(400 * time.Millisecond),
					Action:  ActionFail,
					Package: "pkg/foo",
					Test:    "TestFoo/SubTest2",
					Elapsed: 0.1,
				},
				{
					Time:    baseTime.Add(1 * time.Second),
					Action:  ActionFail,
					Package: "pkg/foo",
					Test:    "TestFoo",
					Elapsed: 1.0,
				},
			},
			metadata: types.ValidatorMetadata{
				FuncName: "TestFoo",
				Package:  "pkg/foo",
			},
			wantStatus:   types.TestStatusFail,
			wantError:    false,
			wantSubTests: 2,
			wantSubTestDurs: map[string]time.Duration{
				"TestFoo/SubTest1": 100 * time.Millisecond, // 200ms - 100ms
				"TestFoo/SubTest2": 100 * time.Millisecond, // 400ms - 300ms
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert events to JSON string
			jsonOutput := eventToJSON(tt.events)

			result := r.parseTestOutput(strings.NewReader(jsonOutput), tt.metadata)

			assert.NotNil(t, result, "result should not be nil")
			assert.Equal(t, tt.wantStatus, result.Status, "unexpected status")
			assert.Equal(t, tt.wantError, result.Error != nil, "unexpected error presence")
			assert.Equal(t, tt.wantSubTests, len(result.SubTests), "unexpected number of subtests")

			// Additional check for duration
			if tt.wantStatus != types.TestStatusSkip {
				assert.Greater(t, result.Duration, time.Duration(0), "duration should be greater than 0")
			}

			// Verify subtest durations if specified
			if tt.wantSubTestDurs != nil {
				for subTestName, expectedDuration := range tt.wantSubTestDurs {
					subTest, exists := result.SubTests[subTestName]
					assert.True(t, exists, "expected subtest %s to exist", subTestName)
					if exists {
						assert.Equal(t, expectedDuration, subTest.Duration,
							"unexpected duration for subtest %s", subTestName)
					}
				}
			}
		})
	}
}

// TestSubTestDurationCalculation verifies that durations for subtests are correctly calculated
// using either start/end times or the Elapsed field as fallback
func TestSubTestDurationCalculation(t *testing.T) {
	r := setupDefaultTestRunner(t)

	// Helper function to convert TestEvent slices to JSON
	eventToJSON := func(events []TestEvent) string {
		var lines []string
		for _, event := range events {
			data, err := json.Marshal(event)
			require.NoError(t, err)
			lines = append(lines, string(data))
		}
		return strings.Join(lines, "\n")
	}

	// Time values for test events
	baseTime := time.Date(2023, 5, 1, 12, 0, 0, 0, time.UTC)
	expectedDuration1 := 200 * time.Millisecond // SubTest1: 300ms - 100ms = 200ms
	expectedDuration2 := 500 * time.Millisecond // Elapsed value of 0.5s converted to Duration

	// Test scenario where both subtests have ActionStart, but one uses time diff and one uses Elapsed
	events := []TestEvent{
		// Main test start
		{
			Time:    baseTime,
			Action:  ActionStart,
			Package: "pkg/foo",
			Test:    "TestFoo",
		},
		// SubTest1 with start and pass events (should calculate duration from timestamps)
		{
			Time:    baseTime.Add(100 * time.Millisecond),
			Action:  ActionStart,
			Package: "pkg/foo",
			Test:    "TestFoo/SubTest1",
		},
		{
			Time:    baseTime.Add(300 * time.Millisecond),
			Action:  ActionPass,
			Package: "pkg/foo",
			Test:    "TestFoo/SubTest1",
			Elapsed: 0.3, // This should be ignored in favor of the actual time diff
		},
		// SubTest2 with only pass event (should use Elapsed)
		{
			Time:    baseTime.Add(400 * time.Millisecond),
			Action:  ActionPass,
			Package: "pkg/foo",
			Test:    "TestFoo/SubTest2",
			Elapsed: 0.5, // This should be used since there's no start event
		},
		// Main test end
		{
			Time:    baseTime.Add(1 * time.Second),
			Action:  ActionPass,
			Package: "pkg/foo",
			Test:    "TestFoo",
			Elapsed: 1.0,
		},
	}

	metadata := types.ValidatorMetadata{
		FuncName: "TestFoo",
		Package:  "pkg/foo",
	}

	jsonOutput := eventToJSON(events)
	result := r.parseTestOutput(strings.NewReader(jsonOutput), metadata)

	// Verify subtests were created
	require.Equal(t, 2, len(result.SubTests), "Expected 2 subtests")

	// Verify SubTest1 duration (calculated from start/end times)
	subTest1, exists := result.SubTests["TestFoo/SubTest1"]
	require.True(t, exists, "SubTest1 should exist")
	assert.Equal(t, expectedDuration1, subTest1.Duration,
		"SubTest1 duration should be calculated from start/end times")

	// Verify SubTest2 duration (calculated from Elapsed)
	subTest2, exists := result.SubTests["TestFoo/SubTest2"]
	require.True(t, exists, "SubTest2 should exist")
	assert.Equal(t, expectedDuration2, subTest2.Duration,
		"SubTest2 duration should be calculated from Elapsed field")
}

func TestRunTestTimeout_SingleTest_ExceedsTimeout(t *testing.T) {
	ctx := context.Background()
	r := setupDefaultTestRunner(t)

	// Create a simple test file in the work directory
	testContent := []byte(`
package main

import "time"
import "testing"

func TestDirectToGate(t *testing.T) {
	time.Sleep(2 * time.Second)
	t.Log("Test running")
}
`)
	err := os.WriteFile(filepath.Join(r.workDir, "main_test.go"), testContent, 0644)
	require.NoError(t, err)

	result, err := r.RunTest(ctx, types.ValidatorMetadata{
		ID:       "test1",
		Gate:     "test-gate",
		FuncName: "TestDirectToGate",
		Package:  ".",
		Timeout:  1 * time.Second,
	})

	require.NoError(t, err)
	assert.Equal(t, types.TestStatusFail, result.Status)
	assert.Equal(t, "test1", result.Metadata.ID)
	assert.Equal(t, "test-gate", result.Metadata.Gate)
	assert.Equal(t, ".", result.Metadata.Package)
	assert.False(t, result.Metadata.RunAll)

	// Check that the error message contains the expected timeout information
	assert.Contains(t, result.Error.Error(), "TIMEOUT: Test timed out after 1s")
	assert.Contains(t, result.Error.Error(), "actual duration:")
}

func TestRunTestTimeout_SingleTest_DoesNotExceedTimeout(t *testing.T) {
	ctx := context.Background()
	r := setupDefaultTestRunner(t)

	// Create a simple test file in the work directory
	testContent := []byte(`
package main

import "time"
import "testing"

func TestDirectToGate(t *testing.T) {
	time.Sleep(1 * time.Second)
	t.Log("Test running")
}
`)
	err := os.WriteFile(filepath.Join(r.workDir, "main_test.go"), testContent, 0644)
	require.NoError(t, err)

	result, err := r.RunTest(ctx, types.ValidatorMetadata{
		ID:       "test1",
		Gate:     "test-gate",
		FuncName: "TestDirectToGate",
		Package:  ".",
		Timeout:  2 * time.Second,
	})

	require.NoError(t, err)
	assert.Equal(t, types.TestStatusPass, result.Status)
	assert.Equal(t, "test1", result.Metadata.ID)
	assert.Equal(t, "test-gate", result.Metadata.Gate)
	assert.Equal(t, ".", result.Metadata.Package)
	assert.False(t, result.Metadata.RunAll)
}

// TestRunTest_NetworkName tests that the network name is correctly passed when calling RunTest
func TestRunTest_NetworkName(t *testing.T) {
	// Create a temporary directory for test execution
	workDir := t.TempDir()

	// Create test config
	testContent := []byte(`
package test

import "testing"

func TestExample(t *testing.T) {
	// This is a simple test
}
`)
	err := os.WriteFile(filepath.Join(workDir, "test_example.go"), testContent, 0644)
	require.NoError(t, err)

	// Initialize go module in test directory
	initGoModule(t, workDir, "test")

	// Create a registry with mock validators
	configContent := []byte(`
gates:
  - id: test-gate
    tests:
      - name: TestExample
        package: "."
`)
	validatorConfigPath := filepath.Join(workDir, "validators.yaml")
	err = os.WriteFile(validatorConfigPath, configContent, 0644)
	require.NoError(t, err)

	reg, err := registry.NewRegistry(registry.Config{
		ValidatorConfigFile: validatorConfigPath,
	})
	require.NoError(t, err)

	// Create expected network name
	expectedNetworkName := "test-network-name"

	// Create runner with test network name
	r, err := NewTestRunner(Config{
		Registry:    reg,
		WorkDir:     workDir,
		Log:         log.New(),
		GoBinary:    "echo", // Mock the Go binary to avoid actual execution
		NetworkName: expectedNetworkName,
	})
	require.NoError(t, err)

	// Cast to access the internal fields
	runner := r.(*runner)

	// Set a runID to avoid errors
	runner.runID = "test-run-id"

	// Verify the network name was correctly set in the runner
	assert.Equal(t, expectedNetworkName, runner.networkName,
		"Expected network name to be set in the runner")

	// Get a validator from the registry
	validators := reg.GetValidators()
	require.NotEmpty(t, validators, "Registry should have validators")
}

func TestRunTest_PackagePath_Local(t *testing.T) {
	r := setupDefaultTestRunner(t)

	origPath := os.Getenv("PATH")
	defer func() { _ = os.Setenv("PATH", origPath) }()

	testCases := []struct {
		name         string
		packagePath  string
		expectStatus types.TestStatus
		expectErrMsg string
	}{
		{
			name:         "Local path does not exist",
			packagePath:  "./does-not-exist",
			expectStatus: types.TestStatusFail,
			expectErrMsg: "local package path does not exist",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := r.RunTest(context.Background(), types.ValidatorMetadata{
				ID:       "test",
				Gate:     "test-gate",
				FuncName: "TestFunc",
				Package:  tc.packagePath,
			})
			require.NoError(t, err)
			assert.Equal(t, tc.expectStatus, result.Status)
			assert.Error(t, result.Error)
			assert.Contains(t, result.Error.Error(), tc.expectErrMsg)
		})
	}
}

// TestUserEnvironmentForwarding verifies that arbitrary user-provided environment variables
// are forwarded into the child `go test` process that op-acceptor spawns.
// This covers the use-case from op-devstack where variables like DEVSTACK_L2CL_KIND
// may influence behavior and should be honored by tests.
func TestUserEnvironmentForwarding(t *testing.T) {
	ctx := context.Background()
	r := setupDefaultTestRunner(t)

	// Create a test that reads a specific env var and logs it
	testContent := []byte(`
package main

import (
    "os"
    "testing"
)

func TestEnvForwarding(t *testing.T) {
    if v := os.Getenv("DEVSTACK_L2CL_KIND"); v == "" {
        t.Fatalf("DEVSTACK_L2CL_KIND not forwarded")
    } else {
        t.Logf("DEVSTACK_L2CL_KIND=%s", v)
    }
}
`)
	err := os.WriteFile(filepath.Join(r.workDir, "main_test.go"), testContent, 0644)
	require.NoError(t, err)

	// Ensure our process environment contains the variable that should be forwarded.
	// The runner builds the child env based on os.Environ() with additions, so setting
	// here simulates a user invoking `DEVSTACK_L2CL_KIND=kind op-acceptor ...`.
	const key = "DEVSTACK_L2CL_KIND"
	const val = "super"
	t.Setenv(key, val)

	// Run the test through the runner, which will spawn `go test`.
	res, err := r.RunTest(ctx, types.ValidatorMetadata{
		ID:       "env-forward",
		Gate:     "test-gate",
		FuncName: "TestEnvForwarding",
		Package:  ".",
	})
	require.NoError(t, err)
	assert.Equal(t, types.TestStatusPass, res.Status)
	assert.Contains(t, res.Stdout, "DEVSTACK_L2CL_KIND=super")
}

func TestPackageTimeoutErrorMessage(t *testing.T) {
	// Create a temporary directory for test execution
	tempDir := t.TempDir()

	// Create a test file with fast and slow tests
	testFile := filepath.Join(tempDir, "timeout_test.go")
	testContent := `
package main

import (
	"testing"
	"time"
)

func TestFast(t *testing.T) {
	// This test passes quickly
}

func TestSlowTimeout(t *testing.T) {
	// This test will timeout
	time.Sleep(5 * time.Second)
}

func TestAnotherSlowTimeout(t *testing.T) {
	// This test will also timeout
	time.Sleep(5 * time.Second)
}

func TestAnotherFast(t *testing.T) {
	// This test passes quickly
}

func TestSkipped(t *testing.T) {
	// This test is skipped
	t.Skip("This test is intentionally skipped")
}
`
	err := os.WriteFile(testFile, []byte(testContent), 0644)
	require.NoError(t, err)

	// Initialize go module in test directory
	initGoModule(t, tempDir, "testmodule")

	// Create a YAML configuration for the registry
	configContent := []byte(`
gates:
  - id: test-gate
    description: "Test gate with timeout tests"
    tests:
      - name: TestFast
        package: "."
        timeout: 2s
      - name: TestSlowTimeout
        package: "."
        timeout: 1s
      - name: TestAnotherSlowTimeout
        package: "."
        timeout: 1s
      - name: TestAnotherFast
        package: "."
        timeout: 2s
      - name: TestSkipped
        package: "."
        timeout: 2s
`)

	// Write the validator config to a file
	validatorConfigPath := filepath.Join(tempDir, "validators.yaml")
	err = os.WriteFile(validatorConfigPath, configContent, 0644)
	require.NoError(t, err)

	// Create registry with correct configuration
	registry, err := registry.NewRegistry(registry.Config{
		ValidatorConfigFile: validatorConfigPath,
		Log:                 log.New(),
		DefaultTimeout:      10 * time.Second,
	})
	require.NoError(t, err)

	// Create test runner
	cfg := Config{
		Registry:           registry,
		TargetGate:         []string{"test-gate"},
		WorkDir:            tempDir,
		Log:                log.New(),
		GoBinary:           "go",
		AllowSkips:         false,
		OutputRealtimeLogs: false,
		TestLogLevel:       "INFO",
	}

	testRunner, err := NewTestRunner(cfg)
	require.NoError(t, err)

	// Directly test the runTestList method with multiple tests to trigger timeout scenario
	testNames := []string{"TestFast", "TestSlowTimeout", "TestAnotherSlowTimeout", "TestAnotherFast", "TestSkipped"}

	packageMetadata := types.ValidatorMetadata{
		ID:      "package-timeout-test",
		Gate:    "test-gate",
		Type:    types.ValidatorTypeTest,
		Package: ".",
		RunAll:  true,
		Timeout: 1 * time.Second, // Short timeout to force timeouts on slow tests
	}

	// Run the test list - this should timeout on some tests
	ctx := context.Background()

	// Use the internal runTestList method to test package-level error handling
	runnerInternal := testRunner.(*runner)
	result, err := runnerInternal.runTestList(ctx, packageMetadata, testNames)

	// The test should succeed (no error returned) but the result should indicate failures
	require.NoError(t, err)
	require.NotNil(t, result)

	// The package should fail due to timeouts
	require.Equal(t, types.TestStatusFail, result.Status)

	// Check that the error message is concise and contains the timeout test names
	require.NotNil(t, result.Error)
	errorMsg := result.Error.Error()

	// The error message should contain "package test failures include timeouts:" followed by test names
	require.Contains(t, errorMsg, "package test failures include timeouts:")

	// The error message should contain the names of the timed out tests
	require.Contains(t, errorMsg, "TestSlowTimeout")
	require.Contains(t, errorMsg, "TestAnotherSlowTimeout")

	// Verify the format - it should look like: "package test failures include timeouts: [TestSlowTimeout TestAnotherSlowTimeout]"
	require.Regexp(t, `package test failures include timeouts: \[.*TestSlowTimeout.*TestAnotherSlowTimeout.*\]`, errorMsg)

	// Verify that the skipped test is marked as skipped
	require.Contains(t, result.SubTests, "TestSkipped", "Skipped test should be present in results")
	skippedTest := result.SubTests["TestSkipped"]
	require.Equal(t, types.TestStatusSkip, skippedTest.Status, "TestSkipped should have skip status")
	require.Nil(t, skippedTest.Error, "Skipped tests should not have error messages (skip reasons are not errors)")

	t.Logf("Package timeout error message: %s", errorMsg)
}

func TestArbitraryDepthSubtests(t *testing.T) {
	// Create a test result with arbitrarily deep nesting (5 levels)
	result := &RunnerResult{
		Gates: map[string]*GateResult{
			"test-gate": {
				ID:          "test-gate",
				Description: "Test gate with deeply nested subtests",
				Tests: map[string]*types.TestResult{
					"TestDeepNesting": {
						Metadata: types.ValidatorMetadata{
							FuncName: "TestDeepNesting",
							Package:  "example.com/deep",
						},
						Status:   types.TestStatusPass,
						Duration: time.Second,
						SubTests: map[string]*types.TestResult{
							"Level1Subtest": {
								Metadata: types.ValidatorMetadata{
									FuncName: "Level1Subtest",
									Package:  "example.com/deep",
								},
								Status:   types.TestStatusPass,
								Duration: 500 * time.Millisecond,
								SubTests: map[string]*types.TestResult{
									"Level2Subtest": {
										Metadata: types.ValidatorMetadata{
											FuncName: "Level2Subtest",
											Package:  "example.com/deep",
										},
										Status:   types.TestStatusFail,
										Duration: 200 * time.Millisecond,
										Error:    fmt.Errorf("level 2 error"),
										SubTests: map[string]*types.TestResult{
											"Level3Subtest": {
												Metadata: types.ValidatorMetadata{
													FuncName: "Level3Subtest",
													Package:  "example.com/deep",
												},
												Status:   types.TestStatusPass,
												Duration: 100 * time.Millisecond,
												SubTests: map[string]*types.TestResult{
													"Level4Subtest": {
														Metadata: types.ValidatorMetadata{
															FuncName: "Level4Subtest",
															Package:  "example.com/deep",
														},
														Status:   types.TestStatusSkip,
														Duration: 50 * time.Millisecond,
														SubTests: map[string]*types.TestResult{
															"Level5Subtest": {
																Metadata: types.ValidatorMetadata{
																	FuncName: "Level5Subtest",
																	Package:  "example.com/deep",
																},
																Status:   types.TestStatusPass,
																Duration: 25 * time.Millisecond,
																Error:    nil,
																SubTests: nil, // Deepest level
															},
														},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
				Status:   types.TestStatusFail,
				Duration: time.Second,
				Stats: ResultStats{
					Total:   6, // Main test + 5 subtests
					Passed:  4,
					Failed:  1,
					Skipped: 1,
				},
			},
		},
		Status:   types.TestStatusFail,
		Duration: time.Second,
		Stats: ResultStats{
			Total:   6,
			Passed:  4,
			Failed:  1,
			Skipped: 1,
		},
		RunID: "deep-nesting-test",
	}

	output := result.String()

	// Verify that all levels are present in the output
	assert.Contains(t, output, "TestDeepNesting")
	assert.Contains(t, output, "Level1Subtest")
	assert.Contains(t, output, "Level2Subtest")
	assert.Contains(t, output, "Level3Subtest")
	assert.Contains(t, output, "Level4Subtest")
	assert.Contains(t, output, "Level5Subtest")

	// Verify proper tree structure symbols are present
	assert.Contains(t, output, ui.TreeBranch)     // Branch connectors
	assert.Contains(t, output, ui.TreeLastBranch) // Last branch connectors
	assert.Contains(t, output, ui.TreeVertical)   // Vertical lines

	// Verify error is properly indented
	assert.Contains(t, output, "level 2 error")

	// Print the output for visual inspection
	t.Logf("Deep nesting output:\n%s", output)

	// Verify that each level has proper indentation by checking for proper nesting
	// using the actual BuildTreePrefix function to generate expected patterns
	lines := strings.Split(output, "\n")

	// Track which levels we've found in the output
	foundLevel1 := false
	foundLevel2 := false
	foundLevel3 := false
	foundLevel4 := false
	foundLevel5 := false

	// Define expected patterns using the actual tree building logic
	testCases := []struct {
		name         string
		depth        int
		parentIsLast []bool
		foundFlag    *bool
	}{
		{"Level1Subtest", 2, []bool{false}, &foundLevel1},
		{"Level2Subtest", 3, []bool{false, true}, &foundLevel2},
		{"Level3Subtest", 4, []bool{false, true, true}, &foundLevel3},
		{"Level4Subtest", 5, []bool{false, true, true, true}, &foundLevel4},
		{"Level5Subtest", 6, []bool{false, true, true, true, true}, &foundLevel5},
	}

	for _, line := range lines {
		for _, tc := range testCases {
			if strings.Contains(line, tc.name) {
				*tc.foundFlag = true
				// Generate expected pattern using the actual tree prefix builder
				expectedPrefix := ui.BuildTreePrefix(tc.depth, true, tc.parentIsLast)
				assert.True(t, strings.Contains(line, expectedPrefix),
					"%s should have proper depth %d indentation with prefix '%s', got line: %s",
					tc.name, tc.depth, expectedPrefix, line)
				break
			}
		}
	}

	assert.True(t, foundLevel1, "Level 1 subtest should be in output")
	assert.True(t, foundLevel2, "Level 2 subtest should be in output")
	assert.True(t, foundLevel3, "Level 3 subtest should be in output")
	assert.True(t, foundLevel4, "Level 4 subtest should be in output")
	assert.True(t, foundLevel5, "Level 5 subtest should be in output")
}

// TestDevstackOrchestratorEnvironment verifies that the DEVSTACK_ORCHESTRATOR environment variable is correctly set
func TestDevstackOrchestratorEnvironment(t *testing.T) {
	ctx := context.Background()

	// Create a simple test file in the work directory
	testContent := []byte(`
package main

import (
	"os"
	"testing"
)

func TestOrchestratorEnvironment(t *testing.T) {
    // Get orchestrator from environment
    orchestrator := os.Getenv("DEVSTACK_ORCHESTRATOR")
    if orchestrator == "" {
		t.Log("DEVSTACK_ORCHESTRATOR not set")
    } else {
		t.Log("DEVSTACK_ORCHESTRATOR set to", orchestrator)
	}
}
`)
	configContent := []byte(`
gates:
  - id: orchestrator-gate
    description: "Gate with a test that checks orchestrator environment"
    tests:
      - name: TestOrchestratorEnvironment
        package: "./main"
`)

	t.Run("sysgo orchestrator (env is nil)", func(t *testing.T) {
		r := setupTestRunner(t, testContent, configContent)
		r.env = nil // Simulate sysgo orchestrator
		err := os.WriteFile(filepath.Join(r.workDir, "main_test.go"), testContent, 0644)
		require.NoError(t, err)

		result, err := r.RunTest(ctx, types.ValidatorMetadata{
			ID:       "test1",
			Gate:     "orchestrator-gate",
			FuncName: "TestOrchestratorEnvironment",
			Package:  ".",
		})

		require.NoError(t, err)
		assert.Equal(t, types.TestStatusPass, result.Status)
		assert.Contains(t, result.Stdout, fmt.Sprintf("DEVSTACK_ORCHESTRATOR set to %s", flags.OrchestratorSysgo))
	})

	t.Run("sysext orchestrator (env is not nil)", func(t *testing.T) {
		r := setupTestRunner(t, testContent, configContent)
		// Set up a mock devnet environment to simulate sysext
		r.env = &env.DevnetEnv{
			URL: "file:///tmp/test.json",
		}
		err := os.WriteFile(filepath.Join(r.workDir, "main_test.go"), testContent, 0644)
		require.NoError(t, err)

		result, err := r.RunTest(ctx, types.ValidatorMetadata{
			ID:       "test1",
			Gate:     "orchestrator-gate",
			FuncName: "TestOrchestratorEnvironment",
			Package:  ".",
		})

		require.NoError(t, err)
		assert.Equal(t, types.TestStatusPass, result.Status)
		assert.Contains(t, result.Stdout, fmt.Sprintf("DEVSTACK_ORCHESTRATOR set to %s", flags.OrchestratorSysext))
	})

	t.Run("reproducible environment includes orchestrator", func(t *testing.T) {
		// Test sysgo with allowSkips=false (default)
		r := setupTestRunner(t, testContent, configContent)
		r.env = nil
		r.runID = "test-run-id"
		r.allowSkips = false

		reproEnv := r.ReproducibleEnv()
		envStr := reproEnv.String()
		assert.Contains(t, envStr, fmt.Sprintf("DEVSTACK_ORCHESTRATOR=%s", flags.OrchestratorSysgo))
		assert.Contains(t, envStr, "DEVNET_EXPECT_PRECONDITIONS_MET=true")
		assert.Contains(t, envStr, "DEVSTACK_KEYS_SALT=test-run-id")

		// Test sysext with allowSkips=false (default)
		r.env = &env.DevnetEnv{
			URL: "file:///tmp/test.json",
		}

		reproEnv = r.ReproducibleEnv()
		envStr = reproEnv.String()
		assert.Contains(t, envStr, fmt.Sprintf("DEVSTACK_ORCHESTRATOR=%s", flags.OrchestratorSysext))
		assert.Contains(t, envStr, "DEVNET_EXPECT_PRECONDITIONS_MET=true")
		assert.Contains(t, envStr, "DEVSTACK_KEYS_SALT=test-run-id")

		// Test with allowSkips=true
		r.allowSkips = true
		reproEnv = r.ReproducibleEnv()
		envStr = reproEnv.String()
		assert.Contains(t, envStr, fmt.Sprintf("DEVSTACK_ORCHESTRATOR=%s", flags.OrchestratorSysext))
		assert.NotContains(t, envStr, "DEVNET_EXPECT_PRECONDITIONS_MET")
		assert.Contains(t, envStr, "DEVSTACK_KEYS_SALT=test-run-id")
	})
}
func TestStdoutCaptured_PackageModeAndSingleTest(t *testing.T) {
	ctx := context.Background()
	// Create a runner with a simple package that logs to stdout
	testContent := []byte(`
package feature_test

import "testing"

func TestLogsA(t *testing.T) { t.Log("alpha") }
func TestLogsB(t *testing.T) { t.Log("beta") }
`)
	configContent := []byte(`
gates:
  - id: out-gate
    description: "Gate with stdout tests"
    suites:
      out-suite:
        description: "Suite"
        tests:
          - package: "./feature"
            run_all: true
    tests:
      - name: TestLogsA
        package: "./feature"
`)
	r := setupTestRunner(t, testContent, configContent)

	// 1) Package mode (FuncName empty): should capture stdout in the package result
	pkgRes, err := r.RunTest(ctx, types.ValidatorMetadata{
		ID:      "pkg",
		Gate:    "out-gate",
		Suite:   "out-suite",
		Package: "./feature",
		RunAll:  true,
		Type:    types.ValidatorTypeTest,
	})
	require.NoError(t, err)
	require.NotNil(t, pkgRes)
	// Stdout for package mode may be large; just ensure non-empty and contains JSON or RUN markers
	require.NotEmpty(t, pkgRes.Stdout)
	assert.Contains(t, pkgRes.Stdout, "=== RUN")

	// 2) Single test mode: capture stdout for the single test result
	singleRes, err := r.RunTest(ctx, types.ValidatorMetadata{
		ID:       "single",
		Gate:     "out-gate",
		FuncName: "TestLogsA",
		Package:  "./feature",
		Type:     types.ValidatorTypeTest,
	})
	require.NoError(t, err)
	require.NotNil(t, singleRes)
	require.NotEmpty(t, singleRes.Stdout)
	assert.Contains(t, singleRes.Stdout, "alpha")
}

// Verifies that specifying a package glob ("./parent/...") in a gate includes sub-packages
func TestGate_PackageGlobIncludesSubpackages(t *testing.T) {
	ctx := context.Background()

	// Gate config uses a glob to include all sub-packages under ./parent
	configContent := []byte(`
gates:
  - id: glob-gate
    description: "Gate with glob package"
    suites:
      glob-suite:
        description: "Suite with globbed packages"
        tests:
          - package: "./parent/..."
            run_all: true
`)

	// Initialize runner
	r := setupTestRunner(t, nil, configContent)

	// Create parent and child packages with tests
	parentPkg := filepath.Join(r.workDir, "parent", "pkg1")
	childPkg := filepath.Join(r.workDir, "parent", "child", "pkg2")
	require.NoError(t, os.MkdirAll(parentPkg, 0755))
	require.NoError(t, os.MkdirAll(childPkg, 0755))

	parentTest := []byte(`package pkg1_test

import "testing"

func TestParentPkg(t *testing.T) { t.Log("parent ok") }
`)
	childTest := []byte(`package pkg2_test

import "testing"

func TestChildPkg(t *testing.T) { t.Log("child ok") }
`)

	require.NoError(t, os.WriteFile(filepath.Join(parentPkg, "pkg1_test.go"), parentTest, 0644))
	require.NoError(t, os.WriteFile(filepath.Join(childPkg, "pkg2_test.go"), childTest, 0644))

	// Execute
	result, err := r.RunAllTests(ctx)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify gate and suite
	require.Contains(t, result.Gates, "glob-gate")
	gate := result.Gates["glob-gate"]
	require.Contains(t, gate.Suites, "glob-suite")
	suite := gate.Suites["glob-suite"]

	// Should have exactly one package-level test entry (the glob), with subtests from both packages
	require.Len(t, suite.Tests, 1)
	var pkgTest *types.TestResult
	for _, tRes := range suite.Tests {
		pkgTest = tRes
		break
	}
	require.NotNil(t, pkgTest)
	assert.True(t, pkgTest.Metadata.RunAll)
	assert.Equal(t, types.TestStatusPass, pkgTest.Status)

	// The package-mode subtests should include both TestParentPkg and TestChildPkg
	// Count should be 2 and both should be present
	require.Len(t, pkgTest.SubTests, 2)
	_, hasParent := pkgTest.SubTests["TestParentPkg"]
	_, hasChild := pkgTest.SubTests["TestChildPkg"]
	assert.True(t, hasParent, "should contain TestParentPkg from parent package")
	assert.True(t, hasChild, "should contain TestChildPkg from child subpackage")
}
