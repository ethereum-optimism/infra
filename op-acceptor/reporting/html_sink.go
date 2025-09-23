package reporting

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
)

// extractTestNameFromParent extracts the test function name from a parent test name
// For example, from "TestRPCConnectivity" or "TestRPCConnectivity/SubTest" it returns "TestRPCConnectivity"
func extractTestNameFromParent(parentName string) string {
	// If the parent name contains a slash, it's already a subtest, extract the root test name
	if idx := strings.Index(parentName, "/"); idx != -1 {
		return parentName[:idx]
	}
	// Otherwise, it's the root test name
	return parentName
}

// ReportingHTMLSink generates HTML reports using the TestTree intermediate representation
type ReportingHTMLSink struct {
	formatter               *TreeHTMLFormatter
	baseDir                 string
	loggerRunID             string
	networkName             string
	gateName                string
	testResults             map[string][]*types.TestResult
	getReadableTestFilename func(metadata types.ValidatorMetadata) string
	jsContent               []byte
	configSnapshots         map[string]*types.EffectiveConfigSnapshot // map of runID to effective config snapshot
}

// NewReportingHTMLSink creates a new HTML sink using TestTree
func NewReportingHTMLSink(baseDir, loggerRunID, networkName, gateName, templateContent string, jsContent []byte, getReadableTestFilename func(types.ValidatorMetadata) string) (*ReportingHTMLSink, error) {
	formatter, err := NewTreeHTMLFormatter(templateContent)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTML formatter: %w", err)
	}

	return &ReportingHTMLSink{
		formatter:               formatter,
		baseDir:                 baseDir,
		loggerRunID:             loggerRunID,
		networkName:             networkName,
		gateName:                gateName,
		testResults:             make(map[string][]*types.TestResult),
		getReadableTestFilename: getReadableTestFilename,
		jsContent:               jsContent,
		configSnapshots:         make(map[string]*types.EffectiveConfigSnapshot),
	}, nil
}

// SetConfigSnapshot associates an effective config snapshot with a runID
func (s *ReportingHTMLSink) SetConfigSnapshot(runID string, snap *types.EffectiveConfigSnapshot) {
	if runID == "" || snap == nil {
		return
	}
	s.configSnapshots[runID] = snap
}

// Consume collects test results for later HTML generation
func (s *ReportingHTMLSink) Consume(result *types.TestResult, runID string) error {
	if s.testResults[runID] == nil {
		s.testResults[runID] = make([]*types.TestResult, 0)
	}
	s.testResults[runID] = append(s.testResults[runID], result)
	return nil
}

// Complete generates the HTML summary file using TestTree
func (s *ReportingHTMLSink) Complete(runID string) error {
	return s.CompleteWithTiming(runID, 0)
}

// CompleteWithTiming generates the HTML summary file using TestTree with enhanced timing
func (s *ReportingHTMLSink) CompleteWithTiming(runID string, wallClockTime time.Duration) error {
	// Get test results for this specific runID
	results, exists := s.testResults[runID]
	if !exists {
		results = make([]*types.TestResult, 0)
	}

	// Build the TestTree
	builder := types.NewTestTreeBuilder().
		WithSubtests(true).
		WithLogPathGenerator(func(test *types.TestResult, isSubtest bool, parentName string) string {
			// Deterministic: prefer the exact artifact basename produced by the file sink
			base := test.ArtifactBaseName
			if base == "" {
				base = s.getReadableTestFilename(test.Metadata)
			}

			subdir := "passed"
			if test.Status == types.TestStatusFail || test.Status == types.TestStatusError {
				subdir = "failed"
			}
			return filepath.Join(subdir, base+".txt")
		})

	tree := builder.BuildFromTestResults(results, runID, s.networkName)
	if snap, ok := s.configSnapshots[runID]; ok {
		tree.Config = snap
	}

	// Override tree duration with wall clock time if provided
	if wallClockTime > 0 {
		tree.Duration = wallClockTime
	}

	outputDir := filepath.Join(s.baseDir, "testrun-"+runID)

	// Create directories
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory %s: %w", outputDir, err)
	}

	staticDir := filepath.Join(outputDir, "static")
	if err := os.MkdirAll(staticDir, 0755); err != nil {
		return fmt.Errorf("failed to create static directory %s: %w", staticDir, err)
	}

	// Copy JavaScript file
	jsFile := filepath.Join(staticDir, "results.js")
	if err := os.WriteFile(jsFile, s.jsContent, 0644); err != nil {
		return fmt.Errorf("failed to write JavaScript file: %w", err)
	}

	// Generate HTML output using TestTree
	htmlOutput, err := s.formatter.Format(tree)
	if err != nil {
		return fmt.Errorf("failed to format HTML: %w", err)
	}

	// Write HTML file
	htmlFile := filepath.Join(outputDir, "results.html")
	if err := os.WriteFile(htmlFile, []byte(htmlOutput), 0644); err != nil {
		return fmt.Errorf("failed to write HTML file: %w", err)
	}

	return nil
}
