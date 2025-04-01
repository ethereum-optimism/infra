package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
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
	resultBuffer []*types.TestResult   // Buffer for storing results
	asyncWriters map[string]*AsyncFile // Map of async file writers
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
// The runID must be provided, otherwise an error is returned
func NewFileLogger(baseDir string, runID string) (*FileLogger, error) {
	if runID == "" {
		return nil, fmt.Errorf("runID is required")
	}

	dirName := fmt.Sprintf("testrun-%s", runID)
	runDir := filepath.Join(baseDir, dirName)
	failDir := filepath.Join(runDir, "failed")

	// Create directories
	dirs := []string{
		runDir,
		filepath.Join(runDir, "tests"),
		failDir,
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	logger := &FileLogger{
		baseDir:      runDir,
		logDir:       baseDir,
		failedDir:    failDir,
		summaryFile:  filepath.Join(runDir, "summary.log"),
		allLogsFile:  filepath.Join(runDir, "all.log"),
		asyncWriters: make(map[string]*AsyncFile),
		resultBuffer: make([]*types.TestResult, 0),
	}

	// Add default sinks
	logger.sinks = append(logger.sinks, &IndividualTestFileSink{logger: logger})
	logger.sinks = append(logger.sinks, &AllLogsFileSink{logger: logger})
	logger.sinks = append(logger.sinks, &ConciseSummarySink{logger: logger})

	return logger, nil
}

// getAsyncWriter gets or creates an AsyncFile for the given path
func (l *FileLogger) getAsyncWriter(path string) (*AsyncFile, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Check if we already have a writer for this path
	if writer, ok := l.asyncWriters[path]; ok {
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
		return "", fmt.Errorf("runID is required")
	}
	return filepath.Join(l.logDir, fmt.Sprintf("testrun-%s", runID)), nil
}

// LogTestResult processes a test result through all registered sinks
// If runID is provided, it will log to that specific run directory
func (l *FileLogger) LogTestResult(result *types.TestResult, runID string) error {
	if runID == "" {
		return fmt.Errorf("runID is required")
	}

	// Store the result in the buffer
	l.mu.Lock()
	l.resultBuffer = append(l.resultBuffer, result)
	l.mu.Unlock()

	// Process the result through all sinks
	for _, sink := range l.sinks {
		if err := sink.Consume(result, runID); err != nil {
			return err
		}
	}

	return nil
}

// LogSummary writes a summary of the test run to a file
// The runID must be provided, otherwise an error is returned
func (l *FileLogger) LogSummary(summary string, runID string) error {
	if runID == "" {
		return fmt.Errorf("runID is required")
	}

	// Use the specified runID directory
	baseDir, err := l.GetDirectoryForRunID(runID)
	if err != nil {
		return err
	}

	// Create the directory if it doesn't exist
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", baseDir, err)
	}

	// Get the async writer for the summary file
	summaryFile := filepath.Join(baseDir, "summary.log")
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
		return fmt.Errorf("runID is required")
	}

	// Notify all sinks that we're done
	for _, sink := range l.sinks {
		if err := sink.Complete(runID); err != nil {
			return err
		}
	}

	// Close all async writers
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
		return "", fmt.Errorf("runID is required")
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
		return "", fmt.Errorf("runID is required")
	}
	baseDir, err := l.GetDirectoryForRunID(runID)
	if err != nil {
		return "", err
	}
	return filepath.Join(baseDir, "summary.log"), nil
}

// GetAllLogsFileForRunID returns the all logs file for a specific runID
// The runID must be provided, otherwise an error is returned
func (l *FileLogger) GetAllLogsFileForRunID(runID string) (string, error) {
	if runID == "" {
		return "", fmt.Errorf("runID is required")
	}
	baseDir, err := l.GetDirectoryForRunID(runID)
	if err != nil {
		return "", err
	}
	return filepath.Join(baseDir, "all.log"), nil
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

	// Build the final filename with appropriate components
	var nameBuilder strings.Builder

	if prefix != "" {
		nameBuilder.WriteString(prefix)
		nameBuilder.WriteString("_")
	}

	if pkgName != "" {
		nameBuilder.WriteString(pkgName)
		nameBuilder.WriteString("_")
	}

	nameBuilder.WriteString(fileName)

	// Finally ensure the name is safe for a filename
	return safeFilename(nameBuilder.String())
}

// Sink implementations

// IndividualTestFileSink writes each test result to its own file
type IndividualTestFileSink struct {
	logger *FileLogger
}

// Consume writes a test result to a dedicated file
func (s *IndividualTestFileSink) Consume(result *types.TestResult, runID string) error {
	// Use the specified runID directory
	baseDir, err := s.logger.GetDirectoryForRunID(runID)
	if err != nil {
		return err
	}
	failedDir := filepath.Join(baseDir, "failed")

	// Create directories if they don't exist
	dirs := []string{
		baseDir,
		filepath.Join(baseDir, "tests"),
		failedDir,
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	// Generate a safe filename based on the test metadata
	filename := getReadableTestFilename(result.Metadata)

	// Full path to the test log file
	testFilePath := filepath.Join(baseDir, "tests", filename+".log")

	// Get or create the async writer
	writer, err := s.logger.getAsyncWriter(testFilePath)
	if err != nil {
		return err
	}

	// Prepare the log content
	var content strings.Builder
	fmt.Fprintf(&content, "Test: %s\n", result.Metadata.FuncName)
	fmt.Fprintf(&content, "Package: %s\n", result.Metadata.Package)
	fmt.Fprintf(&content, "Status: %s\n", result.Status)
	fmt.Fprintf(&content, "Duration: %s\n", result.Duration)
	fmt.Fprintf(&content, "Gate: %s\n", result.Metadata.Gate)
	fmt.Fprintf(&content, "Suite: %s\n", result.Metadata.Suite)
	fmt.Fprintf(&content, "---------------------------------------------------\n\n")

	// Write error if any
	if result.Error != nil {
		fmt.Fprintf(&content, "ERROR: %s\n\n", result.Error.Error())
	}

	// Write stdout/stderr output
	if result.Stdout != "" {
		fmt.Fprintf(&content, "STDOUT:\n%s\n", result.Stdout)
	}

	// Write the content to the file
	if err := writer.Write([]byte(content.String())); err != nil {
		return err
	}

	// If test failed, create a link in the failed directory
	if result.Status == types.TestStatusFail {
		failedFilePath := filepath.Join(failedDir, filename+".log")

		// For failed tests, we'll create the file directly for simplicity
		// instead of using a hard link which might not work across different filesystems
		if err := os.WriteFile(failedFilePath, []byte(content.String()), 0644); err != nil {
			return fmt.Errorf("failed to write failed test log: %w", err)
		}
	}

	return nil
}

// Complete is a no-op for IndividualTestFileSink
func (s *IndividualTestFileSink) Complete(runID string) error {
	return nil
}

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
	logger      *FileLogger
	passed      int
	failed      int
	skipped     int
	errored     int
	startTime   time.Time
	failedTests []string
}

// Consume updates the summary statistics
func (s *ConciseSummarySink) Consume(result *types.TestResult, runID string) error {
	// Initialize start time if this is the first result
	if s.startTime.IsZero() {
		s.startTime = time.Now()
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
		s.failedTests = append(s.failedTests, testName)
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
	duration := time.Since(s.startTime)

	// Build the concise summary
	var summary strings.Builder
	fmt.Fprintf(&summary, "TEST SUMMARY\n")
	fmt.Fprintf(&summary, "============\n")
	fmt.Fprintf(&summary, "Run ID: %s\n", runID)
	fmt.Fprintf(&summary, "Time: %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(&summary, "Duration: %s\n\n", duration)

	fmt.Fprintf(&summary, "Results:\n")
	fmt.Fprintf(&summary, "  Total:   %d\n", total)
	fmt.Fprintf(&summary, "  Passed:  %d\n", s.passed)
	fmt.Fprintf(&summary, "  Failed:  %d\n", s.failed)
	fmt.Fprintf(&summary, "  Skipped: %d\n", s.skipped)
	fmt.Fprintf(&summary, "  Errors:  %d\n\n", s.errored)

	// Add pass rate percentage
	passRate := 0.0
	if total > 0 {
		passRate = float64(s.passed) / float64(total) * 100
	}
	fmt.Fprintf(&summary, "Pass Rate: %.1f%%\n\n", passRate)

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
