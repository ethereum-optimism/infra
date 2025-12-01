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

	"github.com/acarl005/stripansi"

	"github.com/ethereum-optimism/infra/op-acceptor/reporting"
	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/ethereum-optimism/infra/op-acceptor/ui"
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
func NewFileLogger(baseDir string, runID string, networkName string, gateRuns []string) (*FileLogger, error) {
	if runID == "" {
		return nil, fmt.Errorf("runID cannot be empty")
	}

	if baseDir == "" {
		return nil, fmt.Errorf("baseDir cannot be empty")
	}

	gateRun := strings.Join(gateRuns, "_")

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

	// Load raw template content for ReportingHTMLSink
	templateContent, err := GetRawTemplateContent(HTMLResultsTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to load HTML template: %w", err)
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
	return l.CompleteWithTiming(runID, 0)
}

// CompleteWithTiming finalizes all sinks with enhanced timing and closes all file writers
func (l *FileLogger) CompleteWithTiming(runID string, wallClockTime time.Duration) error {
	if runID == "" {
		return fmt.Errorf("runID cannot be empty")
	}

	for _, sink := range l.sinks {
		// Try to use enhanced timing method if available
		if enhancedSink, ok := sink.(interface {
			CompleteWithTiming(string, time.Duration) error
		}); ok {
			if err := enhancedSink.CompleteWithTiming(runID, wallClockTime); err != nil {
				return fmt.Errorf("error completing enhanced sink: %w", err)
			}
		} else {
			// Fall back to regular Complete method
			if err := sink.Complete(runID); err != nil {
				return fmt.Errorf("error completing sink: %w", err)
			}
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
		// Clean up function names that start with "./"
		fileName = strings.TrimPrefix(fileName, "./")
	} else if metadata.RunAll {
		// For package tests that run all tests, use package basename
		if metadata.Package != "" {
			// Handle special case where package is "." (current directory)
			if metadata.Package == "." {
				// Use a generic name - will rarely happen in practice
				fileName = "package"
			} else {
				packageParts := strings.Split(metadata.Package, "/")
				// Find the last non-empty part
				for i := len(packageParts) - 1; i >= 0; i-- {
					if packageParts[i] != "" && packageParts[i] != "." {
						fileName = packageParts[i]
						break
					}
				}
				// If we didn't find a package name, use a fallback
				if fileName == "" {
					fileName = "PackageSuite"
				}
			}
		} else {
			fileName = "PackageSuite"
		}
	} else {
		fileName = metadata.ID // Fallback to ID if no function name
		// Clean up ID that starts with "./"
		fileName = strings.TrimPrefix(fileName, "./")
	}

	// Extract package basename for cleaner filenames
	pkgName := ""
	if metadata.Package != "" {
		// Handle special case where package is "." (current directory)
		// or "./something" (subdirectory of current directory)
		if metadata.Package == "." {
			// Package "." is rare - usually packages are like "./base", "./isthmus", etc.
			// Don't add a package prefix for "."
			pkgName = ""
		} else if strings.HasPrefix(metadata.Package, "./") {
			// For packages like "./base", extract the directory name
			pkgName = strings.TrimPrefix(metadata.Package, "./")
			// If it contains further slashes, take the last part
			if idx := strings.LastIndex(pkgName, "/"); idx != -1 {
				pkgName = pkgName[idx+1:]
			}
		} else if strings.Contains(metadata.Package, "github.com") {
			// Handle GitHub-style package paths
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

	// Build the final filename components then join with underscores to avoid stray separators
	var components []string
	if prefix != "" {
		components = append(components, prefix)
	}
	if pkgName != "" && pkgName != prefix {
		components = append(components, pkgName)
	}
	baseName := fileName
	// Avoid duplicated package/file for package-level entries (no FuncName),
	// regardless of temporary RunAll flag propagation timing
	if metadata.FuncName == "" && pkgName != "" && pkgName == fileName {
		baseName = ""
	}
	if baseName != "" {
		components = append(components, baseName)
	}

	// Finally ensure the name is safe for a filename
	return safeFilename(strings.Join(components, "_"))
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

	var content strings.Builder

	content.WriteString("\n")
	content.WriteString(ui.BuildBoxHeader(fmt.Sprintf("TEST: %s", truncateString(result.Metadata.FuncName, 60)), 72))

	// Add test metadata in a structured format
	content.WriteString(ui.BuildBoxLine(fmt.Sprintf("Status:   %s", result.Status), 72))
	content.WriteString(ui.BuildBoxLine(fmt.Sprintf("Package:  %s", truncateString(result.Metadata.Package, 62)), 72))
	content.WriteString(ui.BuildBoxLine(fmt.Sprintf("Gate:     %s", truncateString(result.Metadata.Gate, 62)), 72))
	content.WriteString(ui.BuildBoxLine(fmt.Sprintf("Suite:    %s", truncateString(result.Metadata.Suite, 62)), 72))
	content.WriteString(ui.BuildBoxLine(fmt.Sprintf("Duration: %s", result.Duration), 72))
	content.WriteString(ui.BuildBoxLine(fmt.Sprintf("Time:     %s", time.Now().Format(time.RFC3339)), 72))
	content.WriteString(ui.BuildBoxFooter(72))
	content.WriteString("\n")

	// Add error and stdout in clearly marked sections
	if result.Error != nil {
		content.WriteString("ERROR:\n")
		content.WriteString("~~~~~~\n")
		content.WriteString(fmt.Sprintf("%s\n\n", result.Error.Error()))
	}

	if result.Stdout != "" {
		content.WriteString("STDOUT:\n")
		content.WriteString("~~~~~~~\n")
		// Indent the stdout for better readability
		indentedOutput := indentText(result.Stdout, "  ")
		content.WriteString(fmt.Sprintf("%s\n", indentedOutput))
	}

	// Add a clear separator at the end
	content.WriteString("\n")

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
		// Write this subtest and all nested subtests recursively, tracking full hierarchical name
		if err := s.writeSubtestRecursive(result.Metadata, subTestName, subTest, passedDir, failedDir, runID); err != nil {
			return fmt.Errorf("failed to create subtest log file for %s: %w", subTestName, err)
		}
	}

	return nil
}

// writeSubtestRecursive writes log files for a subtest and all of its nested subtests
func (s *PerTestFileSink) writeSubtestRecursive(parentMeta types.ValidatorMetadata, fullPath string, subTest *types.TestResult, passedDir, failedDir, runID string) error {
	// Create a copy of the subtest with proper metadata for filename generation
	subTestResult := &types.TestResult{
		Metadata: types.ValidatorMetadata{
			ID:       subTest.Metadata.ID,
			Gate:     parentMeta.Gate,    // Use parent's gate
			Suite:    parentMeta.Suite,   // Use parent's suite
			FuncName: fullPath,           // Use the full subtest path name
			Package:  parentMeta.Package, // Use parent's package
			RunAll:   false,
		},
		Status:   subTest.Status,
		Error:    subTest.Error,
		Duration: subTest.Duration,
		Stdout:   subTest.Stdout,
		SubTests: subTest.SubTests,
	}

	// Compute and propagate the artifact basename to the original subTest as well
	computedBase := getReadableTestFilename(subTestResult.Metadata)
	subTest.ArtifactBaseName = computedBase

	if err := s.createTestLogFileOnce(subTestResult, passedDir, failedDir, runID); err != nil {
		return err
	}

	// Recurse into nested subtests, if any
	for nestedName, nested := range subTest.SubTests {
		nextPath := fullPath
		if nextPath != "" {
			nextPath += "/" + nestedName
		} else {
			nextPath = nestedName
		}
		if err := s.writeSubtestRecursive(parentMeta, nextPath, nested, passedDir, failedDir, runID); err != nil {
			return err
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

	// Full path to the test log file (use .txt to be consistent with links)
	testFilePath := filepath.Join(targetDir, filename+".txt")

	// Check if we've already processed this test file
	s.mu.Lock()
	if s.processedTests[testFilePath] {
		s.mu.Unlock()
		return nil // Already processed, skip
	}
	s.processedTests[testFilePath] = true
	s.mu.Unlock()

	// Now create the test log file
	return s.createTestLogFiles(result, passedDir, failedDir)
}

// createTestLogFiles creates three separate log files for a single test result:
// 1. A plaintext log file containing the processed plaintext output
// 2. A JSON log file containing the raw JSON output
// 3. A summary log file containing the result summary
func (s *PerTestFileSink) createTestLogFiles(result *types.TestResult, passedDir, failedDir string) error {
	// Generate a safe filename based on the test metadata
	filename := getReadableTestFilename(result.Metadata)
	// Persist the artifact basename on the result for downstream sinks
	result.ArtifactBaseName = filename

	// Determine which directory to use based on test status
	var targetDir string
	if result.Status == types.TestStatusFail || result.Status == types.TestStatusError {
		targetDir = failedDir
	} else {
		targetDir = passedDir
	}

	// Create the three separate files
	plaintextPath := filepath.Join(targetDir, filename+".txt")
	jsonPath := filepath.Join(targetDir, filename+".json")
	summaryPath := filepath.Join(targetDir, filename+".log")

	// Check if this is a timeout failure for special handling
	isTimeout := result.TimedOut

	// 1. Create the plaintext file
	err := s.createPlaintextFile(result, plaintextPath, isTimeout)
	if err != nil {
		return fmt.Errorf("failed to create plaintext file: %w", err)
	}

	// 2. Create the JSON file
	err = s.createJSONFile(result, jsonPath, isTimeout)
	if err != nil {
		return fmt.Errorf("failed to create JSON file: %w", err)
	}

	// 3. Create the summary file
	err = s.createSummaryFile(result, summaryPath, isTimeout)
	if err != nil {
		return fmt.Errorf("failed to create summary file: %w", err)
	}

	return nil
}

// createPlaintextFile creates the plaintext output file
func (s *PerTestFileSink) createPlaintextFile(result *types.TestResult, filePath string, isTimeout bool) error {
	// Get or create the async writer
	writer, err := s.logger.getAsyncWriter(filePath)
	if err != nil {
		return err
	}

	// Extract the plaintext output from JSON
	var plaintext strings.Builder
	if result.Stdout != "" {
		// First, try to parse as JSON (go test -json output)
		parser := NewJSONOutputParser(result.Stdout)
		parser.ProcessJSONOutput(func(_ map[string]interface{}, outputText string) {
			// Strip ANSI escape sequences from the output
			plaintext.WriteString(stripansi.Strip(outputText))
		})

		// If JSON parsing produced no output, the Stdout might already be plain text
		// (e.g., for subtests extracted from a package run)
		if plaintext.Len() == 0 && strings.Contains(result.Stdout, "===") {
			// It's already plain text, strip ANSI sequences and use it
			plaintext.WriteString(stripansi.Strip(result.Stdout))
		}
	}

	var content strings.Builder

	// For timeout cases, prominently display the timeout error at the beginning
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
			fmt.Fprintf(&content, "%s", plaintext.String())
		} else {
			// Handle cases where no output was captured
			// This should be rare after parser fixes, but can happen if:
			// - A test genuinely produces no output (no t.Log, no assertions, etc.)
			// - The test was skipped before any output
			// - There was an error capturing output
			if result.Metadata.FuncName != "" {
				// Provide informative message about the test result
				fmt.Fprintf(&content, "Test completed with status: %s\n", result.Status)
				if result.Duration > 0 {
					fmt.Fprintf(&content, "Duration: %v\n", result.Duration)
				}
				if result.Error != nil {
					fmt.Fprintf(&content, "Error: %v\n", result.Error)
				} else {
					fmt.Fprintf(&content, "No output was produced by this test.\n")
				}
			} else {
				// Package-level result with no output
				fmt.Fprintf(&content, "No output captured.\n")
			}
		}
	}

	// Write the content to the file
	return writer.Write([]byte(content.String()))
}

// createJSONFile creates the JSON output file
func (s *PerTestFileSink) createJSONFile(result *types.TestResult, filePath string, isTimeout bool) error {
	// Get or create the async writer
	writer, err := s.logger.getAsyncWriter(filePath)
	if err != nil {
		return err
	}

	var content strings.Builder

	// Include the raw JSON output
	if result.Stdout != "" {
		if isTimeout {
			fmt.Fprintf(&content, "# PARTIAL JSON OUTPUT (BEFORE TIMEOUT)\n")
			fmt.Fprintf(&content, "# ------------------------------------\n")
		}
		fmt.Fprintf(&content, "%s", result.Stdout)
		if !strings.HasSuffix(result.Stdout, "\n") {
			fmt.Fprintf(&content, "\n")
		}
	} else if isTimeout {
		fmt.Fprintf(&content, "# No JSON output captured before timeout.\n")
		// Include our timeout marker if we stored one
		fmt.Fprintf(&content, "# Timeout marker that would be stored:\n")
		fmt.Fprintf(&content, `{"Time":"%s","Action":"timeout","Package":"%s","Test":"%s","Output":"TEST TIMED OUT - no JSON output captured\n"}`,
			time.Now().Format(time.RFC3339), result.Metadata.Package, result.Metadata.FuncName)
		fmt.Fprintf(&content, "\n")
	} else {
		fmt.Fprintf(&content, "# No JSON output available.\n")
	}

	// Write the content to the file
	return writer.Write([]byte(content.String()))
}

// createSummaryFile creates the summary file
func (s *PerTestFileSink) createSummaryFile(result *types.TestResult, filePath string, isTimeout bool) error {
	// Get or create the async writer
	writer, err := s.logger.getAsyncWriter(filePath)
	if err != nil {
		return err
	}

	var content strings.Builder

	// Check if this is a timeout failure
	if result.Status == types.TestStatusFail || result.Status == types.TestStatusError {
		if isTimeout {
			fmt.Fprintf(&content, "TIMEOUT ERROR SUMMARY:\n")
			fmt.Fprintf(&content, "======================\n\n")
			fmt.Fprintf(&content, "This test failed due to timeout!\n")
			fmt.Fprintf(&content, "Timeout Duration: %v\n", result.Metadata.Timeout)
			fmt.Fprintf(&content, "Error: %s\n\n", result.Error.Error())
		} else {
			fmt.Fprintf(&content, "ERROR SUMMARY:\n")
			fmt.Fprintf(&content, "=============\n\n")

			// Extract critical error information from non-timeout errors
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
		// For passed tests, a simpler summary
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
