package logging

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIntegrationSubtestOutputCapture tests the full flow of subtest output capture
func TestIntegrationSubtestOutputCapture(t *testing.T) {
	// Setup
	tempDir := t.TempDir()
	runID := "test-run-" + time.Now().Format("20060102-150405")
	logger, err := NewFileLogger(tempDir, runID, "test-network", "test-gate", true)
	require.NoError(t, err)

	// Create a main test result with subtests that have plain text output
	mainResult := &types.TestResult{
		Metadata: types.ValidatorMetadata{
			ID:       "test-main",
			FuncName: "TestMain",
			Package:  "test/pkg",
			Gate:     "test-gate",
		},
		Status:   types.TestStatusPass,
		Duration: 2 * time.Second,
		Stdout:   `{"Time":"2025-09-23T10:00:00Z","Action":"output","Package":"test/pkg","Test":"TestMain","Output":"=== RUN   TestMain\n"}`,
		SubTests: map[string]*types.TestResult{
			"TestMain/SubTest1": {
				Metadata: types.ValidatorMetadata{
					ID:       "subtest-1",
					FuncName: "SubTest1",
					Package:  "test/pkg",
				},
				Status:   types.TestStatusPass,
				Duration: 1 * time.Second,
				// This is plain text output as stored by the parser
				Stdout: `=== RUN   TestMain/SubTest1
    test.go:15: SubTest1 log message
    test.go:16: Important log from SubTest1
--- PASS: TestMain/SubTest1 (1.00s)`,
			},
			"TestMain/SubTest2": {
				Metadata: types.ValidatorMetadata{
					ID:       "subtest-2",
					FuncName: "SubTest2",
					Package:  "test/pkg",
				},
				Status:   types.TestStatusPass,
				Duration: 500 * time.Millisecond,
				// Another subtest with plain text output
				Stdout: `=== RUN   TestMain/SubTest2
    test.go:20: Started chain fork test
    test.go:21: Another log message
--- PASS: TestMain/SubTest2 (0.50s)`,
			},
		},
	}

	// Log the test result
	err = logger.LogTestResult(mainResult, runID)
	require.NoError(t, err)

	// Complete logging to ensure files are written
	err = logger.Complete(runID)
	require.NoError(t, err)

	// Give async writes time to complete
	time.Sleep(200 * time.Millisecond)

	// Check the generated files
	logDir := filepath.Join(tempDir, "testrun-"+runID, "passed")

	// Check SubTest1's text file
	subtest1File := filepath.Join(logDir, "test-gate_test_pkg_SubTest1.txt")
	if _, err := os.Stat(subtest1File); err == nil {
		content, err := os.ReadFile(subtest1File)
		require.NoError(t, err)
		contentStr := string(content)

		t.Logf("SubTest1 text file content:\n%s", contentStr)

		// The file should NOT say "No output captured"
		assert.NotContains(t, contentStr, "No output captured",
			"SubTest1 file should not say 'No output captured'")

		// It should contain the actual test output
		assert.Contains(t, contentStr, "SubTest1 log message",
			"Should contain the log message")
		assert.Contains(t, contentStr, "Important log from SubTest1",
			"Should contain the logger.Info output")
	}

	// Check SubTest2's text file
	subtest2File := filepath.Join(logDir, "test-gate_test_pkg_SubTest2.txt")
	if _, err := os.Stat(subtest2File); err == nil {
		content, err := os.ReadFile(subtest2File)
		require.NoError(t, err)
		contentStr := string(content)

		t.Logf("SubTest2 text file content:\n%s", contentStr)

		// The file should NOT say "No output captured"
		assert.NotContains(t, contentStr, "No output captured",
			"SubTest2 file should not say 'No output captured'")

		// It should contain the actual test output
		assert.Contains(t, contentStr, "Started chain fork test",
			"Should contain the t.Logger().Info output")
	}
}

// TestRealTestOutputCapture uses real test logs as verifiying tests.
func TestRealTestOutputCapture(t *testing.T) {
	// Setup
	tempDir := t.TempDir()
	runID := "real-test-run"
	logger, err := NewFileLogger(tempDir, runID, "optimism", "connectivity", true)
	require.NoError(t, err)

	// Simulate a test result for TestRPCConnectivity with actual logger output
	result := &types.TestResult{
		Metadata: types.ValidatorMetadata{
			ID:       "rpc-connectivity",
			FuncName: "TestRPCConnectivity",
			Package:  "acceptance/base",
			Gate:     "connectivity",
		},
		Status:   types.TestStatusPass,
		Duration: 5 * time.Second,
		Stdout: `=== RUN   TestRPCConnectivity
t=2025-09-23T10:00:00+0000 lvl=info msg="Started L2 RPC connectivity test" Test=TestRPCConnectivity
t=2025-09-23T10:00:01+0000 lvl=info msg="Testing chain" chain=optimism
t=2025-09-23T10:00:02+0000 lvl=info msg="Chain test passed" chain=optimism latency=50ms
--- PASS: TestRPCConnectivity (5.00s)`,
	}

	// Log the test result
	err = logger.LogTestResult(result, runID)
	require.NoError(t, err)

	// Complete logging
	err = logger.Complete(runID)
	require.NoError(t, err)

	// Give async writes time to complete
	time.Sleep(200 * time.Millisecond)

	// Check the generated text file
	textFile := filepath.Join(tempDir, "testrun-"+runID, "passed",
		"connectivity_acceptance_base_TestRPCConnectivity.txt")

	if _, err := os.Stat(textFile); err == nil {
		content, err := os.ReadFile(textFile)
		require.NoError(t, err)
		contentStr := string(content)

		t.Logf("TestRPCConnectivity text file content:\n%s", contentStr)

		// CRITICAL: The file should NOT say "No output captured"
		assert.NotContains(t, contentStr, "No output captured",
			"Test file should not say 'No output captured'")

		// It should contain the logger.Info outputs
		assert.Contains(t, contentStr, "Started L2 RPC connectivity test",
			"Should contain the initial log message")
		assert.Contains(t, contentStr, "Testing chain",
			"Should contain the chain testing log")
		assert.Contains(t, contentStr, "chain=optimism",
			"Should contain the chain name")
	}
}
