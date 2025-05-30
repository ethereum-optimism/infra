package reporting

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"text/template"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
)

// StatusDisplay represents display information for a test status
type StatusDisplay struct {
	Text  string // Human-readable status text
	Class string // CSS class or style identifier
}

// getStatusDisplay returns human-readable status text and CSS class
func getStatusDisplay(status types.TestStatus) StatusDisplay {
	switch status {
	case types.TestStatusPass:
		return StatusDisplay{Text: "PASS", Class: "pass"}
	case types.TestStatusFail:
		return StatusDisplay{Text: "FAIL", Class: "fail"}
	case types.TestStatusSkip:
		return StatusDisplay{Text: "SKIP", Class: "skip"}
	case types.TestStatusError:
		return StatusDisplay{Text: "ERROR", Class: "error"}
	default:
		return StatusDisplay{Text: "UNKNOWN", Class: "unknown"}
	}
}

// formatDuration formats a duration for display
func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return d.Truncate(time.Millisecond).String()
}

// generateTreePrefix creates tree-style prefixes for hierarchical display
func generateTreePrefix(level int, isSubTest bool) string {
	switch level {
	case 0: // Gate level
		return ""
	case 1: // Suite level
		return "├── "
	case 2: // Test level
		if isSubTest {
			return "│   │   ├── "
		}
		return "│   ├── "
	case 3: // Subtest level
		return "│   │   ├── "
	default:
		return strings.Repeat("│   ", level-1) + "├── "
	}
}

// ReportFormatter defines the interface for different report output formats
type ReportFormatter interface {
	Format(data *ReportData) (string, error)
}

// ReportWriter defines the interface for writing reports to various destinations
type ReportWriter interface {
	Write(content string) error
}

// FileWriter writes reports to a file
type FileWriter struct {
	path string
}

// NewFileWriter creates a new file writer
func NewFileWriter(path string) *FileWriter {
	return &FileWriter{path: path}
}

// Write writes the content to the file
func (fw *FileWriter) Write(content string) error {
	return os.WriteFile(fw.path, []byte(content), 0644)
}

// StdoutWriter writes reports to stdout
type StdoutWriter struct{}

// NewStdoutWriter creates a new stdout writer
func NewStdoutWriter() *StdoutWriter {
	return &StdoutWriter{}
}

// Write writes the content to stdout
func (sw *StdoutWriter) Write(content string) error {
	_, err := fmt.Print(content)
	return err
}

// HTMLFormatter formats reports as HTML
type HTMLFormatter struct {
	template *template.Template
}

// NewHTMLFormatter creates a new HTML formatter
func NewHTMLFormatter(templateContent string) (*HTMLFormatter, error) {
	tmpl, err := template.New("report").Parse(templateContent)
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML template: %w", err)
	}

	return &HTMLFormatter{
		template: tmpl,
	}, nil
}

// Format formats the report data as HTML
func (hf *HTMLFormatter) Format(data *ReportData) (string, error) {
	// Convert ReportData to the structure expected by the HTML template
	htmlData := &HTMLSummaryData{
		RunID:             data.RunID,
		Time:              data.Timestamp.Format(time.RFC3339),
		TotalDuration:     formatDuration(data.Duration),
		Total:             data.Stats.Total,
		Passed:            data.Stats.Passed,
		Failed:            data.Stats.Failed,
		Skipped:           data.Stats.Skipped,
		Errored:           data.Stats.Errored,
		PassRateFormatted: data.PassRateText,
		HasFailures:       data.HasFailures,
		Tests:             convertToTestResultRows(data.AllTests),
		DevnetName:        data.NetworkName,
		GateRun:           data.GateName,
		PackageLogPath:    data.PackageLogPath,
	}

	var buf bytes.Buffer
	if err := hf.template.Execute(&buf, htmlData); err != nil {
		return "", fmt.Errorf("failed to execute HTML template: %w", err)
	}

	return buf.String(), nil
}

// HTMLSummaryData maintains compatibility with existing HTML template
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
	DevnetName        string
	GateRun           string
	PackageLogPath    string // Path to package-level logs for header link
}

// TestResultRow maintains compatibility with existing HTML template
type TestResultRow struct {
	StatusClass       string
	StatusText        string
	TestName          string
	Package           string
	Gate              string
	Suite             string
	DurationFormatted string
	LogPath           string
	IsSubTest         bool
	ParentTest        string
	ExecutionOrder    int
}

// convertToTestResultRows converts ReportTestItems to TestResultRows for HTML template compatibility
func convertToTestResultRows(items []ReportTestItem) []TestResultRow {
	rows := make([]TestResultRow, len(items))
	for i, item := range items {
		statusDisplay := getStatusDisplay(item.Status)
		rows[i] = TestResultRow{
			StatusClass:       statusDisplay.Class,
			StatusText:        statusDisplay.Text,
			TestName:          item.Name,
			Package:           item.Package,
			Gate:              item.Gate,
			Suite:             item.Suite,
			DurationFormatted: formatDuration(item.Duration),
			LogPath:           item.LogPath,
			IsSubTest:         item.IsSubTest,
			ParentTest:        item.ParentTest,
			ExecutionOrder:    item.ExecutionOrder,
		}
	}
	return rows
}

// TableFormatter formats reports as ASCII tables
type TableFormatter struct {
	showIndividualTests bool
	title               string
}

// NewTableFormatter creates a new table formatter
func NewTableFormatter(title string, showIndividualTests bool) *TableFormatter {
	return &TableFormatter{
		showIndividualTests: showIndividualTests,
		title:               title,
	}
}

// Format formats the report data as an ASCII table
func (tf *TableFormatter) Format(data *ReportData) (string, error) {
	var buf bytes.Buffer

	t := table.NewWriter()
	t.SetOutputMirror(&buf)
	t.SetTitle(tf.title)

	// Configure columns
	t.AppendHeader(table.Row{
		"Type", "ID", "Duration", "Tests", "Passed", "Failed", "Skipped", "Status",
	})

	// Set column configurations for better readability
	t.SetColumnConfigs([]table.ColumnConfig{
		{Name: "Type", AutoMerge: true},
		{Name: "ID", WidthMax: 200, WidthMaxEnforcer: text.WrapSoft},
		{Name: "Duration", Align: text.AlignRight},
		{Name: "Tests", Align: text.AlignRight},
		{Name: "Passed", Align: text.AlignRight},
		{Name: "Failed", Align: text.AlignRight},
		{Name: "Skipped", Align: text.AlignRight},
	})

	// Add gates and their contents
	for _, gate := range data.Gates {
		// Gate row
		t.AppendRow(table.Row{
			"Gate",
			gate.Name,
			formatDuration(gate.Duration),
			"-", // Don't count gate as a test
			gate.Stats.Passed,
			gate.Stats.Failed,
			gate.Stats.Skipped,
			tf.getResultString(gate.Status),
		})

		// Print direct gate tests first
		if tf.showIndividualTests {
			tf.addTestsWithSubtests(t, gate.Tests, false)
		}

		// Print suites in this gate
		for _, suite := range gate.Suites {
			t.AppendRow(table.Row{
				"Suite",
				fmt.Sprintf("├── %s", suite.Name),
				formatDuration(suite.Duration),
				"-", // Don't count suite as a test
				suite.Stats.Passed,
				suite.Stats.Failed,
				suite.Stats.Skipped,
				tf.getResultString(suite.Status),
			})

			// Print tests in this suite
			if tf.showIndividualTests {
				tf.addTestsWithSubtests(t, suite.Tests, true)
			}
		}

		t.AppendSeparator()
	}

	// Update the table style based on overall result status
	if data.HasFailures {
		t.SetStyle(table.StyleColoredBlackOnRedWhite)
	} else if data.Stats.Skipped > 0 {
		t.SetStyle(table.StyleColoredBlackOnYellowWhite)
	} else {
		t.SetStyle(table.StyleColoredBlackOnGreenWhite)
	}

	// Add summary footer
	overallStatus := "PASS"
	if data.HasFailures {
		overallStatus = "FAIL"
	} else if data.Stats.Skipped > 0 {
		overallStatus = "SKIP"
	}

	t.AppendFooter(table.Row{
		"TOTAL",
		"",
		formatDuration(data.Duration),
		data.Stats.Total,
		data.Stats.Passed,
		data.Stats.Failed,
		data.Stats.Skipped,
		overallStatus,
	})

	t.Render()
	return buf.String(), nil
}

// addTestsWithSubtests adds tests to the table, properly grouping subtests under their parent tests
func (tf *TableFormatter) addTestsWithSubtests(t table.Writer, tests []ReportTestItem, isInSuite bool) {
	// Group tests by parent-child relationships
	parentTests := make([]ReportTestItem, 0)
	subtestsByParent := make(map[string][]ReportTestItem)

	// Separate parent tests from subtests
	for _, test := range tests {
		if test.IsSubTest {
			subtestsByParent[test.ParentTest] = append(subtestsByParent[test.ParentTest], test)
		} else {
			parentTests = append(parentTests, test)
		}
	}

	// Display parent tests followed by their subtests
	for i, test := range parentTests {
		// Determine prefix for main test
		var prefix string
		if isInSuite {
			prefix = "│   ├──"
			if i == len(parentTests)-1 && len(subtestsByParent[test.Name]) == 0 {
				prefix = "│   └──"
			}
		} else {
			prefix = "├──"
			if i == len(parentTests)-1 && len(subtestsByParent[test.Name]) == 0 {
				prefix = "└──"
			}
		}

		// Add main test row
		t.AppendRow(table.Row{
			tf.getTestType(test),
			fmt.Sprintf("%s %s", prefix, test.Name),
			formatDuration(test.Duration),
			"1",
			tf.boolToInt(test.Status == types.TestStatusPass),
			tf.boolToInt(test.Status == types.TestStatusFail || test.Status == types.TestStatusError),
			tf.boolToInt(test.Status == types.TestStatusSkip),
			tf.getResultString(test.Status),
		})

		// Add subtests for this parent test
		if subtests, hasSubtests := subtestsByParent[test.Name]; hasSubtests {
			for j, subtest := range subtests {
				// Determine prefix for subtest
				var subtestPrefix string
				if isInSuite {
					if i == len(parentTests)-1 && j == len(subtests)-1 {
						subtestPrefix = "│       └──"
					} else {
						subtestPrefix = "│   │   ├──"
					}
				} else {
					if i == len(parentTests)-1 && j == len(subtests)-1 {
						subtestPrefix = "    └──"
					} else {
						subtestPrefix = "│   ├──"
					}
				}

				// Add subtest row
				t.AppendRow(table.Row{
					"", // Empty type for subtests
					fmt.Sprintf("%s %s", subtestPrefix, subtest.Name),
					formatDuration(subtest.Duration),
					"1",
					tf.boolToInt(subtest.Status == types.TestStatusPass),
					tf.boolToInt(subtest.Status == types.TestStatusFail || subtest.Status == types.TestStatusError),
					tf.boolToInt(subtest.Status == types.TestStatusSkip),
					tf.getResultString(subtest.Status),
				})
			}
		}
	}
}

// getTestType returns the appropriate type label for a test item
func (tf *TableFormatter) getTestType(test ReportTestItem) string {
	if test.IsSubTest {
		return ""
	}
	return "Test"
}

// getResultString converts a TestStatus to a display string
func (tf *TableFormatter) getResultString(status interface{}) string {
	switch v := status.(type) {
	case string:
		return strings.ToUpper(v)
	default:
		return strings.ToUpper(fmt.Sprintf("%v", v))
	}
}

// boolToInt converts a boolean to int for table display
func (tf *TableFormatter) boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// TextSummaryFormatter formats reports as plain text summaries
type TextSummaryFormatter struct {
	includeDetails bool
}

// NewTextSummaryFormatter creates a new text summary formatter
func NewTextSummaryFormatter(includeDetails bool) *TextSummaryFormatter {
	return &TextSummaryFormatter{
		includeDetails: includeDetails,
	}
}

// Format formats the report data as a text summary
func (tsf *TextSummaryFormatter) Format(data *ReportData) (string, error) {
	var summary strings.Builder

	fmt.Fprintf(&summary, "TEST SUMMARY\n")
	fmt.Fprintf(&summary, "============\n")
	fmt.Fprintf(&summary, "Run ID: %s\n", data.RunID)
	fmt.Fprintf(&summary, "Time: %s\n", data.Timestamp.Format(time.RFC3339))
	fmt.Fprintf(&summary, "Duration: %s\n\n", formatDuration(data.Duration))

	// Add timeout warning if there were any timeouts
	if data.HasTimeouts {
		fmt.Fprintf(&summary, "⚠️  WARNING: %d TEST(S) TIMED OUT! ⚠️\n\n", data.Stats.Timeouts)
	}

	fmt.Fprintf(&summary, "Results:\n")
	fmt.Fprintf(&summary, "  Total:   %d\n", data.Stats.Total)
	fmt.Fprintf(&summary, "  Passed:  %d\n", data.Stats.Passed)
	fmt.Fprintf(&summary, "  Failed:  %d\n", data.Stats.Failed)
	fmt.Fprintf(&summary, "  Skipped: %d\n", data.Stats.Skipped)
	fmt.Fprintf(&summary, "  Errors:  %d\n", data.Stats.Errored)
	if data.HasTimeouts {
		fmt.Fprintf(&summary, "  Timeouts: %d\n", data.Stats.Timeouts)
	}
	fmt.Fprintf(&summary, "\n")

	// Add timeout information prominently if there were timeouts
	if len(data.TimeoutTestNames) > 0 {
		fmt.Fprintf(&summary, "TIMED OUT TESTS:\n")
		fmt.Fprintf(&summary, "================\n")
		for _, test := range data.TimeoutTestNames {
			fmt.Fprintf(&summary, "  ⏰ %s\n", test)
		}
		fmt.Fprintf(&summary, "\n")
	}

	// Include a list of failed tests if there are any
	if len(data.FailedTestNames) > 0 {
		fmt.Fprintf(&summary, "Failed tests:\n")
		for _, test := range data.FailedTestNames {
			fmt.Fprintf(&summary, "  - %s\n", test)
		}
		fmt.Fprintf(&summary, "\n")
	}

	// Add detailed results if requested
	if tsf.includeDetails {
		fmt.Fprintf(&summary, "DETAILED RESULTS:\n")
		fmt.Fprintf(&summary, "=================\n")

		for _, gate := range data.Gates {
			fmt.Fprintf(&summary, "Gate: %s (%s) [%s]\n", gate.Name, formatDuration(gate.Duration), strings.ToUpper(string(gate.Status)))

			for _, suite := range gate.Suites {
				fmt.Fprintf(&summary, "  Suite: %s (%s) [%s]\n", suite.Name, formatDuration(suite.Duration), strings.ToUpper(string(suite.Status)))

				for _, test := range suite.Tests {
					prefix := "    "
					if test.IsSubTest {
						prefix = "      "
					}
					statusDisplay := getStatusDisplay(test.Status)
					fmt.Fprintf(&summary, "%s- %s (%s) [%s]\n", prefix, test.Name, formatDuration(test.Duration), statusDisplay.Text)
				}
			}

			for _, test := range gate.Tests {
				prefix := "  "
				if test.IsSubTest {
					prefix = "    "
				}
				statusDisplay := getStatusDisplay(test.Status)
				fmt.Fprintf(&summary, "%s- %s (%s) [%s]\n", prefix, test.Name, formatDuration(test.Duration), statusDisplay.Text)
			}

			fmt.Fprintf(&summary, "\n")
		}
	}

	return summary.String(), nil
}

// ReportGenerator combines builder, formatter, and writer for easy report generation
type ReportGenerator struct {
	builder   *ReportBuilder
	formatter ReportFormatter
	writer    ReportWriter
}

// NewReportGenerator creates a new report generator
func NewReportGenerator(builder *ReportBuilder, formatter ReportFormatter, writer ReportWriter) *ReportGenerator {
	return &ReportGenerator{
		builder:   builder,
		formatter: formatter,
		writer:    writer,
	}
}

// GenerateFromTestResults generates a report from test results
func (rg *ReportGenerator) GenerateFromTestResults(testResults []*types.TestResult, runID, networkName, gateName string) error {
	// Build the report data
	reportData := rg.builder.BuildFromTestResults(testResults, runID, networkName, gateName)

	// Format the report
	content, err := rg.formatter.Format(reportData)
	if err != nil {
		return fmt.Errorf("failed to format report: %w", err)
	}

	// Write the report
	if err := rg.writer.Write(content); err != nil {
		return fmt.Errorf("failed to write report: %w", err)
	}

	return nil
}

// GenerateReport generates a report from pre-built report data
func (rg *ReportGenerator) GenerateReport(reportData *ReportData) error {
	// Format the report
	content, err := rg.formatter.Format(reportData)
	if err != nil {
		return fmt.Errorf("failed to format report: %w", err)
	}

	// Write the report
	if err := rg.writer.Write(content); err != nil {
		return fmt.Errorf("failed to write report: %w", err)
	}

	return nil
}
