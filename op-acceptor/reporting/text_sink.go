package reporting

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
)

// ReportingTextSummarySink uses the TestTree intermediate representation
type ReportingTextSummarySink struct {
	formatter   *TreeTextFormatter
	baseDir     string
	loggerRunID string
	networkName string
	gateName    string
	testResults map[string][]*types.TestResult
}

// NewReportingTextSummarySink creates a new text summary sink using TestTree
func NewReportingTextSummarySink(baseDir, loggerRunID, networkName, gateName string, includeDetails bool) *ReportingTextSummarySink {
	formatter := NewTreeTextFormatter(
		false, // includeContainers - cleaner summary without container noise
		true,  // includeStats
		includeDetails,
		false, // showExecutionOrder - not needed for summary
	)

	return &ReportingTextSummarySink{
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

// Complete generates the text summary file using TestTree
func (s *ReportingTextSummarySink) Complete(runID string) error {
	// Get test results for this specific runID
	results, exists := s.testResults[runID]
	if !exists {
		results = make([]*types.TestResult, 0)
	}

	// Build the TestTree
	builder := types.NewTestTreeBuilder().WithSubtests(true)
	tree := builder.BuildFromTestResults(results, runID, s.networkName)

	outputDir := filepath.Join(s.baseDir, "testrun-"+runID)

	// Create the output directory if it doesn't exist
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory %s: %w", outputDir, err)
	}

	// Generate the text summary
	content, err := s.formatter.Format(tree)
	if err != nil {
		return fmt.Errorf("failed to format text summary: %w", err)
	}

	// Write to file
	summaryFile := filepath.Join(outputDir, "summary.log")
	if err := os.WriteFile(summaryFile, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write summary file: %w", err)
	}

	return nil
}

// TableReporter provides functionality to generate table output using TestTree
type TableReporter struct {
	formatter *TreeTableFormatter
}

// NewTableReporter creates a new table reporter using TestTree
func NewTableReporter(title string, showIndividualTests bool) *TableReporter {
	formatter := NewTreeTableFormatter(
		title,
		showIndividualTests, // showContainers
		false,               // showExecutionOrder - can be enabled if needed
	)

	return &TableReporter{
		formatter: formatter,
	}
}

// GenerateTableFromTestResults generates a table report using TestTree and returns the content as a string
func (tr *TableReporter) GenerateTableFromTestResults(testResults []*types.TestResult, runID, networkName, gateName string) (string, error) {
	// Build the TestTree
	builder := types.NewTestTreeBuilder().WithSubtests(true)
	tree := builder.BuildFromTestResults(testResults, runID, networkName)

	// Format and return the table
	return tr.formatter.Format(tree)
}

// PrintTableFromTestResults generates and prints a table report to stdout using TestTree
func (tr *TableReporter) PrintTableFromTestResults(testResults []*types.TestResult, runID, networkName, gateName string) error {
	content, err := tr.GenerateTableFromTestResults(testResults, runID, networkName, gateName)
	if err != nil {
		return err
	}

	_, err = fmt.Print(content)
	return err
}
