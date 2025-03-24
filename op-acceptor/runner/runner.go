package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"errors"

	"github.com/ethereum-optimism/infra/op-acceptor/metrics"
	"github.com/ethereum-optimism/infra/op-acceptor/registry"
	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/google/uuid"
)

// Go test2json (TestEvent)action constants for JSON test output
// See https://cs.opensource.google/go/go/+/master:src/cmd/test2json/main.go;l=34-60
const (
	ActionStart  = "start"
	ActionPass   = "pass"
	ActionFail   = "fail"
	ActionSkip   = "skip"
	ActionOutput = "output"
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
}

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

	return &runner{
		registry:   cfg.Registry,
		validators: validators,
		workDir:    cfg.WorkDir,
		log:        cfg.Log,
		timeout:    cfg.Timeout,
		goBinary:   cfg.GoBinary,
		allowSkips: cfg.AllowSkips,
	}, nil
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

// runTestList runs a list of tests and aggregates their results
func (r *runner) runTestList(metadata types.ValidatorMetadata, testNames []string) (*types.TestResult, error) {
	if len(testNames) == 0 {
		r.log.Warn("No tests found to run in package", "package", metadata.Package)
		return &types.TestResult{
			Metadata: metadata,
			Status:   types.TestStatusSkip,
			Duration: 0,
			SubTests: make(map[string]*types.TestResult),
		}, nil
	}

	var result types.TestStatus = types.TestStatusPass
	var testErrors []error
	var totalDuration time.Duration
	testResults := make(map[string]*types.TestResult)
	var failedTestsStdout strings.Builder

	// Run each test in the list
	for _, testName := range testNames {
		// Create a new metadata with the specific test name
		testMetadata := metadata
		testMetadata.FuncName = testName

		// Run the individual test
		testResult, err := r.runSingleTest(testMetadata)
		if err != nil {
			return nil, fmt.Errorf("running test %s: %w", testName, err)
		}

		// Store the individual test result
		testResults[testName] = testResult
		totalDuration += testResult.Duration

		// Update overall status based on individual test result
		if testResult.Status == types.TestStatusFail {
			result = types.TestStatusFail
			if testResult.Error != nil {
				testErrors = append(testErrors, fmt.Errorf("%s: %w", testName, testResult.Error))
			}

			// Collect stdout from failing tests
			if testResult.Stdout != "" {
				failedTestsStdout.WriteString(fmt.Sprintf("\n--- Test: %s ---\n", testName))
				failedTestsStdout.WriteString(testResult.Stdout)
			}
		}
	}

	// If any tests failed, log the collected stdout
	failedStdout := failedTestsStdout.String()
	if result == types.TestStatusFail && failedStdout != "" {
		r.log.Debug("Package test failed",
			"package", metadata.Package,
			"stdout_from_failed_tests", failedStdout)
	}

	// Create the aggregate result
	return &types.TestResult{
		Metadata: metadata,
		Status:   result,
		Error:    errors.Join(testErrors...),
		Duration: totalDuration,
		SubTests: testResults,
		Stdout:   failedStdout,
	}, nil
}

// TestEvent represents a single event from the go test JSON output
type TestEvent struct {
	Time    time.Time // Time the event occurred
	Action  string    // The action taken (run, pause, cont, pass, fail, skip, output)
	Package string    // The package being tested
	Test    string    // The test function name (may be empty for package events)
	Output  string    // Output text (may be empty)
	Elapsed float64   // Elapsed time in seconds for the specific action
}

// runSingleTest runs a specific test
func (r *runner) runSingleTest(metadata types.ValidatorMetadata) (*types.TestResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()

	args := r.buildTestArgs(metadata)
	cmd := exec.CommandContext(ctx, r.goBinary, args...)
	cmd.Dir = r.workDir

	// Set environment variables for the test
	env := os.Environ()
	if !r.allowSkips {
		// Set DEVNET_EXPECT_PRECONDITIONS_MET=true to make tests fail instead of skip when preconditions are not met
		env = append(env, "DEVNET_EXPECT_PRECONDITIONS_MET=true")
	}
	cmd.Env = env

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	r.log.Debug("Running test command",
		"dir", cmd.Dir,
		"package", metadata.Package,
		"test", metadata.FuncName,
		"command", cmd.String(),
		"timeout", r.timeout,
		"allowSkips", r.allowSkips)

	// Run the command
	err := cmd.Run()

	// Check for timeout first
	if ctx.Err() == context.DeadlineExceeded {
		return &types.TestResult{
			Metadata: metadata,
			Status:   types.TestStatusFail,
			Error:    fmt.Errorf("test timed out after %v", r.timeout),
		}, nil
	}

	// Parse the JSON output
	parsedResult := r.parseTestOutput(stdout.Bytes(), metadata)

	// If we couldn't parse the output for some reason, create a minimal failing result
	if parsedResult == nil {
		parsedResult = &types.TestResult{
			Metadata: metadata,
			Status:   types.TestStatusFail,
			Error:    fmt.Errorf("failed to parse test output"),
		}
	}

	// Capture stdout in the test result when failing
	if (parsedResult.Status == types.TestStatusFail || parsedResult.Status == types.TestStatusSkip) && stdout.Len() > 0 {
		parsedResult.Stdout = stdout.String()
	}

	// Add any stderr output to the error
	if err != nil && stderr.Len() > 0 {
		if parsedResult.Error != nil {
			parsedResult.Error = fmt.Errorf("%w\nstderr: %s", parsedResult.Error, stderr.String())
		} else {
			parsedResult.Error = fmt.Errorf("stderr: %s", stderr.String())
		}
	}

	return parsedResult, nil
}

// parseTestOutput parses the JSON test output and extracts test result information
func (r *runner) parseTestOutput(output []byte, metadata types.ValidatorMetadata) *types.TestResult {
	if len(output) == 0 {
		r.log.Debug("Empty test output", "test", metadata.FuncName, "package", metadata.Package)
		return newFailedTestResult(metadata, fmt.Errorf("empty test output"))
	}

	result := &types.TestResult{
		Metadata: metadata,
		Status:   types.TestStatusPass, // Default to pass unless determined otherwise
		SubTests: make(map[string]*types.TestResult),
	}

	var testStart, testEnd time.Time
	var errorMsg strings.Builder
	var hasSkip bool
	var hasAnyValidEvent bool

	subTestStatuses := make(map[string]types.TestStatus)
	lines := bytes.Split(output, []byte("\n"))

	for _, line := range lines {
		if len(line) == 0 {
			continue
		}

		event, err := parseTestEvent(line)
		if err != nil {
			r.log.Debug("Failed to parse test JSON output line", "error", err, "line", string(line))
			continue
		}

		hasAnyValidEvent = true

		if isMainTestEvent(event, metadata.FuncName) {
			processMainTestEvent(event, result, &testStart, &testEnd, &errorMsg, &hasSkip)
		} else {
			processSubTestEvent(event, result, subTestStatuses, &hasSkip)
		}
	}

	if !hasAnyValidEvent {
		return newFailedTestResult(metadata, fmt.Errorf("no valid JSON output from test"))
	}

	// Set the test duration
	result.Duration = calculateTestDuration(testStart, testEnd)

	// Set the error message if any
	if errorMsg.Len() > 0 {
		result.Error = fmt.Errorf("%s", errorMsg.String())
	}

	// Final check for skipped tests
	if hasSkip && result.Status != types.TestStatusFail && len(result.SubTests) == 0 {
		result.Status = types.TestStatusSkip
	}

	r.log.Debug("Parsed test output",
		"test", metadata.FuncName,
		"package", metadata.Package,
		"status", result.Status,
		"subtests", len(result.SubTests),
		"hasAnyValidEvent", hasAnyValidEvent,
		"hasError", result.Error != nil)

	return result
}

// parseTestEvent parses a single line of test output into a TestEvent
func parseTestEvent(line []byte) (TestEvent, error) {
	var event TestEvent
	err := json.Unmarshal(line, &event)
	return event, err
}

// isMainTestEvent checks if the event belongs to the main test or package
func isMainTestEvent(event TestEvent, mainTestName string) bool {
	return event.Test == "" || event.Test == mainTestName
}

// processMainTestEvent handles events for the main test
func processMainTestEvent(event TestEvent, result *types.TestResult, testStart, testEnd *time.Time,
	errorMsg *strings.Builder, hasSkip *bool) {
	switch event.Action {
	case ActionStart:
		*testStart = event.Time
	case ActionPass:
		*testEnd = event.Time
		result.Status = types.TestStatusPass
	case ActionFail:
		*testEnd = event.Time
		result.Status = types.TestStatusFail
	case ActionSkip:
		*testEnd = event.Time
		result.Status = types.TestStatusSkip
		*hasSkip = true
	case ActionOutput:
		if event.Output != "" {
			errorMsg.WriteString(event.Output)
		}
	}
}

// processSubTestEvent handles events for subtests
func processSubTestEvent(event TestEvent, result *types.TestResult,
	subTestStatuses map[string]types.TestStatus, hasSkip *bool) {
	subTest, exists := result.SubTests[event.Test]
	if !exists {
		subTest = &types.TestResult{
			Metadata: types.ValidatorMetadata{
				FuncName: event.Test,
				Package:  result.Metadata.Package,
			},
			Status: types.TestStatusPass, // Default to pass
		}
		result.SubTests[event.Test] = subTest
	}

	switch event.Action {
	case ActionPass:
		subTest.Status = types.TestStatusPass
		subTestStatuses[event.Test] = types.TestStatusPass
	case ActionFail:
		subTest.Status = types.TestStatusFail
		subTestStatuses[event.Test] = types.TestStatusFail
		// A failing subtest means the main test fails too
		result.Status = types.TestStatusFail
	case ActionSkip:
		subTest.Status = types.TestStatusSkip
		subTestStatuses[event.Test] = types.TestStatusSkip
		*hasSkip = true
	case ActionOutput:
		updateSubTestError(subTest, event.Output)
	}
}

// updateSubTestError updates a subtest's error message
func updateSubTestError(subTest *types.TestResult, output string) {
	if output == "" {
		return
	}

	if subTest.Error == nil {
		subTest.Error = fmt.Errorf("%s", output)
	} else {
		subTest.Error = fmt.Errorf("%s\n%s", subTest.Error.Error(), output)
	}
}

// calculateTestDuration calculates the duration of a test
func calculateTestDuration(start, end time.Time) time.Duration {
	if !start.IsZero() && !end.IsZero() {
		return end.Sub(start)
	} else if !start.IsZero() {
		// If we have a start but no end, use time since start
		return time.Since(start)
	}
	return 0
}

// newFailedTestResult creates a new failed test result
func newFailedTestResult(metadata types.ValidatorMetadata, err error) *types.TestResult {
	return &types.TestResult{
		Metadata: metadata,
		Status:   types.TestStatusFail,
		Error:    err,
		SubTests: make(map[string]*types.TestResult),
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

	// Always use JSON output for more reliable parsing
	args = append(args, "-json")

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
