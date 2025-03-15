package main_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestExitCodeBehavior verifies that op-acceptor returns the correct exit codes in run-once mode:
// - Exit code 0 when all tests pass
// - Exit code 1 when any tests fail
// - Exit code 2 when there's a runtime error (panic)
func TestExitCodeBehavior(t *testing.T) {
	// Setup paths
	cwd, err := os.Getwd()
	require.NoError(t, err, "Failed to get current working directory")

	projectRoot := filepath.Dir(cwd)

	// Binary path
	opAcceptorBin := filepath.Join(projectRoot, "bin", "op-acceptor")

	// Build the binary if it doesn't exist
	if !fileExists(opAcceptorBin) {
		t.Logf("Building op-acceptor binary...")

		// Create bin directory if needed
		err = os.MkdirAll(filepath.Dir(opAcceptorBin), 0755)
		require.NoError(t, err, "Failed to create directory for binary")

		// Build the binary
		buildCmd := exec.Command("go", "build", "-o", opAcceptorBin, filepath.Join(projectRoot, "cmd"))
		var buildOutput bytes.Buffer
		buildCmd.Stdout = &buildOutput
		buildCmd.Stderr = &buildOutput

		err = buildCmd.Run()
		if err != nil {
			t.Logf("Build output:\n%s", buildOutput.String())
			t.Fatalf("Failed to build op-acceptor binary: %v", err)
		}

		t.Logf("Successfully built binary at %s", opAcceptorBin)
	}

	// Verify binary exists
	require.FileExists(t, opAcceptorBin, "op-acceptor binary not found")

	// Define test cases
	testCases := []struct {
		name           string
		setupFunc      func(t *testing.T, testDir string) (gateID string, validatorPath string, inputTestDir string) // Function to set up test environment, returns gate and validator config
		expectedStatus int                                                                                           // Expected exit code
	}{
		{
			name: "Passing tests should exit with code 0",
			setupFunc: func(t *testing.T, testDir string) (string, string, string) {
				packageName := "passing"
				testName := "TestAlwaysPasses"
				gateID := "test-gate-passes"

				// Create a simple passing test
				createMockGoMod(t, testDir)
				createMockTest(t, testDir, packageName, "passing_test.go", true)
				validatorPath := createMockValidatorConfig(t, testDir, packageName, testName, gateID)

				return gateID, validatorPath, testDir
			},
			expectedStatus: 0,
		},
		{
			name: "Failing tests should exit with code 1",
			setupFunc: func(t *testing.T, testDir string) (string, string, string) {
				packageName := "failing"
				testName := "TestAlwaysFails"
				gateID := "test-gate-fails"

				// Create a simple failing test
				createMockGoMod(t, testDir)
				createMockTest(t, testDir, packageName, "failing_test.go", false)
				validatorPath := createMockValidatorConfig(t, testDir, packageName, testName, gateID)

				return gateID, validatorPath, testDir
			},
			expectedStatus: 1,
		},
		{
			name: "Runtime error should exit with code 2",
			setupFunc: func(t *testing.T, testDir string) (string, string, string) {
				gateID := "test-gate-passes"
				nonExistentDir := filepath.Join(testDir, "non-existent-dir")
				testName := "TestDoesNotExist"

				// Create validator config that points to a non-existent directory
				validatorPath := createMockInvalidValidatorConfig(t, testDir, "dummy", testName, gateID)

				return gateID, validatorPath, nonExistentDir
			},
			expectedStatus: 2,
		},
	}

	// Run each test case
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create temporary test directory
			tempDir, err := os.MkdirTemp("/tmp", "op-acceptor-test-")
			require.NoError(t, err, "Failed to create temporary directory")
			defer os.RemoveAll(tempDir)

			// Setup test environment
			gate, validatorPath, testDir := tc.setupFunc(t, tempDir)

			// Run op-acceptor
			exitCode := runOpAcceptor(t, opAcceptorBin, testDir, validatorPath, gate)
			require.Equal(t, tc.expectedStatus, exitCode, "Unexpected exit code")
		})
	}
}

func createMockGoMod(t *testing.T, testDir string) {

	// Create go.mod file for the test module
	goModPath := filepath.Join(testDir, "go.mod")
	goModContent := `module github.com/test/test

go 1.20
`

	require.NoError(t, os.WriteFile(goModPath, []byte(goModContent), 0644))
}

// createMockTest creates a test file that either passes or fails
func createMockTest(t *testing.T, testDir, packageName, filename string, passing bool) string {

	// Create package directory
	packageDir := filepath.Join(testDir, packageName)
	require.NoError(t, os.MkdirAll(packageDir, 0755))

	// Create test file
	testPath := filepath.Join(packageDir, filename)
	var testContent string

	if passing {
		testContent = fmt.Sprintf(`package %s

import (
	"testing"
)

func TestAlwaysPasses(t *testing.T) {
	// This test will always pass
}
`, packageName)
	} else {
		testContent = fmt.Sprintf(`package %s

import (
	"testing"
)

func TestAlwaysFails(t *testing.T) {
	// This test will always fail
	t.Error("This test intentionally fails")
}
`, packageName)
	}

	fmt.Println("Writing test file to", testPath)
	require.NoError(t, os.WriteFile(testPath, []byte(testContent), 0644))

	return packageDir
}

// createMockValidatorConfig creates a validator configuration file
func createMockValidatorConfig(t *testing.T, testDir, packageName, testName, gateID string) string {
	packageDir := filepath.Join(testDir, packageName)

	validatorPath := filepath.Join(testDir, "test-validators.yaml")
	validatorConfig := fmt.Sprintf(`# Test validator configuration file for exit code testing

gates:
  - id: %s
    description: "Test gate for exit code testing"
    suites:
      test-suite:
        description: "Test suite for exit code testing"
        tests:
          - name: %s
            package: %s
`, gateID, testName, packageDir)

	require.NoError(t, os.WriteFile(validatorPath, []byte(validatorConfig), 0644))
	return validatorPath
}

// createMockInvalidValidatorConfig creates a validator configuration file with a non-existent package path
// to simulate a runtime error
func createMockInvalidValidatorConfig(t *testing.T, testDir, nonExistentPath, testName, gateID string) string {
	validatorPath := filepath.Join(testDir, "test-validators.yaml")
	validatorConfig := fmt.Sprintf(`# Test validator configuration file for exit code testing

gates:
  - id: %s
    description: "Test gate for exit code testing"
    suites:
      test-suite:
        description: "Test suite for exit code testing"
        tests:
          - name: %s
            package: %s
`, gateID, testName, nonExistentPath)

	require.NoError(t, os.WriteFile(validatorPath, []byte(validatorConfig), 0644))
	return validatorPath
}

// Helper function to run op-acceptor with given parameters and return the exit code
func runOpAcceptor(t *testing.T, binary, testdir, validators, gate string) int {
	t.Logf("Running op-acceptor with testdir=%s, gate=%s, validators=%s", testdir, gate, validators)

	cmd := exec.Command(binary,
		"--run-interval=0",
		"--gate="+gate,
		"--testdir="+testdir,
		"--validators="+validators)

	err := cmd.Run()
	exitCode := getExitCode(err)

	t.Logf("Exit code: %d", exitCode)

	return exitCode
}

// Helper function to check if a file exists
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Helper function to get the exit code from an exec.ExitError
func getExitCode(err error) int {
	if err == nil {
		return 0
	}

	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}

	return -1 // Unknown error
}
