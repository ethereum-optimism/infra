package reporting

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHTMLSinkGeneratesCorrectFileLinks verifies that HTML links match actual files created
func TestHTMLSinkGeneratesCorrectFileLinks(t *testing.T) {
	tempDir := t.TempDir()

	// Simple template that includes test links
	// TestTree has Root field which contains TestTreeNode with Children
	// We need to handle nested subtests properly
	templateContent := `<html>
<body>
{{if .TestNodes}}
  {{range .TestNodes}}
    {{if .LogPath}}
      <a href="{{.LogPath}}">{{.Name}}</a>
    {{end}}
  {{end}}
{{end}}
</body>
</html>`

	// Mock the getReadableTestFilename function to match actual implementation
	getReadableTestFilename := func(metadata types.ValidatorMetadata) string {
		// This mimics the actual getReadableTestFilename logic
		fileName := ""
		if metadata.Gate != "" {
			fileName = metadata.Gate
		} else {
			fileName = "gateless"
		}

		// Add package last part
		if metadata.Package != "" {
			parts := strings.Split(metadata.Package, "/")
			for i := len(parts) - 1; i >= 0; i-- {
				if parts[i] != "" && parts[i] != "." {
					fileName = fileName + "_" + parts[i]
					break
				}
			}
			if metadata.Package == "." || metadata.Package == "" {
				fileName = fileName + "_base"
			}
		}

		// Replace slashes in test names with underscores
		if metadata.FuncName != "" {
			funcName := strings.ReplaceAll(metadata.FuncName, "/", "_")
			fileName = fileName + "_" + funcName
		}

		return fileName
	}

	jsContent := []byte(`console.log("test");`)
	sink, err := NewReportingHTMLSink(tempDir, "test-run", "network", "gate", templateContent, jsContent, getReadableTestFilename)
	require.NoError(t, err)

	// Create test results with subtests that match the real scenario
	testResults := []*types.TestResult{
		// Parent test: TestRPCConnectivity
		{
			Metadata: types.ValidatorMetadata{
				ID:       "rpc-connectivity",
				Gate:     "gateless",
				FuncName: "TestRPCConnectivity",
				Package:  "./base",
			},
			Status:   types.TestStatusPass,
			Duration: 3 * time.Second,
			SubTests: map[string]*types.TestResult{
				"TestRPCConnectivity/L2_Chain_L2Network-901": {
					Metadata: types.ValidatorMetadata{
						FuncName: "TestRPCConnectivity/L2_Chain_L2Network-901",
						Package:  "./base",
						Gate:     "gateless",
					},
					Status: types.TestStatusPass,
					SubTests: map[string]*types.TestResult{
						"TestRPCConnectivity/L2_Chain_L2Network-901/L2EL_Node_L2ELNode-sequencer-901": {
							Metadata: types.ValidatorMetadata{
								FuncName: "TestRPCConnectivity/L2_Chain_L2Network-901/L2EL_Node_L2ELNode-sequencer-901",
								Package:  "./base",
								Gate:     "gateless",
							},
							Status: types.TestStatusPass,
						},
					},
				},
			},
		},
		// Parent-level test
		{
			Metadata: types.ValidatorMetadata{
				ID:       "faucet-fund",
				Gate:     "gateless",
				FuncName: "TestFaucetFund",
				Package:  "./base",
			},
			Status:   types.TestStatusPass,
			Duration: 2 * time.Second,
		},
		// Package-level test
		{
			Metadata: types.ValidatorMetadata{
				ID:      "./base",
				Gate:    "gateless",
				Package: "./base",
				RunAll:  true,
			},
			Status:   types.TestStatusPass,
			Duration: 5 * time.Second,
		},
	}

	// Process the results
	runID := "test-run"
	for _, result := range testResults {
		err = sink.Consume(result, runID)
		require.NoError(t, err)
	}

	// Complete the sink to generate HTML
	err = sink.CompleteWithTiming(runID, 10*time.Second)
	require.NoError(t, err)

	// Read the generated HTML
	htmlFile := filepath.Join(tempDir, "testrun-"+runID, "results.html")
	htmlContent, err := os.ReadFile(htmlFile)
	require.NoError(t, err)

	htmlStr := string(htmlContent)
	t.Logf("Generated HTML (first 1000 chars):\n%.1000s", htmlStr)

	// First, create the actual files to match what op-acceptor creates
	actualFiles := map[string]bool{
		"passed/gateless_base.txt":                                                                             true,
		"passed/gateless_base_TestFaucetFund.txt":                                                              true,
		"passed/gateless_base_TestRPCConnectivity.txt":                                                         true,
		"passed/gateless_base_TestRPCConnectivity_L2_Chain_L2Network-901.txt":                                  true,
		"passed/gateless_base_TestRPCConnectivity_L2_Chain_L2Network-901_L2EL_Node_L2ELNode-sequencer-901.txt": true,
	}

	// Create dummy files to simulate actual file creation
	passedDir := filepath.Join(tempDir, "testrun-"+runID, "passed")
	err = os.MkdirAll(passedDir, 0755)
	require.NoError(t, err)

	for filename := range actualFiles {
		filePath := filepath.Join(tempDir, "testrun-"+runID, filename)
		err = os.WriteFile(filePath, []byte("test content"), 0644)
		require.NoError(t, err)
	}

	// Verify correct links are generated and match actual files
	// Note: The simple test template only shows TestNodes which is a flat list,
	// so deeply nested subtests might not appear in the test HTML
	expectedLinks := []string{
		// Package test - should be gateless_base.txt, not gateless_base_base.txt
		"passed/gateless_base.txt",
		// Parent tests
		"passed/gateless_base_TestFaucetFund.txt",
		"passed/gateless_base_TestRPCConnectivity.txt",
		// First-level subtest with parent test name included
		"passed/gateless_base_TestRPCConnectivity_L2_Chain_L2Network-901.txt",
		// Deep nested subtest might not appear in TestNodes flat list
		// "passed/gateless_base_TestRPCConnectivity_L2_Chain_L2Network-901_L2EL_Node_L2ELNode-sequencer-901.txt",
	}

	// Extract all links from HTML
	linkPattern := regexp.MustCompile(`href="(passed/[^"]+\.txt)"`)
	matches := linkPattern.FindAllStringSubmatch(htmlStr, -1)

	// Collect all found links
	foundLinks := make(map[string]bool)
	for _, match := range matches {
		if len(match) > 1 {
			foundLinks[match[1]] = true
		}
	}

	// Verify all expected links are present
	for _, expectedLink := range expectedLinks {
		assert.True(t, foundLinks[expectedLink],
			"HTML should contain correct link: %s", expectedLink)
	}

	// Verify no incorrect links are generated
	incorrectLinks := []string{
		// Should not have doubled package name
		"passed/gateless_base_base.txt",
		// Should not have subtests without parent test name
		"passed/gateless_base_L2_Chain_L2Network-901.txt",
		"passed/gateless_base_L2_Chain_L2Network-901_L2EL_Node_L2ELNode-sequencer-901.txt",
	}

	for _, incorrectLink := range incorrectLinks {
		assert.False(t, foundLinks[incorrectLink],
			"HTML should NOT contain incorrect link: %s", incorrectLink)
	}

	// Verify all generated links correspond to actual files
	for link := range foundLinks {
		assert.True(t, actualFiles[link],
			"Generated link %s should correspond to an actual file", link)
	}
}

// TestExtractTestNameFromParent verifies the helper function works correctly
func TestExtractTestNameFromParent(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Simple test name",
			input:    "TestRPCConnectivity",
			expected: "TestRPCConnectivity",
		},
		{
			name:     "Subtest with one level",
			input:    "TestRPCConnectivity/L2_Chain",
			expected: "TestRPCConnectivity",
		},
		{
			name:     "Subtest with multiple levels",
			input:    "TestRPCConnectivity/L2_Chain/L2EL_Node",
			expected: "TestRPCConnectivity",
		},
		{
			name:     "Empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := extractTestNameFromParent(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}
