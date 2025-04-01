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

func TestFileLogger(t *testing.T) {
	// Create a temporary directory for test logs
	tmpDir, err := os.MkdirTemp("", "filelogger_test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Create a new FileLogger with a valid runID
	runID := "test-run-123"
	logger, err := NewFileLogger(tmpDir, runID)
	require.NoError(t, err)

	// Verify the directory structure
	baseDir := logger.GetBaseDir()
	assert.DirExists(t, baseDir)
	assert.DirExists(t, filepath.Join(baseDir, "tests"))
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

	// Verify individual test log files exist
	expectedPassFilename := filepath.Join(baseDir, "tests", "test-gate-test-suite_package_TestPassingFunction.log")
	assert.FileExists(t, expectedPassFilename)

	expectedFailFilename := filepath.Join(baseDir, "tests", "test-gate-test-suite_package_TestFailingFunction.log")
	assert.FileExists(t, expectedFailFilename)

	expectedSkipFilename := filepath.Join(baseDir, "tests", "test-gate-test-suite_package_TestSkippedFunction.log")
	assert.FileExists(t, expectedSkipFilename)

	// Check that a copy of the failing test exists in the failed directory
	expectedFailedCopyFilename := filepath.Join(baseDir, "failed", "test-gate-test-suite_package_TestFailingFunction.log")
	assert.FileExists(t, expectedFailedCopyFilename)

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
	assert.Contains(t, summaryContentStr, "TEST SUMMARY")
	assert.Contains(t, summaryContentStr, "Total:   3")
	assert.Contains(t, summaryContentStr, "Passed:  1")
	assert.Contains(t, summaryContentStr, "Failed:  1")
	assert.Contains(t, summaryContentStr, "Skipped: 1")
	assert.Contains(t, summaryContentStr, "Failed tests:")
	assert.Contains(t, summaryContentStr, "github.com/example/package.TestFailingFunction")
}

func TestLoggerWithEmptyRunID(t *testing.T) {
	// Create a temporary directory for test logs
	tmpDir, err := os.MkdirTemp("", "filelogger_empty_runid_test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Test that an error is returned when an empty runID is provided to NewFileLogger
	_, err = NewFileLogger(tmpDir, "")
	assert.Error(t, err, "Expected error when creating logger with empty runID")
	assert.Contains(t, err.Error(), "runID is required")

	// Create a valid logger to test the LogTestResult with empty runID
	logger, err := NewFileLogger(tmpDir, "valid-run-id")
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
	assert.Contains(t, err.Error(), "runID is required")

	// Test that an error is returned when an empty runID is provided to LogSummary
	err = logger.LogSummary("Summary", "")
	assert.Error(t, err, "Expected error when logging summary with empty runID")
	assert.Contains(t, err.Error(), "runID is required")

	// Test that an error is returned when an empty runID is provided to Complete
	err = logger.Complete("")
	assert.Error(t, err, "Expected error when completing with empty runID")
	assert.Contains(t, err.Error(), "runID is required")
}

func TestLoggingWithRunID(t *testing.T) {
	// Create a temporary directory for test logs
	tmpDir, err := os.MkdirTemp("", "filelogger_runid_test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Create a new FileLogger with a valid runID
	defaultRunID := "default-run-id"
	logger, err := NewFileLogger(tmpDir, defaultRunID)
	require.NoError(t, err)

	// We'll use a different runID for this test
	differentRunID := "different-run-id"

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
	assert.DirExists(t, filepath.Join(runIDDir, "tests"))
	assert.DirExists(t, filepath.Join(runIDDir, "failed"))

	// Verify that the runID is used in the directory name
	expectedDirName := filepath.Join(tmpDir, "testrun-"+differentRunID)
	assert.Equal(t, expectedDirName, runIDDir)

	// Verify the test log file exists in the runID directory
	expectedFilename := filepath.Join(runIDDir, "tests", "test-gate-test-suite_package_TestFunction.log")
	assert.FileExists(t, expectedFilename)

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
	assert.Contains(t, err.Error(), "runID is required")

	_, err = logger.GetFailedDirForRunID("")
	assert.Error(t, err, "Expected error when getting failed directory with empty runID")
	assert.Contains(t, err.Error(), "runID is required")

	_, err = logger.GetSummaryFileForRunID("")
	assert.Error(t, err, "Expected error when getting summary file with empty runID")
	assert.Contains(t, err.Error(), "runID is required")

	_, err = logger.GetAllLogsFileForRunID("")
	assert.Error(t, err, "Expected error when getting all logs file with empty runID")
	assert.Contains(t, err.Error(), "runID is required")
}

func TestAsyncFileWriter(t *testing.T) {
	// Create a temporary directory
	tmpDir, err := os.MkdirTemp("", "asyncfile_test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

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
	defer os.RemoveAll(tmpDir)

	// Create a new FileLogger with a valid runID
	runID := "custom-sink-test"
	logger, err := NewFileLogger(tmpDir, runID)
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
