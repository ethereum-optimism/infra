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
