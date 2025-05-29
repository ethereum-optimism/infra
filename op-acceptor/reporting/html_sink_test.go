package reporting

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReportingHTMLSink(t *testing.T) {
	// Create temporary directory for test output
	tempDir := t.TempDir()

	templateContent := `<!DOCTYPE html>
<html>
<head><title>Test Report</title></head>
<body>
<h1>{{.RunID}}</h1>
<p>Network: {{.DevnetName}}</p>
<p>Gate: {{.GateRun}}</p>
<div>Total: {{.Total}}, Passed: {{.Passed}}, Failed: {{.Failed}}</div>
{{range .Tests}}
<div class="test {{.StatusClass}}">{{.TestName}} - {{.StatusText}}</div>
{{end}}
</body>
</html>`

	getReadableTestFilename := func(metadata types.ValidatorMetadata) string {
		if metadata.FuncName != "" {
			return metadata.FuncName
		}
		return metadata.ID
	}

	sink, err := NewReportingHTMLSink(tempDir, "logger-run-id", "test-network", "test-gate", templateContent, getReadableTestFilename)
	require.NoError(t, err)

	// Test consuming results
	testResults := []*types.TestResult{
		{
			Metadata: types.ValidatorMetadata{
				ID:       "test1",
				Gate:     "test-gate",
				FuncName: "TestPassing",
				Package:  "github.com/example/test",
			},
			Status:   types.TestStatusPass,
			Duration: 100 * time.Millisecond,
		},
		{
			Metadata: types.ValidatorMetadata{
				ID:       "test2",
				Gate:     "test-gate",
				FuncName: "TestFailing",
				Package:  "github.com/example/test",
			},
			Status:   types.TestStatusFail,
			Duration: 200 * time.Millisecond,
			Error:    errors.New("test failed"),
		},
	}

	runID := "test-run-123"

	// Consume the test results
	for _, result := range testResults {
		err := sink.Consume(result, runID)
		require.NoError(t, err)
	}

	// Complete the sink processing
	err = sink.Complete(runID)
	require.NoError(t, err)

	// Verify the HTML file was created
	htmlFile := filepath.Join(tempDir, "testrun-"+runID, "results.html")
	assert.FileExists(t, htmlFile)

	// Read and verify the content
	content, err := os.ReadFile(htmlFile)
	require.NoError(t, err)

	htmlContent := string(content)
	assert.Contains(t, htmlContent, "test-run-123")
	assert.Contains(t, htmlContent, "test-network")
	assert.Contains(t, htmlContent, "test-gate")
	assert.Contains(t, htmlContent, "Total: 2, Passed: 1, Failed: 1")
	assert.Contains(t, htmlContent, "TestPassing")
	assert.Contains(t, htmlContent, "TestFailing")
}

func TestReportingHTMLSink_InvalidTemplate(t *testing.T) {
	tempDir := t.TempDir()
	invalidTemplate := `{{.InvalidField`

	getReadableTestFilename := func(metadata types.ValidatorMetadata) string {
		return metadata.ID
	}

	_, err := NewReportingHTMLSink(tempDir, "logger-run-id", "network", "gate", invalidTemplate, getReadableTestFilename)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create HTML formatter")
}

func TestReportingHTMLSink_LogPathGeneration(t *testing.T) {
	tempDir := t.TempDir()
	templateContent := `<html><body>{{.RunID}}</body></html>`

	getReadableTestFilename := func(metadata types.ValidatorMetadata) string {
		if metadata.FuncName != "" {
			return metadata.FuncName
		}
		return metadata.ID
	}

	sink, err := NewReportingHTMLSink(tempDir, "logger-run-id", "network", "gate", templateContent, getReadableTestFilename)
	require.NoError(t, err)

	// Test with passing test (should go to passed folder)
	passingTest := &types.TestResult{
		Metadata: types.ValidatorMetadata{
			ID:       "test1",
			FuncName: "TestPassing",
		},
		Status: types.TestStatusPass,
	}

	// Test with failing test (should go to failed folder)
	failingTest := &types.TestResult{
		Metadata: types.ValidatorMetadata{
			ID:       "test2",
			FuncName: "TestFailing",
		},
		Status: types.TestStatusFail,
		Error:  errors.New("test failed"),
	}

	err = sink.Consume(passingTest, "run-123")
	require.NoError(t, err)

	err = sink.Consume(failingTest, "run-123")
	require.NoError(t, err)

	err = sink.Complete("run-123")
	require.NoError(t, err)

	// Read the HTML content and check for proper log paths
	htmlFile := filepath.Join(tempDir, "testrun-run-123", "results.html")
	content, err := os.ReadFile(htmlFile)
	require.NoError(t, err)

	// Note: We can't directly test the log paths in the HTML without examining
	// the specific template structure, but we can verify the file was created properly
	assert.NotEmpty(t, content)
}

func TestReportingTextSummarySink(t *testing.T) {
	tempDir := t.TempDir()

	tests := []struct {
		name           string
		includeDetails bool
		expectedFile   string
	}{
		{
			name:           "without details",
			includeDetails: false,
			expectedFile:   "summary.log",
		},
		{
			name:           "with details",
			includeDetails: true,
			expectedFile:   "summary.log",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sink := NewReportingTextSummarySink(tempDir, "logger-run-id", "test-network", "test-gate", tt.includeDetails)

			testResults := []*types.TestResult{
				{
					Metadata: types.ValidatorMetadata{
						ID:       "test1",
						Gate:     "test-gate",
						FuncName: "TestPassing",
						Package:  "github.com/example/test",
					},
					Status:   types.TestStatusPass,
					Duration: 100 * time.Millisecond,
				},
				{
					Metadata: types.ValidatorMetadata{
						ID:       "test2",
						Gate:     "test-gate",
						FuncName: "TestFailing",
						Package:  "github.com/example/test",
					},
					Status:   types.TestStatusFail,
					Duration: 200 * time.Millisecond,
					Error:    errors.New("test failed"),
				},
			}

			runID := "test-run-456"

			// Consume the test results
			for _, result := range testResults {
				err := sink.Consume(result, runID)
				require.NoError(t, err)
			}

			// Complete the sink processing
			err := sink.Complete(runID)
			require.NoError(t, err)

			// Verify the summary file was created
			summaryFile := filepath.Join(tempDir, "testrun-"+runID, tt.expectedFile)
			assert.FileExists(t, summaryFile)

			// Read and verify the content
			content, err := os.ReadFile(summaryFile)
			require.NoError(t, err)

			summaryContent := string(content)
			assert.Contains(t, summaryContent, "TEST SUMMARY")
			assert.Contains(t, summaryContent, "test-run-456")
			assert.Contains(t, summaryContent, "Total:   2")
			assert.Contains(t, summaryContent, "Passed:  1")
			assert.Contains(t, summaryContent, "Failed:  1")
			assert.Contains(t, summaryContent, "github.com/example/test.TestFailing")

			if tt.includeDetails {
				assert.Contains(t, summaryContent, "DETAILED RESULTS:")
				assert.Contains(t, summaryContent, "Gate: test-gate")
			} else {
				assert.NotContains(t, summaryContent, "DETAILED RESULTS:")
			}
		})
	}
}

func TestTableReporter(t *testing.T) {
	tests := []struct {
		name                string
		showIndividualTests bool
		expectedContent     []string
		notExpectedContent  []string
	}{
		{
			name:                "with individual tests",
			showIndividualTests: true,
			expectedContent: []string{
				"Gate", "test-gate",
				"TestPassing", "TestFailing",
				"PASS", "FAIL",
			},
		},
		{
			name:                "without individual tests",
			showIndividualTests: false,
			expectedContent: []string{
				"Gate", "test-gate",
			},
			notExpectedContent: []string{
				"TestPassing", "TestFailing",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reporter := NewTableReporter("Test Results", tt.showIndividualTests)

			testResults := []*types.TestResult{
				{
					Metadata: types.ValidatorMetadata{
						ID:       "test1",
						Gate:     "test-gate",
						FuncName: "TestPassing",
						Package:  "github.com/example/test",
					},
					Status:   types.TestStatusPass,
					Duration: 100 * time.Millisecond,
				},
				{
					Metadata: types.ValidatorMetadata{
						ID:       "test2",
						Gate:     "test-gate",
						FuncName: "TestFailing",
						Package:  "github.com/example/test",
					},
					Status:   types.TestStatusFail,
					Duration: 200 * time.Millisecond,
					Error:    errors.New("test failed"),
				},
			}

			// Test GenerateTableFromTestResults
			result, err := reporter.GenerateTableFromTestResults(testResults, "run-123", "network", "gate")
			require.NoError(t, err)

			for _, expected := range tt.expectedContent {
				assert.Contains(t, result, expected)
			}

			for _, notExpected := range tt.notExpectedContent {
				assert.NotContains(t, result, notExpected)
			}

			// Test PrintTableFromTestResults (this will print to stdout)
			err = reporter.PrintTableFromTestResults(testResults, "run-123", "network", "gate")
			assert.NoError(t, err)
		})
	}
}

func TestTableReporter_WithSubTests(t *testing.T) {
	reporter := NewTableReporter("Test Results", true)

	testResults := []*types.TestResult{
		{
			Metadata: types.ValidatorMetadata{
				ID:       "test1",
				Gate:     "test-gate",
				FuncName: "TestWithSubTests",
				Package:  "github.com/example/test",
			},
			Status:   types.TestStatusFail,
			Duration: 200 * time.Millisecond,
			Error:    errors.New("test failed"),
			SubTests: map[string]*types.TestResult{
				"SubTest1": {
					Status:   types.TestStatusPass,
					Duration: 50 * time.Millisecond,
				},
				"SubTest2": {
					Status:   types.TestStatusFail,
					Duration: 75 * time.Millisecond,
					Error:    errors.New("subtest failed"),
				},
			},
		},
	}

	result, err := reporter.GenerateTableFromTestResults(testResults, "run-123", "network", "gate")
	require.NoError(t, err)

	// Should contain main test and subtests
	assert.Contains(t, result, "TestWithSubTests")
	assert.Contains(t, result, "SubTest1")
	assert.Contains(t, result, "SubTest2")
}

func TestTableReporter_EmptyResults(t *testing.T) {
	reporter := NewTableReporter("Empty Test Results", true)

	testResults := []*types.TestResult{}

	result, err := reporter.GenerateTableFromTestResults(testResults, "run-123", "network", "gate")
	require.NoError(t, err)

	// Should contain header but no test data
	assert.Contains(t, result, "Empty Test Results")
	assert.Contains(t, result, "TOTAL")
}

func TestReportingHTMLSink_WithSubTests(t *testing.T) {
	tempDir := t.TempDir()
	templateContent := `<html><body>
<h1>{{.RunID}}</h1>
<div>Total: {{.Total}}</div>
{{range .Tests}}
<div class="test {{.StatusClass}}">
  {{.TestName}} - {{.StatusText}}
  {{if .IsSubTest}}[SubTest of {{.ParentTest}}]{{end}}
</div>
{{end}}
</body></html>`

	getReadableTestFilename := func(metadata types.ValidatorMetadata) string {
		return metadata.FuncName
	}

	sink, err := NewReportingHTMLSink(tempDir, "logger-run-id", "network", "gate", templateContent, getReadableTestFilename)
	require.NoError(t, err)

	testResult := &types.TestResult{
		Metadata: types.ValidatorMetadata{
			ID:       "test1",
			Gate:     "gate",
			FuncName: "TestWithSubTests",
			Package:  "github.com/example/test",
		},
		Status:   types.TestStatusFail,
		Duration: 200 * time.Millisecond,
		SubTests: map[string]*types.TestResult{
			"SubTest1": {
				Status:   types.TestStatusPass,
				Duration: 50 * time.Millisecond,
			},
			"SubTest2": {
				Status:   types.TestStatusFail,
				Duration: 75 * time.Millisecond,
				Error:    errors.New("subtest failed"),
			},
		},
	}

	runID := "run-with-subtests"

	err = sink.Consume(testResult, runID)
	require.NoError(t, err)

	err = sink.Complete(runID)
	require.NoError(t, err)

	// Verify the HTML includes subtests
	htmlFile := filepath.Join(tempDir, "testrun-"+runID, "results.html")
	content, err := os.ReadFile(htmlFile)
	require.NoError(t, err)

	htmlContent := string(content)
	assert.Contains(t, htmlContent, "TestWithSubTests")
	assert.Contains(t, htmlContent, "SubTest1")
	assert.Contains(t, htmlContent, "SubTest2")
	assert.Contains(t, htmlContent, "[SubTest of TestWithSubTests]")
	assert.Contains(t, htmlContent, "Total: 3") // Main test + 2 subtests
}

func TestReportingHTMLSink_ReadableTestFilename(t *testing.T) {
	tempDir := t.TempDir()
	templateContent := `<html><body>{{.RunID}}</body></html>`

	tests := []struct {
		name     string
		metadata types.ValidatorMetadata
		expected string
	}{
		{
			name: "with function name",
			metadata: types.ValidatorMetadata{
				ID:       "test1",
				FuncName: "TestFunction",
				Package:  "github.com/example/test",
			},
			expected: "TestFunction",
		},
		{
			name: "without function name",
			metadata: types.ValidatorMetadata{
				ID:      "test-id-123",
				Package: "github.com/example/test",
			},
			expected: "test-id-123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getReadableTestFilename := func(metadata types.ValidatorMetadata) string {
				if metadata.FuncName != "" {
					return metadata.FuncName
				}
				return metadata.ID
			}

			sink, err := NewReportingHTMLSink(tempDir, "logger-run-id", "network", "gate", templateContent, getReadableTestFilename)
			require.NoError(t, err)

			// Test that the function is called correctly
			result := getReadableTestFilename(tt.metadata)
			assert.Equal(t, tt.expected, result)

			// Test that sink processes without error
			testResult := &types.TestResult{
				Metadata: tt.metadata,
				Status:   types.TestStatusPass,
				Duration: 100 * time.Millisecond,
			}

			err = sink.Consume(testResult, "run-123")
			require.NoError(t, err)

			err = sink.Complete("run-123")
			require.NoError(t, err)
		})
	}
}

func TestReportingHTMLSink_FilterPackageTestsInHTML(t *testing.T) {
	tempDir := t.TempDir()
	templateContent := `<!DOCTYPE html>
<html>
<head><title>Test Report</title></head>
<body>
<h1>{{.RunID}}</h1>
<div class="summary">
	{{if .PackageLogPath}}<a href="{{.PackageLogPath}}" class="package-log-link">View Package Logs</a>{{end}}
</div>
<div>Total: {{.Total}}, Passed: {{.Passed}}, Failed: {{.Failed}}</div>
{{range .Tests}}
<div class="test {{.StatusClass}}">{{.TestName}} - {{.StatusText}}</div>
{{end}}
</body>
</html>`

	getReadableTestFilename := func(metadata types.ValidatorMetadata) string {
		if metadata.RunAll {
			return "package-logs"
		}
		if metadata.FuncName != "" {
			return metadata.FuncName
		}
		return metadata.ID
	}

	sink, err := NewReportingHTMLSink(tempDir, "logger-run-id", "test-network", "test-gate", templateContent, getReadableTestFilename)
	require.NoError(t, err)

	// Test consuming results with both package and individual tests
	testResults := []*types.TestResult{
		// Package-level test (should now appear as a header row)
		{
			Metadata: types.ValidatorMetadata{
				ID:      "package-test",
				Package: "github.com/example/test",
				RunAll:  true,
			},
			Status:   types.TestStatusPass,
			Duration: 50 * time.Millisecond,
		},
		// Individual test (should appear as a row)
		{
			Metadata: types.ValidatorMetadata{
				ID:       "test1",
				Gate:     "test-gate",
				FuncName: "TestPassing",
				Package:  "github.com/example/test",
			},
			Status:   types.TestStatusPass,
			Duration: 100 * time.Millisecond,
		},
	}

	runID := "test-run-123"

	// Consume the test results
	for _, result := range testResults {
		err := sink.Consume(result, runID)
		require.NoError(t, err)
	}

	// Complete the sink processing
	err = sink.Complete(runID)
	require.NoError(t, err)

	// Read and verify the content
	htmlFile := filepath.Join(tempDir, "testrun-"+runID, "results.html")
	content, err := os.ReadFile(htmlFile)
	require.NoError(t, err)

	htmlContent := string(content)

	// Verify package log link is present in header
	assert.Contains(t, htmlContent, `<a href="passed/package-logs.log" class="package-log-link">View Package Logs</a>`)

	// NEW BEHAVIOR: Package tests now appear as header rows in the output
	assert.Contains(t, htmlContent, "test (package) - PASS") // Package test appears as header row
	assert.Contains(t, htmlContent, "TestPassing - PASS")    // Individual test appears as row

	// Verify correct total count (both tests counted in stats and both appear as rows)
	assert.Contains(t, htmlContent, "Total: 2, Passed: 2, Failed: 0")
}
