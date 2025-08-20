package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCLISerialFlag tests the --serial flag through the actual CLI
func TestCLISerialFlag(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping CLI integration test in short mode")
	}

	// Create temporary test directory
	testDir := t.TempDir()

	// Initialize go module
	initGoModuleCLI(t, testDir, "test-cli")

	// Create test packages with timing differences for detection
	pkg1Content := []byte(`
package pkg1_test

import (
	"testing"
	"time"
)

func TestPkg1(t *testing.T) {
	time.Sleep(50 * time.Millisecond)
	t.Log("Package 1 test completed")
}
`)

	pkg2Content := []byte(`
package pkg2_test

import (
	"testing"
	"time"
)

func TestPkg2(t *testing.T) {
	time.Sleep(50 * time.Millisecond)
	t.Log("Package 2 test completed")
}
`)

	// Create test packages
	createTestPackage(t, testDir, "pkg1", pkg1Content)
	createTestPackage(t, testDir, "pkg2", pkg2Content)

	// Create validators config
	validatorsContent := []byte(`
gates:
  - id: cli-test-gate
    description: "CLI test gate"
    tests:
      - package: "./pkg1"
        run_all: true
      - package: "./pkg2"
        run_all: true
`)

	validatorsPath := filepath.Join(testDir, "validators.yaml")
	err := os.WriteFile(validatorsPath, validatorsContent, 0644)
	require.NoError(t, err)

	// Build the op-acceptor binary
	binaryPath := buildOpAcceptor(t)

	// Test 1: Default behavior (should be parallel)
	t.Run("default-parallel", func(t *testing.T) {
		start := time.Now()
		output, err := runOpAcceptor(t, binaryPath, []string{
			"--testdir", testDir,
			"--validators", validatorsPath,
			"--gate", "cli-test-gate",
			"--orchestrator", "sysgo", // Use sysgo orchestrator to avoid devnet URL requirement
		})
		parallelDuration := time.Since(start)

		require.NoError(t, err)
		assert.Contains(t, output, "parallel", "Default should use parallel execution")
		assert.Contains(t, output, "PASS", "Tests should pass")

		t.Logf("Default (parallel) execution took: %v", parallelDuration)
	})

	// Test 2: Explicit --serial flag
	t.Run("explicit-serial", func(t *testing.T) {
		start := time.Now()
		output, err := runOpAcceptor(t, binaryPath, []string{
			"--testdir", testDir,
			"--validators", validatorsPath,
			"--gate", "cli-test-gate",
			"--serial",
			"--orchestrator", "sysgo", // Use sysgo orchestrator to avoid devnet URL requirement
		})
		serialDuration := time.Since(start)

		require.NoError(t, err)
		assert.NotContains(t, output, "parallel", "Serial mode should not mention parallel")
		assert.Contains(t, output, "PASS", "Tests should pass")

		t.Logf("Serial execution took: %v", serialDuration)
	})

	// Test 3: Help text includes --serial flag
	t.Run("help-includes-serial", func(t *testing.T) {
		output, err := runOpAcceptor(t, binaryPath, []string{"--help"})

		require.NoError(t, err)
		assert.Contains(t, output, "--serial", "Help should mention --serial flag")
		assert.Contains(t, output, "Run tests serially", "Help should explain --serial flag")
	})
}

// TestCLISerialEnvironmentVariable tests the environment variable equivalent
func TestCLISerialEnvironmentVariable(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping CLI environment variable test in short mode")
	}

	// Create temporary test directory
	testDir := t.TempDir()
	initGoModuleCLI(t, testDir, "test-env")

	// Create simple test
	testContent := []byte(`
package simple_test

import "testing"

func TestSimple(t *testing.T) {
	t.Log("Simple test")
}
`)

	createTestPackage(t, testDir, "simple", testContent)

	validatorsContent := []byte(`
gates:
  - id: env-test-gate
    description: "Environment variable test gate"
    tests:
      - package: "./simple"
        run_all: true
`)

	validatorsPath := filepath.Join(testDir, "validators.yaml")
	err := os.WriteFile(validatorsPath, validatorsContent, 0644)
	require.NoError(t, err)

	binaryPath := buildOpAcceptor(t)

	// Test with environment variable
	output, err := runOpAcceptorWithEnv(t, binaryPath, []string{
		"--testdir", testDir,
		"--validators", validatorsPath,
		"--gate", "env-test-gate",
		"--orchestrator", "sysgo", // Use sysgo orchestrator to avoid devnet URL requirement
	}, map[string]string{
		"OP_ACCEPTOR_SERIAL": "true",
	})

	require.NoError(t, err)
	assert.Contains(t, output, "PASS", "Tests should pass with env var")

	t.Logf("Environment variable OP_ACCEPTOR_SERIAL=true works correctly")
}

// TestCLIExitCodes tests that parallel and serial modes return correct exit codes
func TestCLIExitCodes(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping CLI exit code test in short mode")
	}

	// Create temporary test directory
	testDir := t.TempDir()
	initGoModuleCLI(t, testDir, "test-exit")

	// Create failing test
	failingContent := []byte(`
package failing_test

import "testing"

func TestFailing(t *testing.T) {
	t.Fatal("This test always fails")
}
`)

	createTestPackage(t, testDir, "failing", failingContent)

	validatorsContent := []byte(`
gates:
  - id: exit-test-gate
    description: "Exit code test gate"
    tests:
      - package: "./failing"
        run_all: true
`)

	validatorsPath := filepath.Join(testDir, "validators.yaml")
	err := os.WriteFile(validatorsPath, validatorsContent, 0644)
	require.NoError(t, err)

	binaryPath := buildOpAcceptor(t)

	// Test parallel mode exit code
	t.Run("parallel-exit-code", func(t *testing.T) {
		_, err := runOpAcceptor(t, binaryPath, []string{
			"--testdir", testDir,
			"--validators", validatorsPath,
			"--gate", "exit-test-gate",
		})

		// Should have non-zero exit code due to test failure
		require.Error(t, err)

		exitError := &exec.ExitError{}
		if errors.As(err, &exitError) {
			assert.NotEqual(t, 0, exitError.ExitCode(), "Should have non-zero exit code for failing tests")
		}
	})

	// Test serial mode exit code
	t.Run("serial-exit-code", func(t *testing.T) {
		_, err := runOpAcceptor(t, binaryPath, []string{
			"--testdir", testDir,
			"--validators", validatorsPath,
			"--gate", "exit-test-gate",
			"--serial",
		})

		// Should have non-zero exit code due to test failure
		require.Error(t, err)

		exitError := &exec.ExitError{}
		if errors.As(err, &exitError) {
			assert.NotEqual(t, 0, exitError.ExitCode(), "Should have non-zero exit code for failing tests in serial mode")
		}
	})
}

// Helper functions

func buildOpAcceptor(t *testing.T) string {
	t.Helper()

	binaryPath := filepath.Join(t.TempDir(), "op-acceptor")

	// Build the binary
	cmd := exec.Command("go", "build", "-o", binaryPath, "./main.go")
	cmd.Dir = "." // Current directory should be op-acceptor/cmd

	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "Failed to build op-acceptor: %s", string(output))

	return binaryPath
}

func runOpAcceptor(t *testing.T, binaryPath string, args []string) (string, error) {
	return runOpAcceptorWithEnv(t, binaryPath, args, nil)
}

func runOpAcceptorWithEnv(t *testing.T, binaryPath string, args []string, env map[string]string) (string, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binaryPath, args...)

	// Set environment variables
	cmd.Env = os.Environ()
	for key, value := range env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, value))
	}

	output, err := cmd.CombinedOutput()
	return string(output), err
}

func createTestPackage(t *testing.T, baseDir, packageName string, content []byte) {
	t.Helper()

	packageDir := filepath.Join(baseDir, packageName)
	err := os.MkdirAll(packageDir, 0755)
	require.NoError(t, err)

	testFile := filepath.Join(packageDir, "example_test.go")
	err = os.WriteFile(testFile, content, 0644)
	require.NoError(t, err)
}

func initGoModuleCLI(t *testing.T, dir, moduleName string) {
	t.Helper()

	goModContent := fmt.Sprintf("module %s\n\ngo 1.23.5\n", moduleName)
	err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goModContent), 0644)
	require.NoError(t, err)
}
