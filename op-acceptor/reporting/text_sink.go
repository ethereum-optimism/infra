package reporting

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
)

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
