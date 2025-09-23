package logging

import (
	"testing"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/stretchr/testify/assert"
)

// TestGetReadableTestFilenameWithDotPackage verifies that "." package is handled correctly
func TestGetReadableTestFilenameWithDotPackage(t *testing.T) {
	testCases := []struct {
		name     string
		metadata types.ValidatorMetadata
		expected string
	}{
		{
			name: "Individual test with dot package",
			metadata: types.ValidatorMetadata{
				Gate:     "gateless",
				Package:  ".",
				FuncName: "TestFaucetFund",
			},
			expected: "gateless_TestFaucetFund", // No package prefix for "."
		},
		{
			name: "Package test with dot package",
			metadata: types.ValidatorMetadata{
				Gate:    "gateless",
				Package: ".",
				RunAll:  true,
			},
			expected: "gateless_package", // When RunAll=true and Package=".", it uses "package" as filename
		},
		{
			name: "Subtest with dot package",
			metadata: types.ValidatorMetadata{
				Gate:     "gateless",
				Package:  ".",
				FuncName: "TestRPCConnectivity/L2_Chain_L2Network-901",
			},
			expected: "gateless_TestRPCConnectivity_L2_Chain_L2Network-901", // No package prefix for "."
		},
		{
			name: "Test with normal package",
			metadata: types.ValidatorMetadata{
				Gate:     "gateless",
				Package:  "github.com/example/package",
				FuncName: "TestExample",
			},
			expected: "gateless_package_TestExample",
		},
		{
			name: "Test with base package explicitly",
			metadata: types.ValidatorMetadata{
				Gate:     "gateless",
				Package:  "base",
				FuncName: "TestFaucetFund",
			},
			expected: "gateless_base_TestFaucetFund",
		},
		{
			name: "Test with empty package (fallback to base)",
			metadata: types.ValidatorMetadata{
				Gate:     "gateless",
				Package:  "",
				FuncName: "TestSomething",
			},
			expected: "gateless_TestSomething",
		},
		{
			name: "Package test with dot package and suite",
			metadata: types.ValidatorMetadata{
				Gate:    "isthmus",
				Suite:   "acceptance",
				Package: ".",
				RunAll:  true,
			},
			expected: "isthmus-acceptance_package", // With "." package, uses "package" as filename
		},
		{
			name: "Test with ./base package",
			metadata: types.ValidatorMetadata{
				Gate:     "gateless",
				Package:  "./base",
				FuncName: "TestFaucetFund",
			},
			expected: "gateless_base_TestFaucetFund",
		},
		{
			name: "Test with ./isthmus package",
			metadata: types.ValidatorMetadata{
				Gate:     "fjord",
				Package:  "./isthmus",
				FuncName: "TestDeployment",
			},
			expected: "fjord_isthmus_TestDeployment",
		},
		{
			name: "Package test with ./base",
			metadata: types.ValidatorMetadata{
				Gate:    "gateless",
				Package: "./base",
				RunAll:  true,
			},
			expected: "gateless_base",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := getReadableTestFilename(tc.metadata)
			assert.Equal(t, tc.expected, actual, "Filename should match expected for %s", tc.name)
		})
	}
}

// TestHTMLLinksMatchActualFiles verifies that HTML links match the actual file naming pattern
func TestHTMLLinksMatchActualFiles(t *testing.T) {
	// Test cases based on actual files observed in the test run
	testCases := []struct {
		name         string
		metadata     types.ValidatorMetadata
		expectedFile string
		description  string
	}{
		{
			name: "Package test file",
			metadata: types.ValidatorMetadata{
				Gate:    "gateless",
				Package: ".",
				RunAll:  true,
			},
			expectedFile: "gateless_package.txt",
			description:  "Package test with '.' should generate gateless_package.txt",
		},
		{
			name: "Individual test file",
			metadata: types.ValidatorMetadata{
				Gate:     "gateless",
				Package:  ".",
				FuncName: "TestFaucetFund",
			},
			expectedFile: "gateless_TestFaucetFund.txt",
			description:  "Individual test with '.' package should generate gateless_TestFaucetFund.txt",
		},
		{
			name: "Subtest file",
			metadata: types.ValidatorMetadata{
				Gate:     "gateless",
				Package:  ".",
				FuncName: "TestRPCConnectivity/L2_Chain_L2Network-901",
			},
			expectedFile: "gateless_TestRPCConnectivity_L2_Chain_L2Network-901.txt",
			description:  "Subtest with '.' package should include parent test name",
		},
		{
			name: "Deep nested subtest",
			metadata: types.ValidatorMetadata{
				Gate:     "gateless",
				Package:  ".",
				FuncName: "TestRPCConnectivity/L2_Chain_L2Network-901/L2EL_Node_L2ELNode-sequencer-901",
			},
			expectedFile: "gateless_TestRPCConnectivity_L2_Chain_L2Network-901_L2EL_Node_L2ELNode-sequencer-901.txt",
			description:  "Deep nested subtest should have all parts separated by underscores",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			filename := getReadableTestFilename(tc.metadata)
			actualFile := filename + ".txt"
			assert.Equal(t, tc.expectedFile, actualFile, tc.description)
		})
	}
}
