package runner

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/metrics"
	"github.com/ethereum-optimism/infra/op-acceptor/registry"
	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/google/uuid"
)

// SuiteResult captures aggregated results for a test suite
type SuiteResult struct {
	ID          string
	Description string
	Tests       map[string]*types.TestResult
	Status      types.TestStatus
	Duration    time.Duration
	Stats       ResultStats
}

// GateResult captures aggregated results for a gate
type GateResult struct {
	ID          string
	Description string
	Tests       map[string]*types.TestResult
	Suites      map[string]*SuiteResult
	Status      types.TestStatus
	Duration    time.Duration
	Stats       ResultStats
	Inherited   []string
}

// RunnerResult captures the complete test run results
type RunnerResult struct {
	Gates    map[string]*GateResult
	Status   types.TestStatus
	Duration time.Duration
	Stats    ResultStats
	RunID    string
}

// ResultStats tracks test statistics at each level
type ResultStats struct {
	Total     int
	Passed    int
	Failed    int
	Skipped   int
	StartTime time.Time
	EndTime   time.Time
}

// TestRunner defines the interface for running acceptance tests
type TestRunner interface {
	RunAllTests() (*RunnerResult, error)
	RunTest(metadata types.ValidatorMetadata) (*types.TestResult, error)
}

type runner struct {
	registry   *registry.Registry
	validators []types.ValidatorMetadata
	workDir    string // Directory for running tests
	log        log.Logger
	runID      string
	timeout    time.Duration // Test timeout
	goBinary   string        // Path to the Go binary
	allowSkips bool          // Whether to allow skipping tests when preconditions are not met
	result     *RunnerResult
}

// Config provides the configuration for the test runner
type Config struct {
	Registry   *registry.Registry
	TargetGate string
	WorkDir    string
	Log        log.Logger
	Timeout    time.Duration // Test timeout
	GoBinary   string        // path to the Go binary
	AllowSkips bool          // Whether to allow skipping tests when preconditions are not met
}

// NewTestRunner creates a new test runner instance
func NewTestRunner(cfg Config) (TestRunner, error) {
	if cfg.Registry == nil {
		return nil, fmt.Errorf("registry is required")
	}
	if cfg.WorkDir == "" {
		return nil, fmt.Errorf("work directory is required")
	}
	if cfg.Log == nil {
		cfg.Log = log.New()
		cfg.Log.Error("No logger provided, using default")
	}
	cfg.Log.Info("NewTestRunner()", "targetGate", cfg.TargetGate, "workDir", cfg.WorkDir, "allowSkips", cfg.AllowSkips)

	var validators []types.ValidatorMetadata
	if len(cfg.TargetGate) > 0 {
		validators = cfg.Registry.GetValidatorsByGate(cfg.TargetGate)
	} else {
		validators = cfg.Registry.GetValidators()
	}
	if len(validators) == 0 {
		return nil, fmt.Errorf("no validators found")
	}

	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Minute // Default timeout
	}

	if cfg.GoBinary == "" {
		cfg.GoBinary = "go" // Default to "go" if not specified
	}

	// Generate unique run ID
	runID := uuid.New().String()

	r := &runner{
		registry:   cfg.Registry,
		validators: validators,
		runID:      runID,
		workDir:    cfg.WorkDir,
		log:        cfg.Log,
		timeout:    cfg.Timeout,
		goBinary:   cfg.GoBinary,
		allowSkips: cfg.AllowSkips,
	}

	return r, nil
}

// RunAllTests implements the TestRunner interface
func (r *runner) RunAllTests() (*RunnerResult, error) {
	r.runID = uuid.New().String()
	defer func() {
		r.runID = ""
	}()

	start := time.Now()
	r.log.Debug("Running all tests", "run_id", r.runID)

	result := &RunnerResult{
		Gates: make(map[string]*GateResult),
		Stats: ResultStats{StartTime: start},
	}

	if err := r.processAllGates(result); err != nil {
		return nil, err
	}

	result.Duration = time.Since(start)
	result.Status = determineRunnerStatus(result)
	result.Stats.EndTime = time.Now()
	result.RunID = r.runID
	r.result = result // Store result for use in defer function
	return result, nil
}

// processAllGates handles the execution of all gates
func (r *runner) processAllGates(result *RunnerResult) error {
	// Group validators by gate
	gateValidators := r.groupValidatorsByGate()

	// Process each gate
	for gateName, validators := range gateValidators {
		if err := r.processGate(gateName, validators, result); err != nil {
			return fmt.Errorf("processing gate %s: %w", gateName, err)
		}
	}
	return nil
}

// groupValidatorsByGate organizes validators into their respective gates
func (r *runner) groupValidatorsByGate() map[string][]types.ValidatorMetadata {
	gateValidators := make(map[string][]types.ValidatorMetadata)
	for _, validator := range r.validators {
		gateValidators[validator.Gate] = append(gateValidators[validator.Gate], validator)
	}
	return gateValidators
}

// processGate handles the execution of a single gate and its tests
func (r *runner) processGate(gateName string, validators []types.ValidatorMetadata, result *RunnerResult) error {
	gateStart := time.Now()
	gateResult := &GateResult{
		ID:     gateName,
		Tests:  make(map[string]*types.TestResult),
		Suites: make(map[string]*SuiteResult),
		Stats:  ResultStats{StartTime: gateStart},
	}
	result.Gates[gateName] = gateResult

	// Split validators into suites and direct tests
	suiteValidators, directTests := r.categorizeValidators(validators)

	// Process suites first
	if err := r.processSuites(suiteValidators, gateResult, result); err != nil {
		return err
	}

	// Then process direct gate tests
	if err := r.processDirectTests(directTests, gateResult, result); err != nil {
		return err
	}

	gateResult.Duration = time.Since(gateStart)
	gateResult.Status = determineGateStatus(gateResult)
	gateResult.Stats.EndTime = time.Now()

	return nil
}

// categorizeValidators splits validators into suite tests and direct gate tests
func (r *runner) categorizeValidators(validators []types.ValidatorMetadata) (map[string][]types.ValidatorMetadata, []types.ValidatorMetadata) {
	suiteValidators := make(map[string][]types.ValidatorMetadata)
	var directTests []types.ValidatorMetadata

	for _, validator := range validators {
		if validator.Suite != "" {
			suiteValidators[validator.Suite] = append(suiteValidators[validator.Suite], validator)
		} else {
			directTests = append(directTests, validator)
		}
	}
	return suiteValidators, directTests
}

// processSuites handles the execution of all suites in a gate
func (r *runner) processSuites(suiteValidators map[string][]types.ValidatorMetadata, gateResult *GateResult, result *RunnerResult) error {
	for suiteName, suiteTests := range suiteValidators {
		if err := r.processSuite(suiteName, suiteTests, gateResult, result); err != nil {
			return fmt.Errorf("processing suite %s: %w", suiteName, err)
		}
	}
	return nil
}

// processSuite handles the execution of a single suite
func (r *runner) processSuite(suiteName string, suiteTests []types.ValidatorMetadata, gateResult *GateResult, result *RunnerResult) error {
	suiteStart := time.Now()
	suiteResult := &SuiteResult{
		ID:    suiteName,
		Tests: make(map[string]*types.TestResult),
		Stats: ResultStats{StartTime: suiteStart},
	}
	gateResult.Suites[suiteName] = suiteResult

	// Run all tests in the suite
	for _, validator := range suiteTests {
		if err := r.processTestAndAddToResults(validator, gateResult, suiteResult, result); err != nil {
			return err
		}
	}

	suiteResult.Duration = time.Since(suiteStart)
	suiteResult.Status = determineSuiteStatus(suiteResult)
	suiteResult.Stats.EndTime = time.Now()

	return nil
}

// processDirectTests handles the execution of direct gate tests
func (r *runner) processDirectTests(directTests []types.ValidatorMetadata, gateResult *GateResult, result *RunnerResult) error {
	for _, validator := range directTests {
		if err := r.processTestAndAddToResults(validator, gateResult, nil, result); err != nil {
			return err
		}
	}
	return nil
}

// processTestAndAddToResults runs a single test and adds its results to the appropriate result containers
func (r *runner) processTestAndAddToResults(validator types.ValidatorMetadata, gateResult *GateResult, suiteResult *SuiteResult, result *RunnerResult) error {
	testResult, err := r.RunTest(validator)
	if err != nil {
		return fmt.Errorf("running test %s: %w", validator.ID, err)
	}

	// Get the appropriate key for the test
	testKey := r.getTestKey(validator)

	// Add to suite if provided, otherwise to gate directly
	if suiteResult != nil {
		suiteResult.Tests[testKey] = testResult
	} else {
		gateResult.Tests[testKey] = testResult
	}

	// Update stats in all relevant result containers
	result.updateStats(gateResult, suiteResult, testResult)

	return nil
}

// getTestKey returns the appropriate key to use for a test in result maps
func (r *runner) getTestKey(validator types.ValidatorMetadata) string {
	if validator.RunAll {
		// For package tests that use RunAll, use the package as the key
		return validator.Package
	}
	// For normal tests, use the function name
	return validator.FuncName
}

// RunTest implements the TestRunner interface
func (r *runner) RunTest(metadata types.ValidatorMetadata) (*types.TestResult, error) {
	// Use defer and recover to catch panics and convert them to errors
	var result *types.TestResult
	var err error

	defer func() {
		if rec := recover(); rec != nil {
			errMsg := fmt.Sprintf("runtime error: %v", rec)
			r.log.Error("Panic in RunTest", "error", errMsg, "test", metadata.FuncName)

			// If result hasn't been initialized yet, create one
			if result == nil {
				result = &types.TestResult{
					Metadata: metadata,
					Status:   types.TestStatusFail,
					Error:    fmt.Errorf("%s", errMsg),
				}
			} else {
				// Otherwise just set the error and status
				result.Status = types.TestStatusFail
				result.Error = fmt.Errorf("%s", errMsg)
			}

			// Set error so it's returned properly
			err = fmt.Errorf("%s", errMsg)
		}
	}()

	start := time.Now()
	r.log.Info("Running test", "validator", metadata.ID)

	if metadata.RunAll {
		result, err = r.runAllTestsInPackage(metadata)
	} else {
		result, err = r.runSingleTest(metadata)
	}

	var status types.TestStatus
	if result != nil {
		result.Duration = time.Since(start)
		status = result.Status
	} else {
		status = types.TestStatusError
	}
	// TODO: handle network
	// https://github.com/ethereum-optimism/infra/issues/193
	metrics.RecordValidation("todo", r.runID, metadata.ID, metadata.Type.String(), status)

	return result, err
}

// runAllTestsInPackage discovers and runs all tests in a package
func (r *runner) runAllTestsInPackage(metadata types.ValidatorMetadata) (*types.TestResult, error) {
	testNames, err := r.listTestsInPackage(metadata.Package)
	if err != nil {
		return nil, fmt.Errorf("listing tests in package %s: %w", metadata.Package, err)
	}

	r.log.Info("Found tests in package",
		"package", metadata.Package,
		"count", len(testNames),
		"tests", strings.Join(testNames, ", "))

	return r.runTestList(metadata, testNames)
}

// listTestsInPackage returns all test names in a package
func (r *runner) listTestsInPackage(pkg string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	listCmd := exec.CommandContext(ctx, r.goBinary, "test", pkg, "-list", "^Test")
	listCmd.Dir = r.workDir

	var listOut, listOutErr bytes.Buffer
	listCmd.Stdout = &listOut
	listCmd.Stderr = &listOutErr

	r.log.Debug("Listing tests in package",
		"package", pkg,
		"command", listCmd.String())

	if err := listCmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("listing tests timed out after 30s")
		}
		return nil, fmt.Errorf("command error: %w\nstderr: %s", err, listOutErr.String())
	}

	return parseTestListOutput(listOut.Bytes()), nil
}

// parseTestListOutput extracts valid test names from go test -list output
func parseTestListOutput(output []byte) []string {
	var testNames []string

	for _, line := range bytes.Split(output, []byte("\n")) {
		testName := string(bytes.TrimSpace(line))
		if isValidTestName(testName) {
			testNames = append(testNames, testName)
		}
	}

	return testNames
}

// isValidTestName returns true if the name represents a valid test
func isValidTestName(name string) bool {
	// Reject empty or specific strings like "ok" and strings with question marks
	if name == "" || name == "ok" || strings.HasPrefix(name, "?") {
		return false
	}

	// Filter out lines that start with "ok" and have the package name and timing info
	// Example: "ok github.com/ethereum-optimism/optimism/kurtosis-devnet/pkg/kurtosis 0.335s"
	if strings.HasPrefix(name, "ok ") && strings.Contains(name, ".") && strings.HasSuffix(name, "s") {
		return false
	}

	return true
}

// runTestList runs multiple tests in the same package
func (r *runner) runTestList(metadata types.ValidatorMetadata, testNames []string) (*types.TestResult, error) {
	baseMetadata := metadata
	baseMetadata.RunAll = false

	var allErrors []string
	anySkipped := false

	// Log to structured logger
	r.log.Info("Running tests in package", "package", metadata.Package, "testCount", len(testNames))

	// Create a combined result for all tests in the package
	result := &types.TestResult{
		Metadata: metadata,
		Status:   types.TestStatusPass,               // Default to pass, will be updated below
		SubTests: make(map[string]*types.TestResult), // Initialize SubTests map
	}

	for _, testName := range testNames {
		testMetadata := baseMetadata
		testMetadata.FuncName = testName

		testResult, err := r.runSingleTest(testMetadata)
		if err != nil {
			allErrors = append(allErrors, fmt.Sprintf("Test %s error: %v", testName, err))
			continue
		}

		// Store this test as a subtest
		result.SubTests[testName] = testResult

		// Check test status
		switch testResult.Status {
		case types.TestStatusFail:
			if testResult.Error != nil {
				allErrors = append(allErrors, fmt.Sprintf("Test %s failed: %v", testName, testResult.Error))
			} else {
				allErrors = append(allErrors, fmt.Sprintf("Test %s failed with no error message", testName))
			}
		case types.TestStatusSkip:
			anySkipped = true
		}
	}

	// Determine overall status
	if len(allErrors) > 0 {
		result.Status = types.TestStatusFail
	} else if anySkipped {
		result.Status = types.TestStatusSkip
	} else {
		result.Status = types.TestStatusPass
	}

	// Format all errors into a single error message
	if len(allErrors) > 0 {
		result.Error = fmt.Errorf("%s", r.formatErrors(allErrors))
	}

	// Log to structured logger
	r.log.Info("Package tests completed",
		"package", metadata.Package,
		"status", result.Status,
		"error", result.Error)

	return result, nil
}

// runSingleTest runs a specific test
func (r *runner) runSingleTest(metadata types.ValidatorMetadata) (*types.TestResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()

	// Setup and prepare for test run
	result, cmd, testID, buffers := r.prepareTest(ctx, metadata)
	var stdout, stderr = buffers.stdout, buffers.stderr

	// Execute the test
	testStartTime := time.Now()
	r.logTestHeader(testID, metadata.Package, cmd.String(), testStartTime)

	err := cmd.Run()

	testEndTime := time.Now()
	stdoutStr, stderrStr := stdout.String(), stderr.String()
	duration := testEndTime.Sub(testStartTime)

	// Log outputs
	r.logTestOutput(stdoutStr, stderrStr)

	// Process results
	result, resultErr := r.processTestResult(result, ctx, err, stdoutStr, stderrStr)

	// For tests that run all tests in a package, extract the individual test results
	if metadata.RunAll && result != nil {
		// Parse the test output to extract all test names and statuses
		subtests := parseTestOutput(stdoutStr, stderrStr)

		r.log.Debug(
			"Parsed test output for subtests",
			"metadata", metadata,
			"subtestCount", len(subtests),
			"subtestNames", getKeys(subtests),
			"stdout", stdoutStr,
		)

		// Make sure we have a result before setting SubTests
		if result.SubTests == nil {
			result.SubTests = make(map[string]*types.TestResult)
		}

		// Add the subtests to the result
		for testName, subTest := range subtests {
			result.SubTests[testName] = subTest
		}
	}

	// Log result status
	r.logTestResult(testID, metadata.Package, result.Status, resultErr, duration, err)

	return result, resultErr
}

// getKeys returns the keys of a map as a slice of strings
func getKeys(m map[string]*types.TestResult) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// Buffers holds stdout and stderr buffers
type testBuffers struct {
	stdout, stderr *bytes.Buffer
}

// prepareTest prepares the test command and initializes the result
func (r *runner) prepareTest(ctx context.Context, metadata types.ValidatorMetadata) (*types.TestResult, *exec.Cmd, string, testBuffers) {
	args := r.buildTestArgs(metadata)
	cmd := exec.CommandContext(ctx, r.goBinary, args...)
	cmd.Dir = r.workDir

	// Set environment variables
	env := os.Environ()
	if !r.allowSkips {
		env = append(env, "DEVNET_EXPECT_PRECONDITIONS_MET=true")
	}
	cmd.Env = env

	// Setup output capture
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Get test ID
	testID := metadata.FuncName
	if testID == "" {
		testID = "All tests in " + metadata.Package
	}

	// Log command
	r.log.Debug("Running test command",
		"dir", cmd.Dir,
		"package", metadata.Package,
		"test", metadata.FuncName,
		"command", cmd.String(),
		"timeout", r.timeout,
		"allowSkips", r.allowSkips)

	// Initialize result
	result := &types.TestResult{
		Metadata: metadata,
		Status:   types.TestStatusPass, // Default to pass unless determined otherwise
	}

	return result, cmd, testID, testBuffers{&stdout, &stderr}
}

// logTestHeader logs the test header information
func (r *runner) logTestHeader(testID, packageName, command string, startTime time.Time) {
	// Log test header with structured logger
	r.log.Debug("Starting test",
		"test", testID,
		"package", packageName,
		"command", command,
		"startTime", startTime)
}

// logTestOutput logs the stdout and stderr output
func (r *runner) logTestOutput(stdout, stderr string) {
	// Log detailed outputs at trace level
	if stdout != "" {
		r.log.Debug("Test stdout", "output", stdout)
	}
	if stderr != "" {
		r.log.Debug("Test stderr", "output", stderr)
	}
}

// processTestResult processes the result of a test execution
func (r *runner) processTestResult(result *types.TestResult, ctx context.Context, err error, stdout, stderr string) (*types.TestResult, error) {
	// Determine test status first based on context and error conditions
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		// Test timed out
		result.Status = types.TestStatusFail
		result.Error = fmt.Errorf("test timed out after %v", r.timeout)
		r.log.Warn("Test timed out", "timeout", r.timeout)

	case err != nil:
		// Command failed but may indicate test failure (not a Go execution error)
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.Status = types.TestStatusFail

			// Look for more specific errors in stdout/stderr
			detailedError := extractDetailedError(stdout, stderr)
			if detailedError != "" {
				result.Error = fmt.Errorf("%s\n%s", exitErr.Error(), detailedError)
			} else {
				result.Error = fmt.Errorf("%s\n%s", exitErr.Error(), stderr)
			}
			r.log.Debug("Test failed with exit error", "error", exitErr.Error(), "details", detailedError)
		} else {
			// Actual system error running the test command
			r.log.Error("System error running test", "error", err)
			return nil, fmt.Errorf("failed to execute test %s: %w", result.Metadata.FuncName, err)
		}

	default:
		// Command succeeded - check output for SKIP marker
		if strings.Contains(stdout, "--- SKIP:") {
			result.Status = types.TestStatusSkip

			// Extract skip reason if available
			var skipReason string
			if idx := strings.Index(stdout, "--- SKIP:"); idx != -1 {
				skipLine := stdout[idx:]
				if endIdx := strings.Index(skipLine, "\n"); endIdx != -1 {
					skipReason = skipLine[:endIdx]
				}
			}
			r.log.Debug("Test skipped", "reason", skipReason)
		} else {
			r.log.Debug("Test passed")
		}
	}

	return result, nil
}

// extractDetailedError extracts meaningful error information from test output
func extractDetailedError(stdout, stderr string) string {
	// Common test error patterns to search for, in order of importance
	patterns := []string{
		"precondition not met:", // Environment conditions not met
		"assertion failed:",     // Test assertion that failed
		"unexpected error:",     // Unexpected error during test
		"expected ",             // Expectation that was not met
		"want ",                 // Common pattern in go tests
		"got ",                  // Common pattern in go tests
		"Error:",                // General error message
		"Fatal:",                // Fatal error
		"panic:",                // Panic in test
	}

	// Helper to check both stdout and stderr
	checkOutput := func(pattern, output string) (string, bool) {
		if idx := strings.Index(output, pattern); idx != -1 {
			// Find start of line
			start := idx
			for start > 0 && output[start-1] != '\n' {
				start--
			}

			// Find end of line
			var end int
			if endIdx := strings.Index(output[idx:], "\n"); endIdx != -1 {
				end = idx + endIdx
			} else {
				end = len(output)
			}

			// Return the full line containing the pattern
			return output[start:end], true
		}
		return "", false
	}

	// Check patterns in order of priority
	for _, pattern := range patterns {
		// Check stderr first (usually more informative for errors)
		if msg, found := checkOutput(pattern, stderr); found {
			return msg
		}

		// Then check stdout
		if msg, found := checkOutput(pattern, stdout); found {
			return msg
		}
	}

	// If we couldn't find specific patterns but have stderr error content, use that
	if len(stderr) > 0 && !strings.Contains(stderr, "warning: ") {
		if idx := strings.Index(stderr, "Error"); idx != -1 {
			// Find start of line
			start := idx
			for start > 0 && stderr[start-1] != '\n' {
				start--
			}

			// Find end of line
			if endIdx := strings.Index(stderr[start:], "\n"); endIdx != -1 {
				return stderr[start : start+endIdx]
			}
			return stderr[start:]
		}

		// Just return the first line of stderr if we can't find a better error
		if idx := strings.Index(stderr, "\n"); idx != -1 {
			return stderr[:idx]
		}
		return stderr
	}

	// If we couldn't find specific patterns, look for failed test output in Go test format
	if idx := strings.Index(stdout, "--- FAIL:"); idx != -1 {
		var end int
		if newline := strings.Index(stdout[idx:], "\n"); newline != -1 {
			end = idx + newline
		} else {
			end = len(stdout)
		}
		return stdout[idx:end]
	}

	return ""
}

// logTestResult logs the test result information
func (r *runner) logTestResult(testID, packageName string, status types.TestStatus, resultErr error, duration time.Duration, cmdErr error) {
	// Log test completion with structured logger
	r.log.Info("Test completed",
		"test", testID,
		"package", packageName,
		"status", status,
		"duration", duration,
		"error", resultErr)

	// Log additional debug information for errors
	if cmdErr != nil {
		r.log.Debug("Test execution details",
			"test", testID,
			"package", packageName,
			"error", cmdErr)
	}
}

// buildTestArgs constructs the command line arguments for running a test
func (r *runner) buildTestArgs(metadata types.ValidatorMetadata) []string {
	args := []string{"test"}

	// Determine test target
	if metadata.Package != "" {
		args = append(args, metadata.Package)
	} else {
		// If no package specified, run in all packages
		args = append(args, "./...")
	}

	// Add specific test filter if not running all tests in package
	if !metadata.RunAll && metadata.FuncName != "" {
		args = append(args, "-run", fmt.Sprintf("^%s$", metadata.FuncName))
	}

	// Always disable caching
	args = append(args, "-count", "1")

	// Always use verbose output
	args = append(args, "-v")

	return args
}

// RunGate runs all tests in a specific gate
func (r *runner) RunGate(gate string) error {
	validators := r.registry.GetValidators()

	// Filter tests for this gate
	var gateValidators []types.ValidatorMetadata
	for _, v := range validators {
		if v.Gate == gate && v.Type == types.ValidatorTypeTest {
			gateValidators = append(gateValidators, v)
		}
	}

	if len(gateValidators) == 0 {
		return fmt.Errorf("no tests found for gate %s", gate)
	}

	// Run the tests
	result, err := r.RunAllTests()
	if err != nil {
		return err
	}

	// Check if the gate passed
	for _, g := range result.Gates {
		if g.ID == gate {
			if g.Status != types.TestStatusPass {
				return fmt.Errorf("gate %s failed", gate)
			}
			return nil
		}
	}

	return fmt.Errorf("gate %s not found in results", gate)
}

// GetValidators returns all validators in the test run
func (r *RunnerResult) GetValidators() []types.ValidatorMetadata {
	var validators []types.ValidatorMetadata
	for _, gate := range r.Gates {
		// Add gate tests
		for _, test := range gate.Tests {
			validators = append(validators, test.Metadata)
		}
		// Add suite tests
		for _, suite := range gate.Suites {
			for _, test := range suite.Tests {
				validators = append(validators, test.Metadata)
			}
		}
	}
	return validators
}

// formatDuration formats the duration to seconds with 1 decimal place
func formatDuration(d time.Duration) string {
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// String returns a formatted string representation of the test results
func (r *RunnerResult) String() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Test Run Results (%s):\n", formatDuration(r.Duration)))
	b.WriteString(fmt.Sprintf("Total: %d, Passed: %d, Failed: %d, Skipped: %d\n",
		r.Stats.Total, r.Stats.Passed, r.Stats.Failed, r.Stats.Skipped))

	for gateName, gate := range r.Gates {
		b.WriteString(fmt.Sprintf("\nGate: %s (%s)\n", gateName, formatDuration(gate.Duration)))
		b.WriteString(fmt.Sprintf("├── Status: %s\n", gate.Status))
		b.WriteString(fmt.Sprintf("├── Tests: %d passed, %d failed, %d skipped\n",
			gate.Stats.Passed, gate.Stats.Failed, gate.Stats.Skipped))

		// Print direct gate tests
		for testName, test := range gate.Tests {
			// Get a display name for the test
			displayName := types.GetTestDisplayName(testName, test.Metadata)

			b.WriteString(fmt.Sprintf("├── Test: %s (%s) [status=%s]\n",
				displayName, formatDuration(test.Duration), test.Status))
			if test.Error != nil {
				b.WriteString(fmt.Sprintf("│       └── Error: %s\n", test.Error.Error()))
			}

			// Print subtests if present
			if len(test.SubTests) > 0 {
				i := 0
				for subTestName, subTest := range test.SubTests {
					prefix := "│       ├──"
					if i == len(test.SubTests)-1 {
						prefix = "│       └──"
					}
					b.WriteString(fmt.Sprintf("│       %s Test: %s (%s) [status=%s]\n",
						prefix, subTestName, formatDuration(subTest.Duration), subTest.Status))
					if subTest.Error != nil {
						b.WriteString(fmt.Sprintf("│       │       └── Error: %s\n", subTest.Error.Error()))
					}
					i++
				}
			}
		}

		// Print suites
		for suiteName, suite := range gate.Suites {
			b.WriteString(fmt.Sprintf("└── Suite: %s (%s)\n", suiteName, formatDuration(suite.Duration)))
			b.WriteString(fmt.Sprintf("    ├── Status: %s\n", suite.Status))
			b.WriteString(fmt.Sprintf("    ├── Tests: %d passed, %d failed, %d skipped\n",
				suite.Stats.Passed, suite.Stats.Failed, suite.Stats.Skipped))

			// Print suite tests
			for testName, test := range suite.Tests {
				// Get a display name for the test
				displayName := types.GetTestDisplayName(testName, test.Metadata)

				b.WriteString(fmt.Sprintf("    ├── Test: %s (%s) [status=%s]\n",
					displayName, formatDuration(test.Duration), test.Status))
				if test.Error != nil {
					b.WriteString(fmt.Sprintf("    │       └── Error: %s\n", test.Error.Error()))
				}

				// Print subtests if present
				if len(test.SubTests) > 0 {
					i := 0
					for subTestName, subTest := range test.SubTests {
						prefix := "│       ├──"
						if i == len(test.SubTests)-1 {
							prefix = "│       └──"
						}
						b.WriteString(fmt.Sprintf("    │       %s Test: %s (%s) [status=%s]\n",
							prefix, subTestName, formatDuration(subTest.Duration), subTest.Status))
						if subTest.Error != nil {
							b.WriteString(fmt.Sprintf("    │       │       └── Error: %s\n", subTest.Error.Error()))
						}
						i++
					}
				}
			}
		}
	}
	return b.String()
}

// updateStats updates statistics at all levels
func (r *RunnerResult) updateStats(gate *GateResult, suite *SuiteResult, test *types.TestResult) {
	// Update test suite stats if applicable
	if suite != nil {
		suite.Stats.Total++
		switch test.Status {
		case types.TestStatusPass:
			suite.Stats.Passed++
		case types.TestStatusFail:
			suite.Stats.Failed++
		case types.TestStatusSkip:
			suite.Stats.Skipped++
		}
		suite.Duration += test.Duration
	}

	// Update gate stats
	gate.Stats.Total++
	switch test.Status {
	case types.TestStatusPass:
		gate.Stats.Passed++
	case types.TestStatusFail:
		gate.Stats.Failed++
	case types.TestStatusSkip:
		gate.Stats.Skipped++
	}
	gate.Duration += test.Duration

	// Update overall stats
	r.Stats.Total++
	switch test.Status {
	case types.TestStatusPass:
		r.Stats.Passed++
	case types.TestStatusFail:
		r.Stats.Failed++
	case types.TestStatusSkip:
		r.Stats.Skipped++
	}
	r.Duration += test.Duration

	// Update stats for SubTests if they exist
	if len(test.SubTests) > 0 {
		for _, subTest := range test.SubTests {
			// Update the global stats with this sub-test
			r.Stats.Total++
			switch subTest.Status {
			case types.TestStatusPass:
				r.Stats.Passed++
			case types.TestStatusFail:
				r.Stats.Failed++
			case types.TestStatusSkip:
				r.Stats.Skipped++
			}
			r.Duration += subTest.Duration

			// Update gate stats
			gate.Stats.Total++
			switch subTest.Status {
			case types.TestStatusPass:
				gate.Stats.Passed++
			case types.TestStatusFail:
				gate.Stats.Failed++
			case types.TestStatusSkip:
				gate.Stats.Skipped++
			}
			gate.Duration += subTest.Duration

			// Update suite stats
			if suite != nil {
				suite.Stats.Total++
				switch subTest.Status {
				case types.TestStatusPass:
					suite.Stats.Passed++
				case types.TestStatusFail:
					suite.Stats.Failed++
				case types.TestStatusSkip:
					suite.Stats.Skipped++
				}
				suite.Duration += subTest.Duration
			}
		}
	}
}

// determineGateStatus determines the overall status of a gate based on its tests and suites
func determineGateStatus(gate *GateResult) types.TestStatus {
	if len(gate.Tests) == 0 && len(gate.Suites) == 0 {
		return types.TestStatusSkip
	}

	allSkipped := true
	anyFailed := false

	// Check direct tests
	for _, test := range gate.Tests {
		if test.Status != types.TestStatusSkip {
			allSkipped = false
		}
		if test.Status == types.TestStatusFail {
			anyFailed = true
		}
	}

	// Check suites
	for _, suite := range gate.Suites {
		if suite.Status != types.TestStatusSkip {
			allSkipped = false
		}
		if suite.Status == types.TestStatusFail {
			anyFailed = true
		}
	}

	return determineStatusFromFlags(allSkipped, anyFailed)
}

// determineRunnerStatus determines the overall status of the test run
func determineRunnerStatus(result *RunnerResult) types.TestStatus {
	if len(result.Gates) == 0 {
		return types.TestStatusSkip
	}

	allSkipped := true
	anyFailed := false

	for _, gate := range result.Gates {
		if gate.Status != types.TestStatusSkip {
			allSkipped = false
		}
		if gate.Status == types.TestStatusFail {
			anyFailed = true
		}
	}

	return determineStatusFromFlags(allSkipped, anyFailed)
}

// determineStatusFromFlags is a helper that returns a status based on common flag logic
func determineStatusFromFlags(allSkipped, anyFailed bool) types.TestStatus {
	if allSkipped {
		return types.TestStatusSkip
	}
	if anyFailed {
		return types.TestStatusFail
	}
	return types.TestStatusPass
}

// formatErrors combines multiple test errors into a single error message
func (r *runner) formatErrors(errors []string) string {
	if len(errors) == 0 {
		return ""
	}
	return fmt.Sprintf("Failed tests:\n%s", strings.Join(errors, "\n"))
}

// determineSuiteStatus determines the overall status of a suite based on its tests
func determineSuiteStatus(suite *SuiteResult) types.TestStatus {
	if len(suite.Tests) == 0 {
		return types.TestStatusSkip
	}

	allSkipped := true
	anyFailed := false

	for _, test := range suite.Tests {
		if test.Status != types.TestStatusSkip {
			allSkipped = false
		}
		if test.Status == types.TestStatusFail {
			anyFailed = true
		}
	}

	return determineStatusFromFlags(allSkipped, anyFailed)
}

// parseTestOutput parses the output of a Go test run to extract test results
func parseTestOutput(stdout, _ string) map[string]*types.TestResult {
	subtests := make(map[string]*types.TestResult)
	lines := strings.Split(stdout, "\n")

	// First scan for all RUN lines to identify tests
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Look for test start line (=== RUN TestName)
		if strings.HasPrefix(trimmed, "=== RUN ") {
			parts := strings.Fields(trimmed)
			if len(parts) >= 3 {
				testName := parts[2]
				// Only consider top-level tests, not subtests with slashes
				if !strings.Contains(testName, "/") && isValidTestName(testName) {
					// Initialize test result if not exists
					if _, exists := subtests[testName]; !exists {
						subtests[testName] = &types.TestResult{
							Status: types.TestStatusPass, // Default to pass unless we find otherwise
						}
					}
				}
			}
		}
	}

	// Then scan for PASS/FAIL/SKIP status lines to update the status
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Look for test result lines (--- PASS/FAIL/SKIP: TestName)
		if strings.HasPrefix(trimmed, "--- PASS: ") {
			processResultLine(trimmed, types.TestStatusPass, subtests)
		} else if strings.HasPrefix(trimmed, "--- FAIL: ") {
			processResultLine(trimmed, types.TestStatusFail, subtests)
		} else if strings.HasPrefix(trimmed, "--- SKIP: ") {
			processResultLine(trimmed, types.TestStatusSkip, subtests)
		}
	}

	return subtests
}

// processResultLine extracts test name and status from a result line
func processResultLine(line string, status types.TestStatus, subtests map[string]*types.TestResult) {
	parts := strings.SplitN(line, ": ", 2)
	if len(parts) < 2 {
		return
	}

	// Extract test name and duration
	remainder := parts[1]
	durIdx := strings.LastIndex(remainder, " (")
	if durIdx == -1 {
		return
	}

	testName := strings.TrimSpace(remainder[:durIdx])

	// Handle subtests (e.g., TestName/SubTest) by extracting the main test name
	mainTest := testName
	if idx := strings.Index(testName, "/"); idx != -1 {
		mainTest = testName[:idx]
	}

	// Only process and include valid test names
	if isValidTestName(mainTest) {
		// Create test result if it doesn't exist yet
		if _, exists := subtests[mainTest]; !exists {
			subtests[mainTest] = &types.TestResult{
				Status: status,
			}
		} else {
			// If the test exists, update its status - FAIL trumps all, SKIP trumps PASS
			if status == types.TestStatusFail || (status == types.TestStatusSkip && subtests[mainTest].Status == types.TestStatusPass) {
				subtests[mainTest].Status = status
			}
		}
	}
}
