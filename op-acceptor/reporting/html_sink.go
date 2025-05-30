package reporting

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
)

// ReportingHTMLSink is a modern HTML sink that uses the unified reporting structure
type ReportingHTMLSink struct {
	builder                 *ReportBuilder
	formatter               *HTMLFormatter
	baseDir                 string
	loggerRunID             string // The runID the logger was initialized with
	networkName             string
	gateName                string
	testResults             map[string][]*types.TestResult // Map runID to test results
	getReadableTestFilename func(metadata types.ValidatorMetadata) string
}

// NewReportingHTMLSink creates a new HTML sink using the unified reporting structure
func NewReportingHTMLSink(baseDir, loggerRunID, networkName, gateName, templateContent string, getReadableTestFilename func(types.ValidatorMetadata) string) (*ReportingHTMLSink, error) {
	formatter, err := NewHTMLFormatter(templateContent)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTML formatter: %w", err)
	}

	builder := NewReportBuilder().WithLogPathGenerator(func(test *types.TestResult, isSubTest bool, parentName string) string {
		// Generate filename using the test metadata
		filename := getReadableTestFilename(test.Metadata) + ".log"

		// Determine the subdirectory based on test status
		var subdir string
		if test.Status == types.TestStatusFail || test.Status == types.TestStatusError {
			subdir = "failed"
		} else {
			subdir = "passed"
		}

		return filepath.Join(subdir, filename)
	})

	return &ReportingHTMLSink{
		builder:                 builder,
		formatter:               formatter,
		baseDir:                 baseDir,
		loggerRunID:             loggerRunID,
		networkName:             networkName,
		gateName:                gateName,
		testResults:             make(map[string][]*types.TestResult),
		getReadableTestFilename: getReadableTestFilename,
	}, nil
}

// Consume collects test results for later HTML generation
func (s *ReportingHTMLSink) Consume(result *types.TestResult, runID string) error {
	if s.testResults[runID] == nil {
		s.testResults[runID] = make([]*types.TestResult, 0)
	}
	s.testResults[runID] = append(s.testResults[runID], result)
	return nil
}

// Complete generates the HTML summary file using the unified reporting structure
func (s *ReportingHTMLSink) Complete(runID string) error {
	// Get test results for this specific runID
	results, exists := s.testResults[runID]
	if !exists {
		results = make([]*types.TestResult, 0)
	}

	// Build the report data
	reportData := s.builder.BuildFromTestResults(results, runID, s.networkName, s.gateName)

	outputDir := filepath.Join(s.baseDir, "testrun-"+runID)

	// Create the output directory if it doesn't exist
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory %s: %w", outputDir, err)
	}

	// Create the HTML report file path
	htmlFile := filepath.Join(outputDir, "results.html")

	// Create file writer and report generator
	writer := NewFileWriter(htmlFile)
	generator := NewReportGenerator(s.builder, s.formatter, writer)

	// Generate the report
	return generator.GenerateReport(reportData)
}

// ReportingTextSummarySink is a modern text summary sink that uses the unified reporting structure
type ReportingTextSummarySink struct {
	builder     *ReportBuilder
	formatter   *TextSummaryFormatter
	baseDir     string
	loggerRunID string // The runID the logger was initialized with
	networkName string
	gateName    string
	testResults map[string][]*types.TestResult // Map runID to test results
}

// NewReportingTextSummarySink creates a new text summary sink using the unified reporting structure
func NewReportingTextSummarySink(baseDir, loggerRunID, networkName, gateName string, includeDetails bool) *ReportingTextSummarySink {
	builder := NewReportBuilder()
	formatter := NewTextSummaryFormatter(includeDetails)

	return &ReportingTextSummarySink{
		builder:     builder,
		formatter:   formatter,
		baseDir:     baseDir,
		loggerRunID: loggerRunID,
		networkName: networkName,
		gateName:    gateName,
		testResults: make(map[string][]*types.TestResult),
	}
}

// Consume collects test results for later text summary generation
func (s *ReportingTextSummarySink) Consume(result *types.TestResult, runID string) error {
	if s.testResults[runID] == nil {
		s.testResults[runID] = make([]*types.TestResult, 0)
	}
	s.testResults[runID] = append(s.testResults[runID], result)
	return nil
}

// Complete generates the text summary file using the unified reporting structure
func (s *ReportingTextSummarySink) Complete(runID string) error {
	// Get test results for this specific runID
	results, exists := s.testResults[runID]
	if !exists {
		results = make([]*types.TestResult, 0)
	}

	// Build the report data
	reportData := s.builder.BuildFromTestResults(results, runID, s.networkName, s.gateName)

	outputDir := filepath.Join(s.baseDir, "testrun-"+runID)

	// Create the output directory if it doesn't exist
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory %s: %w", outputDir, err)
	}

	// Create the summary report file path
	summaryFile := filepath.Join(outputDir, "summary.log")

	// Create file writer and report generator
	writer := NewFileWriter(summaryFile)
	generator := NewReportGenerator(s.builder, s.formatter, writer)

	// Generate the report
	return generator.GenerateReport(reportData)
}

// TableReporter provides functionality to generate table output using the unified reporting structure
type TableReporter struct {
	builder   *ReportBuilder
	formatter *TableFormatter
}

// NewTableReporter creates a new table reporter
func NewTableReporter(title string, showIndividualTests bool) *TableReporter {
	builder := NewReportBuilder()
	formatter := NewTableFormatter(title, showIndividualTests)

	return &TableReporter{
		builder:   builder,
		formatter: formatter,
	}
}

// GenerateTableFromTestResults generates a table report and returns the content as a string
func (tr *TableReporter) GenerateTableFromTestResults(testResults []*types.TestResult, runID, networkName, gateName string) (string, error) {
	// Build the report data
	reportData := tr.builder.BuildFromTestResults(testResults, runID, networkName, gateName)

	// Format and return the table
	return tr.formatter.Format(reportData)
}

// PrintTableFromTestResults generates and prints a table report to stdout
func (tr *TableReporter) PrintTableFromTestResults(testResults []*types.TestResult, runID, networkName, gateName string) error {
	content, err := tr.GenerateTableFromTestResults(testResults, runID, networkName, gateName)
	if err != nil {
		return err
	}

	writer := NewStdoutWriter()
	return writer.Write(content)
}
