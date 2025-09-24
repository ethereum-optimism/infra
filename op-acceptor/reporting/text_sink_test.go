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
			assert.Contains(t, summaryContent, "Test Results Summary")
			assert.Contains(t, summaryContent, "test-run-456")
			assert.Contains(t, summaryContent, "Total Tests: 2")
			assert.Contains(t, summaryContent, "Passed: 1")
			assert.Contains(t, summaryContent, "Failed: 1")
			assert.Contains(t, summaryContent, "test-gate/TestFailing")

			if tt.includeDetails {
				// With details, error information should be included in the hierarchy
				assert.Contains(t, summaryContent, "Error: test failed")
			} else {
				assert.NotContains(t, summaryContent, "Error: test failed")
			}
		})
	}
}
