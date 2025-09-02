package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileLogger(t *testing.T) {
	// Create a temporary directory for test logs
	tmpDir, err := os.MkdirTemp("", "filelogger_test")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Create a new FileLogger with a valid runID
	runID := "test-run-123"
	logger, err := NewFileLogger(tmpDir, runID, "test-network", "test-gate")
	require.NoError(t, err)

	// Get the directory with the testrun- prefix
	baseDir, err := logger.GetDirectoryForRunID(runID)
	require.NoError(t, err)

	// Verify the directory structure
	assert.DirExists(t, baseDir)
	assert.DirExists(t, filepath.Join(baseDir, "passed"))
	assert.DirExists(t, filepath.Join(baseDir, "failed"))

	// Create multiple test results
	passResult := &types.TestResult{
		Metadata: types.ValidatorMetadata{
			ID:       "pass-test-id",
			Gate:     "test-gate",
			Suite:    "test-suite",
			FuncName: "TestPassingFunction",
			Package:  "github.com/example/package",
		},
		Status:   types.TestStatusPass,
		Duration: time.Second * 2,
		Stdout:   "Passing test stdout content",
	}

	failResult := &types.TestResult{
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
		Stdout:   "Failing test stdout content",
	}

	skipResult := &types.TestResult{
		Metadata: types.ValidatorMetadata{
			ID:       "skip-test-id",
			Gate:     "test-gate",
			Suite:    "test-suite",
			FuncName: "TestSkippedFunction",
			Package:  "github.com/example/package",
		},
		Status:   types.TestStatusSkip,
		Duration: time.Millisecond * 500,
		Stdout:   "Skipped test stdout content",
	}

	// Log the test results
	err = logger.LogTestResult(passResult, runID)
	require.NoError(t, err)

	err = logger.LogTestResult(failResult, runID)
	require.NoError(t, err)

	err = logger.LogTestResult(skipResult, runID)
	require.NoError(t, err)

	// Complete the logging process
	err = logger.Complete(runID)
	require.NoError(t, err)

	// Wait a short time to ensure async writes complete
	time.Sleep(100 * time.Millisecond)

	// Verify test log files exist in the appropriate directories - now three files per test
	// Passing test files
	passingBasename := "test-gate-test-suite_package_TestPassingFunction"
	assert.FileExists(t, filepath.Join(baseDir, "passed", passingBasename+".txt"))
	assert.FileExists(t, filepath.Join(baseDir, "passed", passingBasename+".json"))
	assert.FileExists(t, filepath.Join(baseDir, "passed", passingBasename+".log"))

	// Skipped test files
	skippedBasename := "test-gate-test-suite_package_TestSkippedFunction"
	assert.FileExists(t, filepath.Join(baseDir, "passed", skippedBasename+".txt"))
	assert.FileExists(t, filepath.Join(baseDir, "passed", skippedBasename+".json"))
	assert.FileExists(t, filepath.Join(baseDir, "passed", skippedBasename+".log"))

	// Failed test files (in failed directory)
	failedBasename := "test-gate-test-suite_package_TestFailingFunction"
	assert.FileExists(t, filepath.Join(baseDir, "failed", failedBasename+".txt"))
	assert.FileExists(t, filepath.Join(baseDir, "failed", failedBasename+".json"))
	assert.FileExists(t, filepath.Join(baseDir, "failed", failedBasename+".log"))

	// Verify all.log file exists
	allLogsFile := logger.GetAllLogsFile()
	assert.FileExists(t, allLogsFile)

	// Verify summary file exists
	summaryFile := logger.GetSummaryFile()
	assert.FileExists(t, summaryFile)

	// Read the content of all.log to verify it contains entries for all tests
	allLogsContent, err := os.ReadFile(allLogsFile)
	require.NoError(t, err)
	allLogsContentStr := string(allLogsContent)

	// Check that all.log contains all test results with the new format
	assert.Contains(t, allLogsContentStr, "TEST: TestPassingFunction")
	assert.Contains(t, allLogsContentStr, "Status:   pass")
	assert.Contains(t, allLogsContentStr, "TEST: TestFailingFunction")
	assert.Contains(t, allLogsContentStr, "Status:   fail")
	assert.Contains(t, allLogsContentStr, "TEST: TestSkippedFunction")
	assert.Contains(t, allLogsContentStr, "Status:   skip")

	// Read the content of summary.log to verify it's concise
	summaryContent, err := os.ReadFile(summaryFile)
	require.NoError(t, err)
	summaryContentStr := string(summaryContent)

	// Check that the summary contains the right statistics
	assert.Contains(t, summaryContentStr, "Test Results Summary")
	assert.Contains(t, summaryContentStr, "Total Tests: 3")
	assert.Contains(t, summaryContentStr, "Passed: 1")
	assert.Contains(t, summaryContentStr, "Failed: 1")
	assert.Contains(t, summaryContentStr, "Skipped: 1")
	assert.Contains(t, summaryContentStr, "Failed Tests:")
	assert.Contains(t, summaryContentStr, "test-gate/test-suite/TestFailingFunction")
}

func TestLoggerWithEmptyRunID(t *testing.T) {
	// Create a temporary directory for test logs
	tmpDir, err := os.MkdirTemp("", "filelogger_empty_runid_test")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Test that an error is returned when an empty runID is provided to NewFileLogger
	_, err = NewFileLogger(tmpDir, "", "test-network", "test-gate")
	assert.Error(t, err, "Expected error when creating logger with empty runID")
	assert.Contains(t, err.Error(), "runID cannot be empty")

	// Create a valid logger to test the LogTestResult with empty runID
	logger, err := NewFileLogger(tmpDir, "valid-run-id", "test-network", "test-gate")
	require.NoError(t, err)

	// Create a test result
	testResult := &types.TestResult{
		Metadata: types.ValidatorMetadata{
			FuncName: "TestFunction",
		},
		Status: types.TestStatusPass,
	}

	// Test that an error is returned when an empty runID is provided to LogTestResult
	err = logger.LogTestResult(testResult, "")
	assert.Error(t, err, "Expected error when logging with empty runID")
	assert.Contains(t, err.Error(), "runID cannot be empty")

	// Test that an error is returned when an empty runID is provided to LogSummary
	err = logger.LogSummary("Summary", "")
	assert.Error(t, err, "Expected error when logging summary with empty runID")
	assert.Contains(t, err.Error(), "runID cannot be empty")

	// Test that an error is returned when an empty runID is provided to Complete
	err = logger.Complete("")
	assert.Error(t, err, "Expected error when completing with empty runID")
	assert.Contains(t, err.Error(), "runID cannot be empty")
}

func TestLoggingWithRunID(t *testing.T) {
	// Create a temporary directory for test logs
	tmpDir, err := os.MkdirTemp("", "filelogger_runid_test")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Create a new FileLogger with a valid runID
	defaultRunID := "default-run-id"
	logger, err := NewFileLogger(tmpDir, defaultRunID, "test-network", "test-gate")
	require.NoError(t, err)

	// We'll use a different runID for this test
	differentRunID := "different-run-id"

	// Create the directory structure for the different runID
	differentRunIDDir := filepath.Join(tmpDir, "testrun-"+differentRunID)
	passedDir := filepath.Join(differentRunIDDir, "passed")
	failedDir := filepath.Join(differentRunIDDir, "failed")

	// Create the directory structure for the different runID
	for _, dir := range []string{differentRunIDDir, passedDir, failedDir} {
		err := os.MkdirAll(dir, 0755)
		require.NoError(t, err, "Failed to create directory: %s", dir)
	}

	// Create a test result
	testResult := &types.TestResult{
		Metadata: types.ValidatorMetadata{
			ID:       "test-id",
			Gate:     "test-gate",
			Suite:    "test-suite",
			FuncName: "TestFunction",
			Package:  "github.com/example/package",
		},
		Status:   types.TestStatusPass,
		Duration: time.Second * 2,
		Stdout:   "Test stdout content with runID",
	}

	// Log the test result with a different runID
	err = logger.LogTestResult(testResult, differentRunID)
	require.NoError(t, err)

	// Complete the logging process
	err = logger.Complete(differentRunID)
	require.NoError(t, err)

	// Wait a short time to ensure async writes complete
	time.Sleep(100 * time.Millisecond)

	// Get the directory for the runID
	runIDDir, err := logger.GetDirectoryForRunID(differentRunID)
	require.NoError(t, err)

	// Verify the directory structure was created
	assert.DirExists(t, runIDDir)
	assert.DirExists(t, filepath.Join(runIDDir, "passed"))
	assert.DirExists(t, filepath.Join(runIDDir, "failed"))

	// Verify that the runID is used in the directory name
	expectedDirName := filepath.Join(tmpDir, "testrun-"+differentRunID)
	assert.Equal(t, expectedDirName, runIDDir)

	// Verify the test log files exist in the runID directory - three files per test
	testBasename := "test-gate-test-suite_package_TestFunction"
	assert.FileExists(t, filepath.Join(runIDDir, "passed", testBasename+".txt"))
	assert.FileExists(t, filepath.Join(runIDDir, "passed", testBasename+".json"))
	assert.FileExists(t, filepath.Join(runIDDir, "passed", testBasename+".log"))

	// Verify all.log file exists for this runID
	allLogsFile, err := logger.GetAllLogsFileForRunID(differentRunID)
	require.NoError(t, err)
	assert.FileExists(t, allLogsFile)

	// Verify summary file exists and contains expected content
	summaryFilePath, err := logger.GetSummaryFileForRunID(differentRunID)
	require.NoError(t, err)
	assert.FileExists(t, summaryFilePath)

	// Test error cases for directory methods with empty runID
	_, err = logger.GetDirectoryForRunID("")
	assert.Error(t, err, "Expected error when getting directory with empty runID")
	assert.Contains(t, err.Error(), "runID cannot be empty")

	_, err = logger.GetFailedDirForRunID("")
	assert.Error(t, err, "Expected error when getting failed directory with empty runID")
	assert.Contains(t, err.Error(), "runID cannot be empty")

	_, err = logger.GetSummaryFileForRunID("")
	assert.Error(t, err, "Expected error when getting summary file with empty runID")
	assert.Contains(t, err.Error(), "runID cannot be empty")

	_, err = logger.GetAllLogsFileForRunID("")
	assert.Error(t, err, "Expected error when getting all logs file with empty runID")
	assert.Contains(t, err.Error(), "runID cannot be empty")
}

func TestAsyncFileWriter(t *testing.T) {
	// Create a temporary directory
	tmpDir, err := os.MkdirTemp("", "asyncfile_test")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Create a test file path
	testFilePath := filepath.Join(tmpDir, "async_test.log")

	// Create a new AsyncFile
	asyncFile, err := NewAsyncFile(testFilePath)
	require.NoError(t, err)

	// Write some data
	testData := []byte("Test async write 1\n")
	err = asyncFile.Write(testData)
	require.NoError(t, err)

	// Write more data
	testData2 := []byte("Test async write 2\n")
	err = asyncFile.Write(testData2)
	require.NoError(t, err)

	// Close the file
	err = asyncFile.Close()
	require.NoError(t, err)

	// Wait a short time to ensure async writes complete
	time.Sleep(100 * time.Millisecond)

	// Verify the file exists and contains both writes
	content, err := os.ReadFile(testFilePath)
	require.NoError(t, err)

	assert.Contains(t, string(content), "Test async write 1")
	assert.Contains(t, string(content), "Test async write 2")

	// Test writing to a closed AsyncFile
	err = asyncFile.Write([]byte("This should fail"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "async file is closed")
}

func TestCustomResultSink(t *testing.T) {
	// Create a temporary directory for test logs
	tmpDir, err := os.MkdirTemp("", "custom_sink_test")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Create a new FileLogger with a valid runID
	runID := "custom-sink-test"
	logger, err := NewFileLogger(tmpDir, runID, "test-network", "test-gate")
	require.NoError(t, err)

	// Create a custom sink for testing
	customSink := &testCustomSink{
		results: make([]*types.TestResult, 0),
	}

	// Add the custom sink to the logger
	logger.sinks = append(logger.sinks, customSink)

	// Create a test result
	testResult := &types.TestResult{
		Metadata: types.ValidatorMetadata{
			FuncName: "TestCustomSink",
			Package:  "github.com/example/package",
		},
		Status: types.TestStatusPass,
	}

	// Log the test result
	err = logger.LogTestResult(testResult, runID)
	require.NoError(t, err)

	// Complete logging
	err = logger.Complete(runID)
	require.NoError(t, err)

	// Verify the custom sink received the result
	assert.Equal(t, 1, len(customSink.results))
	assert.Equal(t, "TestCustomSink", customSink.results[0].Metadata.FuncName)
	assert.True(t, customSink.completed)
}

// Custom sink for testing
type testCustomSink struct {
	results   []*types.TestResult
	completed bool
}

func (s *testCustomSink) Consume(result *types.TestResult, runID string) error {
	s.results = append(s.results, result)
	return nil
}

func (s *testCustomSink) Complete(runID string) error {
	s.completed = true
	return nil
}

// TestPerTestFileSink_WritesTestOutput tests that PerTestFileSink writes test output to passed/failed directories
func TestPerTestFileSink_WritesTestOutput(t *testing.T) {
	// Create a temporary directory
	tmpDir := t.TempDir()
	runID := "test-pertest-sink"

	// Create a file logger
	logger, err := NewFileLogger(tmpDir, runID, "test-network", "test-gate")
	require.NoError(t, err)

	// Access the PerTestFileSink directly
	sink, ok := logger.GetSinkByType("PerTestFileSink")
	require.True(t, ok, "PerTestFileSink should be available")
	perTestSink, ok := sink.(*PerTestFileSink)
	require.True(t, ok, "Sink should be of type *PerTestFileSink")

	// Create test metadata for a passing test
	passingMeta := types.ValidatorMetadata{
		ID:       "test-pass",
		FuncName: "TestPass",
		Package:  "github.com/example/package",
		Gate:     "gate1",
		Suite:    "suite1",
	}

	// Create a passing test result with JSON-formatted output
	passingResult := &types.TestResult{
		Metadata: passingMeta,
		Status:   types.TestStatusPass,
		Duration: 1 * time.Second,
		Stdout: `{"Time":"2025-05-09T16:31:48.748553+10:00","Action":"output","Package":"github.com/example/package","Test":"TestPass","Output":"=== RUN   TestPass\n"}
{"Time":"2025-05-09T16:31:48.748563+10:00","Action":"output","Package":"github.com/example/package","Test":"TestPass","Output":"--- PASS: TestPass (1.00s)\n"}
{"Time":"2025-05-09T16:31:48.748570+10:00","Action":"pass","Package":"github.com/example/package","Test":"TestPass","Elapsed":1}`,
	}

	// Create test metadata for a failing test
	failingMeta := types.ValidatorMetadata{
		ID:       "test-fail",
		FuncName: "TestFail",
		Package:  "github.com/example/package",
		Gate:     "gate1",
		Suite:    "suite1",
	}

	// Create a failing test result with JSON-formatted output including error information
	failingResult := &types.TestResult{
		Metadata: failingMeta,
		Status:   types.TestStatusFail,
		Duration: 500 * time.Millisecond,
		Stdout: `{"Time":"2025-05-09T16:31:48.748553+10:00","Action":"output","Package":"github.com/example/package","Test":"TestFail","Output":"=== RUN   TestFail\n"}
{"Time":"2025-05-09T16:31:48.748563+10:00","Action":"output","Package":"github.com/example/package","Test":"TestFail","Output":"    Error Trace:    /path/to/file.go:123\n"}
{"Time":"2025-05-09T16:31:48.748570+10:00","Action":"output","Package":"github.com/example/package","Test":"TestFail","Output":"    Error:          Test failed: assertion error\n"}
{"Time":"2025-05-09T16:31:48.748571+10:00","Action":"output","Package":"github.com/example/package","Test":"TestFail","Output":"                    expected: int(42)\n"}
{"Time":"2025-05-09T16:31:48.748572+10:00","Action":"output","Package":"github.com/example/package","Test":"TestFail","Output":"                    actual  : int(43)\n"}
{"Time":"2025-05-09T16:31:48.748573+10:00","Action":"output","Package":"github.com/example/package","Test":"TestFail","Output":"    Messages:       Expected values to be equal\n"}
{"Time":"2025-05-09T16:31:48.748580+10:00","Action":"fail","Package":"github.com/example/package","Test":"TestFail","Elapsed":0.5}`,
		Error: fmt.Errorf("Test failed: assertion error"),
	}

	// Process test results through the sink
	require.NoError(t, perTestSink.Consume(passingResult, runID))
	require.NoError(t, perTestSink.Consume(failingResult, runID))

	// Get directory paths
	baseDir, err := logger.GetDirectoryForRunID(runID)
	require.NoError(t, err)
	passedDir := filepath.Join(baseDir, "passed")
	failedDir := filepath.Join(baseDir, "failed")

	// Finalize to ensure all files are written
	require.NoError(t, logger.Complete(runID))

	// Verify the three separate files in passed directory
	passedBasename := getReadableTestFilename(passingMeta)
	passedTxtPath := filepath.Join(passedDir, passedBasename+".txt")
	passedJsonPath := filepath.Join(passedDir, passedBasename+".json")
	passedLogPath := filepath.Join(passedDir, passedBasename+".log")

	// Check plaintext file
	passedTxtContent, err := os.ReadFile(passedTxtPath)
	require.NoError(t, err, "Should be able to read the passing test plaintext file")
	passedTxtStr := string(passedTxtContent)
	assert.Contains(t, passedTxtStr, "=== RUN   TestPass")
	assert.Contains(t, passedTxtStr, "--- PASS: TestPass (1.00s)")
	// Should NOT contain headers since this is a separate file
	assert.NotContains(t, passedTxtStr, "PLAINTEXT OUTPUT:")

	// Check JSON file
	passedJsonContent, err := os.ReadFile(passedJsonPath)
	require.NoError(t, err, "Should be able to read the passing test JSON file")
	passedJsonStr := string(passedJsonContent)
	assert.Contains(t, passedJsonStr, `"Action":"output"`)
	assert.Contains(t, passedJsonStr, `"Package":"github.com/example/package"`)
	// Should NOT contain headers since this is a separate file
	assert.NotContains(t, passedJsonStr, "JSON OUTPUT:")

	// Check summary file
	passedLogContent, err := os.ReadFile(passedLogPath)
	require.NoError(t, err, "Should be able to read the passing test summary file")
	passedLogStr := string(passedLogContent)
	assert.Contains(t, passedLogStr, "RESULT SUMMARY:")
	assert.Contains(t, passedLogStr, "Test passed:")
	assert.Contains(t, passedLogStr, "Duration:")

	// Verify the three separate files in failed directory
	failedBasename := getReadableTestFilename(failingMeta)
	failedTxtPath := filepath.Join(failedDir, failedBasename+".txt")
	failedJsonPath := filepath.Join(failedDir, failedBasename+".json")
	failedLogPath := filepath.Join(failedDir, failedBasename+".log")

	// Check plaintext file
	failedTxtContent, err := os.ReadFile(failedTxtPath)
	require.NoError(t, err, "Should be able to read the failing test plaintext file")
	failedTxtStr := string(failedTxtContent)
	assert.Contains(t, failedTxtStr, "=== RUN   TestFail")
	assert.Contains(t, failedTxtStr, "Error Trace:")
	assert.Contains(t, failedTxtStr, "Error:")
	assert.Contains(t, failedTxtStr, "expected: int(42)")
	assert.Contains(t, failedTxtStr, "actual  : int(43)")
	assert.Contains(t, failedTxtStr, "Messages:")
	// Should NOT contain headers since this is a separate file
	assert.NotContains(t, failedTxtStr, "PLAINTEXT OUTPUT:")

	// Check JSON file
	failedJsonContent, err := os.ReadFile(failedJsonPath)
	require.NoError(t, err, "Should be able to read the failing test JSON file")
	failedJsonStr := string(failedJsonContent)
	assert.Contains(t, failedJsonStr, `"Action":"output"`)
	assert.Contains(t, failedJsonStr, `"Package":"github.com/example/package"`)
	// Should NOT contain headers since this is a separate file
	assert.NotContains(t, failedJsonStr, "JSON OUTPUT:")

	// Check summary file
	failedLogContent, err := os.ReadFile(failedLogPath)
	require.NoError(t, err, "Should be able to read the failing test summary file")
	failedLogStr := string(failedLogContent)
	assert.Contains(t, failedLogStr, "ERROR SUMMARY:")
	assert.Contains(t, failedLogStr, "Test:       TestFail")
	assert.Contains(t, failedLogStr, "Error:      Test failed: assertion error")
	assert.Contains(t, failedLogStr, "Message:    Expected values to be equal")
	assert.Contains(t, failedLogStr, "Error Trace:")
}

// TestHTMLSummarySink_GeneratesHTMLReport tests that the HTML summary sink generates a proper HTML report
func TestHTMLSummarySink_GeneratesHTMLReport(t *testing.T) {
	// Create a temporary directory
	tmpDir := t.TempDir()
	runID := "test-html-summary"

	// Create a file logger
	logger, err := NewFileLogger(tmpDir, runID, "test-network", "test-gate")
	require.NoError(t, err)

	// Create a mix of test results
	testResults := []*types.TestResult{
		{
			Metadata: types.ValidatorMetadata{
				ID:       "test-pass-1",
				FuncName: "TestPassingOne",
				Package:  "github.com/example/package1",
				Gate:     "gate1",
				Suite:    "suite1",
			},
			Status:   types.TestStatusPass,
			Duration: 1 * time.Second,
			Stdout:   "Passing test output",
		},
		{
			Metadata: types.ValidatorMetadata{
				ID:       "test-pass-2",
				FuncName: "TestPassingTwo",
				Package:  "github.com/example/package2",
				Gate:     "gate1",
				Suite:    "suite2",
			},
			Status:   types.TestStatusPass,
			Duration: 500 * time.Millisecond,
			Stdout:   "Another passing test",
		},
		{
			Metadata: types.ValidatorMetadata{
				ID:       "test-fail-1",
				FuncName: "TestFailingOne",
				Package:  "github.com/example/package1",
				Gate:     "gate2",
				Suite:    "suite1",
			},
			Status:   types.TestStatusFail,
			Duration: 1500 * time.Millisecond,
			Stdout:   "Failing test output",
			Error:    fmt.Errorf("Test assertion failed"),
		},
		{
			Metadata: types.ValidatorMetadata{
				ID:       "test-skip-1",
				FuncName: "TestSkipped",
				Package:  "github.com/example/package3",
				Gate:     "gate2",
				Suite:    "suite3",
			},
			Status:   types.TestStatusSkip,
			Duration: 100 * time.Millisecond,
			Stdout:   "Skipped test",
		},
	}

	// Process all test results through the logger
	for _, result := range testResults {
		err := logger.LogTestResult(result, runID)
		require.NoError(t, err)
	}

	// Complete the report generation using the logger
	err = logger.Complete(runID)
	require.NoError(t, err)

	// Get the expected HTML file path
	baseDir, err := logger.GetDirectoryForRunID(runID)
	require.NoError(t, err)
	htmlFile := filepath.Join(baseDir, HTMLResultsFilename)

	// Wait for file to be created and have content with a retry pattern
	var content []byte
	require.Eventually(t, func() bool {
		// Check if file exists
		if _, err := os.Stat(htmlFile); err != nil {
			return false
		}

		// Read the file content
		var readErr error
		content, readErr = os.ReadFile(htmlFile)
		if readErr != nil {
			return false
		}

		// Check if content is not empty and contains the expected title
		return len(content) > 0 && strings.Contains(string(content), "<title>Acceptance Test Results</title>")
	}, 2*time.Second, 10*time.Millisecond, "HTML file should be created with valid content")

	htmlContent := string(content)

	// Verify the HTML content contains expected elements
	assert.NotEmpty(t, htmlContent, "HTML content should not be empty")
	assert.Contains(t, htmlContent, "<title>Acceptance Test Results</title>")
	assert.Contains(t, htmlContent, "TestPassingOne")
	assert.Contains(t, htmlContent, "TestFailingOne")
	assert.Contains(t, htmlContent, "TestSkipped")
	assert.Contains(t, htmlContent, "github.com/example/package1")
	assert.Contains(t, htmlContent, "gate1")
	assert.Contains(t, htmlContent, "suite1")
}

// TestHTMLSummarySink_WithSubtestsAndNetworkInfo tests HTML generation with subtests and network information
func TestHTMLSummarySink_WithSubtestsAndNetworkInfo(t *testing.T) {
	// Create a temporary directory
	tmpDir := t.TempDir()
	runID := "test-html-with-subtests"
	networkName := "isthmus-devnet"
	gateRun := "isthmus"

	// Create a file logger with network and gate information
	logger, err := NewFileLogger(tmpDir, runID, networkName, gateRun)
	require.NoError(t, err)

	// Create subtests
	subtests := map[string]*types.TestResult{
		"TestWithSubtests/subtest_pass": {
			Metadata: types.ValidatorMetadata{
				ID:       "subtest-pass",
				FuncName: "TestWithSubtests/subtest_pass",
			},
			Status:   types.TestStatusPass,
			Duration: 500 * time.Millisecond,
			Stdout:   `{"Time":"2025-05-09T16:31:48.748553+10:00","Action":"output","Package":"github.com/example/package","Test":"TestWithSubtests/subtest_pass","Output":"=== RUN   TestWithSubtests/subtest_pass\n"}`,
		},
		"TestWithSubtests/subtest_fail": {
			Metadata: types.ValidatorMetadata{
				ID:       "subtest-fail",
				FuncName: "TestWithSubtests/subtest_fail",
			},
			Status:   types.TestStatusFail,
			Duration: 300 * time.Millisecond,
			Stdout:   `{"Time":"2025-05-09T16:31:48.748553+10:00","Action":"output","Package":"github.com/example/package","Test":"TestWithSubtests/subtest_fail","Output":"=== RUN   TestWithSubtests/subtest_fail\n"}`,
			Error:    fmt.Errorf("Subtest failed"),
		},
	}

	// Create a main test result with subtests
	mainResult := &types.TestResult{
		Metadata: types.ValidatorMetadata{
			ID:       "test-with-subtests",
			FuncName: "TestWithSubtests",
			Package:  "github.com/example/package",
			Gate:     "isthmus",
			Suite:    "acceptance",
		},
		Status:   types.TestStatusFail, // Main test fails because one subtest failed
		Duration: 1 * time.Second,
		Stdout:   `{"Time":"2025-05-09T16:31:48.748553+10:00","Action":"output","Package":"github.com/example/package","Test":"TestWithSubtests","Output":"=== RUN   TestWithSubtests\n"}`,
		SubTests: subtests,
	}

	// Process the test result through the complete logger (all sinks)
	require.NoError(t, logger.LogTestResult(mainResult, runID))

	// Complete the logging process for all sinks
	require.NoError(t, logger.Complete(runID))

	// Check that the HTML file was created
	baseDir, err := logger.GetDirectoryForRunID(runID)
	require.NoError(t, err)
	htmlFile := filepath.Join(baseDir, HTMLResultsFilename)

	// Ensure the HTML file exists
	_, err = os.Stat(htmlFile)
	require.NoError(t, err, "HTML report file should exist")

	// Read the HTML file content
	content, err := os.ReadFile(htmlFile)
	require.NoError(t, err)
	htmlContent := string(content)

	// Verify the HTML content contains expected elements
	assert.NotEmpty(t, htmlContent, "HTML content should not be empty")
	assert.Contains(t, htmlContent, "<title>Acceptance Test Results</title>")

	// Verify network information is displayed (removed gate from summary, emojis removed)
	assert.Contains(t, htmlContent, "<strong>Network:</strong> isthmus-devnet")

	// Verify main test and subtests are included
	assert.Contains(t, htmlContent, "TestWithSubtests")
	assert.Contains(t, htmlContent, "subtest_pass")
	assert.Contains(t, htmlContent, "subtest_fail")

	// Verify correct package information
	assert.Contains(t, htmlContent, "github.com/example/package")
	assert.Contains(t, htmlContent, "isthmus")
	assert.Contains(t, htmlContent, "acceptance")

	// Verify subtest CSS classes are applied (updated for new template)
	assert.Contains(t, htmlContent, "test-item") // Both tests and subtests use test-item class

	// Verify links to plaintext files (HTML now links to .txt files)
	assert.Contains(t, htmlContent, "passed/isthmus-acceptance_package_TestWithSubtests_subtest_pass.txt")
	assert.Contains(t, htmlContent, "failed/isthmus-acceptance_package_TestWithSubtests_subtest_fail.txt")

	// Verify the corresponding log files actually exist (all three types)
	passedDir := filepath.Join(baseDir, "passed")
	failedDir := filepath.Join(baseDir, "failed")

	// Passing subtest files
	passBasename := "isthmus-acceptance_package_TestWithSubtests_subtest_pass"
	assert.FileExists(t, filepath.Join(passedDir, passBasename+".txt"), "Passing subtest plaintext file should exist")
	assert.FileExists(t, filepath.Join(passedDir, passBasename+".json"), "Passing subtest JSON file should exist")
	assert.FileExists(t, filepath.Join(passedDir, passBasename+".log"), "Passing subtest summary file should exist")

	// Failing subtest files
	failBasename := "isthmus-acceptance_package_TestWithSubtests_subtest_fail"
	assert.FileExists(t, filepath.Join(failedDir, failBasename+".txt"), "Failing subtest plaintext file should exist")
	assert.FileExists(t, filepath.Join(failedDir, failBasename+".json"), "Failing subtest JSON file should exist")
	assert.FileExists(t, filepath.Join(failedDir, failBasename+".log"), "Failing subtest summary file should exist")

	// Verify statistics are correct (main test + 2 subtests = 3 total)
	assert.Contains(t, htmlContent, "<div class=\"stat-value\">3</div>")     // Total
	assert.Contains(t, htmlContent, "<div class=\"stat-value\">33.3%</div>") // Pass rate (1 pass out of 3 total)
}

// TestPerTestFileSink_CreatesSubtestFiles tests that PerTestFileSink creates individual log files for subtests
func TestPerTestFileSink_CreatesSubtestFiles(t *testing.T) {
	// Create a temporary directory
	tmpDir := t.TempDir()
	runID := "test-subtests"

	// Create a file logger
	logger, err := NewFileLogger(tmpDir, runID, "test-network", "test-gate")
	require.NoError(t, err)

	// Access the PerTestFileSink directly
	sink, ok := logger.GetSinkByType("PerTestFileSink")
	require.True(t, ok, "PerTestFileSink should be available")
	perTestSink, ok := sink.(*PerTestFileSink)
	require.True(t, ok, "Sink should be of type *PerTestFileSink")

	// Create test metadata for a main test with subtests
	mainMeta := types.ValidatorMetadata{
		ID:       "test-with-subtests",
		FuncName: "TestWithSubtests",
		Package:  "github.com/example/package",
		Gate:     "gate1",
		Suite:    "suite1",
	}

	// Create subtests
	subtests := map[string]*types.TestResult{
		"TestWithSubtests/subtest_1": {
			Metadata: types.ValidatorMetadata{
				ID:       "subtest-1",
				FuncName: "TestWithSubtests/subtest_1",
			},
			Status:   types.TestStatusPass,
			Duration: 500 * time.Millisecond,
			Stdout:   `{"Time":"2025-05-09T16:31:48.748553+10:00","Action":"output","Package":"github.com/example/package","Test":"TestWithSubtests/subtest_1","Output":"=== RUN   TestWithSubtests/subtest_1\n"}`,
		},
		"TestWithSubtests/subtest_2": {
			Metadata: types.ValidatorMetadata{
				ID:       "subtest-2",
				FuncName: "TestWithSubtests/subtest_2",
			},
			Status:   types.TestStatusFail,
			Duration: 300 * time.Millisecond,
			Stdout:   `{"Time":"2025-05-09T16:31:48.748553+10:00","Action":"output","Package":"github.com/example/package","Test":"TestWithSubtests/subtest_2","Output":"=== RUN   TestWithSubtests/subtest_2\n"}`,
			Error:    fmt.Errorf("Subtest failed"),
		},
	}

	// Create a main test result with subtests
	mainResult := &types.TestResult{
		Metadata: mainMeta,
		Status:   types.TestStatusFail, // Main test fails because one subtest failed
		Duration: 1 * time.Second,
		Stdout:   `{"Time":"2025-05-09T16:31:48.748553+10:00","Action":"output","Package":"github.com/example/package","Test":"TestWithSubtests","Output":"=== RUN   TestWithSubtests\n"}`,
		SubTests: subtests,
	}

	// Process the test result through the sink
	require.NoError(t, perTestSink.Consume(mainResult, runID))

	// Get directory paths
	baseDir, err := logger.GetDirectoryForRunID(runID)
	require.NoError(t, err)
	passedDir := filepath.Join(baseDir, "passed")
	failedDir := filepath.Join(baseDir, "failed")

	// Finalize to ensure all files are written
	require.NoError(t, logger.Complete(runID))

	// Verify the main test files exist in failed directory (since it failed) - three files
	mainBasename := getReadableTestFilename(mainMeta)
	assert.FileExists(t, filepath.Join(failedDir, mainBasename+".txt"), "Main test plaintext file should exist")
	assert.FileExists(t, filepath.Join(failedDir, mainBasename+".json"), "Main test JSON file should exist")
	assert.FileExists(t, filepath.Join(failedDir, mainBasename+".log"), "Main test summary file should exist")

	// Verify subtest files exist in their respective directories - three files each
	subtest1Basename := "gate1-suite1_package_TestWithSubtests_subtest_1"
	assert.FileExists(t, filepath.Join(passedDir, subtest1Basename+".txt"), "Passing subtest plaintext file should exist")
	assert.FileExists(t, filepath.Join(passedDir, subtest1Basename+".json"), "Passing subtest JSON file should exist")
	assert.FileExists(t, filepath.Join(passedDir, subtest1Basename+".log"), "Passing subtest summary file should exist")

	subtest2Basename := "gate1-suite1_package_TestWithSubtests_subtest_2"
	assert.FileExists(t, filepath.Join(failedDir, subtest2Basename+".txt"), "Failing subtest plaintext file should exist")
	assert.FileExists(t, filepath.Join(failedDir, subtest2Basename+".json"), "Failing subtest JSON file should exist")
	assert.FileExists(t, filepath.Join(failedDir, subtest2Basename+".log"), "Failing subtest summary file should exist")

	// Verify the content of the subtest summary files
	subtest1LogPath := filepath.Join(passedDir, subtest1Basename+".log")
	subtest1LogContent, err := os.ReadFile(subtest1LogPath)
	require.NoError(t, err)
	subtest1LogStr := string(subtest1LogContent)
	assert.Contains(t, subtest1LogStr, "RESULT SUMMARY:")
	assert.Contains(t, subtest1LogStr, "Test passed: TestWithSubtests/subtest_1")

	subtest2LogPath := filepath.Join(failedDir, subtest2Basename+".log")
	subtest2LogContent, err := os.ReadFile(subtest2LogPath)
	require.NoError(t, err)
	subtest2LogStr := string(subtest2LogContent)
	assert.Contains(t, subtest2LogStr, "ERROR SUMMARY:")
}

// TestDuplicationFix verifies that logging the same test multiple times doesn't create duplicate content
func TestDuplicationFix(t *testing.T) {
	// Create a temporary directory for logs
	logDir := t.TempDir()
	runID := "duplication-test"

	// Create a file logger
	logger, err := NewFileLogger(logDir, runID, "test-network", "test-gate")
	require.NoError(t, err)

	// Create a test result that would have caused duplication before
	testResult := &types.TestResult{
		Metadata: types.ValidatorMetadata{
			ID:       "test1",
			FuncName: "TestChainFork",
			Package:  "github.com/ethereum-optimism/optimism/op-acceptance-tests/tests/base",
			Gate:     "base",
			Suite:    "",
			Timeout:  1 * time.Second,
		},
		Status:   types.TestStatusFail,
		Duration: 1203 * time.Millisecond,
		Error:    fmt.Errorf("TIMEOUT: Test timed out after 1s (actual duration: 1.203660834s)"),
		Stdout:   "",   // Empty stdout simulates timeout with no output
		TimedOut: true, // Mark as timed out for our new timeout handling
	}

	// Log the same test result multiple times (which would cause duplication before)
	for i := 0; i < 3; i++ {
		err = logger.LogTestResult(testResult, runID)
		require.NoError(t, err)
	}

	// Complete the logging
	err = logger.Complete(runID)
	require.NoError(t, err)

	// Get the correct base directory for the runID
	baseDir, err := logger.GetDirectoryForRunID(runID)
	require.NoError(t, err)

	// Check that the three separate files were created in the failed directory
	failedDir := filepath.Join(baseDir, "failed")
	failedFiles, err := os.ReadDir(failedDir)
	require.NoError(t, err)

	// Should have exactly 3 files: .txt, .json, .log
	assert.Len(t, failedFiles, 3, "Expected exactly 3 files in failed directory")

	// Check that we have one file of each type
	var hasText, hasJson, hasLog bool
	for _, file := range failedFiles {
		switch {
		case strings.HasSuffix(file.Name(), ".txt"):
			hasText = true
		case strings.HasSuffix(file.Name(), ".json"):
			hasJson = true
		case strings.HasSuffix(file.Name(), ".log"):
			hasLog = true
		}
	}
	assert.True(t, hasText, "Should have a .txt file")
	assert.True(t, hasJson, "Should have a .json file")
	assert.True(t, hasLog, "Should have a .log file")

	// Read the summary file to verify timeout information is present
	for _, file := range failedFiles {
		if strings.HasSuffix(file.Name(), ".log") {
			logFilePath := filepath.Join(failedDir, file.Name())
			logContent, err := os.ReadFile(logFilePath)
			require.NoError(t, err)
			logContentStr := string(logContent)
			assert.Contains(t, logContentStr, "TIMEOUT ERROR SUMMARY:")
			break
		}
	}

	// Read the plaintext file to verify timeout information is present
	for _, file := range failedFiles {
		if strings.HasSuffix(file.Name(), ".txt") {
			txtFilePath := filepath.Join(failedDir, file.Name())
			txtContent, err := os.ReadFile(txtFilePath)
			require.NoError(t, err)
			txtContentStr := string(txtContent)
			assert.Contains(t, txtContentStr, "*** TIMEOUT ERROR ***")
			assert.Contains(t, txtContentStr, "TIMEOUT: Test timed out after 1s")
			break
		}
	}

}

// TestHTMLSink_TestsWithSubtestsAlwaysDisplayed verifies that tests with subtests are never filtered out
func TestHTMLSink_TestsWithSubtestsAlwaysDisplayed(t *testing.T) {
	// Create a temporary directory
	tmpDir := t.TempDir()
	runID := "test-subtests-display"
	networkName := "test-network"
	gateRun := "test-gate"

	// Create a file logger
	logger, err := NewFileLogger(tmpDir, runID, networkName, gateRun)
	require.NoError(t, err)

	// Simulate the fjord scenario: a test with subtests but minimal metadata
	fjordTest := &types.TestResult{
		Metadata: types.ValidatorMetadata{
			ID:      "fjord-test",
			Package: "github.com/ethereum-optimism/optimism/op-acceptance-tests/tests/fjord",
			Gate:    "holocene",
			Suite:   "",
			// Note: FuncName is empty, simulating the issue
		},
		Status:   types.TestStatusSkip, // Main test skipped
		Duration: 100 * time.Millisecond,
		SubTests: map[string]*types.TestResult{
			"TestFjordOne": {
				Metadata: types.ValidatorMetadata{FuncName: "TestFjordOne"},
				Status:   types.TestStatusSkip,
				Duration: 50 * time.Millisecond,
			},
			"TestFjordTwo": {
				Metadata: types.ValidatorMetadata{FuncName: "TestFjordTwo"},
				Status:   types.TestStatusSkip,
				Duration: 50 * time.Millisecond,
			},
		},
	}

	// Also add a package test in the same package to test filtering doesn't interfere
	packageTest := &types.TestResult{
		Metadata: types.ValidatorMetadata{
			ID:      "all-fjord-tests",
			Package: "github.com/ethereum-optimism/optimism/op-acceptance-tests/tests/fjord",
			Gate:    "holocene",
			Suite:   "",
			RunAll:  true,
		},
		Status:   types.TestStatusSkip,
		Duration: 200 * time.Millisecond,
		SubTests: map[string]*types.TestResult{
			"TestFjordThree": {
				Metadata: types.ValidatorMetadata{FuncName: "TestFjordThree"},
				Status:   types.TestStatusSkip,
				Duration: 100 * time.Millisecond,
			},
		},
	}

	// Log both test results
	require.NoError(t, logger.LogTestResult(fjordTest, runID))
	require.NoError(t, logger.LogTestResult(packageTest, runID))

	// Complete the logging process
	require.NoError(t, logger.Complete(runID))

	// Read the HTML file
	baseDir, err := logger.GetDirectoryForRunID(runID)
	require.NoError(t, err)
	htmlFile := filepath.Join(baseDir, HTMLResultsFilename)

	content, err := os.ReadFile(htmlFile)
	require.NoError(t, err)
	htmlContent := string(content)

	// Verify the fjord test with subtests is displayed (should not be filtered out)
	assert.Contains(t, htmlContent, "fjord-test", "Test with subtests should be displayed even with empty FuncName")

	// Verify the subtests are shown
	assert.Contains(t, htmlContent, "TestFjordOne", "Subtest should be displayed")
	assert.Contains(t, htmlContent, "TestFjordTwo", "Subtest should be displayed")

	// Verify the package test is also shown
	assert.Contains(t, htmlContent, "(package)", "Package test should be displayed")
	assert.Contains(t, htmlContent, "TestFjordThree", "Package test subtest should be displayed")

	// Count total rows - should have: 1 fjord test + 2 fjord subtests + 1 package test + 1 package subtest = 5 rows
	testItemCount := strings.Count(htmlContent, "class=\"test-item")
	assert.Equal(t, 5, testItemCount, "Should have all tests and subtests displayed")
}

// TestExtractErrorInfoFromJSON verifies that error information is correctly extracted from test output
func TestExtractErrorInfoFromJSON(t *testing.T) {
	// Test with mixed content containing error information
	mixedOutput := `Running tests
=== RUN   TestExample
{"Time":"2025-05-09T16:31:48.432668+10:00","Action":"start","Package":"simple"}
{"Time":"2025-05-09T16:31:48.748402+10:00","Action":"run","Package":"simple","Test":"TestExample"}
{"Time":"2025-05-09T16:31:48.748553+10:00","Action":"output","Package":"simple","Test":"TestExample","Output":"=== RUN   TestExample\n"}
{"Time":"2025-05-09T16:31:48.748563+10:00","Action":"output","Package":"simple","Test":"TestExample","Output":"    Error Trace:    /path/to/file.go:123\n"}
{"Time":"2025-05-09T16:31:48.748570+10:00","Action":"output","Package":"simple","Test":"TestExample","Output":"    Error:          Not equal: \n"}
{"Time":"2025-05-09T16:31:48.748571+10:00","Action":"output","Package":"simple","Test":"TestExample","Output":"                    expected: int(42)\n"}
{"Time":"2025-05-09T16:31:48.748572+10:00","Action":"output","Package":"simple","Test":"TestExample","Output":"                    actual  : int(43)\n"}
{"Time":"2025-05-09T16:31:48.748573+10:00","Action":"output","Package":"simple","Test":"TestExample","Output":"    Messages:       Values should match\n"}
{"Time":"2025-05-09T16:31:48.748580+10:00","Action":"fail","Package":"simple","Test":"TestExample","Elapsed":0}`

	// Extract the error information
	errorInfo := extractErrorData(mixedOutput)

	// Verify extracted fields
	assert.Equal(t, "TestExample", errorInfo.TestName)
	assert.Contains(t, errorInfo.ErrorTrace, "/path/to/file.go:123")
	assert.Contains(t, errorInfo.ErrorMessage, "Not equal")

	// The expected and actual values are extracted from different lines,
	// so they need to be checked separately without int(42)/int(43) directly
	assert.NotEmpty(t, errorInfo.Expected)
	assert.NotEmpty(t, errorInfo.Actual)

	// Verify message extraction
	assert.Contains(t, errorInfo.Messages, "Values should match")

	// Test with no error information
	noErrorOutput := `{"Time":"2025-05-09T16:31:48.432668+10:00","Action":"start","Package":"simple"}
{"Time":"2025-05-09T16:31:48.748402+10:00","Action":"run","Package":"simple","Test":"TestPass"}
{"Time":"2025-05-09T16:31:48.748553+10:00","Action":"output","Package":"simple","Test":"TestPass","Output":"=== RUN   TestPass\n"}
{"Time":"2025-05-09T16:31:48.748563+10:00","Action":"output","Package":"simple","Test":"TestPass","Output":"--- PASS: TestPass (0.00s)\n"}
{"Time":"2025-05-09T16:31:48.748570+10:00","Action":"pass","Package":"simple","Test":"TestPass","Elapsed":0}`

	// Extract from passing test with no errors
	passingInfo := extractErrorData(noErrorOutput)

	// Verify minimal information is available
	assert.Equal(t, "TestPass", passingInfo.TestName)
	assert.Empty(t, passingInfo.ErrorMessage)
	assert.Empty(t, passingInfo.Expected)
	assert.Empty(t, passingInfo.Actual)
	assert.Empty(t, passingInfo.Messages)
	assert.Empty(t, passingInfo.ErrorTrace)

	// Test with empty input
	emptyInfo := extractErrorData("")
	assert.Empty(t, emptyInfo.TestName)
	assert.Empty(t, emptyInfo.ErrorMessage)
}
