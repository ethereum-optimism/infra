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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	passing_dir = "passing"
	failing_dir = "failing"
	panic_dir   = "panicking"
)

// TestExitCodeBehavior verifies that op-acceptor returns the correct exit codes in run-once mode:
// - Exit code 0 when all tests pass
// - Exit code 1 when any tests fail
// - Exit code 2 when there's a runtime error
func TestExitCodeBehavior(t *testing.T) {
	// Find the binary path
	projectRoot, err := os.Getwd()
	require.NoError(t, err, "Failed to get current directory")
	projectRoot = filepath.Dir(projectRoot) // Go up one directory to project root
	opAcceptorBin := filepath.Join(projectRoot, "bin", "op-acceptor")

	// Make sure the binary exists
	ensureBinaryExists(t, projectRoot, opAcceptorBin)

	// Define test cases
	testCases := []struct {
		name           string
		setupFunc      func(t *testing.T, testDir string) (string, string, string) // Returns gate, validators, testdir
		expectedStatus int                                                         // Expected exit code
		defaultTimeout time.Duration                                               // Default timeout for the test runner
		timeout        *time.Duration                                              // Timeout for the test
	}{
		{
			name: "Passing tests should exit with code 0",
			setupFunc: func(t *testing.T, testDir string) (string, string, string) {
				packageName := "passing"
				testName := "TestAlwaysPasses"
				gateID := "test-gate-passes"

				// Create a simple passing test
				createMockTest(t, testDir, true, 0)
				validatorPath := createValidatorConfig(t, testDir, packageName, testName, gateID, false, nil)

				return gateID, validatorPath, testDir
			},
			expectedStatus: exitcodes.Success,
			defaultTimeout: 5 * time.Second,
		},
		{
			name: "Failing tests should exit with code 1",
			setupFunc: func(t *testing.T, testDir string) (string, string, string) {
				packageName := "failing"
				testName := "TestAlwaysFails"
				gateID := "test-gate-fails"

				// Create a simple failing test
				createMockTest(t, testDir, false, 0)
				validatorPath := createValidatorConfig(t, testDir, packageName, testName, gateID, false, nil)

				return gateID, validatorPath, testDir
			},
			expectedStatus: exitcodes.TestFailure,
			defaultTimeout: 5 * time.Second,
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
				createMockPanicTest(t, testDir)
				validatorPath := createValidatorConfig(t, testDir, packageName, testName, gateID, false, nil)

				return gateID, validatorPath, testDir
			},
			// Go's test framework catches panics and treats them as test failures (exit code 1)
			// rather than propagating them as runtime errors (exit code 2)
			expectedStatus: exitcodes.TestFailure,
			defaultTimeout: 5 * time.Second,
		},
		{
			name: "Test should timeout with default timeout",
			setupFunc: func(t *testing.T, testDir string) (string, string, string) {
				packageName := "passing"
				testName := "TestAlwaysPasses"
				gateID := "test-gate-passes"

				// Create a simple passing test after timeout
				createMockTest(t, testDir, true, 5*time.Second)
				validatorPath := createValidatorConfig(t, testDir, packageName, testName, gateID, false, nil)

				return gateID, validatorPath, testDir
			},
			// Go's test framework catches panics and treats them as test failures (exit code 1)
			// rather than propagating them as runtime errors (exit code 2)
			expectedStatus: exitcodes.TestFailure,
			defaultTimeout: 2 * time.Second,
		},
		{
			name: "Test should not timeout",
			setupFunc: func(t *testing.T, testDir string) (string, string, string) {
				packageName := "passing"
				testName := "TestAlwaysPasses"
				gateID := "test-gate-passes"

				// Create a simple passing test after timeout
				createMockTest(t, testDir, true, 1*time.Second)
				validatorPath := createValidatorConfig(t, testDir, packageName, testName, gateID, false, nil)

				return gateID, validatorPath, testDir
			},
			// Go's test framework catches panics and treats them as test failures (exit code 1)
			// rather than propagating them as runtime errors (exit code 2)
			expectedStatus: exitcodes.Success,
			defaultTimeout: 10 * time.Second,
		},
		{
			name: "Test should timeout with test-level timeout",
			setupFunc: func(t *testing.T, testDir string) (string, string, string) {
				packageName := "passing"
				testName := "TestAlwaysPasses"
				gateID := "test-gate-passes"

				// Create a simple passing test after timeout
				createMockTest(t, testDir, true, 3*time.Second)
				duration := 1 * time.Second
				validatorPath := createValidatorConfig(t, testDir, packageName, testName, gateID, false, &duration)

				return gateID, validatorPath, testDir
			},
			// Go's test framework catches panics and treats them as test failures (exit code 1)
			// rather than propagating them as runtime errors (exit code 2)
			expectedStatus: exitcodes.TestFailure,
			defaultTimeout: 10 * time.Second,
		},
		{
			name: "Test should not timeout with either test-level or default timeout",
			setupFunc: func(t *testing.T, testDir string) (string, string, string) {
				packageName := "passing"
				testName := "TestAlwaysPasses"
				gateID := "test-gate-passes"

				// Create a simple passing test after timeout
				createMockTest(t, testDir, true, 1*time.Second)
				duration := 3 * time.Second
				validatorPath := createValidatorConfig(t, testDir, packageName, testName, gateID, false, &duration)

				return gateID, validatorPath, testDir
			},
			// Go's test framework catches panics and treats them as test failures (exit code 1)
			// rather than propagating them as runtime errors (exit code 2)
			expectedStatus: exitcodes.Success,
			defaultTimeout: 5 * time.Second,
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
			exitCode := runOpAcceptor(t, opAcceptorBin, testDir, validatorPath, gate, tc.defaultTimeout)
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

// createMockTest creates a test file that either passes or fails
func createMockTest(t *testing.T, testDir string, passing bool, sleepDuration time.Duration) string {
	t.Helper()

	// Create the package directory
	packageDir := filepath.Join(testDir, passing_dir)
	if !passing {
		packageDir = filepath.Join(testDir, failing_dir)
	}

	err := os.MkdirAll(packageDir, 0755)
	require.NoError(t, err)

	// Create a go.mod file to make this a valid module
	goModPath := filepath.Join(packageDir, "go.mod")
	goModContent := `module test

go 1.21
`
	err = os.WriteFile(goModPath, []byte(goModContent), 0644)
	require.NoError(t, err)

	// Create the test file
	testFileName := "passing_test.go"
	if !passing {
		testFileName = "failing_test.go"
	}
	testFilePath := filepath.Join(packageDir, testFileName)

	// Create a simple test that either passes or fails
	testContent := `package test

import (
	"testing"
	"time"
)

func TestAlways`

	if passing {
		testContent += fmt.Sprintf(`Passes(t *testing.T) {
	// This test always passes
	d, err := time.ParseDuration("%s")
	if err != nil {
		t.Fatalf("Failed to parse duration: %%v", err)
	}
	time.Sleep(d)
}`, sleepDuration.String())
	} else {
		testContent += fmt.Sprintf(`Fails(t *testing.T) {
	d, err := time.ParseDuration("%s")
	if err != nil {
		t.Fatalf("Failed to parse duration: %%v", err)
	}
	time.Sleep(d)
	t.Fail()
}`, sleepDuration.String())
	}

	err = os.WriteFile(testFilePath, []byte(testContent), 0644)
	require.NoError(t, err)

	t.Logf("Writing test file to %s", testFilePath)
	return packageDir
}

// Helper function to write files with error checking
func writeFile(t *testing.T, path, content string) {
	require.NoError(t, os.WriteFile(path, []byte(content), 0644),
		fmt.Sprintf("Failed to write file: %s", path))
}

// createValidatorConfig creates a validator configuration file
// useInvalidPath can be set to true to create a config with an invalid path
func createValidatorConfig(t *testing.T, testDir, packageName, testName, gateID string, useInvalidPath bool, testTimeout *time.Duration) string {
	var packagePath string
	if useInvalidPath {
		packagePath = packageName // Use the raw name to create an invalid path
	} else {
		// Use a relative path from the test directory to the package directory
		// This avoids the "cannot import absolute path" error
		packagePath = "./tests/" + packageName

		// Ensure tests directory exists
		testsDir := filepath.Join(testDir, "tests")
		require.NoError(t, os.MkdirAll(testsDir, 0755))

		// Make sure the tests directory is a valid Go module
		goModPath := filepath.Join(testsDir, "go.mod")
		goModContent := `module tests

go 1.21
`
		err := os.WriteFile(goModPath, []byte(goModContent), 0644)
		require.NoError(t, err)

		// Move the package directory under tests/
		oldDir := filepath.Join(testDir, packageName)
		newDir := filepath.Join(testsDir, packageName)
		require.NoError(t, os.Rename(oldDir, newDir))
	}

	validatorPath := filepath.Join(testDir, "test-validators.yaml")
	var validatorConfig string
	if testTimeout != nil {
		validatorConfig = fmt.Sprintf(`# Test validator configuration file for exit code testing

gates:
  - id: %s
    description: "Test gate for exit code testing"
    suites:
      test-suite:
        description: "Test suite for exit code testing"
        tests:
          - name: %s
            package: %s
            timeout: %s
`, gateID, testName, packagePath, testTimeout.String())
	} else {
		validatorConfig = fmt.Sprintf(`# Test validator configuration file for exit code testing

gates:
  - id: %s
    description: "Test gate for exit code testing"
    suites:
      test-suite:
        description: "Test suite for exit code testing"
        tests:
          - name: %s
            package: %s
`, gateID, testName, packagePath)
	}

	writeFile(t, validatorPath, validatorConfig)
	return validatorPath
}

// Helper function to run op-acceptor with given parameters and return the exit code
func runOpAcceptor(t *testing.T, binary, testdir, validators, gate string, defaultTimeout time.Duration) int {
	t.Logf("Running op-acceptor with testdir=%s, gate=%s, validators=%s", testdir, gate, validators)

	// Create a temporary devnet manifest file for testing
	devnetFile := createMockDevnetFile(t)

	// Create a command with timeout context
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	execCmd := exec.CommandContext(ctx, binary,
		"--run-interval=0", // This ensures the process runs once and exits
		"--gate="+gate,
		"--testdir="+testdir,
		"--validators="+validators,
		"--default-timeout="+defaultTimeout.String())

	// Set environment variables for the test
	// This forces Go to run tests in the current directory, regardless of module settings
	execCmd.Env = append(os.Environ(),
		"GO111MODULE=off",
		"GOPATH=/tmp/go",             // Use a temporary GOPATH to avoid conflicts
		"DEVNET_ENV_URL="+devnetFile) // Mock devnet file for testing

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

// createMockDevnetFile creates a temporary devnet manifest file for testing
func createMockDevnetFile(t *testing.T) string {
	t.Helper()

	// Create a temporary file for the devnet manifest
	tempFile, err := os.CreateTemp("", "test-devnet-*.json")
	require.NoError(t, err)
	defer tempFile.Close()

	// Create a valid devnet manifest structure
	validContent := `{
		"name": "test-network",
		"l1": {
			"name": "test-l1",
			"id": "1",
			"nodes": [],
			"addresses": {},
			"wallets": {}
		},
		"l2": []
	}`

	// Write the content to the file
	_, err = tempFile.WriteString(validContent)
	require.NoError(t, err)

	// Return the file path
	return tempFile.Name()
}

// createMockPanicTest creates a test file that deliberately panics
func createMockPanicTest(t *testing.T, testDir string) string {
	t.Helper()

	// Create the package directory
	packageDir := filepath.Join(testDir, panic_dir)
	err := os.MkdirAll(packageDir, 0755)
	require.NoError(t, err)

	// Create a go.mod file to make this a valid module
	goModPath := filepath.Join(packageDir, "go.mod")
	goModContent := `module test

go 1.21
`
	err = os.WriteFile(goModPath, []byte(goModContent), 0644)
	require.NoError(t, err)

	// Create the test file that will panic
	testFilePath := filepath.Join(packageDir, "panic_test.go")
	testContent := `package test

import (
	"testing"
)

func TestExplicitPanic(t *testing.T) {
	panic("This test explicitly panics")
}
`

	err = os.WriteFile(testFilePath, []byte(testContent), 0644)
	require.NoError(t, err)

	t.Logf("Writing test file to %s", testFilePath)
	return packageDir
}

func TestDefaultOrchestratorBehavior(t *testing.T) {
	// Setup binary paths
	projectRoot, err := os.Getwd()
	require.NoError(t, err, "Failed to get current directory")
	projectRoot = filepath.Dir(projectRoot) // Go up one directory to project root
	opAcceptorBin := filepath.Join(projectRoot, "bin", "op-acceptor")

	// Ensure the binary exists
	ensureBinaryExists(t, projectRoot, opAcceptorBin)

	testCases := []struct {
		name           string
		setDevnetURL   bool
		devnetContent  string
		expectedExit   int
		expectedOutput string
	}{
		{
			name:           "Default sysext orchestrator fails without DEVNET_ENV_URL",
			setDevnetURL:   false,
			expectedExit:   exitcodes.RuntimeErr,
			expectedOutput: "devnet environment URL not provided",
		},
		{
			name:           "Default sysext orchestrator setup succeeds with valid DEVNET_ENV_URL",
			setDevnetURL:   true,
			devnetContent:  `{"name": "test-net", "l1": {"name": "l1", "id": "1", "nodes": [], "addresses": {}, "wallets": {}}, "l2": []}`,
			expectedExit:   exitcodes.TestFailure,
			expectedOutput: "Using sysext orchestrator with devnet environment",
		},
		{
			name:           "Default sysext orchestrator fails with invalid DEVNET_ENV_URL",
			setDevnetURL:   true,
			devnetContent:  "invalid json",
			expectedExit:   exitcodes.RuntimeErr,
			expectedOutput: "failed to load devnet environment",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create temporary test directory
			tempDir, err := os.MkdirTemp("/tmp", "op-acceptor-default-test-")
			require.NoError(t, err, "Failed to create temporary directory")
			defer os.RemoveAll(tempDir)

			// Create a simple test that will fail (we're testing orchestrator setup, not test success)
			createMockTest(t, tempDir, false, 0) // failing test
			validatorPath := createValidatorConfig(t, tempDir, "failing", "TestAlwaysFails", "test-gate", false, nil)

			// Setup environment
			originalEnv := os.Getenv("DEVNET_ENV_URL")
			defer func() {
				if originalEnv != "" {
					os.Setenv("DEVNET_ENV_URL", originalEnv)
				} else {
					os.Unsetenv("DEVNET_ENV_URL")
				}
			}()

			var devnetFile string
			if tc.setDevnetURL {
				// Create devnet file
				devnetFile = filepath.Join(tempDir, "devnet.json")
				err := os.WriteFile(devnetFile, []byte(tc.devnetContent), 0644)
				require.NoError(t, err)
				os.Setenv("DEVNET_ENV_URL", devnetFile)
			} else {
				os.Unsetenv("DEVNET_ENV_URL")
			}

			// Run command
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			cmd := exec.CommandContext(ctx, opAcceptorBin,
				"--run-interval=0",
				"--gate=test-gate",
				"--testdir="+tempDir,
				"--validators="+validatorPath,
				"--log.level=debug")

			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr

			err = cmd.Run()

			// Check output contains expected message (if any)
			if tc.expectedOutput != "" {
				output := stdout.String() + stderr.String()
				assert.Contains(t, output, tc.expectedOutput)
			}

			// Check exit code
			if err == nil {
				assert.Equal(t, exitcodes.Success, tc.expectedExit)
			} else if exitErr, ok := err.(*exec.ExitError); ok {
				assert.Equal(t, tc.expectedExit, exitErr.ExitCode())
			} else {
				t.Fatalf("Unexpected error type: %v", err)
			}
		})
	}
}

func TestExplicitOrchestratorOverride(t *testing.T) {
	// Setup binary paths
	projectRoot, err := os.Getwd()
	require.NoError(t, err, "Failed to get current directory")
	projectRoot = filepath.Dir(projectRoot) // Go up one directory to project root
	opAcceptorBin := filepath.Join(projectRoot, "bin", "op-acceptor")

	// Ensure the binary exists
	ensureBinaryExists(t, projectRoot, opAcceptorBin)

	testCases := []struct {
		name           string
		orchestrator   string
		setDevnetURL   bool
		expectedExit   int
		expectedOutput string
	}{
		{
			name:           "Explicit sysgo works without DEVNET_ENV_URL",
			orchestrator:   "sysgo",
			setDevnetURL:   false,
			expectedExit:   exitcodes.TestFailure, // Test will fail but should get past orchestrator setup
			expectedOutput: "Using sysgo orchestrator (in-memory Go)",
		},
		{
			name:           "Explicit sysext fails without DEVNET_ENV_URL",
			orchestrator:   "sysext",
			setDevnetURL:   false,
			expectedExit:   exitcodes.RuntimeErr,
			expectedOutput: "devnet environment URL not provided",
		},
		{
			name:           "Invalid orchestrator fails with validation error",
			orchestrator:   "invalid",
			setDevnetURL:   false,
			expectedExit:   exitcodes.TestFailure, // urfave/cli returns exit code 1 for validation errors
			expectedOutput: "",                    // urfave/cli validation errors may not always appear in captured output
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create temporary test directory
			tempDir, err := os.MkdirTemp("/tmp", "op-acceptor-explicit-test-")
			require.NoError(t, err, "Failed to create temporary directory")
			defer os.RemoveAll(tempDir)

			// Create a simple test that will fail (we're testing orchestrator setup, not test success)
			createMockTest(t, tempDir, false, 0) // failing test
			validatorPath := createValidatorConfig(t, tempDir, "failing", "TestAlwaysFails", "test-gate", false, nil)

			// Setup environment
			originalEnv := os.Getenv("DEVNET_ENV_URL")
			defer func() {
				if originalEnv != "" {
					os.Setenv("DEVNET_ENV_URL", originalEnv)
				} else {
					os.Unsetenv("DEVNET_ENV_URL")
				}
			}()

			if tc.setDevnetURL {
				// Create devnet file
				devnetFile := filepath.Join(tempDir, "devnet.json")
				devnetContent := `{"name": "test-net", "l1": {"name": "l1", "id": "1", "nodes": [], "addresses": {}, "wallets": {}}, "l2": []}`
				err := os.WriteFile(devnetFile, []byte(devnetContent), 0644)
				require.NoError(t, err)
				os.Setenv("DEVNET_ENV_URL", devnetFile)
			} else {
				os.Unsetenv("DEVNET_ENV_URL")
			}

			// Run command
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			cmd := exec.CommandContext(ctx, opAcceptorBin,
				"--run-interval=0",
				"--gate=test-gate",
				"--testdir="+tempDir,
				"--validators="+validatorPath,
				"--orchestrator="+tc.orchestrator,
				"--log.level=debug")

			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr

			err = cmd.Run()

			// Check output contains expected message (if any)
			if tc.expectedOutput != "" {
				output := stdout.String() + stderr.String()
				assert.Contains(t, output, tc.expectedOutput)
			}

			// Check exit code
			if err == nil {
				assert.Equal(t, exitcodes.Success, tc.expectedExit)
			} else if exitErr, ok := err.(*exec.ExitError); ok {
				assert.Equal(t, tc.expectedExit, exitErr.ExitCode())
			} else {
				t.Fatalf("Unexpected error type: %v", err)
			}
		})
	}
}

func TestDevnetEnvURLFlagPrecedence(t *testing.T) {
	// Setup binary paths
	projectRoot, err := os.Getwd()
	require.NoError(t, err, "Failed to get current directory")
	projectRoot = filepath.Dir(projectRoot) // Go up one directory to project root
	opAcceptorBin := filepath.Join(projectRoot, "bin", "op-acceptor")

	// Ensure the binary exists
	ensureBinaryExists(t, projectRoot, opAcceptorBin)

	testCases := []struct {
		name                     string
		setEnvVar                bool
		envVarContent            string
		setCliFlag               bool
		cliFlagContent           string
		expectedExit             int
		expectedOutput           string
		expectCliTakesPrecedence bool
	}{
		{
			name:                     "CLI flag takes precedence over env var",
			setEnvVar:                true,
			envVarContent:            `{"name": "env-net", "l1": {"name": "l1", "id": "1", "nodes": [], "addresses": {}, "wallets": {}}, "l2": []}`,
			setCliFlag:               true,
			cliFlagContent:           `{"name": "cli-net", "l1": {"name": "l1", "id": "1", "nodes": [], "addresses": {}, "wallets": {}}, "l2": []}`,
			expectedExit:             exitcodes.TestFailure, // Test will fail but should get past orchestrator setup
			expectedOutput:           "cli-net",             // Should use CLI flag value, not env var
			expectCliTakesPrecedence: true,
		},
		{
			name:                     "Env var used when CLI flag not provided",
			setEnvVar:                true,
			envVarContent:            `{"name": "env-net", "l1": {"name": "l1", "id": "1", "nodes": [], "addresses": {}, "wallets": {}}, "l2": []}`,
			setCliFlag:               false,
			expectedExit:             exitcodes.TestFailure, // Test will fail but should get past orchestrator setup
			expectedOutput:           "env-net",             // Should use env var value
			expectCliTakesPrecedence: false,
		},
		{
			name:                     "CLI flag used when env var not set",
			setEnvVar:                false,
			setCliFlag:               true,
			cliFlagContent:           `{"name": "cli-net", "l1": {"name": "l1", "id": "1", "nodes": [], "addresses": {}, "wallets": {}}, "l2": []}`,
			expectedExit:             exitcodes.TestFailure, // Test will fail but should get past orchestrator setup
			expectedOutput:           "cli-net",             // Should use CLI flag value
			expectCliTakesPrecedence: true,
		},
		{
			name:                     "Error when neither CLI flag nor env var provided",
			setEnvVar:                false,
			setCliFlag:               false,
			expectedExit:             exitcodes.RuntimeErr,
			expectedOutput:           "devnet environment URL not provided",
			expectCliTakesPrecedence: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create temporary test directory
			tempDir, err := os.MkdirTemp("/tmp", "op-acceptor-precedence-test-")
			require.NoError(t, err, "Failed to create temporary directory")
			defer os.RemoveAll(tempDir)

			// Create a simple test that will fail (we're testing orchestrator setup, not test success)
			createMockTest(t, tempDir, false, 0) // failing test
			validatorPath := createValidatorConfig(t, tempDir, "failing", "TestAlwaysFails", "test-gate", false, nil)

			// Setup environment
			originalEnv := os.Getenv("DEVNET_ENV_URL")
			defer func() {
				if originalEnv != "" {
					os.Setenv("DEVNET_ENV_URL", originalEnv)
				} else {
					os.Unsetenv("DEVNET_ENV_URL")
				}
			}()

			var envFile, cliFile string

			// Setup environment variable if needed
			if tc.setEnvVar {
				envFile = filepath.Join(tempDir, "env-devnet.json")
				err := os.WriteFile(envFile, []byte(tc.envVarContent), 0644)
				require.NoError(t, err)
				os.Setenv("DEVNET_ENV_URL", envFile)
			} else {
				os.Unsetenv("DEVNET_ENV_URL")
			}

			// Setup CLI flag file if needed
			var cmdArgs []string
			cmdArgs = append(cmdArgs,
				"--run-interval=0",
				"--gate=test-gate",
				"--testdir="+tempDir,
				"--validators="+validatorPath,
				"--orchestrator=sysext", // Use sysext to require devnet env URL
				"--log.level=debug")

			if tc.setCliFlag {
				cliFile = filepath.Join(tempDir, "cli-devnet.json")
				err := os.WriteFile(cliFile, []byte(tc.cliFlagContent), 0644)
				require.NoError(t, err)
				cmdArgs = append(cmdArgs, "--devnet-env-url="+cliFile)
			}

			// Run command
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			cmd := exec.CommandContext(ctx, opAcceptorBin, cmdArgs...)

			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr

			err = cmd.Run()

			// Check output contains expected message (if any)
			if tc.expectedOutput != "" {
				output := stdout.String() + stderr.String()
				assert.Contains(t, output, tc.expectedOutput)
			}

			// Specific check for CLI precedence
			if tc.expectCliTakesPrecedence && tc.setEnvVar && tc.setCliFlag {
				output := stdout.String() + stderr.String()
				// Should contain CLI flag network name, not env var network name
				assert.Contains(t, output, "cli-net")
				assert.NotContains(t, output, "env-net")
			}

			// Check exit code
			if err == nil {
				assert.Equal(t, exitcodes.Success, tc.expectedExit)
			} else if exitErr, ok := err.(*exec.ExitError); ok {
				assert.Equal(t, tc.expectedExit, exitErr.ExitCode())
			} else {
				t.Fatalf("Unexpected error type: %v", err)
			}
		})
	}
}
