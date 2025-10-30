package logging

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPlaintextFileCreation tests that the plaintext file handles both JSON and plain text stdout correctly
func TestPlaintextFileCreation(t *testing.T) {
	// Setup
	tempDir := t.TempDir()
	logger, err := NewFileLogger(tempDir, "test-run", "test-network", "test-gate", true)
	require.NoError(t, err)

	sink := &PerTestFileSink{logger: logger}

	testCases := []struct {
		name           string
		stdout         string
		expectedOutput string
		shouldContain  []string
		shouldNotHave  string
	}{
		{
			name: "JSON stdout",
			stdout: `{"Time":"2025-09-23T10:00:00Z","Action":"output","Package":"test/pkg","Test":"TestExample","Output":"=== RUN   TestExample\n"}
{"Time":"2025-09-23T10:00:01Z","Action":"output","Package":"test/pkg","Test":"TestExample","Output":"    test.go:10: Log message\n"}
{"Time":"2025-09-23T10:00:02Z","Action":"output","Package":"test/pkg","Test":"TestExample","Output":"--- PASS: TestExample (1.00s)\n"}`,
			shouldContain: []string{
				"=== RUN TestExample", // Whitespace collapsed
				"Log message",
				"--- PASS: TestExample",
			},
			shouldNotHave: "No output captured",
		},
		{
			name: "Plain text stdout (from old runs or subtests)",
			stdout: `=== RUN   TestExample
    test.go:10: Log message
--- PASS: TestExample (1.00s)`,
			shouldContain: []string{
				"=== RUN TestExample", // Whitespace collapsed
				"Log message",
				"--- PASS: TestExample",
			},
			shouldNotHave: "No output captured",
		},
		{
			name:          "Empty stdout for subtest",
			stdout:        "",
			shouldContain: []string{},
		},
		{
			name:          "Non-test plain text",
			stdout:        "Some random output that isn't test output",
			shouldContain: []string{},
			shouldNotHave: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a test result with the stdout
			result := &types.TestResult{
				Metadata: types.ValidatorMetadata{
					ID:       "test-" + tc.name,
					FuncName: "TestExample",
					Package:  "test/pkg",
				},
				Status:   types.TestStatusPass,
				Duration: time.Second,
				Stdout:   tc.stdout,
			}

			// Create plaintext file
			filePath := tempDir + "/test-" + tc.name + ".txt"
			err := sink.createPlaintextFile(result, filePath, false)
			require.NoError(t, err)

			// Wait for async write to complete
			time.Sleep(100 * time.Millisecond)

			// Read the file content
			content, err := os.ReadFile(filePath)
			require.NoError(t, err)

			contentStr := string(content)
			t.Logf("Content for %s:\n%s", tc.name, contentStr)

			// Check expected content
			for _, expected := range tc.shouldContain {
				assert.Contains(t, contentStr, expected, "Should contain: %s", expected)
			}

			if tc.shouldNotHave != "" {
				assert.NotContains(t, contentStr, tc.shouldNotHave, "Should not contain: %s", tc.shouldNotHave)
			}

			// Special check for the main issue
			if tc.stdout != "" && strings.Contains(tc.stdout, "===") {
				// If stdout contains test output markers, we should never see "No output captured"
				assert.NotContains(t, contentStr, "No output captured",
					"Files with test output should never show 'No output captured'")
			}
		})
	}
}

// TestSubtestOutputHandling specifically tests the handling of subtests without stdout
func TestSubtestOutputHandling(t *testing.T) {
	// Setup
	tempDir := t.TempDir()
	logger, err := NewFileLogger(tempDir, "test-run", "test-network", "test-gate", true)
	require.NoError(t, err)

	sink := &PerTestFileSink{logger: logger}

	// Create a subtest result with no stdout (typical for subtests extracted from package runs)
	subtest := &types.TestResult{
		Metadata: types.ValidatorMetadata{
			ID:       "subtest-1",
			FuncName: "TestParent/SubTest",
			Package:  "test/pkg",
		},
		Status:   types.TestStatusPass,
		Duration: 500 * time.Millisecond,
		Stdout:   "",                                 // No stdout for subtests
		SubTests: make(map[string]*types.TestResult), // Empty subtests map
	}

	// Create plaintext file
	filePath := tempDir + "/subtest.txt"
	err = sink.createPlaintextFile(subtest, filePath, false)
	require.NoError(t, err)

	// Wait for async write
	time.Sleep(100 * time.Millisecond)

	// Read the file content
	content, err := os.ReadFile(filePath)
	require.NoError(t, err)

	contentStr := string(content)
	t.Logf("Subtest content:\n%s", contentStr)

	// With the fix, subtests should show informative output instead of "No output captured"
	if strings.Contains(contentStr, "No output captured") {
		// This is acceptable only if we're dealing with a parent test with subtests
		// Individual subtests should show status information
		assert.NotEmpty(t, subtest.SubTests, "Only parent tests with subtests should show 'No output captured'")
	} else {
		// Should contain status information
		assert.Contains(t, contentStr, "status", "Should contain status information")
	}
}
