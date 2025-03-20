package runner

import (
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
	result, err := r.RunAllTests()
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
