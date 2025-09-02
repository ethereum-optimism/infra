package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/ethereum/go-ethereum/log"
)

var _ TestExecutor = (*testExecutor)(nil)

// TestExecutor handles individual test execution and process management.
// It provides methods to execute single tests or entire packages, with proper
// timeout handling, error management, and result parsing.
type TestExecutor interface {
	// Execute runs a single test or package based on the metadata provided.
	// If metadata.FuncName is empty, it will execute all tests in the package.
	Execute(ctx context.Context, metadata types.ValidatorMetadata) (*types.TestResult, error)

	// ExecutePackage runs all tests in a specific package.
	ExecutePackage(ctx context.Context, metadata types.ValidatorMetadata) (*types.TestResult, error)
}

// testExecutor implements TestExecutor
type testExecutor struct {
	testDir      string
	timeout      time.Duration
	goBinary     string
	envProvider  func() Env
	cmdBuilder   func(ctx context.Context, name string, arg ...string) (*exec.Cmd, func())
	outputParser OutputParser
	jsonStore    JSONStore
}

// JSONStore handles storing and retrieving raw JSON output
type JSONStore interface {
	Store(testID string, rawJSON []byte)
	Get(testID string) ([]byte, bool)
}

// OutputParser handles parsing test output
type OutputParser interface {
	Parse(output []byte, metadata types.ValidatorMetadata) *types.TestResult
	ParseWithTimeout(output []byte, metadata types.ValidatorMetadata, timeout time.Duration) *types.TestResult
}

// NewTestExecutor creates a new test executor
func NewTestExecutor(testDir string, timeout time.Duration, goBinary string, envProvider func() Env,
	cmdBuilder func(ctx context.Context, name string, arg ...string) (*exec.Cmd, func()),
	outputParser OutputParser, jsonStore JSONStore) (TestExecutor, error) {

	// Input validation
	if testDir == "" {
		return nil, fmt.Errorf("testDir cannot be empty")
	}
	if goBinary == "" {
		goBinary = DefaultGoBinary
	}
	if envProvider == nil {
		return nil, fmt.Errorf("envProvider cannot be nil")
	}
	if cmdBuilder == nil {
		return nil, fmt.Errorf("cmdBuilder cannot be nil")
	}
	if outputParser == nil {
		return nil, fmt.Errorf("outputParser cannot be nil")
	}

	return &testExecutor{
		testDir:      testDir,
		timeout:      timeout,
		goBinary:     goBinary,
		envProvider:  envProvider,
		cmdBuilder:   cmdBuilder,
		outputParser: outputParser,
		jsonStore:    jsonStore,
	}, nil
}

// Execute runs a single test
func (e *testExecutor) Execute(ctx context.Context, metadata types.ValidatorMetadata) (*types.TestResult, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context cannot be nil")
	}

	if metadata.Package == "" {
		return nil, fmt.Errorf("package cannot be empty in metadata")
	}

	log.Info("Running test", "test", metadata.FuncName, "package", metadata.Package, "suite", metadata.Suite)

	if metadata.FuncName == "" {
		return e.ExecutePackage(ctx, metadata)
	}

	return e.runSingleTest(ctx, metadata)
}

// ExecutePackage runs all tests in a package
func (e *testExecutor) ExecutePackage(ctx context.Context, metadata types.ValidatorMetadata) (*types.TestResult, error) {
	log.Info("Running all tests in package", "package", metadata.Package, "suite", metadata.Suite)

	// Run the entire package in one go
	// This allows our parser to get individual test timings from the JSON stream
	return e.runSingleTest(ctx, metadata)
}

func (e *testExecutor) runSingleTest(ctx context.Context, metadata types.ValidatorMetadata) (*types.TestResult, error) {
	args := e.buildTestArgs(metadata)

	cmd, cleanup := e.cmdBuilder(ctx, e.goBinary, args...)
	defer cleanup()

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	startTime := time.Now()
	err := cmd.Run()
	duration := time.Since(startTime)

	stdout := stdoutBuf.Bytes()
	stderr := stderrBuf.Bytes()

	var result *types.TestResult
	if e.timeout > 0 && duration >= e.timeout {
		result = e.outputParser.ParseWithTimeout(stdout, metadata, e.timeout)
	} else {
		result = e.outputParser.Parse(stdout, metadata)
	}

	if result == nil {
		result = &types.TestResult{
			Metadata: metadata,
			Status:   types.TestStatusFail,
			Error:    errors.New("failed to parse test output"),
			Duration: duration,
		}
	}

	// Store raw JSON if available
	if e.jsonStore != nil {
		e.jsonStore.Store(e.getTestKey(metadata), stdout)
	}

	// Handle execution errors
	if err != nil {
		exitErr := &exec.ExitError{}
		if errors.As(err, &exitErr) {
			if exitErr.ExitCode() == 1 && result.Status != types.TestStatusPass {
				// Test failed, which is expected
			} else if exitErr.ExitCode() == 2 {
				result.Status = types.TestStatusFail
				result.Error = fmt.Errorf("test compilation failed: %s", string(stderr))
			} else {
				result.Status = types.TestStatusFail
				result.Error = fmt.Errorf("test execution failed with exit code %d: %s", exitErr.ExitCode(), string(stderr))
			}
		} else {
			result.Status = types.TestStatusFail
			result.Error = fmt.Errorf("failed to run test: %w", err)
		}
	}

	// Add stderr to error if present
	if len(stderr) > 0 && result.Error != nil {
		result.Error = fmt.Errorf("%w\nstderr: %s", result.Error, string(stderr))
	}

	return result, nil
}

func (e *testExecutor) buildTestArgs(metadata types.ValidatorMetadata) []string {
	args := []string{TestCommand, JSONFlag, VerboseFlag}

	if e.timeout > 0 {
		args = append(args, TimeoutFlag, e.timeout.String())
	}

	args = append(args, metadata.Package)

	if metadata.FuncName != "" {
		args = append(args, RunFlag, fmt.Sprintf("^%s$", metadata.FuncName))
	}

	return args
}

func (e *testExecutor) getTestKey(metadata types.ValidatorMetadata) string {
	if metadata.FuncName != "" {
		return fmt.Sprintf("%s::%s", metadata.Package, metadata.FuncName)
	}
	return metadata.Package
}

// TestEvent represents a test event from go test -json output
type TestEvent struct {
	Time    time.Time
	Action  string
	Package string
	Test    string
	Elapsed float64
	Output  string
}
