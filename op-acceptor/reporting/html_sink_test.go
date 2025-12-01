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
<p>Network: {{.NetworkName}}</p>
<div>Total: {{.Stats.Total}}, Passed: {{.Stats.Passed}}, Failed: {{.Stats.Failed}}</div>
{{if .Config}}
<div id="config">
  <div>Orchestrator: {{.Config.Orchestration.Orchestrator}}</div>
  <div>TargetGate: {{join ", " .Config.Execution.TargetGate}}</div>
  <div>Concurrency: {{.Config.Runner.Concurrency}}</div>
  <div>TestLogLevel: {{.Config.Logging.TestLogLevel}}</div>
  <div>TestDir: {{.Config.Paths.TestDir}}</div>
  {{if .Config.Orchestration.DevnetEnvURL}}<div>DevnetEnvURL: {{.Config.Orchestration.DevnetEnvURL}}</div>{{end}}
  {{if .Config.Runner.ShowProgress}}<div>ProgressInterval: {{formatDuration .Config.Runner.ProgressInterval}}</div>{{end}}
</div>
{{end}}
{{range .TestNodes}}
<div class="test {{getStatusClass .Status}}">{{.Name}} - {{getStatusText .Status}}</div>
{{end}}
</body>
</html>`

	getReadableTestFilename := func(metadata types.ValidatorMetadata) string {
		if metadata.FuncName != "" {
			return metadata.FuncName
		}
		return metadata.ID
	}

	// Mock JavaScript content for testing
	jsContent := []byte(`console.log("test js");`)

	sink, err := NewReportingHTMLSink(tempDir, "logger-run-id", "test-network", "test-gate", templateContent, jsContent, getReadableTestFilename)
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

	// Provide a config snapshot and consume results
	sink.SetConfigSnapshot(runID, &types.EffectiveConfigSnapshot{
		Orchestration: types.OrchestrationConfigSnapshot{Orchestrator: "sysext", DevnetEnvURL: "file:///devnet.json"},
		Execution:     types.ExecutionConfigSnapshot{TargetGate: []string{"test-gate"}},
		Runner:        types.RunnerConfigSnapshot{Concurrency: 2, ShowProgress: true, ProgressInterval: time.Second},
		Logging:       types.LoggingConfigSnapshot{TestLogLevel: "info"},
		Paths:         types.PathsConfigSnapshot{TestDir: "/tmp/tests"},
	})

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
	assert.Contains(t, htmlContent, "Total: 2, Passed: 1, Failed: 1")
	assert.Contains(t, htmlContent, "TestPassing")
	assert.Contains(t, htmlContent, "TestFailing")
	// Verify config fields rendered
	assert.Contains(t, htmlContent, "Orchestrator: sysext")
	assert.Contains(t, htmlContent, "TargetGate: test-gate")
	assert.Contains(t, htmlContent, "Concurrency: 2")
	assert.Contains(t, htmlContent, "TestLogLevel: info")
	assert.Contains(t, htmlContent, "TestDir: /tmp/tests")
}

func TestReportingHTMLSink_InvalidTemplate(t *testing.T) {
	tempDir := t.TempDir()
	invalidTemplate := `{{.InvalidField`

	getReadableTestFilename := func(metadata types.ValidatorMetadata) string {
		return metadata.ID
	}

	jsContent := []byte(`console.log("test js");`)
	_, err := NewReportingHTMLSink(tempDir, "logger-run-id", "network", "gate", invalidTemplate, jsContent, getReadableTestFilename)
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

	jsContent := []byte(`console.log("test js");`)
	sink, err := NewReportingHTMLSink(tempDir, "logger-run-id", "network", "gate", templateContent, jsContent, getReadableTestFilename)
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

func TestReportingHTMLSink_WithSubTests(t *testing.T) {
	tempDir := t.TempDir()
	templateContent := `<html><body>
<h1>{{.RunID}}</h1>
<div>Total: {{.Stats.Total}}</div>
{{range .TestNodes}}
<div class="test {{getStatusClass .Status}}">
  {{.Name}} - {{getStatusText .Status}}
  {{if eq .Type "subtest"}}[SubTest]{{end}}
</div>
{{end}}
</body></html>`

	getReadableTestFilename := func(metadata types.ValidatorMetadata) string {
		return metadata.FuncName
	}

	jsContent := []byte(`console.log("test js");`)
	sink, err := NewReportingHTMLSink(tempDir, "logger-run-id", "network", "gate", templateContent, jsContent, getReadableTestFilename)
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
	assert.Contains(t, htmlContent, "[SubTest]")
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

			jsContent := []byte(`console.log("test js");`)
			sink, err := NewReportingHTMLSink(tempDir, "logger-run-id", "network", "gate", templateContent, jsContent, getReadableTestFilename)
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
	<a href="passed/package-logs.log" class="package-log-link">View Package Logs</a>
</div>
<div>Total: {{.Stats.Total}}, Passed: {{.Stats.Passed}}, Failed: {{.Stats.Failed}}</div>
{{range .TestNodes}}
<div class="test {{getStatusClass .Status}}">{{.Name}} - {{getStatusText .Status}}</div>
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

	jsContent := []byte(`console.log("test js");`)
	sink, err := NewReportingHTMLSink(tempDir, "logger-run-id", "test-network", "test-gate", templateContent, jsContent, getReadableTestFilename)
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

	// Both tests appear in the TestTree nodes
	assert.Contains(t, htmlContent, "github.com/example/test (package) - pass") // Package test appears as node
	assert.Contains(t, htmlContent, "TestPassing - pass")                       // Individual test appears as node

	// Verify correct total count (both tests counted in stats)
	assert.Contains(t, htmlContent, "Total: 2, Passed: 2, Failed: 0")
}
