package logging

import (
	"bytes"
	"context"
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
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Create a new FileLogger with a valid runID
	runID := "test-run-raw-json"
	logger, err := NewFileLogger(tmpDir, runID, "test-network", "test-gate")
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
	defer func() { _ = os.RemoveAll(tmpDir) }()

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
	err = cmd.Run()
	// We expect this to have a non-zero exit code due to failing tests, so we don't check the error
	// Log any unexpected errors for debugging, but don't fail the test since we expect some tests to fail
	if err != nil {
		t.Logf("go test command completed with error (expected due to failing tests): %v", err)
	}
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
	logger, err := NewFileLogger(tmpDir, runID, "test-network", "test-gate")
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

		switch event.Test {
		case "TestPass":
			passLines = append(passLines, line)
		case "TestFail":
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

		switch event.Test {
		case "TestPass":
			passCount++
		case "TestFail":
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

// TestRawJSONSink_ComprehensiveLogging tests that raw JSON logging works for all test scenarios
func TestRawJSONSink_ComprehensiveLogging(t *testing.T) {
	// Create a temporary directory for test logs
	tmpDir, err := os.MkdirTemp("", "comprehensive_raw_json_test")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Create a new FileLogger with a valid runID
	runID := "comprehensive-test-run"
	logger, err := NewFileLogger(tmpDir, runID, "test-network", "test-gate")
	require.NoError(t, err)

	// Get the RawJSONSink from the logger
	sink, ok := logger.GetSinkByType("RawJSONSink")
	require.True(t, ok, "RawJSONSink should be available")

	rawSink, ok := sink.(*RawJSONSink)
	require.True(t, ok, "Sink should be of type *RawJSONSink")

	// Test scenarios: individual passing test, individual failing test, and package test
	now := time.Now()

	// Individual passing test raw JSON
	passingTestJSON := createTestJSON([]GoTestEvent{
		{Time: now, Action: "start", Package: "example/pkg", Test: "TestPass"},
		{Time: now.Add(time.Second), Action: "pass", Package: "example/pkg", Test: "TestPass", Elapsed: 1.0},
	})

	// Individual failing test raw JSON
	failingTestJSON := createTestJSON([]GoTestEvent{
		{Time: now, Action: "start", Package: "example/pkg", Test: "TestFail"},
		{Time: now.Add(time.Second), Action: "output", Package: "example/pkg", Test: "TestFail", Output: "Test failed\n"},
		{Time: now.Add(2 * time.Second), Action: "fail", Package: "example/pkg", Test: "TestFail", Elapsed: 2.0},
	})

	// Package test raw JSON (aggregated from multiple tests)
	packageTestJSON := createTestJSON([]GoTestEvent{
		{Time: now, Action: "start", Package: "example/pkg", Test: "TestOne"},
		{Time: now.Add(time.Second), Action: "pass", Package: "example/pkg", Test: "TestOne", Elapsed: 1.0},
		{Time: now.Add(time.Second), Action: "start", Package: "example/pkg", Test: "TestTwo"},
		{Time: now.Add(2 * time.Second), Action: "fail", Package: "example/pkg", Test: "TestTwo", Elapsed: 1.0},
	})

	// Create test results for different scenarios
	testResults := []*types.TestResult{
		{
			Metadata: types.ValidatorMetadata{
				ID:       "individual-pass-test",
				Gate:     "test-gate",
				Suite:    "test-suite",
				FuncName: "TestPass",
				Package:  "example/pkg",
			},
			Status:   types.TestStatusPass,
			Duration: time.Second,
		},
		{
			Metadata: types.ValidatorMetadata{
				ID:       "individual-fail-test",
				Gate:     "test-gate",
				Suite:    "test-suite",
				FuncName: "TestFail",
				Package:  "example/pkg",
			},
			Status:   types.TestStatusFail,
			Error:    fmt.Errorf("Test failed"),
			Duration: 2 * time.Second,
		},
		{
			Metadata: types.ValidatorMetadata{
				ID:      "package-test",
				Gate:    "test-gate",
				Suite:   "test-suite",
				Package: "example/pkg",
				RunAll:  true,
			},
			Status:   types.TestStatusFail, // Package fails because one test fails
			Error:    fmt.Errorf("TestTwo: Test failed"),
			Duration: 2 * time.Second,
			SubTests: map[string]*types.TestResult{
				"TestOne": {
					Metadata: types.ValidatorMetadata{FuncName: "TestOne", Package: "example/pkg"},
					Status:   types.TestStatusPass,
					Duration: time.Second,
				},
				"TestTwo": {
					Metadata: types.ValidatorMetadata{FuncName: "TestTwo", Package: "example/pkg"},
					Status:   types.TestStatusFail,
					Error:    fmt.Errorf("Test failed"),
					Duration: time.Second,
				},
			},
		},
	}

	// Store the raw JSON for each test
	rawSink.StoreRawJSON(testResults[0].Metadata.ID, passingTestJSON)
	rawSink.StoreRawJSON(testResults[1].Metadata.ID, failingTestJSON)
	rawSink.StoreRawJSON(testResults[2].Metadata.ID, packageTestJSON)

	// Log all test results
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
	require.NotEmpty(t, rawEventsContent, "raw_go_events.log should not be empty")

	// Parse and verify the content
	lines := strings.Split(string(rawEventsContent), "\n")
	var validEvents []GoTestEvent

	for _, line := range lines {
		if line == "" {
			continue
		}

		var event GoTestEvent
		err := json.Unmarshal([]byte(line), &event)
		require.NoError(t, err, "Line should be valid JSON: %s", line)
		validEvents = append(validEvents, event)
	}

	// Verify we have events from all test types
	require.GreaterOrEqual(t, len(validEvents), 6, "Should have events from all tests")

	// Count events by test name to verify all tests are represented
	eventCounts := make(map[string]int)
	for _, event := range validEvents {
		if event.Test != "" {
			eventCounts[event.Test]++
		}
	}

	// Verify we have events for all individual tests
	assert.GreaterOrEqual(t, eventCounts["TestPass"], 1, "Should have events for TestPass")
	assert.GreaterOrEqual(t, eventCounts["TestFail"], 1, "Should have events for TestFail")
	assert.GreaterOrEqual(t, eventCounts["TestOne"], 1, "Should have events for TestOne (from package test)")
	assert.GreaterOrEqual(t, eventCounts["TestTwo"], 1, "Should have events for TestTwo (from package test)")

	// Verify that both passing and failing tests are represented
	hasPassEvent := false
	hasFailEvent := false
	for _, event := range validEvents {
		if event.Action == "pass" {
			hasPassEvent = true
		}
		if event.Action == "fail" {
			hasFailEvent = true
		}
	}

	assert.True(t, hasPassEvent, "Should have at least one pass event")
	assert.True(t, hasFailEvent, "Should have at least one fail event")

	t.Logf("Successfully verified raw JSON logging for %d test results with %d total events",
		len(testResults), len(validEvents))
}

// createTestJSON is a helper function to create JSON from test events
func createTestJSON(events []GoTestEvent) []byte {
	var result []byte
	for _, event := range events {
		data, _ := json.Marshal(event)
		result = append(result, data...)
		result = append(result, '\n')
	}
	return result
}

// TestRawJSONSink_IntegrationTest demonstrates end-to-end raw JSON logging
// for both passing and failing tests in a realistic scenario
// This test requires 'go' to be available on the system and will skip if not found
func TestRawJSONSink_IntegrationTest(t *testing.T) {
	// Check if 'go' command is available on the system
	_, err := exec.LookPath("go")
	if err != nil {
		t.Skip("Skipping integration test: 'go' command not found on system")
	}

	// Create a temporary directory
	tmpDir, err := os.MkdirTemp("", "integration_raw_json_test")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Create test files that will pass and fail
	testFileContent := `
package integration

import (
	"testing"
	"time"
)

func TestPassingIntegration(t *testing.T) {
	t.Log("This test will pass")
	time.Sleep(10 * time.Millisecond)
}

func TestFailingIntegration(t *testing.T) {
	t.Log("This test will fail")
	time.Sleep(10 * time.Millisecond)
	t.Error("This test is designed to fail")
}

func TestSkippedIntegration(t *testing.T) {
	t.Skip("This test is skipped")
}
`
	err = os.WriteFile(filepath.Join(tmpDir, "integration_test.go"), []byte(testFileContent), 0644)
	require.NoError(t, err)

	// Create go.mod for the test
	err = os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module integration\n\ngo 1.21\n"), 0644)
	require.NoError(t, err)

	// Run the actual go test with -json flag to get real output
	cmd := exec.Command("go", "test", "-json", "-v")
	cmd.Dir = tmpDir
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	// Run the command (ignore error since we expect some tests to fail)
	// Add a timeout to prevent hanging in case of issues
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd = exec.CommandContext(ctx, "go", "test", "-json", "-v")
	cmd.Dir = tmpDir
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	// We expect this to have a non-zero exit code due to failing tests, so we don't check the error
	// However, if the context was cancelled, we should fail the test
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatal("go test command timed out")
	}
	// Log any unexpected errors for debugging, but don't fail the test since we expect some tests to fail
	if err != nil && ctx.Err() == nil {
		t.Logf("go test command completed with error (expected due to failing tests): %v", err)
	}

	// Get the raw JSON output from go test
	rawGoTestOutput := stdout.Bytes()
	if len(rawGoTestOutput) == 0 {
		t.Logf("stderr output: %s", stderr.String())
		t.Fatal("Raw JSON output from go test should not be empty")
	}

	// Create a file logger to test our raw JSON sink
	runID := "integration-test-run"
	logger, err := NewFileLogger(tmpDir, runID, "test-network", "test-gate")
	require.NoError(t, err)

	// Get the RawJSONSink
	sink, ok := logger.GetSinkByType("RawJSONSink")
	require.True(t, ok, "RawJSONSink should be available")
	rawSink, ok := sink.(*RawJSONSink)
	require.True(t, ok, "Sink should be of type *RawJSONSink")

	// Parse the raw output to separate tests
	testOutputs := make(map[string][]string)
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
		if err != nil {
			continue // Skip non-JSON lines
		}

		if event.Test != "" {
			testOutputs[event.Test] = append(testOutputs[event.Test], line)
		}
	}

	// Verify we captured all expected tests
	expectedTests := []string{"TestPassingIntegration", "TestFailingIntegration", "TestSkippedIntegration"}
	for _, testName := range expectedTests {
		assert.NotEmpty(t, testOutputs[testName], "Should have captured output for %s", testName)
	}

	// Create test results and store raw JSON for each
	testResults := []*types.TestResult{}
	for testName, lines := range testOutputs {
		// Determine status based on test name
		var status types.TestStatus
		var testError error
		switch testName {
		case "TestPassingIntegration":
			status = types.TestStatusPass
		case "TestFailingIntegration":
			status = types.TestStatusFail
			testError = fmt.Errorf("This test is designed to fail")
		case "TestSkippedIntegration":
			status = types.TestStatusSkip
		}

		// Create test result
		result := &types.TestResult{
			Metadata: types.ValidatorMetadata{
				ID:       fmt.Sprintf("integration-%s", testName),
				Gate:     "integration-gate",
				Suite:    "integration-suite",
				FuncName: testName,
				Package:  "integration",
			},
			Status:   status,
			Error:    testError,
			Duration: 10 * time.Millisecond,
		}
		testResults = append(testResults, result)

		// Store raw JSON for this test
		rawJSON := []byte(strings.Join(lines, "\n") + "\n")
		rawSink.StoreRawJSON(result.Metadata.ID, rawJSON)
	}

	// Log all test results
	for _, result := range testResults {
		err = logger.LogTestResult(result, runID)
		require.NoError(t, err)
	}

	// Complete the logging process
	err = logger.Complete(runID)
	require.NoError(t, err)

	// Wait for async writes to complete
	time.Sleep(100 * time.Millisecond)

	// Verify the raw_go_events.log file was created and contains all tests
	rawEventsFile, err := logger.GetRawEventsFileForRunID(runID)
	require.NoError(t, err)
	assert.FileExists(t, rawEventsFile)

	// Read and verify the content
	rawEventsContent, err := os.ReadFile(rawEventsFile)
	require.NoError(t, err)
	require.NotEmpty(t, rawEventsContent, "raw_go_events.log should not be empty")

	// Count events by test and action to verify completeness
	lines := strings.Split(string(rawEventsContent), "\n")
	testEventCounts := make(map[string]map[string]int)

	for _, line := range lines {
		if line == "" {
			continue
		}

		var event struct {
			Action string `json:"Action"`
			Test   string `json:"Test"`
		}

		err := json.Unmarshal([]byte(line), &event)
		require.NoError(t, err, "Line should be valid JSON: %s", line)

		if event.Test != "" {
			if testEventCounts[event.Test] == nil {
				testEventCounts[event.Test] = make(map[string]int)
			}
			testEventCounts[event.Test][event.Action]++
		}
	}

	// Verify all test types are represented with appropriate actions
	assert.Greater(t, testEventCounts["TestPassingIntegration"]["pass"], 0, "Should have pass events for passing test")
	assert.Greater(t, testEventCounts["TestFailingIntegration"]["fail"], 0, "Should have fail events for failing test")
	assert.Greater(t, testEventCounts["TestSkippedIntegration"]["skip"], 0, "Should have skip events for skipped test")

	t.Logf("Integration test successful: captured events for %d tests with complete raw JSON logging", len(testResults))
	t.Logf("Raw events file: %s (%d bytes)", rawEventsFile, len(rawEventsContent))
}

// TestRawJSONSink_IntegrationTest_SkipsWhenGoNotAvailable verifies that the integration test
// properly skips when the 'go' command is not available on the system
func TestRawJSONSink_IntegrationTest_SkipsWhenGoNotAvailable(t *testing.T) {
	// Save the original PATH
	originalPath := os.Getenv("PATH")
	defer func() {
		// Restore the original PATH
		_ = os.Setenv("PATH", originalPath)
	}()

	// Set PATH to empty to simulate 'go' not being available
	_ = os.Setenv("PATH", "")

	// Create a test that should be skipped
	skipped := false

	// We can't directly call t.Skip() in a test, so we'll check the behavior manually
	// by calling exec.LookPath directly
	_, err := exec.LookPath("go")
	if err != nil {
		skipped = true
		t.Log("Test would be skipped: 'go' command not found on system")
	}

	// Verify that the test would indeed be skipped
	assert.True(t, skipped, "Integration test should be skipped when 'go' command is not available")

	// Restore PATH and verify go is available again
	_ = os.Setenv("PATH", originalPath)
	_, err = exec.LookPath("go")
	assert.NoError(t, err, "go command should be available after restoring PATH")
}
