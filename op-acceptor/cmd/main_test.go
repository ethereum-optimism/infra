package main_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/exitcodes"
	"github.com/stretchr/testify/require"
)

// TestExitCodeBehavior verifies that op-acceptor returns the correct exit codes in run-once mode:
// - Exit code 0 when all tests pass
// - Exit code 1 when any tests fail
// - Exit code 2 when there's a runtime error
func TestExitCodeBehavior(t *testing.T) {
	// Setup paths
	cwd, err := os.Getwd()
	require.NoError(t, err, "Failed to get current working directory")
	projectRoot := filepath.Dir(cwd)

	// Binary path
	opAcceptorBin := filepath.Join(projectRoot, "bin", "op-acceptor")
	ensureBinaryExists(t, projectRoot, opAcceptorBin)

	// Define test cases
	testCases := []struct {
		name           string
		setupFunc      func(t *testing.T, testDir string) (gateID string, validatorPath string, inputTestDir string)
		expectedStatus int
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
				validatorPath := createValidatorConfig(t, testDir, packageName, testName, gateID, false)

				return gateID, validatorPath, testDir
			},
			expectedStatus: exitcodes.Success,
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
				validatorPath := createValidatorConfig(t, testDir, packageName, testName, gateID, false)

				return gateID, validatorPath, testDir
			},
			expectedStatus: exitcodes.TestFailure,
		},
		// {
		// 	// TODO: This fails in CI, but not locally.
		// 	// Investigate if this is a bug in the runtime error handling,
		// 	// or if it's an OS-specific issue.
		// 	// https://github.com/ethereum-optimism/infra/issues/244
		// 	name: "Runtime error should exit with code 2",
		// 	setupFunc: func(t *testing.T, testDir string) (string, string, string) {
		// 		gateID := "test-gate-passes"
		// 		nonExistentDir := filepath.Join(testDir, "non-existent-dir")
		// 		testName := "TestDoesNotExist"

		// 		// Create validator config that points to a non-existent directory
		// 		validatorPath := createValidatorConfig(t, testDir, "dummy", testName, gateID, true)

		// 		return gateID, validatorPath, nonExistentDir
		// 	},
		// 	expectedStatus: exitcodes.RuntimeErr,
		// },
		{
			name: "Test with panic should exit with code 1",
			setupFunc: func(t *testing.T, testDir string) (string, string, string) {
				packageName := "panicking"
				testName := "TestExplicitPanic"
				gateID := "test-gate-panic"

				// Create a test that deliberately panics
				createMockGoMod(t, testDir)
				createMockPanicTest(t, testDir, packageName, "panic_test.go")
				validatorPath := createValidatorConfig(t, testDir, packageName, testName, gateID, false)

				return gateID, validatorPath, testDir
			},
			// Go's test framework catches panics and treats them as test failures (exit code 1)
			// rather than propagating them as runtime errors (exit code 2)
			expectedStatus: exitcodes.TestFailure,
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

// ensureBinaryExists builds the op-acceptor binary if it doesn't exist
func ensureBinaryExists(t *testing.T, projectRoot, binaryPath string) {
	// Build the binary if it doesn't exist
	if !fileExists(binaryPath) {
		t.Logf("Building op-acceptor binary...")

		// Create bin directory if needed
		err := os.MkdirAll(filepath.Dir(binaryPath), 0755)
		require.NoError(t, err, "Failed to create directory for binary")

		// Build the binary
		buildCmd := exec.Command("go", "build", "-o", binaryPath, filepath.Join(projectRoot, "cmd"))
		var buildOutput bytes.Buffer
		buildCmd.Stdout = &buildOutput
		buildCmd.Stderr = &buildOutput

		err = buildCmd.Run()
		if err != nil {
			t.Logf("Build output:\n%s", buildOutput.String())
			t.Fatalf("Failed to build op-acceptor binary: %v", err)
		}

		t.Logf("Successfully built binary at %s", binaryPath)
	}

	// Verify binary exists
	require.FileExists(t, binaryPath, "op-acceptor binary not found")
}

func createMockGoMod(t *testing.T, testDir string) {
	// Initialize the module with go mod init
	cmd := exec.Command("go", "mod", "init", "test")
	cmd.Dir = testDir
	require.NoError(t, cmd.Run(), "Failed to initialize module")
}

// createMockTest creates a test file that either passes or fails
func createMockTest(t *testing.T, testDir, packageName, filename string, passing bool) string {
	// Create package directory
	packageDir := filepath.Join(testDir, packageName)
	require.NoError(t, os.MkdirAll(packageDir, 0755))

	// Create test file
	testPath := filepath.Join(packageDir, filename)

	// Use a template approach for test content
	testTemplate := `package %s

import "testing"

func %s(t *testing.T) {
	%s
}
`

	var testName, testBody string
	if passing {
		testName = "TestAlwaysPasses"
		testBody = "// This test will always pass"
	} else {
		testName = "TestAlwaysFails"
		testBody = `t.Fatal("This test intentionally fails")`
	}

	testContent := fmt.Sprintf(testTemplate, packageName, testName, testBody)

	t.Logf("Writing test file to %s", testPath)
	writeFile(t, testPath, testContent)

	return packageDir
}

// Helper function to write files with error checking
func writeFile(t *testing.T, path, content string) {
	require.NoError(t, os.WriteFile(path, []byte(content), 0644),
		fmt.Sprintf("Failed to write file: %s", path))
}

// createValidatorConfig creates a validator configuration file
// useInvalidPath can be set to true to create a config with an invalid path
func createValidatorConfig(t *testing.T, testDir, packageName, testName, gateID string, useInvalidPath bool) string {
	packageDir := filepath.Join(testDir, packageName)
	if useInvalidPath {
		packageDir = packageName // Use the raw name to create an invalid path
	}

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

	writeFile(t, validatorPath, validatorConfig)
	return validatorPath
}

// Helper function to run op-acceptor with given parameters and return the exit code
func runOpAcceptor(t *testing.T, binary, testdir, validators, gate string) int {
	t.Logf("Running op-acceptor with testdir=%s, gate=%s, validators=%s", testdir, gate, validators)

	// Create a command with timeout context
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	execCmd := exec.CommandContext(ctx, binary,
		"--run-interval=0", // This ensures the process runs once and exits
		"--gate="+gate,
		"--testdir="+testdir,
		"--validators="+validators)

	// Capture output for debugging
	var stdout, stderr bytes.Buffer
	execCmd.Stdout = &stdout
	execCmd.Stderr = &stderr

	err := execCmd.Run()

	// Log output regardless of success/failure
	if stdout.Len() > 0 {
		t.Logf("stdout:\n%s", stdout.String())
	}
	if stderr.Len() > 0 {
		t.Logf("stderr:\n%s", stderr.String())
	}

	// Check if the context deadline was exceeded
	if ctx.Err() == context.DeadlineExceeded {
		t.Logf("Command timed out")
		// Kill the process if it's still running
		if execCmd.Process != nil {
			killErr := execCmd.Process.Kill()
			if killErr != nil {
				t.Logf("Failed to kill process: %v", killErr)
			}
		}
		return exitcodes.RuntimeErr // Return error code for timeout
	}

	if err == nil {
		return exitcodes.Success
	}

	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}

	return exitcodes.RuntimeErr // Return error code for unexpected errors
}

// Helper function to check if a file exists
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// createMockPanicTest creates a test file that deliberately panics
func createMockPanicTest(t *testing.T, testDir, packageName, filename string) string {
	// Create package directory
	packageDir := filepath.Join(testDir, packageName)
	require.NoError(t, os.MkdirAll(packageDir, 0755))

	// Create test file
	testPath := filepath.Join(packageDir, filename)

	// Test content with explicit panic
	testContent := fmt.Sprintf(`package %s

import (
	"testing"
)

func TestExplicitPanic(t *testing.T) {
	// This test will deliberately panic
	panic("This is a deliberate panic to test error handling")
}
`, packageName)

	t.Logf("Writing panic test file to %s", testPath)
	writeFile(t, testPath, testContent)

	return packageDir
}
