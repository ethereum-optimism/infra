package logging

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/reporting"
	"github.com/ethereum-optimism/infra/op-acceptor/types"
)

const (
	HTMLResultsTemplate = "results.tmpl.html"
	HTMLResultsFilename = "results.html"
	RunDirectoryPrefix  = "testrun-" // Standardized prefix for run directories
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

// NewFileLogger creates a new FileLogger with given configuration
func NewFileLogger(baseDir string, runID string, networkName, gateRun string) (*FileLogger, error) {
	if runID == "" {
		return nil, fmt.Errorf("runID cannot be empty")
	}

	if baseDir == "" {
		return nil, fmt.Errorf("baseDir cannot be empty")
	}

	// Use the standardized prefix for the run directory
	logDir := filepath.Join(baseDir, RunDirectoryPrefix+runID)
	failedDir := filepath.Join(logDir, "failed")
	summaryFile := filepath.Join(logDir, "summary.log")
	allLogsFile := filepath.Join(logDir, "all.log")

	// Create directories if they don't exist
	dirs := []string{
		baseDir,
		logDir,
		failedDir,
		filepath.Join(logDir, "passed"),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	// Initialize all sinks
	logger := &FileLogger{
		baseDir:      baseDir,
		logDir:       logDir,
		failedDir:    failedDir,
		summaryFile:  summaryFile,
		allLogsFile:  allLogsFile,
		sinks:        make([]ResultSink, 0),
		asyncWriters: make(map[string]*AsyncFile),
		runID:        runID,
	}

	// Initialize the AllLogsFileSink
	allLogsSink := &AllLogsFileSink{logger: logger}
	logger.sinks = append(logger.sinks, allLogsSink)

	// Initialize the PerTestFileSink
	perTestSink := &PerTestFileSink{
		logger:         logger,
		processedTests: make(map[string]bool),
	}
	logger.sinks = append(logger.sinks, perTestSink)

	// Initialize the RawJSONSink
	rawJSONSink := &RawJSONSink{logger: logger}
	logger.sinks = append(logger.sinks, rawJSONSink)

	// Load HTML template
	templateContent, err := templateFS.ReadFile("templates/" + HTMLResultsTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to read HTML template: %w", err)
	}

	// Load JavaScript content
	jsContent, err := GetStaticFile("results.js")
	if err != nil {
		return nil, fmt.Errorf("failed to read JavaScript file: %w", err)
	}

	// Initialize the new ReportingHTMLSink
	htmlSink, err := reporting.NewReportingHTMLSink(baseDir, runID, networkName, gateRun, string(templateContent), jsContent, getReadableTestFilename)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTML sink: %w", err)
	}
	logger.sinks = append(logger.sinks, htmlSink)

	// Initialize the new ReportingTextSummarySink
	textSummarySink := reporting.NewReportingTextSummarySink(baseDir, runID, networkName, gateRun, false)
	logger.sinks = append(logger.sinks, textSummarySink)

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
	// If the runID matches the logger's current runID, return logDir
	if runID == l.runID {
		return l.logDir, nil
	}
	// Always use the standardized prefix for run directories
	return filepath.Join(l.baseDir, RunDirectoryPrefix+runID), nil
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
	return l.logDir
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
		// For package tests that run all tests, use package basename instead of "AllTests"
		if metadata.Package != "" {
			packageParts := strings.Split(metadata.Package, "/")
			// Find the last non-empty part
			for i := len(packageParts) - 1; i >= 0; i-- {
				if packageParts[i] != "" {
					fileName = packageParts[i]
					break
				}
			}
			// If we didn't find a package name, use a fallback
			if fileName == "" {
				fileName = "PackageSuite"
			}
		} else {
			fileName = "PackageSuite"
		}
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

// formatDuration formats a duration for display
func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return d.Truncate(time.Millisecond).String()
}
