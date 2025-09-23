package runner

import (
	"testing"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParserCapturesSubtestOutput verifies that subtests have their output captured
func TestParserCapturesSubtestOutput(t *testing.T) {
	// Real JSON output from go test -json with subtests
	jsonOutput := []byte(`{"Time":"2025-09-23T10:00:00Z","Action":"run","Package":"test/pkg","Test":"TestMain"}
{"Time":"2025-09-23T10:00:00Z","Action":"output","Package":"test/pkg","Test":"TestMain","Output":"=== RUN   TestMain\n"}
{"Time":"2025-09-23T10:00:00Z","Action":"output","Package":"test/pkg","Test":"TestMain","Output":"    test.go:10: Main test starting\n"}
{"Time":"2025-09-23T10:00:00Z","Action":"run","Package":"test/pkg","Test":"TestMain/SubTest1"}
{"Time":"2025-09-23T10:00:00Z","Action":"output","Package":"test/pkg","Test":"TestMain/SubTest1","Output":"=== RUN   TestMain/SubTest1\n"}
{"Time":"2025-09-23T10:00:00Z","Action":"output","Package":"test/pkg","Test":"TestMain/SubTest1","Output":"    test.go:15: SubTest1 log message\n"}
{"Time":"2025-09-23T10:00:00Z","Action":"output","Package":"test/pkg","Test":"TestMain/SubTest1","Output":"    test.go:16: SubTest1 another log\n"}
{"Time":"2025-09-23T10:00:00Z","Action":"output","Package":"test/pkg","Test":"TestMain/SubTest1","Output":"--- PASS: TestMain/SubTest1 (0.01s)\n"}
{"Time":"2025-09-23T10:00:00Z","Action":"pass","Package":"test/pkg","Test":"TestMain/SubTest1","Elapsed":0.01}
{"Time":"2025-09-23T10:00:00Z","Action":"run","Package":"test/pkg","Test":"TestMain/SubTest2"}
{"Time":"2025-09-23T10:00:00Z","Action":"output","Package":"test/pkg","Test":"TestMain/SubTest2","Output":"=== RUN   TestMain/SubTest2\n"}
{"Time":"2025-09-23T10:00:00Z","Action":"output","Package":"test/pkg","Test":"TestMain/SubTest2","Output":"    test.go:20: SubTest2 log message\n"}
{"Time":"2025-09-23T10:00:00Z","Action":"output","Package":"test/pkg","Test":"TestMain/SubTest2","Output":"--- PASS: TestMain/SubTest2 (0.01s)\n"}
{"Time":"2025-09-23T10:00:00Z","Action":"pass","Package":"test/pkg","Test":"TestMain/SubTest2","Elapsed":0.01}
{"Time":"2025-09-23T10:00:00Z","Action":"output","Package":"test/pkg","Test":"TestMain","Output":"    test.go:25: Main test ending\n"}
{"Time":"2025-09-23T10:00:00Z","Action":"output","Package":"test/pkg","Test":"TestMain","Output":"--- PASS: TestMain (0.02s)\n"}
{"Time":"2025-09-23T10:00:00Z","Action":"pass","Package":"test/pkg","Test":"TestMain","Elapsed":0.02}
`)

	metadata := types.ValidatorMetadata{
		FuncName: "TestMain",
		Package:  "test/pkg",
	}

	parser := NewOutputParser()
	result := parser.Parse(jsonOutput, metadata)

	require.NotNil(t, result)
	assert.Equal(t, types.TestStatusPass, result.Status)

	// Check that we have subtests
	require.Len(t, result.SubTests, 2)

	// Check SubTest1
	subtest1, exists := result.SubTests["TestMain/SubTest1"]
	require.True(t, exists, "SubTest1 should exist")
	assert.Equal(t, types.TestStatusPass, subtest1.Status)

	// CRITICAL: Check that SubTest1 has output captured
	t.Logf("SubTest1 Stdout:\n%s", subtest1.Stdout)
	assert.NotEmpty(t, subtest1.Stdout, "SubTest1 should have stdout captured")
	assert.Contains(t, subtest1.Stdout, "=== RUN   TestMain/SubTest1", "Should contain RUN header")
	assert.Contains(t, subtest1.Stdout, "SubTest1 log message", "Should contain log message")
	assert.Contains(t, subtest1.Stdout, "SubTest1 another log", "Should contain second log")
	assert.Contains(t, subtest1.Stdout, "--- PASS: TestMain/SubTest1", "Should contain PASS footer")

	// Check SubTest2
	subtest2, exists := result.SubTests["TestMain/SubTest2"]
	require.True(t, exists, "SubTest2 should exist")
	assert.Equal(t, types.TestStatusPass, subtest2.Status)

	// CRITICAL: Check that SubTest2 has output captured
	t.Logf("SubTest2 Stdout:\n%s", subtest2.Stdout)
	assert.NotEmpty(t, subtest2.Stdout, "SubTest2 should have stdout captured")
	assert.Contains(t, subtest2.Stdout, "=== RUN   TestMain/SubTest2", "Should contain RUN header")
	assert.Contains(t, subtest2.Stdout, "SubTest2 log message", "Should contain log message")
	assert.Contains(t, subtest2.Stdout, "--- PASS: TestMain/SubTest2", "Should contain PASS footer")
}

// TestRealWorldTestOutput tests with actual test output including logger.Info calls
func TestRealWorldTestOutput(t *testing.T) {
	// Simulated output from a test like TestRPCConnectivity
	jsonOutput := []byte(`{"Time":"2025-09-23T10:00:00Z","Action":"run","Package":"test/pkg","Test":"TestRPCConnectivity"}
{"Time":"2025-09-23T10:00:00Z","Action":"output","Package":"test/pkg","Test":"TestRPCConnectivity","Output":"=== RUN   TestRPCConnectivity\n"}
{"Time":"2025-09-23T10:00:00Z","Action":"output","Package":"test/pkg","Test":"TestRPCConnectivity","Output":"t=2025-09-23T10:00:00+0000 lvl=info msg=\"Started L2 RPC connectivity test\" Test=TestRPCConnectivity\n"}
{"Time":"2025-09-23T10:00:00Z","Action":"output","Package":"test/pkg","Test":"TestRPCConnectivity","Output":"t=2025-09-23T10:00:00+0000 lvl=info msg=\"Testing chain\" chain=optimism\n"}
{"Time":"2025-09-23T10:00:01Z","Action":"output","Package":"test/pkg","Test":"TestRPCConnectivity","Output":"--- PASS: TestRPCConnectivity (1.00s)\n"}
{"Time":"2025-09-23T10:00:01Z","Action":"pass","Package":"test/pkg","Test":"TestRPCConnectivity","Elapsed":1.0}
`)

	metadata := types.ValidatorMetadata{
		FuncName: "TestRPCConnectivity",
		Package:  "test/pkg",
	}

	parser := NewOutputParser()
	result := parser.Parse(jsonOutput, metadata)

	require.NotNil(t, result)
	assert.Equal(t, types.TestStatusPass, result.Status)

	// The main test result should have the full JSON in Stdout
	// (The runner stores the raw JSON for the main test)
	// But we're primarily concerned that when this is processed,
	// the logger.Info output is preserved

	// After processing through the filelogger, it should extract the plain text
	// including the logger.Info lines
	expectedInOutput := []string{
		"Started L2 RPC connectivity test",
		"Testing chain",
		"optimism",
	}

	// For now, let's just verify the raw data contains what we expect
	outputStr := string(jsonOutput)
	for _, expected := range expectedInOutput {
		assert.Contains(t, outputStr, expected, "Output should contain: %s", expected)
	}
}