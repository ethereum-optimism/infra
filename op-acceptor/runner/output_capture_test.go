package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum-optimism/infra/op-acceptor/logging"
	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStdoutContentVerification verifies what's actually in result.Stdout
func TestStdoutContentVerification(t *testing.T) {
	// Create a test directory with a simple test
	testDir := t.TempDir()

	// Create a simple test file
	testFile := filepath.Join(testDir, "verify_test.go")
	testContent := `package main

import "testing"

func TestVerifyOutput(t *testing.T) {
	t.Log("Verification message")
	t.Logf("Formatted message: %d", 42)
}
`
	err := os.WriteFile(testFile, []byte(testContent), 0644)
	require.NoError(t, err)

	// Initialize go module
	initGoModule(t, testDir, "test/verify")

	// Use the setupDefaultTestRunner pattern to get a properly initialized runner
	// but override the workDir
	r := setupDefaultTestRunner(t)
	r.workDir = testDir

	// Create test metadata
	metadata := types.ValidatorMetadata{
		ID:       "verify-test",
		FuncName: "TestVerifyOutput",
		Package:  ".",
		Gate:     "verify-gate",
	}

	// Run the test and capture stdout
	ctx := context.Background()
	result, err := r.runSingleTest(ctx, metadata)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Log the exact stdout content
	t.Logf("Raw Stdout length: %d", len(result.Stdout))
	t.Logf("First 200 chars of Stdout: %.200s", result.Stdout)

	// CRITICAL: Stdout should contain JSON from go test -json
	assert.NotEmpty(t, result.Stdout, "Stdout should not be empty")
	assert.Contains(t, result.Stdout, `"Action"`, "Stdout should contain JSON with Action field")
	assert.Contains(t, result.Stdout, `"Output"`, "Stdout should contain JSON with Output field")
	assert.Contains(t, result.Stdout, "Verification message", "Should contain the log message")
	assert.Contains(t, result.Stdout, "Formatted message: 42", "Should contain the formatted message")

	// Verify it's JSON, not plain text
	if strings.Contains(result.Stdout, `"Action":"output"`) {
		t.Log("✓ Stdout contains JSON (CORRECT)")
	} else if strings.Contains(result.Stdout, "=== RUN") && !strings.Contains(result.Stdout, `"Action"`) {
		t.Error("✗ Stdout contains PLAIN TEXT instead of JSON (BUG)")
	}
}

// TestProcessJSONOutput tests the JSON output processor
func TestProcessJSONOutput(t *testing.T) {
	// Test with actual JSON output
	jsonOutput := `{"Time":"2025-09-23T10:00:00Z","Action":"output","Package":"test/pkg","Test":"TestExample","Output":"=== RUN   TestExample\n"}
{"Time":"2025-09-23T10:00:01Z","Action":"output","Package":"test/pkg","Test":"TestExample","Output":"    test.go:10: Log message\n"}
{"Time":"2025-09-23T10:00:02Z","Action":"output","Package":"test/pkg","Test":"TestExample","Output":"--- PASS: TestExample (1.00s)\n"}
{"Time":"2025-09-23T10:00:02Z","Action":"pass","Package":"test/pkg","Test":"TestExample","Elapsed":1.0}
`

	parser := logging.NewJSONOutputParser(jsonOutput)
	var plaintext strings.Builder
	parser.ProcessJSONOutput(func(_ map[string]interface{}, outputText string) {
		plaintext.WriteString(outputText)
	})

	result := plaintext.String()
	t.Logf("Processed output:\n%s", result)

	// Should extract the plain text from JSON
	assert.Contains(t, result, "=== RUN   TestExample")
	assert.Contains(t, result, "Log message")
	assert.Contains(t, result, "--- PASS: TestExample")

	// Test with plain text (what's happening in the bug)
	plainOutput := `=== RUN   TestExample
    test.go:10: Log message
--- PASS: TestExample (1.00s)
`

	parser2 := logging.NewJSONOutputParser(plainOutput)
	var plaintext2 strings.Builder
	parser2.ProcessJSONOutput(func(_ map[string]interface{}, outputText string) {
		plaintext2.WriteString(outputText)
	})

	result2 := plaintext2.String()
	t.Logf("Processed plain text as JSON (should be empty):\n%s", result2)

	// When given plain text, the JSON parser produces nothing
	assert.Empty(t, result2, "JSON parser should produce empty output when given plain text")
	t.Log("This confirms the bug: plain text in Stdout field causes 'No output captured'")
}
