package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFailingTestStdoutLogging verifies that stdout is captured when tests fail
func TestFailingTestStdoutLogging(t *testing.T) {
	// Setup test with a failing test that outputs to stdout
	testContent := []byte(`
package feature_test

import (
	"fmt"
	"testing"
)

func TestWithStdout(t *testing.T) {
	fmt.Println("This is some stdout output that should be captured")
	fmt.Println("This is a second line of output")
	t.Error("This test deliberately fails")
}
`)

	configContent := []byte(`
gates:
  - id: logging-gate
    description: "Gate with a test that outputs to stdout"
    suites:
      logging-suite:
        description: "Suite with a failing test that outputs to stdout"
        tests:
          - name: TestWithStdout
            package: "./feature"
`)

	// Setup the test runner
	r := setupTestRunner(t, testContent, configContent)

	// Run the test
	result, err := r.RunAllTests(context.Background())
	require.NoError(t, err)
	assert.Equal(t, types.TestStatusFail, result.Status)

	// Verify the structure
	require.Contains(t, result.Gates, "logging-gate")
	gate := result.Gates["logging-gate"]
	require.Contains(t, gate.Suites, "logging-suite")
	suite := gate.Suites["logging-suite"]

	// Get the failing test
	var failingTest *types.TestResult
	for _, test := range suite.Tests {
		failingTest = test
		break
	}
	require.NotNil(t, failingTest)

	// Verify the test failed and has stdout captured
	assert.Equal(t, types.TestStatusFail, failingTest.Status)
	assert.NotNil(t, failingTest.Error)
	assert.NotEmpty(t, failingTest.Stdout)
	assert.Contains(t, failingTest.Stdout, "This is some stdout output that should be captured")
	assert.Contains(t, failingTest.Stdout, "This is a second line of output")
}

// TestLogLevelEnvironment verifies that the TEST_LOG_LEVEL environment variable is correctly set and used
func TestLogLevelEnvironment(t *testing.T) {
	ctx := context.Background()

	// Create a simple test file in the work directory
	testContent := []byte(`
package main

import (
	"os"
	"testing"
)

func TestLogLevelEnvironment(t *testing.T) {
    // Get log level from environment
    logLevel := os.Getenv("TEST_LOG_LEVEL")
    if logLevel == "" {
		t.Log("TEST_LOG_LEVEL not set")
    } else {
		t.Log("TEST_LOG_LEVEL set to", logLevel)
	}
}
`)
	configContent := []byte(`
gates:
  - id: logging-gate
    description: "Gate with a test that outputs logs"
    suites:
      logging-suite:
        description: "Suite with a test that outputs logs"
        tests:
          - name: TestLogLevelEnvironment
            package: "./main"
`)

	r := setupTestRunner(t, testContent, configContent)
	r.testLogLevel = "debug"
	err := os.WriteFile(filepath.Join(r.workDir, "main_test.go"), testContent, 0644)
	require.NoError(t, err)

	result, err := r.RunTest(ctx, types.ValidatorMetadata{
		ID:       "test1",
		Gate:     "logging-gate",
		FuncName: "TestLogLevelEnvironment",
		Package:  ".",
	})

	require.NoError(t, err)
	assert.Equal(t, types.TestStatusPass, result.Status)
	assert.Equal(t, "test1", result.Metadata.ID)
	assert.Equal(t, "logging-gate", result.Metadata.Gate)
	assert.Equal(t, ".", result.Metadata.Package)
	assert.False(t, result.Metadata.RunAll)
	assert.Contains(t, result.Stdout, "TEST_LOG_LEVEL set to debug")
}

// TestOutputTestLogs verifies that test logs are properly captured and output when enabled
func TestOutputTestLogs(t *testing.T) {
	// Create a test file that outputs logs at different levels
	testContent := []byte(`
package feature_test

import (
	"testing"
)

func TestWithLogs(t *testing.T) {
	t.Log("This is a test output")
}
`)

	configContent := []byte(`
gates:
  - id: logging-gate
    description: "Gate with a test that outputs logs"
    suites:
      logging-suite:
        description: "Suite with a test that outputs logs"
        tests:
          - name: TestWithLogs
            package: "./feature"
`)

	// Setup the test runner with OutputTestLogs enabled
	r := setupTestRunner(t, testContent, configContent)
	r.outputTestLogs = true

	// Run the test
	result, err := r.RunAllTests(context.Background())
	require.NoError(t, err)
	assert.Equal(t, types.TestStatusPass, result.Status)

	// Verify the structure
	require.Contains(t, result.Gates, "logging-gate")
	gate := result.Gates["logging-gate"]
	require.Contains(t, gate.Suites, "logging-suite")
	suite := gate.Suites["logging-suite"]

	// Get the test result
	var testResult *types.TestResult
	for _, test := range suite.Tests {
		testResult = test
		break
	}
	require.NotNil(t, testResult)

	// Verify the test passed and has logs captured
	assert.Equal(t, types.TestStatusPass, testResult.Status)
	assert.NotEmpty(t, testResult.Stdout)
	assert.Contains(t, testResult.Stdout, "This is a test output")

	// Now run with OutputTestLogs disabled
	r.outputTestLogs = false
	result, err = r.RunAllTests(context.Background())
	require.NoError(t, err)
	assert.Equal(t, types.TestStatusPass, result.Status)

	// Get the test result again
	var testResult2 *types.TestResult
	for _, test := range result.Gates["logging-gate"].Suites["logging-suite"].Tests {
		testResult2 = test
		break
	}
	require.NotNil(t, testResult)

	// Verify that logs are not captured when OutputTestLogs is disabled
	assert.Equal(t, types.TestStatusPass, testResult2.Status)
	assert.Contains(t, testResult2.Stdout, "This is a test output")
}
