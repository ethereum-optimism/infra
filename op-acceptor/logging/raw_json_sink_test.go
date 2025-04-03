package logging

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRawJSONSink(t *testing.T) {
	// Create a temporary directory for test logs
	tmpDir, err := os.MkdirTemp("", "raw_json_sink_test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Create a new FileLogger with a valid runID
	runID := "test-run-raw-json"
	logger, err := NewFileLogger(tmpDir, runID)
	require.NoError(t, err)

	// Get the RawJSONSink from the logger
	sink, ok := logger.GetSinkByType("RawJSONSink")
	require.True(t, ok, "RawJSONSink should be available")

	rawSink, ok := sink.(*RawJSONSink)
	require.True(t, ok, "Sink should be of type *RawJSONSink")

	// Create mock raw JSON events
	now := time.Now()
	rawEvents := []GoTestEvent{
		{
			Time:    now.Add(-3 * time.Second),
			Action:  "start",
			Package: "github.com/example/package",
			Test:    "TestPassingFunction",
		},
		{
			Time:    now.Add(-2 * time.Second),
			Action:  "pass",
			Package: "github.com/example/package",
			Test:    "TestPassingFunction",
			Elapsed: 1.0,
		},
		{
			Time:    now.Add(-1 * time.Second),
			Action:  "start",
			Package: "github.com/example/package",
			Test:    "TestFailingFunction",
		},
		{
			Time:    now,
			Action:  "fail",
			Package: "github.com/example/package",
			Test:    "TestFailingFunction",
			Elapsed: 1.0,
		},
	}

	// Serialize the events to JSON
	var rawJSON []byte
	for _, event := range rawEvents {
		data, err := json.Marshal(event)
		require.NoError(t, err)
		rawJSON = append(rawJSON, data...)
		rawJSON = append(rawJSON, '\n')
	}

	// Create test results
	testResults := []*types.TestResult{
		{
			Metadata: types.ValidatorMetadata{
				ID:       "pass-test-id",
				Gate:     "test-gate",
				Suite:    "test-suite",
				FuncName: "TestPassingFunction",
				Package:  "github.com/example/package",
			},
			Status:   types.TestStatusPass,
			Duration: time.Second * 2,
		},
		{
			Metadata: types.ValidatorMetadata{
				ID:       "fail-test-id",
				Gate:     "test-gate",
				Suite:    "test-suite",
				FuncName: "TestFailingFunction",
				Package:  "github.com/example/package",
			},
			Status:   types.TestStatusFail,
			Error:    assert.AnError,
			Duration: time.Second * 1,
		},
	}

	// Store the raw JSON for each test
	rawSink.StoreRawJSON(testResults[0].Metadata.ID, rawJSON)
	rawSink.StoreRawJSON(testResults[1].Metadata.ID, rawJSON)

	// Log the test results
	for _, result := range testResults {
		err = logger.LogTestResult(result, runID)
		require.NoError(t, err)
	}

	// Complete the logging process
	err = logger.Complete(runID)
	require.NoError(t, err)

	// Wait a short time to ensure async writes complete
	time.Sleep(100 * time.Millisecond)

	// Get the raw events file path
	rawEventsFile, err := logger.GetRawEventsFileForRunID(runID)
	require.NoError(t, err)
	assert.FileExists(t, rawEventsFile)

	// Read the content of raw_go_events.log to verify format
	rawEventsContent, err := os.ReadFile(rawEventsFile)
	require.NoError(t, err)

	// Parse each line and verify structure
	lines := strings.Split(string(rawEventsContent), "\n")
	var nonEmptyLines []string
	for _, line := range lines {
		if line != "" {
			nonEmptyLines = append(nonEmptyLines, line)
		}
	}

	require.GreaterOrEqual(t, len(nonEmptyLines), 4,
		"Should have at least 4 events (one per test event in the raw JSON)")

	// Check each event's structure
	for _, line := range nonEmptyLines {
		var event GoTestEvent
		err := json.Unmarshal([]byte(line), &event)
		require.NoError(t, err, "Line should be valid JSON: %s", line)

		// Verify required fields
		assert.NotZero(t, event.Time, "Time should be set")
		assert.NotEmpty(t, event.Action, "Action should be set")
		assert.NotEmpty(t, event.Package, "Package should be set")

		// Verify action is one of the expected values
		assert.Contains(t, []string{"start", "pass", "fail", "skip", "output"}, event.Action,
			"Action should be valid: %s", event.Action)

		// For pass/fail events, make sure elapsed is set
		if event.Action == "pass" || event.Action == "fail" {
			assert.Greater(t, event.Elapsed, 0.0, "Elapsed time should be positive for pass/fail events")
		}
	}
}

// TestRealGoTestOutput creates a real Go test and verifies our RawJSONSink processes its output correctly
func TestRealGoTestOutput(t *testing.T) {
	// Create a temporary directory
	tmpDir, err := os.MkdirTemp("", "raw_json_real_test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Create a simple test file that will pass and fail tests
	testFilePath := filepath.Join(tmpDir, "simple_test.go")
	testFileContent := `
package simple

import (
	"testing"
	"time"
)

func TestPass(t *testing.T) {
	// This test will pass
	time.Sleep(50 * time.Millisecond)
}

func TestFail(t *testing.T) {
	// This test will fail
	time.Sleep(50 * time.Millisecond)
	t.Error("This test is expected to fail")
}
`
	err = os.WriteFile(testFilePath, []byte(testFileContent), 0644)
	require.NoError(t, err)

	// We need a go.mod for the test to run properly
	err = os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module simple\n\ngo 1.21\n"), 0644)
	require.NoError(t, err)

	// Run the go test with -json flag and capture output
	cmd := exec.Command("go", "test", "-json")
	cmd.Dir = tmpDir
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	t.Log("Running go test -json...")
	_ = cmd.Run() // Ignore error since we expect TestFail to fail
	t.Logf("Go test stderr output: %s", stderr.String())

	// Get the raw JSON output from go test
	rawGoTestOutput := stdout.Bytes()
	if len(rawGoTestOutput) > 0 {
		t.Logf("Raw JSON output length: %d", len(rawGoTestOutput))
		safeLen := min(200, len(rawGoTestOutput))
		t.Logf("First %d bytes: %s", safeLen, string(rawGoTestOutput[:safeLen]))
	} else {
		t.Logf("Raw JSON output is empty")
	}
	require.NotEmpty(t, rawGoTestOutput, "Raw JSON output from go test should not be empty")

	// Create a test result and file logger
	runID := "real-go-test-run"
	logger, err := NewFileLogger(tmpDir, runID)
	require.NoError(t, err)

	// Get the RawJSONSink
	sink, ok := logger.GetSinkByType("RawJSONSink")
	require.True(t, ok, "RawJSONSink should be available")
	rawSink, ok := sink.(*RawJSONSink)
	require.True(t, ok, "Sink should be of type *RawJSONSink")

	// Parse the raw go test output to find the test events
	var passLines []string
	var failLines []string

	for _, line := range strings.Split(string(rawGoTestOutput), "\n") {
		if line == "" {
			continue
		}

		var event struct {
			Action  string `json:"Action"`
			Test    string `json:"Test"`
			Package string `json:"Package"`
		}

		err = json.Unmarshal([]byte(line), &event)
		require.NoError(t, err)

		if event.Test == "TestPass" {
			passLines = append(passLines, line)
		} else if event.Test == "TestFail" {
			failLines = append(failLines, line)
		}
	}

	t.Logf("Found %d lines for TestPass and %d lines for TestFail",
		len(passLines), len(failLines))

	require.NotEmpty(t, passLines, "Should have found events for TestPass")
	require.NotEmpty(t, failLines, "Should have found events for TestFail")

	// Join the lines with newlines
	passJSON := []byte(strings.Join(passLines, "\n") + "\n")
	failJSON := []byte(strings.Join(failLines, "\n") + "\n")

	// Create test results matching the real tests
	passResult := &types.TestResult{
		Metadata: types.ValidatorMetadata{
			ID:       "real-pass-id",
			Gate:     "test-gate",
			Suite:    "test-suite",
			FuncName: "TestPass",
			Package:  "simple",
		},
		Status:   types.TestStatusPass,
		Duration: time.Millisecond * 50,
	}

	failResult := &types.TestResult{
		Metadata: types.ValidatorMetadata{
			ID:       "real-fail-id",
			Gate:     "test-gate",
			Suite:    "test-suite",
			FuncName: "TestFail",
			Package:  "simple",
		},
		Status:   types.TestStatusFail,
		Error:    fmt.Errorf("This test is expected to fail"),
		Duration: time.Millisecond * 50,
	}

	// Store the raw JSON for each test
	rawSink.StoreRawJSON(passResult.Metadata.ID, passJSON)
	rawSink.StoreRawJSON(failResult.Metadata.ID, failJSON)

	// Log the test results
	err = logger.LogTestResult(passResult, runID)
	require.NoError(t, err)
	err = logger.LogTestResult(failResult, runID)
	require.NoError(t, err)

	// Complete the logging process
	err = logger.Complete(runID)
	require.NoError(t, err)

	// Wait a short time to ensure async writes complete
	time.Sleep(100 * time.Millisecond)

	// Get and read the raw events file
	rawEventsFile, err := logger.GetRawEventsFileForRunID(runID)
	require.NoError(t, err)
	assert.FileExists(t, rawEventsFile)

	// Read the content to verify
	rawEventsContent, err := os.ReadFile(rawEventsFile)
	require.NoError(t, err)
	require.NotEmpty(t, rawEventsContent, "raw_go_events.log should not be empty")

	// Log some details about the file for debugging
	t.Logf("raw_go_events.log file size: %d bytes", len(rawEventsContent))
	t.Logf("raw_go_events.log file path: %s", rawEventsFile)

	// Verify it has valid JSON lines that can be parsed by gotestsum
	lines := strings.Split(string(rawEventsContent), "\n")
	validJsonCount := 0
	goTestEventCount := 0

	for _, line := range lines {
		if line == "" {
			continue
		}

		// First try to parse as a generic JSON to ensure it's valid JSON
		var rawJSON map[string]interface{}
		err := json.Unmarshal([]byte(line), &rawJSON)
		if err == nil {
			validJsonCount++

			// Now verify it has the shape of a go test event
			var event GoTestEvent
			err = json.Unmarshal([]byte(line), &event)
			if err == nil && event.Action != "" && event.Package != "" {
				goTestEventCount++
			}
		}
	}

	t.Logf("Found %d valid JSON lines and %d go test events in raw_go_events.log",
		validJsonCount, goTestEventCount)

	// Ensure we found some valid go test events
	require.Greater(t, goTestEventCount, 0,
		"raw_go_events.log should contain at least one valid go test event")

	// Count how many events we have for each test in the output
	passCount := 0
	failCount := 0

	for _, line := range strings.Split(string(rawEventsContent), "\n") {
		if line == "" {
			continue
		}

		var event struct {
			Test string `json:"Test"`
		}

		err = json.Unmarshal([]byte(line), &event)
		if err != nil {
			t.Logf("Error parsing JSON line: %v", err)
			continue
		}

		if event.Test == "TestPass" {
			passCount++
		} else if event.Test == "TestFail" {
			failCount++
		}
	}

	// Verify we have events for both tests
	assert.GreaterOrEqual(t, passCount, 1, "Should have events for TestPass")
	assert.GreaterOrEqual(t, failCount, 1, "Should have events for TestFail")
	t.Logf("Found %d events for TestPass and %d events for TestFail in output file",
		passCount, failCount)
}

// min returns the smaller of x or y - for test use only
func min(x, y int) int {
	if x < y {
		return x
	}
	return y
}
