package reporting

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTMLFormatter(t *testing.T) {
	templateContent := `
<!DOCTYPE html>
<html>
<head><title>Test Report</title></head>
<body>
<h1>Test Report: {{.RunID}}</h1>
<p>Time: {{.Time}}</p>
<p>Duration: {{.TotalDuration}}</p>
<p>Network: {{.DevnetName}}</p>
<p>Gate: {{.GateRun}}</p>
<div>Total: {{.Total}}, Passed: {{.Passed}}, Failed: {{.Failed}}</div>
<div>Pass Rate: {{.PassRateFormatted}}%</div>
{{if .HasFailures}}<div class="failures">Has Failures</div>{{end}}
{{range .Tests}}
<div class="test {{.StatusClass}}">
  {{.TestName}} ({{.Package}}) - {{.StatusText}} - {{.DurationFormatted}}
  {{if .IsSubTest}}[SubTest of {{.ParentTest}}]{{end}}
</div>
{{end}}
</body>
</html>`

	formatter, err := NewHTMLFormatter(templateContent)
	require.NoError(t, err)

	reportData := createTestReportData()
	result, err := formatter.Format(reportData)
	require.NoError(t, err)

	// Check that key elements are present
	assert.Contains(t, result, "Test Report: test-run-123")
	assert.Contains(t, result, "Network: test-network")
	assert.Contains(t, result, "Gate: test-gate")
	assert.Contains(t, result, "Total: 3, Passed: 1, Failed: 2")
	assert.Contains(t, result, "TestPassing")
	assert.Contains(t, result, "TestFailing")
	assert.Contains(t, result, "Has Failures")
}

func TestHTMLFormatter_InvalidTemplate(t *testing.T) {
	invalidTemplate := `{{.InvalidField`
	_, err := NewHTMLFormatter(invalidTemplate)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse HTML template")
}

func TestTableFormatter(t *testing.T) {
	tests := []struct {
		name                string
		showIndividualTests bool
		expectedContent     []string
	}{
		{
			name:                "with individual tests",
			showIndividualTests: true,
			expectedContent: []string{
				"Gate", "test-gate1",
				"TestPassing", "TestFailing",
				"PASS", "FAIL",
			},
		},
		{
			name:                "without individual tests",
			showIndividualTests: false,
			expectedContent: []string{
				"Gate", "test-gate1",
				// Should not contain individual test names
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			formatter := NewTableFormatter("Test Results", tt.showIndividualTests)
			reportData := createTestReportData()

			result, err := formatter.Format(reportData)
			require.NoError(t, err)

			for _, expected := range tt.expectedContent {
				assert.Contains(t, result, expected)
			}

			if !tt.showIndividualTests {
				assert.NotContains(t, result, "TestPassing")
				assert.NotContains(t, result, "TestFailing")
			}
		})
	}
}

func TestTableFormatter_ResultColors(t *testing.T) {
	tests := []struct {
		name        string
		reportData  *ReportData
		expectColor string
	}{
		{
			name: "green for all passing",
			reportData: &ReportData{
				Stats:       ReportStats{Total: 1, Passed: 1},
				HasFailures: false,
			},
			expectColor: "Green", // Table style should be green
		},
		{
			name: "red for failures",
			reportData: &ReportData{
				Stats:       ReportStats{Total: 2, Passed: 1, Failed: 1},
				HasFailures: true,
			},
			expectColor: "Red", // Table style should be red
		},
		{
			name: "yellow for skipped",
			reportData: &ReportData{
				Stats:       ReportStats{Total: 2, Passed: 1, Skipped: 1},
				HasFailures: false,
			},
			expectColor: "Yellow", // Table style should be yellow
		},
	}

	formatter := NewTableFormatter("Test Results", false)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := formatter.Format(tt.reportData)
			require.NoError(t, err)
			assert.NotEmpty(t, result)
			// Note: We can't easily test the actual color styling without examining the
			// internal table styling, but we can verify that formatting works
		})
	}
}

func TestTextSummaryFormatter(t *testing.T) {
	tests := []struct {
		name            string
		includeDetails  bool
		expectedContent []string
		notExpected     []string
	}{
		{
			name:           "with details",
			includeDetails: true,
			expectedContent: []string{
				"TEST SUMMARY",
				"Run ID: test-run-123",
				"Total:   3",
				"Passed:  1",
				"Failed:  2",
				"Failed tests:",
				"github.com/example/test.TestFailing",
				"DETAILED RESULTS:",
				"Gate: test-gate1",
			},
		},
		{
			name:           "without details",
			includeDetails: false,
			expectedContent: []string{
				"TEST SUMMARY",
				"Run ID: test-run-123",
				"Total:   3",
				"Failed tests:",
			},
			notExpected: []string{
				"DETAILED RESULTS:",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			formatter := NewTextSummaryFormatter(tt.includeDetails)
			reportData := createTestReportData()

			result, err := formatter.Format(reportData)
			require.NoError(t, err)

			for _, expected := range tt.expectedContent {
				assert.Contains(t, result, expected)
			}

			for _, notExpected := range tt.notExpected {
				assert.NotContains(t, result, notExpected)
			}
		})
	}
}

func TestTextSummaryFormatter_WithTimeouts(t *testing.T) {
	reportData := createTestReportData()
	// Add timeout information
	reportData.HasTimeouts = true
	reportData.Stats.Timeouts = 1
	reportData.TimeoutTestNames = []string{"github.com/example/test.TestTimeout"}

	formatter := NewTextSummaryFormatter(false)
	result, err := formatter.Format(reportData)
	require.NoError(t, err)

	assert.Contains(t, result, "⚠️  WARNING: 1 TEST(S) TIMED OUT! ⚠️")
	assert.Contains(t, result, "TIMED OUT TESTS:")
	assert.Contains(t, result, "⏰ github.com/example/test.TestTimeout")
	assert.Contains(t, result, "Timeouts: 1")
}

func TestFileWriter(t *testing.T) {
	// Create a temporary file
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test-report.txt")

	writer := NewFileWriter(testFile)
	testContent := "This is a test report content"

	err := writer.Write(testContent)
	require.NoError(t, err)

	// Verify file was created and content is correct
	content, err := os.ReadFile(testFile)
	require.NoError(t, err)
	assert.Equal(t, testContent, string(content))
}

func TestStdoutWriter(t *testing.T) {
	// This is difficult to test directly, but we can at least verify it doesn't error
	writer := NewStdoutWriter()
	err := writer.Write("test content\n")
	assert.NoError(t, err)
}

func TestReportGenerator(t *testing.T) {
	// Create temporary file for output
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test-report.html")

	// Create components
	templateContent := `<html><body><h1>{{.RunID}}</h1><p>Total: {{.Total}}</p></body></html>`
	formatter, err := NewHTMLFormatter(templateContent)
	require.NoError(t, err)

	writer := NewFileWriter(testFile)
	builder := NewReportBuilder()
	generator := NewReportGenerator(builder, formatter, writer)

	// Test GenerateFromTestResults
	testResults := createTestResults()
	err = generator.GenerateFromTestResults(testResults, "run-123", "network", "gate")
	require.NoError(t, err)

	// Verify file was created and has expected content
	content, err := os.ReadFile(testFile)
	require.NoError(t, err)
	assert.Contains(t, string(content), "run-123")
	assert.Contains(t, string(content), "Total: 3")
}

func TestReportGenerator_GenerateReport(t *testing.T) {
	// Create temporary file for output
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test-report.txt")

	// Create components
	formatter := NewTextSummaryFormatter(false)
	writer := NewFileWriter(testFile)
	builder := NewReportBuilder()
	generator := NewReportGenerator(builder, formatter, writer)

	// Test GenerateReport with pre-built data
	reportData := createTestReportData()
	err := generator.GenerateReport(reportData)
	require.NoError(t, err)

	// Verify file was created and has expected content
	content, err := os.ReadFile(testFile)
	require.NoError(t, err)
	assert.Contains(t, string(content), "TEST SUMMARY")
	assert.Contains(t, string(content), "test-run-123")
}

func TestHTMLSummaryData_Conversion(t *testing.T) {
	reportData := createTestReportData()
	testResultRows := convertToTestResultRows(reportData.AllTests)

	assert.Equal(t, len(reportData.AllTests), len(testResultRows))

	// Check first test conversion
	if len(testResultRows) > 0 {
		firstRow := testResultRows[0]
		firstTest := reportData.AllTests[0]

		statusDisplay := getStatusDisplay(firstTest.Status)
		assert.Equal(t, statusDisplay.Class, firstRow.StatusClass)
		assert.Equal(t, statusDisplay.Text, firstRow.StatusText)
		assert.Equal(t, firstTest.Name, firstRow.TestName)
		assert.Equal(t, firstTest.Package, firstRow.Package)
		assert.Equal(t, firstTest.Gate, firstRow.Gate)
		assert.Equal(t, firstTest.Suite, firstRow.Suite)
		assert.Equal(t, formatDuration(firstTest.Duration), firstRow.DurationFormatted)
		assert.Equal(t, firstTest.LogPath, firstRow.LogPath)
		assert.Equal(t, firstTest.IsSubTest, firstRow.IsSubTest)
		assert.Equal(t, firstTest.ParentTest, firstRow.ParentTest)
	}
}

func TestTableFormatter_BoolToInt(t *testing.T) {
	formatter := NewTableFormatter("Test", true)

	assert.Equal(t, 1, formatter.boolToInt(true))
	assert.Equal(t, 0, formatter.boolToInt(false))
}

func TestTableFormatter_GetResultString(t *testing.T) {
	formatter := NewTableFormatter("Test", true)

	tests := []struct {
		input    interface{}
		expected string
	}{
		{types.TestStatusPass, "PASS"},
		{types.TestStatusFail, "FAIL"},
		{"skip", "SKIP"},
		{"unknown", "UNKNOWN"},
	}

	for _, tt := range tests {
		result := formatter.getResultString(tt.input)
		assert.Equal(t, strings.ToUpper(tt.expected), result)
	}
}

func TestTableFormatter_GetTestType(t *testing.T) {
	formatter := NewTableFormatter("Test", true)

	mainTest := ReportTestItem{IsSubTest: false}
	subTest := ReportTestItem{IsSubTest: true}

	assert.Equal(t, "Test", formatter.getTestType(mainTest))
	assert.Equal(t, "", formatter.getTestType(subTest))
}

// Helper functions for creating test data

func createTestReportData() *ReportData {
	return &ReportData{
		RunID:       "test-run-123",
		NetworkName: "test-network",
		GateName:    "test-gate",
		Timestamp:   time.Now(),
		Duration:    5 * time.Second,
		Stats: ReportStats{
			Total:   3,
			Passed:  1,
			Failed:  2,
			Skipped: 0,
			Errored: 0,
		},
		PassRate:     33.3,
		PassRateText: "33.3",
		HasFailures:  true,
		HasTimeouts:  false,
		Gates: []ReportGate{
			{
				Name:     "test-gate1",
				Status:   types.TestStatusFail,
				Duration: 5 * time.Second,
				Stats: ReportStats{
					Total:  3,
					Passed: 1,
					Failed: 2,
				},
				Tests: []ReportTestItem{
					{
						ID:         "test1",
						Name:       "TestPassing",
						Package:    "github.com/example/test",
						Gate:       "test-gate1",
						Status:     types.TestStatusPass,
						Duration:   100 * time.Millisecond,
						LogPath:    "logs/test1.log",
						HasLogFile: true,
					},
					{
						ID:         "test2",
						Name:       "TestFailing",
						Package:    "github.com/example/test",
						Gate:       "test-gate1",
						Status:     types.TestStatusFail,
						Error:      errors.New("test failed"),
						Duration:   200 * time.Millisecond,
						LogPath:    "logs/test2.log",
						HasLogFile: true,
					},
					{
						ID:         "subtest1",
						Name:       "SubTest1",
						Package:    "github.com/example/test",
						Gate:       "test-gate1",
						IsSubTest:  true,
						ParentTest: "TestFailing",
						Status:     types.TestStatusFail,
						Error:      errors.New("subtest failed"),
						Duration:   50 * time.Millisecond,
						LogPath:    "logs/subtest1.log",
						HasLogFile: true,
					},
				},
			},
		},
		AllTests: []ReportTestItem{
			{
				ID:         "test1",
				Name:       "TestPassing",
				Package:    "github.com/example/test",
				Gate:       "test-gate1",
				Status:     types.TestStatusPass,
				Duration:   100 * time.Millisecond,
				LogPath:    "logs/test1.log",
				HasLogFile: true,
			},
			{
				ID:         "test2",
				Name:       "TestFailing",
				Package:    "github.com/example/test",
				Gate:       "test-gate1",
				Status:     types.TestStatusFail,
				Error:      errors.New("test failed"),
				Duration:   200 * time.Millisecond,
				LogPath:    "logs/test2.log",
				HasLogFile: true,
			},
			{
				ID:         "subtest1",
				Name:       "SubTest1",
				Package:    "github.com/example/test",
				Gate:       "test-gate1",
				IsSubTest:  true,
				ParentTest: "TestFailing",
				Status:     types.TestStatusFail,
				Error:      errors.New("subtest failed"),
				Duration:   50 * time.Millisecond,
				LogPath:    "logs/subtest1.log",
				HasLogFile: true,
			},
		},
		FailedTests: []ReportTestItem{
			{
				ID:       "test2",
				Name:     "TestFailing",
				Package:  "github.com/example/test",
				Gate:     "test-gate1",
				Status:   types.TestStatusFail,
				Error:    errors.New("test failed"),
				Duration: 200 * time.Millisecond,
			},
			{
				ID:         "subtest1",
				Name:       "SubTest1",
				Package:    "github.com/example/test",
				Gate:       "test-gate1",
				IsSubTest:  true,
				ParentTest: "TestFailing",
				Status:     types.TestStatusFail,
				Error:      errors.New("subtest failed"),
				Duration:   50 * time.Millisecond,
			},
		},
		FailedTestNames: []string{
			"github.com/example/test.TestFailing",
			"github.com/example/test.SubTest1",
		},
	}
}

func createTestResults() []*types.TestResult {
	return []*types.TestResult{
		{
			Metadata: types.ValidatorMetadata{
				ID:       "test1",
				Gate:     "test-gate1",
				FuncName: "TestPassing",
				Package:  "github.com/example/test",
			},
			Status:   types.TestStatusPass,
			Duration: 100 * time.Millisecond,
		},
		{
			Metadata: types.ValidatorMetadata{
				ID:       "test2",
				Gate:     "test-gate1",
				FuncName: "TestFailing",
				Package:  "github.com/example/test",
			},
			Status:   types.TestStatusFail,
			Duration: 200 * time.Millisecond,
			Error:    errors.New("test failed"),
			SubTests: map[string]*types.TestResult{
				"SubTest1": {
					Status:   types.TestStatusFail,
					Duration: 50 * time.Millisecond,
					Error:    errors.New("subtest failed"),
				},
			},
		},
	}
}
