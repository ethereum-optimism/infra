package logging

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
)

const (
	HTMLResultsTemplate = "results.tmpl.html"
	HTMLResultsFilename = "results.html"
)

// ResultSink is an interface for different ways of consuming test results
type ResultSink interface {
	// Consume processes a single test result
	Consume(result *types.TestResult, runID string) error
	// Complete is called when all results have been consumed
	Complete(runID string) error
}

// FileLogger handles writing test output to files
type FileLogger struct {
	baseDir      string                // Base directory for logs
	logDir       string                // Root log directory
	failedDir    string                // Directory for failed tests
	summaryFile  string                // Path to the summary file
	allLogsFile  string                // Path to the combined log file
	mu           sync.Mutex            // Protects concurrent file operations
	sinks        []ResultSink          // Collection of result consumers
	asyncWriters map[string]*AsyncFile // Map of async file writers
	runID        string                // Current run ID
}

// AsyncFile provides non-blocking file writing capabilities
type AsyncFile struct {
	file    *os.File
	queue   chan []byte
	wg      sync.WaitGroup
	mu      sync.Mutex
	stopped bool
}

// NewAsyncFile creates a new AsyncFile for non-blocking writes
func NewAsyncFile(filepath string) (*AsyncFile, error) {
	file, err := os.Create(filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to create file %s: %w", filepath, err)
	}

	af := &AsyncFile{
		file:  file,
		queue: make(chan []byte, 100), // Buffer channel to reduce blocking
	}

	// Start the background writer
	af.wg.Add(1)
	go af.processQueue()

	return af, nil
}

// Write queues data to be written asynchronously
func (af *AsyncFile) Write(data []byte) error {
	af.mu.Lock()
	defer af.mu.Unlock()

	if af.stopped {
		return fmt.Errorf("async file is closed")
	}

	// Make a copy of the data to avoid race conditions
	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)

	// Send data to the queue
	af.queue <- dataCopy
	return nil
}

// processQueue processes the write queue in the background
func (af *AsyncFile) processQueue() {
	defer af.wg.Done()

	for data := range af.queue {
		_, err := af.file.Write(data)
		if err != nil {
			// Log the error but continue processing
			fmt.Fprintf(os.Stderr, "Error writing to file: %v\n", err)
		}
	}
}

// Close stops the async writer and closes the file
func (af *AsyncFile) Close() error {
	af.mu.Lock()
	if !af.stopped {
		af.stopped = true
		close(af.queue)
	}
	af.mu.Unlock()

	// Wait for all writes to complete
	af.wg.Wait()
	return af.file.Close()
}

// NewFileLogger creates a new file logger with the specified base directory and runID
func NewFileLogger(baseDir string, runID string, networkName, gateRun string) (*FileLogger, error) {
	if runID == "" {
		return nil, fmt.Errorf("runID cannot be empty")
	}

	// Create subdirectory for this specific run
	runDir := filepath.Join(baseDir, fmt.Sprintf("testrun-%s", runID))
	failedDir := filepath.Join(runDir, "failed")
	passedDir := filepath.Join(runDir, "passed")

	// Create directories if they don't exist
	dirs := []string{
		baseDir,
		runDir,
		failedDir,
		passedDir,
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	summaryFile := filepath.Join(runDir, "summary.log")
	allLogsFile := filepath.Join(runDir, "all.log")

	logger := &FileLogger{
		baseDir:      runDir,
		logDir:       baseDir,
		failedDir:    failedDir,
		summaryFile:  summaryFile,
		allLogsFile:  allLogsFile,
		asyncWriters: make(map[string]*AsyncFile),
		runID:        runID,
	}

	// Add sinks - order matters
	logger.sinks = append(logger.sinks, &AllLogsFileSink{logger: logger})
	logger.sinks = append(logger.sinks, &ConciseSummarySink{logger: logger})
	logger.sinks = append(logger.sinks, &RawJSONSink{logger: logger})
	logger.sinks = append(logger.sinks, &PerTestFileSink{
		logger:         logger,
		processedTests: make(map[string]bool),
	})
	logger.sinks = append(logger.sinks, NewHTMLSummarySink(logger, networkName, gateRun))

	return logger, nil
}

// getAsyncWriter gets or creates an AsyncFile for the given path
func (l *FileLogger) getAsyncWriter(path string) (*AsyncFile, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Check if we already have a writer for this path
	if writer, exists := l.asyncWriters[path]; exists {
		return writer, nil
	}

	// Create a new writer
	writer, err := NewAsyncFile(path)
	if err != nil {
		return nil, err
	}

	// Store it for future use
	l.asyncWriters[path] = writer
	return writer, nil
}

// closeAllWriters closes all async writers
func (l *FileLogger) closeAllWriters() {
	l.mu.Lock()
	defer l.mu.Unlock()

	for _, writer := range l.asyncWriters {
		_ = writer.Close() // Ignore errors on close
	}
	l.asyncWriters = make(map[string]*AsyncFile)
}

// GetDirectoryForRunID returns the path for a specific runID
// The runID must be provided, otherwise an error is returned
func (l *FileLogger) GetDirectoryForRunID(runID string) (string, error) {
	if runID == "" {
		return "", fmt.Errorf("runID cannot be empty")
	}
	dirName := fmt.Sprintf("testrun-%s", runID)
	return filepath.Join(l.logDir, dirName), nil
}

// LogTestResult processes a test result through all registered sinks
// If runID is provided, it will log to that specific run directory
func (l *FileLogger) LogTestResult(result *types.TestResult, runID string) error {
	if runID == "" {
		return fmt.Errorf("runID cannot be empty")
	}

	// Feed test result to all sinks
	for _, sink := range l.sinks {
		if err := sink.Consume(result, runID); err != nil {
			return fmt.Errorf("error in sink: %w", err)
		}
	}

	return nil
}

// LogSummary writes a summary of the test run to a file
// The runID must be provided, otherwise an error is returned
func (l *FileLogger) LogSummary(summary string, runID string) error {
	if runID == "" {
		return fmt.Errorf("runID cannot be empty")
	}

	// Get the summary file path for this runID
	summaryFile, err := l.GetSummaryFileForRunID(runID)
	if err != nil {
		return err
	}

	// Get or create the async writer
	writer, err := l.getAsyncWriter(summaryFile)
	if err != nil {
		return err
	}

	// Write the summary
	return writer.Write([]byte(summary))
}

// Complete finalizes all sinks and closes all file writers
func (l *FileLogger) Complete(runID string) error {
	if runID == "" {
		return fmt.Errorf("runID cannot be empty")
	}

	for _, sink := range l.sinks {
		if err := sink.Complete(runID); err != nil {
			return fmt.Errorf("error completing sink: %w", err)
		}
	}

	// Close all writers after completion
	l.closeAllWriters()

	return nil
}

// GetBaseDir returns the base directory for this test run
func (l *FileLogger) GetBaseDir() string {
	return l.baseDir
}

// GetFailedDir returns the directory containing logs for failed tests
func (l *FileLogger) GetFailedDir() string {
	return l.failedDir
}

// GetSummaryFile returns the path to the summary file
func (l *FileLogger) GetSummaryFile() string {
	return l.summaryFile
}

// GetAllLogsFile returns the path to the all logs file
func (l *FileLogger) GetAllLogsFile() string {
	return l.allLogsFile
}

// GetFailedDirForRunID returns the failed directory for a specific runID
// The runID must be provided, otherwise an error is returned
func (l *FileLogger) GetFailedDirForRunID(runID string) (string, error) {
	if runID == "" {
		return "", fmt.Errorf("runID cannot be empty")
	}
	baseDir, err := l.GetDirectoryForRunID(runID)
	if err != nil {
		return "", err
	}
	return filepath.Join(baseDir, "failed"), nil
}

// GetSummaryFileForRunID returns the summary file for a specific runID
// The runID must be provided, otherwise an error is returned
func (l *FileLogger) GetSummaryFileForRunID(runID string) (string, error) {
	if runID == "" {
		return "", fmt.Errorf("runID cannot be empty")
	}
	baseDir, err := l.GetDirectoryForRunID(runID)
	if err != nil {
		return "", err
	}
	return filepath.Join(baseDir, "summary.log"), nil
}

// GetAllLogsFileForRunID returns the path to the all.log file for the given runID
func (l *FileLogger) GetAllLogsFileForRunID(runID string) (string, error) {
	if runID == "" {
		return "", fmt.Errorf("runID cannot be empty")
	}
	baseDir, err := l.GetDirectoryForRunID(runID)
	if err != nil {
		return "", err
	}
	return filepath.Join(baseDir, "all.log"), nil
}

// GetRawEventsFileForRunID returns the path to the raw_go_events.log file for the given runID
func (l *FileLogger) GetRawEventsFileForRunID(runID string) (string, error) {
	if runID == "" {
		return "", fmt.Errorf("runID cannot be empty")
	}
	baseDir, err := l.GetDirectoryForRunID(runID)
	if err != nil {
		return "", err
	}
	return filepath.Join(baseDir, "raw_go_events.log"), nil
}

// GetSinkByType returns a sink of the specified type if it exists
// The type is determined by the name of the sink's struct
func (l *FileLogger) GetSinkByType(sinkType string) (ResultSink, bool) {
	for _, sink := range l.sinks {
		// Get the type name of the sink
		typeName := fmt.Sprintf("%T", sink)
		// Strip package prefix if present
		if idx := strings.LastIndex(typeName, "."); idx >= 0 {
			typeName = typeName[idx+1:]
		}
		// Remove pointer symbol if present
		typeName = strings.TrimPrefix(typeName, "*")

		if typeName == sinkType {
			return sink, true
		}
	}
	return nil, false
}

// Helper functions

// safeFilename converts a string to a safe filename by replacing problematic characters
func safeFilename(s string) string {
	// Replace characters that might be problematic in filenames
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	s = strings.ReplaceAll(s, ":", "_")
	s = strings.ReplaceAll(s, "*", "_")
	s = strings.ReplaceAll(s, "?", "_")
	s = strings.ReplaceAll(s, "\"", "_")
	s = strings.ReplaceAll(s, "<", "_")
	s = strings.ReplaceAll(s, ">", "_")
	s = strings.ReplaceAll(s, "|", "_")
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.ReplaceAll(s, "...", "")
	return s
}

// getReadableTestFilename generates a more user-friendly filename for a test
// It formats package names to be more concise and keeps test names readable
func getReadableTestFilename(metadata types.ValidatorMetadata) string {
	var fileName string

	// Use the function name as the base
	if metadata.FuncName != "" {
		fileName = metadata.FuncName
	} else if metadata.RunAll {
		// For package tests that run all tests
		fileName = "AllTests"
	} else {
		fileName = metadata.ID // Fallback to ID if no function name
	}

	// Extract package basename for cleaner filenames
	pkgName := ""
	if metadata.Package != "" {
		// Handle GitHub-style package paths
		if strings.Contains(metadata.Package, "github.com") {
			// For github.com/org/repo/pkg/subpkg -> extract the last part
			parts := strings.Split(metadata.Package, "/")
			if len(parts) > 0 {
				// Use the last path component but avoid empty strings
				for i := len(parts) - 1; i >= 0; i-- {
					if parts[i] != "" {
						pkgName = parts[i]
						break
					}
				}
			}
		} else {
			// For other package paths, use the same approach
			pkgParts := strings.Split(metadata.Package, "/")
			if len(pkgParts) > 0 {
				// Use the last path component but avoid empty strings
				for i := len(pkgParts) - 1; i >= 0; i-- {
					if pkgParts[i] != "" {
						pkgName = pkgParts[i]
						break
					}
				}
			} else {
				pkgName = metadata.Package
			}
		}
	}

	// Add gate and/or suite for context when present
	prefix := ""
	if metadata.Gate != "" && metadata.Suite != "" {
		prefix = fmt.Sprintf("%s-%s", metadata.Gate, metadata.Suite)
	} else if metadata.Gate != "" {
		prefix = metadata.Gate
	} else if metadata.Suite != "" {
		prefix = metadata.Suite
	}

	// Build the final filename with appropriate components, avoiding duplication
	var nameBuilder strings.Builder

	// Add prefix if it exists and doesn't duplicate the package name
	if prefix != "" && prefix != pkgName {
		nameBuilder.WriteString(prefix)
		nameBuilder.WriteString("_")
	}

	// Add package name if it exists and doesn't duplicate the prefix or function name
	if pkgName != "" && pkgName != prefix && pkgName != fileName {
		nameBuilder.WriteString(pkgName)
		nameBuilder.WriteString("_")
	}

	nameBuilder.WriteString(fileName)

	// Finally ensure the name is safe for a filename
	return safeFilename(nameBuilder.String())
}

// Sink implementations

// AllLogsFileSink writes all test results to a single "all.log" file
type AllLogsFileSink struct {
	logger *FileLogger
}

// Consume writes a test result to the all.log file
func (s *AllLogsFileSink) Consume(result *types.TestResult, runID string) error {
	// Get the all.log file path for this runID
	allLogsFile, err := s.logger.GetAllLogsFileForRunID(runID)
	if err != nil {
		return err
	}

	// Get or create the async writer
	writer, err := s.logger.getAsyncWriter(allLogsFile)
	if err != nil {
		return err
	}

	// Use a cleaner, more structured format that's easier to read for large outputs
	var content strings.Builder

	// Create a clear header with visual distinction
	fmt.Fprintf(&content, "\n")
	fmt.Fprintf(&content, "┌─────────────────────────────────────────────────────────────────────┐\n")
	fmt.Fprintf(&content, "│ TEST: %-64s │\n", truncateString(result.Metadata.FuncName, 64))
	fmt.Fprintf(&content, "├─────────────────────────────────────────────────────────────────────┤\n")

	// Add test metadata in a structured format
	fmt.Fprintf(&content, "│ Status:   %-62s │\n", result.Status)
	fmt.Fprintf(&content, "│ Package:  %-62s │\n", truncateString(result.Metadata.Package, 62))
	fmt.Fprintf(&content, "│ Gate:     %-62s │\n", truncateString(result.Metadata.Gate, 62))
	fmt.Fprintf(&content, "│ Suite:    %-62s │\n", truncateString(result.Metadata.Suite, 62))
	fmt.Fprintf(&content, "│ Duration: %-62s │\n", result.Duration)
	fmt.Fprintf(&content, "│ Time:     %-62s │\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(&content, "└─────────────────────────────────────────────────────────────────────┘\n\n")

	// Add error and stdout in clearly marked sections
	if result.Error != nil {
		fmt.Fprintf(&content, "ERROR:\n")
		fmt.Fprintf(&content, "~~~~~~\n")
		fmt.Fprintf(&content, "%s\n\n", result.Error.Error())
	}

	if result.Stdout != "" {
		fmt.Fprintf(&content, "STDOUT:\n")
		fmt.Fprintf(&content, "~~~~~~~\n")
		// Indent the stdout for better readability
		indentedOutput := indentText(result.Stdout, "  ")
		fmt.Fprintf(&content, "%s\n", indentedOutput)
	}

	// Add a clear separator at the end
	fmt.Fprintf(&content, "\n")

	// Write the content to the file
	return writer.Write([]byte(content.String()))
}

// indentText adds indentation to each line of text for better readability
func indentText(text, indent string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = indent + line
		}
	}
	return strings.Join(lines, "\n")
}

// truncateString truncates a string to the specified max length
// and adds an ellipsis if needed
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// Complete is a no-op for AllLogsFileSink
func (s *AllLogsFileSink) Complete(runID string) error {
	return nil
}

// ConciseSummarySink creates a concise summary in summary.log
type ConciseSummarySink struct {
	logger       *FileLogger
	passed       int
	failed       int
	skipped      int
	errored      int
	timeouts     int // Track timeout failures separately
	failedTests  []string
	timeoutTests []string // Track which tests timed out
	testResults  []*types.TestResult
}

// Consume updates the summary statistics
func (s *ConciseSummarySink) Consume(result *types.TestResult, runID string) error {
	// Store the test result
	s.testResults = append(s.testResults, result)

	// Check if this is a timeout failure
	isTimeout := result.TimedOut
	if isTimeout {
		s.timeouts++
	}

	// Update statistics based on test status
	switch result.Status {
	case types.TestStatusPass:
		s.passed++
	case types.TestStatusFail:
		s.failed++
		// Keep track of failed tests for the summary
		testName := result.Metadata.FuncName
		if result.Metadata.Package != "" {
			testName = fmt.Sprintf("%s.%s", result.Metadata.Package, testName)
		}

		if isTimeout {
			s.failedTests = append(s.failedTests, testName+" (TIMEOUT)")
			s.timeoutTests = append(s.timeoutTests, testName)
		} else {
			s.failedTests = append(s.failedTests, testName)
		}
	case types.TestStatusSkip:
		s.skipped++
	case types.TestStatusError:
		s.errored++
		// Keep track of errored tests too
		testName := result.Metadata.FuncName
		if result.Metadata.Package != "" {
			testName = fmt.Sprintf("%s.%s", result.Metadata.Package, testName)
		}
		s.failedTests = append(s.failedTests, testName+" (ERROR)")
	}

	// Also check subtests for timeouts
	if len(result.SubTests) > 0 {
		for subTestName, subTest := range result.SubTests {
			if subTest.TimedOut {
				s.timeouts++
				fullSubTestName := fmt.Sprintf("%s/%s", result.Metadata.FuncName, subTestName)
				if result.Metadata.Package != "" {
					fullSubTestName = fmt.Sprintf("%s.%s", result.Metadata.Package, fullSubTestName)
				}
				s.timeoutTests = append(s.timeoutTests, fullSubTestName)

				// Add to failed tests if not already there
				found := false
				for _, existing := range s.failedTests {
					if strings.Contains(existing, fullSubTestName) {
						found = true
						break
					}
				}
				if !found {
					s.failedTests = append(s.failedTests, fullSubTestName+" (SUBTEST TIMEOUT)")
				}
			}
		}
	}

	return nil
}

// Complete generates and writes the final summary
func (s *ConciseSummarySink) Complete(runID string) error {
	// Get the summary file path for this runID
	summaryFile, err := s.logger.GetSummaryFileForRunID(runID)
	if err != nil {
		return err
	}

	// Get or create the async writer
	writer, err := s.logger.getAsyncWriter(summaryFile)
	if err != nil {
		return err
	}

	// Calculate totals and duration
	total := s.passed + s.failed + s.skipped + s.errored

	// Calculate total duration from sum of all test durations
	var totalDuration time.Duration
	for _, result := range s.testResults {
		totalDuration += result.Duration
	}

	// Build the concise summary
	var summary strings.Builder
	fmt.Fprintf(&summary, "TEST SUMMARY\n")
	fmt.Fprintf(&summary, "============\n")
	fmt.Fprintf(&summary, "Run ID: %s\n", runID)
	fmt.Fprintf(&summary, "Time: %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(&summary, "Duration: %s\n\n", totalDuration)

	// Add timeout warning if there were any timeouts
	if s.timeouts > 0 {
		fmt.Fprintf(&summary, "⚠️  WARNING: %d TEST(S) TIMED OUT! ⚠️\n\n", s.timeouts)
	}

	fmt.Fprintf(&summary, "Results:\n")
	fmt.Fprintf(&summary, "  Total:   %d\n", total)
	fmt.Fprintf(&summary, "  Passed:  %d\n", s.passed)
	fmt.Fprintf(&summary, "  Failed:  %d\n", s.failed)
	fmt.Fprintf(&summary, "  Skipped: %d\n", s.skipped)
	fmt.Fprintf(&summary, "  Errors:  %d\n", s.errored)
	if s.timeouts > 0 {
		fmt.Fprintf(&summary, "  Timeouts: %d\n", s.timeouts)
	}
	fmt.Fprintf(&summary, "\n")

	// Add timeout information prominently if there were timeouts
	if len(s.timeoutTests) > 0 {
		fmt.Fprintf(&summary, "TIMED OUT TESTS:\n")
		fmt.Fprintf(&summary, "================\n")
		for _, test := range s.timeoutTests {
			fmt.Fprintf(&summary, "  ⏰ %s\n", test)
		}
		fmt.Fprintf(&summary, "\n")
	}

	// Include a list of failed tests if there are any
	if len(s.failedTests) > 0 {
		fmt.Fprintf(&summary, "Failed tests:\n")
		for _, test := range s.failedTests {
			fmt.Fprintf(&summary, "  - %s\n", test)
		}
		fmt.Fprintf(&summary, "\n")
	}

	// Add location of the detailed logs
	fmt.Fprintf(&summary, "Full details: see %s\n", s.logger.GetAllLogsFile())

	// Write the summary
	return writer.Write([]byte(summary.String()))
}

// GetRunID returns the current runID
func (l *FileLogger) GetRunID() string {
	return l.runID
}

// PerTestFileSink creates dedicated log files for each test in passed/failed directories
// containing the complete go test output that would be shown by `go test`
type PerTestFileSink struct {
	logger         *FileLogger
	processedTests map[string]bool // Track which test files we've already written
	mu             sync.Mutex      // Protect the processedTests map
}

// Consume writes a complete test result to a dedicated file in the passed or failed directory
func (s *PerTestFileSink) Consume(result *types.TestResult, runID string) error {
	// Use the specified runID directory
	baseDir, err := s.logger.GetDirectoryForRunID(runID)
	if err != nil {
		return err
	}

	// Create passed and failed directories if they don't exist
	passedDir := filepath.Join(baseDir, "passed")
	failedDir := filepath.Join(baseDir, "failed")

	dirs := []string{
		baseDir,
		passedDir,
		failedDir,
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	// Create log file for the main test
	err = s.createTestLogFileOnce(result, passedDir, failedDir, runID)
	if err != nil {
		return fmt.Errorf("failed to create main test log file: %w", err)
	}

	// Create individual log files for each subtest
	for subTestName, subTest := range result.SubTests {
		// Create a copy of the subtest with proper metadata for filename generation
		subTestResult := &types.TestResult{
			Metadata: types.ValidatorMetadata{
				ID:       subTest.Metadata.ID,
				Gate:     result.Metadata.Gate,    // Use parent's gate
				Suite:    result.Metadata.Suite,   // Use parent's suite
				FuncName: subTestName,             // Use the subtest name
				Package:  result.Metadata.Package, // Use parent's package
				RunAll:   false,
			},
			Status:   subTest.Status,
			Error:    subTest.Error,
			Duration: subTest.Duration,
			Stdout:   subTest.Stdout,
		}

		err = s.createTestLogFileOnce(subTestResult, passedDir, failedDir, runID)
		if err != nil {
			return fmt.Errorf("failed to create subtest log file for %s: %w", subTestName, err)
		}
	}

	return nil
}

// createTestLogFileOnce creates a log file for a single test result, but only once per unique file path
func (s *PerTestFileSink) createTestLogFileOnce(result *types.TestResult, passedDir, failedDir string, runID string) error {
	// Generate a safe filename based on the test metadata
	filename := getReadableTestFilename(result.Metadata)

	// Determine which directory to use based on test status
	var targetDir string
	if result.Status == types.TestStatusFail || result.Status == types.TestStatusError {
		targetDir = failedDir
	} else {
		targetDir = passedDir
	}

	// Full path to the test log file
	testFilePath := filepath.Join(targetDir, filename+".log")

	// Check if we've already processed this test file
	s.mu.Lock()
	if s.processedTests[testFilePath] {
		s.mu.Unlock()
		return nil // Already processed, skip
	}
	s.processedTests[testFilePath] = true
	s.mu.Unlock()

	// Now create the test log file
	return s.createTestLogFile(result, passedDir, failedDir, runID)
}

// createTestLogFile creates a log file for a single test result
func (s *PerTestFileSink) createTestLogFile(result *types.TestResult, passedDir, failedDir string, runID string) error {
	// Generate a safe filename based on the test metadata
	filename := getReadableTestFilename(result.Metadata)

	// Determine which directory to use based on test status
	var targetDir string
	if result.Status == types.TestStatusFail || result.Status == types.TestStatusError {
		targetDir = failedDir
	} else {
		targetDir = passedDir
	}

	// Full path to the test log file
	testFilePath := filepath.Join(targetDir, filename+".log")

	// Get or create the async writer
	writer, err := s.logger.getAsyncWriter(testFilePath)
	if err != nil {
		return err
	}

	// Check if this is a timeout failure for special handling
	isTimeout := result.TimedOut

	// Build error summary header
	var content strings.Builder

	// Check if this is a timeout failure
	if result.Status == types.TestStatusFail || result.Status == types.TestStatusError {
		fmt.Fprintf(&content, "\n%s\n", strings.Repeat("-", 80))
		if isTimeout {
			fmt.Fprintf(&content, "TIMEOUT ERROR SUMMARY:\n")
			fmt.Fprintf(&content, "======================\n\n")
			fmt.Fprintf(&content, "This test failed due to timeout!\n")
			fmt.Fprintf(&content, "Timeout Duration: %v\n", result.Metadata.Timeout)
			fmt.Fprintf(&content, "Error: %s\n\n", result.Error.Error())
		} else {
			fmt.Fprintf(&content, "ERROR SUMMARY:\n")
			fmt.Fprintf(&content, "=============\n\n")
		}
	}

	// Extract the plaintext output first from all JSON Output fields
	var plaintext strings.Builder
	if result.Stdout != "" {
		parser := NewJSONOutputParser(result.Stdout)
		parser.ProcessJSONOutput(func(_ map[string]interface{}, outputText string) {
			plaintext.WriteString(outputText)
		})
	}

	// 1. Write the plaintext output first, with timeout information if applicable
	fmt.Fprintf(&content, "PLAINTEXT OUTPUT:\n")
	fmt.Fprintf(&content, "================\n\n")

	// For timeout cases, prominently display the timeout error at the beginning of plaintext output
	if isTimeout {
		fmt.Fprintf(&content, "*** TIMEOUT ERROR ***\n")
		fmt.Fprintf(&content, "%s\n", result.Error.Error())
		fmt.Fprintf(&content, "*** END TIMEOUT ERROR ***\n\n")

		if plaintext.Len() > 0 {
			fmt.Fprintf(&content, "PARTIAL OUTPUT BEFORE TIMEOUT:\n")
			fmt.Fprintf(&content, "------------------------------\n")
			fmt.Fprintf(&content, "%s\n", plaintext.String())
		} else {
			fmt.Fprintf(&content, "No output captured before timeout occurred.\n")
		}
	} else {
		// For non-timeout cases, show regular output
		if plaintext.Len() > 0 {
			fmt.Fprintf(&content, "%s\n", plaintext.String())
		} else {
			fmt.Fprintf(&content, "No output captured.\n")
		}
	}

	// 2. Add a clear separator between plaintext and JSON
	fmt.Fprintf(&content, "\n%s\n", strings.Repeat("-", 80))
	fmt.Fprintf(&content, "JSON OUTPUT:\n")
	fmt.Fprintf(&content, "============\n\n")

	// 3. Include the raw JSON output for full debug information
	if result.Stdout != "" {
		if isTimeout {
			fmt.Fprintf(&content, "PARTIAL JSON OUTPUT (BEFORE TIMEOUT):\n")
			fmt.Fprintf(&content, "-------------------------------------\n")
		}
		fmt.Fprintf(&content, "%s\n", result.Stdout)
	} else if isTimeout {
		fmt.Fprintf(&content, "No JSON output captured before timeout.\n")
		// Include our timeout marker if we stored one
		fmt.Fprintf(&content, "\nTimeout marker that would be stored:\n")
		fmt.Fprintf(&content, `{"Time":"%s","Action":"timeout","Package":"%s","Test":"%s","Output":"TEST TIMED OUT - no JSON output captured\n"}`,
			time.Now().Format(time.RFC3339), result.Metadata.Package, result.Metadata.FuncName)
		fmt.Fprintf(&content, "\n")
	} else {
		fmt.Fprintf(&content, "No JSON output available.\n")
	}

	// 4. Add a separator before the error summary section
	if result.Status == types.TestStatusFail || result.Status == types.TestStatusError {
		// Extract critical error information from non-timeout errors
		if !isTimeout {
			errorInfo := extractErrorData(result.Stdout)

			if errorInfo.TestName != "" {
				fmt.Fprintf(&content, "Test:       %s\n", errorInfo.TestName)
			}

			if errorInfo.ErrorMessage != "" {
				fmt.Fprintf(&content, "Error:      %s\n", errorInfo.ErrorMessage)
			}

			if errorInfo.Expected != "" && errorInfo.Actual != "" {
				fmt.Fprintf(&content, "Expected:   %s\n", errorInfo.Expected)
				fmt.Fprintf(&content, "Actual:     %s\n", errorInfo.Actual)
			}

			if errorInfo.Messages != "" {
				fmt.Fprintf(&content, "Message:    %s\n", errorInfo.Messages)
			}

			if errorInfo.ErrorTrace != "" {
				fmt.Fprintf(&content, "\nError Trace:\n%s\n", errorInfo.ErrorTrace)
			}
		}
	} else {
		// For passed tests, a simpler summary at the end
		fmt.Fprintf(&content, "\n%s\n", strings.Repeat("-", 80))
		fmt.Fprintf(&content, "RESULT SUMMARY:\n")
		fmt.Fprintf(&content, "===============\n\n")
		fmt.Fprintf(&content, "Test passed: %s\n", result.Metadata.FuncName)
		fmt.Fprintf(&content, "Duration:    %s\n", formatDuration(result.Duration))
	}

	// Write the content to the file
	return writer.Write([]byte(content.String()))
}

// JSONOutputParser processes 'go test' JSON test output streams, converting them into structured data
type JSONOutputParser struct {
	reader io.Reader
}

// NewJSONOutputParser creates a new JSON parser from a string input
func NewJSONOutputParser(input string) *JSONOutputParser {
	return &JSONOutputParser{
		reader: strings.NewReader(input),
	}
}

// NewJSONOutputParserFromReader creates a new JSON parser from an io.Reader
func NewJSONOutputParserFromReader(reader io.Reader) *JSONOutputParser {
	return &JSONOutputParser{
		reader: reader,
	}
}

// ProcessJSONOutput processes JSON output by applying the provided handler to each output line
// The handler is called for each JSON line that has an "output" action
func (p *JSONOutputParser) ProcessJSONOutput(handler func(jsonData map[string]interface{}, outputText string)) {
	scanner := bufio.NewScanner(p.reader)
	for scanner.Scan() {
		line := scanner.Text()

		// Skip empty lines
		if strings.TrimSpace(line) == "" {
			continue
		}

		// Only process JSON lines
		if !strings.HasPrefix(strings.TrimSpace(line), "{") || !strings.HasSuffix(strings.TrimSpace(line), "}") {
			continue
		}

		// Try to parse as JSON
		var jsonData map[string]interface{}
		if err := json.Unmarshal([]byte(line), &jsonData); err != nil {
			continue
		}

		// Extract information only from output actions
		action, ok := jsonData["Action"].(string)
		if !ok || action != "output" {
			continue
		}

		// Extract the output text
		outputText, ok := jsonData["Output"].(string)
		if !ok || outputText == "" {
			continue
		}

		// Call the handler with the JSON data and output text
		handler(jsonData, outputText)
	}
}

// GetOutputAsString extracts and concatenates all "Output" fields from JSON
// and returns them as a single string
func (p *JSONOutputParser) GetOutputAsString() string {
	var outputBuilder strings.Builder
	p.ProcessJSONOutput(func(_ map[string]interface{}, outputText string) {
		outputBuilder.WriteString(outputText)
	})
	return outputBuilder.String()
}

// ErrorInfo holds extracted error information from test output
type ErrorInfo struct {
	TestName     string
	ErrorMessage string
	Expected     string
	Actual       string
	Messages     string
	ErrorTrace   string
}

// GetErrorInfo parses the JSON output to extract error information
func (p *JSONOutputParser) GetErrorInfo() ErrorInfo {
	var info ErrorInfo

	p.ProcessJSONOutput(func(jsonData map[string]interface{}, outputText string) {
		// Extract test name
		if testName, ok := jsonData["Test"].(string); ok && testName != "" {
			info.TestName = testName
		}

		// Extract error trace
		if strings.Contains(outputText, "Error Trace:") {
			parts := strings.Split(outputText, "Error Trace:")
			if len(parts) > 1 {
				trace := strings.TrimSpace(parts[1])
				endIdx := strings.Index(trace, "Error:")
				if endIdx > 0 {
					info.ErrorTrace = trace[:endIdx]
				} else {
					info.ErrorTrace = trace
				}
			}
		}

		// Extract error message
		if strings.Contains(outputText, "Error:") {
			parts := strings.Split(outputText, "Error:")
			if len(parts) > 1 {
				errorLine := strings.TrimSpace(parts[1])
				endIdx := strings.Index(errorLine, "\n")
				if endIdx > 0 {
					info.ErrorMessage = errorLine[:endIdx]
				} else {
					info.ErrorMessage = errorLine
				}
			}
		}

		// Extract expected value
		if strings.Contains(outputText, "expected:") {
			parts := strings.Split(outputText, "expected:")
			if len(parts) > 1 {
				expectedLine := strings.TrimSpace(parts[1])
				endIdx := strings.Index(expectedLine, "\n")
				if endIdx > 0 {
					info.Expected = expectedLine[:endIdx]
				} else {
					info.Expected = expectedLine
				}
			}
		}

		// Extract actual value
		if strings.Contains(outputText, "actual") {
			parts := strings.Split(outputText, "actual")
			if len(parts) > 1 {
				actualLine := strings.TrimSpace(parts[1])
				endIdx := strings.Index(actualLine, "\n")
				if endIdx > 0 {
					info.Actual = actualLine[:endIdx]
				} else {
					info.Actual = actualLine
				}
			}
		}

		// Extract messages
		if strings.Contains(outputText, "Messages:") {
			parts := strings.Split(outputText, "Messages:")
			if len(parts) > 1 {
				message := strings.TrimSpace(parts[1])
				endIdx := strings.Index(message, "\n")
				if endIdx > 0 {
					info.Messages += message[:endIdx] + "\n"
				} else {
					info.Messages += message + "\n"
				}
			}
		}
	})

	return info
}

// Helper functions for backward compatibility or convenience

// extractPlainText returns all output text from JSON as a string
func extractPlainText(input string) string {
	parser := NewJSONOutputParser(input)
	return parser.GetOutputAsString()
}

// extractErrorData extracts error information from JSON output
func extractErrorData(input string) ErrorInfo {
	if input == "" {
		return ErrorInfo{}
	}
	parser := NewJSONOutputParser(input)
	return parser.GetErrorInfo()
}

// Complete is a no-op for PerTestFileSink
func (s *PerTestFileSink) Complete(runID string) error {
	return nil
}

// TestResultRow represents a row in the HTML test results table
type TestResultRow struct {
	StatusClass       string
	StatusText        string
	TestName          string
	Package           string
	Gate              string
	Suite             string
	DurationFormatted string
	LogPath           string
	IsSubTest         bool   // Whether this is a subtest
	ParentTest        string // Name of the parent test for subtests
}

// HTMLSummaryData contains all the data needed for the HTML template
type HTMLSummaryData struct {
	RunID             string
	Time              string
	TotalDuration     string
	Total             int
	Passed            int
	Failed            int
	Skipped           int
	Errored           int
	PassRateFormatted string
	HasFailures       bool
	Tests             []TestResultRow
	DevnetName        string // Name of the devnet being tested
	GateRun           string // Name of the gate being run
}

// HTMLSummarySink creates an HTML report for better visualization of test results
type HTMLSummarySink struct {
	logger      *FileLogger
	passed      int
	failed      int
	skipped     int
	errored     int
	testResults []*types.TestResult
	networkName string // Name of the network being tested
	gateRun     string // Name of the gate being run
}

// NewHTMLSummarySink creates a new HTMLSummarySink with network and gate information
func NewHTMLSummarySink(logger *FileLogger, networkName, gateRun string) *HTMLSummarySink {
	return &HTMLSummarySink{
		logger:      logger,
		testResults: make([]*types.TestResult, 0),
		networkName: networkName,
		gateRun:     gateRun,
	}
}

// Consume collects test results for later HTML generation
func (s *HTMLSummarySink) Consume(result *types.TestResult, runID string) error {
	// Store the test result for later processing
	s.testResults = append(s.testResults, result)

	// Update statistics based on test status
	switch result.Status {
	case types.TestStatusPass:
		s.passed++
	case types.TestStatusFail:
		s.failed++
	case types.TestStatusSkip:
		s.skipped++
	case types.TestStatusError:
		s.errored++
	}

	// Update statistics for subtests
	for _, subTest := range result.SubTests {
		switch subTest.Status {
		case types.TestStatusPass:
			s.passed++
		case types.TestStatusFail:
			s.failed++
		case types.TestStatusSkip:
			s.skipped++
		case types.TestStatusError:
			s.errored++
		}
	}

	return nil
}

// Complete generates the HTML summary file
func (s *HTMLSummarySink) Complete(runID string) error {
	// Get the base directory for this runID
	baseDir, err := s.logger.GetDirectoryForRunID(runID)
	if err != nil {
		return err
	}

	// Calculate totals and duration
	total := s.passed + s.failed + s.skipped + s.errored

	// Calculate total duration from sum of all test durations
	var totalDuration time.Duration
	for _, result := range s.testResults {
		totalDuration += result.Duration
		// Add subtest durations
		for _, subTest := range result.SubTests {
			totalDuration += subTest.Duration
		}
	}

	// Calculate pass rate
	passRate := 0.0
	if total > 0 {
		passRate = float64(s.passed) / float64(total) * 100
	}

	// Prepare the test result rows (including subtests)
	// First, identify package tests and their subtests
	packageTestsWithSubtests := make(map[string]map[string]bool) // package -> subtest names from package tests
	packageTests := make([]*types.TestResult, 0)
	individualTests := make([]*types.TestResult, 0)

	for _, result := range s.testResults {
		if result.Metadata.RunAll && len(result.SubTests) > 0 {
			// This is a package test with subtests
			packageTests = append(packageTests, result)
			packageName := result.Metadata.Package
			if packageTestsWithSubtests[packageName] == nil {
				packageTestsWithSubtests[packageName] = make(map[string]bool)
			}
			for subTestName := range result.SubTests {
				packageTestsWithSubtests[packageName][subTestName] = true
			}
		} else {
			// This is an individual test (including regular tests with subtests)
			individualTests = append(individualTests, result)
		}
	}

	// Filter out individual tests that are duplicated as subtests in package tests in the same package
	// BUT never filter out tests that have their own subtests
	filteredIndividualTests := make([]*types.TestResult, 0)
	for _, result := range individualTests {
		// Never filter out tests that have their own subtests - they should always be displayed
		if len(result.SubTests) > 0 {
			filteredIndividualTests = append(filteredIndividualTests, result)
			continue
		}

		// For tests without subtests, check if they're duplicated as subtests in package tests
		testName := result.Metadata.FuncName
		if testName == "" {
			testName = result.Metadata.ID
		}
		packageName := result.Metadata.Package

		// Only filter out if this test name exists as a subtest in a package test in the same package
		// AND this test doesn't have its own subtests
		if testName != "" && packageName != "" {
			if subtestsInSamePackage, exists := packageTestsWithSubtests[packageName]; exists && subtestsInSamePackage[testName] {
				// This individual test is duplicated as a subtest in a package test - filter it out
				continue
			}
		}

		// Keep this individual test
		filteredIndividualTests = append(filteredIndividualTests, result)
	}

	tests := make([]TestResultRow, 0)

	// Add filtered individual tests (including their subtests if any)
	for _, result := range filteredIndividualTests {
		mainTestRow := s.createTestResultRow(result, false, "")
		tests = append(tests, mainTestRow)

		// Add subtests for individual tests
		for subTestName, subTest := range result.SubTests {
			subTestResult := &types.TestResult{
				Metadata: types.ValidatorMetadata{
					ID:       subTest.Metadata.ID,
					Gate:     result.Metadata.Gate,
					Suite:    result.Metadata.Suite,
					FuncName: subTestName,
					Package:  result.Metadata.Package,
					RunAll:   false,
				},
				Status:   subTest.Status,
				Error:    subTest.Error,
				Duration: subTest.Duration,
				Stdout:   subTest.Stdout,
			}
			subTestRow := s.createTestResultRow(subTestResult, true, result.Metadata.FuncName)
			tests = append(tests, subTestRow)
		}
	}

	// Add package tests and their subtests
	for _, result := range packageTests {
		// Add the main package test row
		mainTestRow := s.createTestResultRow(result, false, "")
		tests = append(tests, mainTestRow)

		// Add subtest rows
		for subTestName, subTest := range result.SubTests {
			// Create a subtest result with proper metadata for filename generation
			subTestResult := &types.TestResult{
				Metadata: types.ValidatorMetadata{
					ID:       subTest.Metadata.ID,
					Gate:     result.Metadata.Gate,    // Use parent's gate
					Suite:    result.Metadata.Suite,   // Use parent's suite
					FuncName: subTestName,             // Use the subtest name
					Package:  result.Metadata.Package, // Use parent's package
					RunAll:   false,
				},
				Status:   subTest.Status,
				Error:    subTest.Error,
				Duration: subTest.Duration,
				Stdout:   subTest.Stdout,
			}

			subTestRow := s.createTestResultRow(subTestResult, true, result.Metadata.FuncName)
			// The test name is already set correctly from the metadata
			tests = append(tests, subTestRow)
		}
	}

	// Prepare the template data
	data := HTMLSummaryData{
		RunID:             runID,
		Time:              time.Now().Format(time.RFC3339),
		TotalDuration:     formatDuration(totalDuration),
		Total:             total,
		Passed:            s.passed,
		Failed:            s.failed,
		Skipped:           s.skipped,
		Errored:           s.errored,
		PassRateFormatted: fmt.Sprintf("%.1f", passRate),
		HasFailures:       s.failed+s.errored > 0,
		Tests:             tests,
		DevnetName:        s.networkName,
		GateRun:           s.gateRun,
	}

	// Parse the template
	tmpl, err := GetHTMLTemplate(HTMLResultsTemplate)
	if err != nil {
		return fmt.Errorf("failed to parse HTML template: %w", err)
	}

	// Execute the template
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("failed to execute HTML template: %w", err)
	}

	// Create the HTML report filepath
	htmlFile := filepath.Join(baseDir, HTMLResultsFilename)

	// Get or create the async writer
	writer, err := s.logger.getAsyncWriter(htmlFile)
	if err != nil {
		return err
	}
	defer writer.Close()

	// Write the HTML content
	return writer.Write(buf.Bytes())
}

// createTestResultRow creates a TestResultRow from a TestResult
func (s *HTMLSummarySink) createTestResultRow(result *types.TestResult, isSubTest bool, parentTest string) TestResultRow {
	// Determine status class
	statusClass := ""
	statusText := ""
	switch result.Status {
	case types.TestStatusPass:
		statusClass = "pass"
		statusText = "PASS"
	case types.TestStatusFail:
		statusClass = "fail"
		statusText = "FAIL"
	case types.TestStatusSkip:
		statusClass = "skip"
		statusText = "SKIP"
	case types.TestStatusError:
		statusClass = "error"
		statusText = "ERROR"
	}

	// Generate filename using the test metadata
	filename := getReadableTestFilename(result.Metadata) + ".log"
	logPath := ""
	if result.Status == types.TestStatusFail || result.Status == types.TestStatusError {
		logPath = "failed/" + filename
	} else {
		logPath = "passed/" + filename
	}

	testName := result.Metadata.FuncName
	if testName == "" {
		if result.Metadata.RunAll {
			testName = "AllTests"
		} else {
			testName = result.Metadata.ID
		}
	}

	// If still empty, use package name as fallback for better visibility
	if testName == "" {
		if result.Metadata.Package != "" {
			// Extract package basename for display
			parts := strings.Split(result.Metadata.Package, "/")
			if len(parts) > 0 {
				testName = parts[len(parts)-1] + " (package)"
			} else {
				testName = result.Metadata.Package + " (package)"
			}
		} else {
			testName = "Unknown Test"
		}
	}

	return TestResultRow{
		StatusClass:       statusClass,
		StatusText:        statusText,
		TestName:          testName,
		Package:           result.Metadata.Package,
		Gate:              result.Metadata.Gate,
		Suite:             result.Metadata.Suite,
		DurationFormatted: formatDuration(result.Duration),
		LogPath:           logPath,
		IsSubTest:         isSubTest,
		ParentTest:        parentTest,
	}
}

// formatDuration formats a time.Duration to a human-readable string
// Extracted from the existing formatDuration function
func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%.2fms", float64(d.Milliseconds()))
	}
	return fmt.Sprintf("%.2fs", d.Seconds())
}
