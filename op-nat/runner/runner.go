package runner

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"errors"

	"github.com/ethereum-optimism/infra/op-nat/metrics"
	"github.com/ethereum-optimism/infra/op-nat/registry"
	"github.com/ethereum-optimism/infra/op-nat/types"
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
	timeout    time.Duration // Add timeout configuration
}

type Config struct {
	Registry   *registry.Registry
	TargetGate string
	WorkDir    string
	Log        log.Logger
	Timeout    time.Duration // Add timeout configuration
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
	cfg.Log.Info("NewTestRunner()", "targetGate", cfg.TargetGate, "workDir", cfg.WorkDir)

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

	return &runner{
		registry:   cfg.Registry,
		validators: validators,
		workDir:    cfg.WorkDir,
		log:        cfg.Log,
		timeout:    cfg.Timeout,
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
		testResult, err := r.RunTest(validator)
		if err != nil {
			return fmt.Errorf("running test %s: %w", validator.ID, err)
		}

		suiteResult.Tests[validator.FuncName] = testResult
		result.updateStats(gateResult, suiteResult, testResult)
	}

	suiteResult.Duration = time.Since(suiteStart)
	suiteResult.Status = determineSuiteStatus(suiteResult)
	suiteResult.Stats.EndTime = time.Now()

	return nil
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

	if allSkipped {
		return types.TestStatusSkip
	}
	if anyFailed {
		return types.TestStatusFail
	}
	return types.TestStatusPass
}

// processDirectTests handles the execution of direct gate tests
func (r *runner) processDirectTests(directTests []types.ValidatorMetadata, gateResult *GateResult, result *RunnerResult) error {
	for _, validator := range directTests {
		testResult, err := r.RunTest(validator)
		if err != nil {
			return fmt.Errorf("running test %s: %w", validator.ID, err)
		}

		gateResult.Tests[validator.FuncName] = testResult
		result.updateStats(gateResult, nil, testResult)
	}
	return nil
}

// RunTest implements the TestRunner interface
func (r *runner) RunTest(metadata types.ValidatorMetadata) (*types.TestResult, error) {
	start := time.Now()
	r.log.Info("Running test", "validator", metadata.ID)

	var result *types.TestResult
	var err error
	if metadata.RunAll {
		result, err = r.runAllTestsInPackage(metadata)
	} else {
		result, err = r.runSingleTest(metadata)
	}

	if result != nil {
		result.Duration = time.Since(start)
	}

	// TODO: handle network
	// https://github.com/ethereum-optimism/infra/issues/193
	metrics.RecordValidation("todo", r.runID, metadata.ID, metadata.Type.String(), result.Status)
	return result, err
}

// runAllTestsInPackage discovers and runs all tests in a package
func (r *runner) runAllTestsInPackage(metadata types.ValidatorMetadata) (*types.TestResult, error) {
	testNames, err := r.listTestsInPackage(metadata.Package)
	if err != nil {
		return nil, err
	}
	r.log.Info("runAllTestsInPackage() found tests", "package", metadata.Package, "count", len(testNames))
	return r.runTestList(metadata, testNames)
}

// listTestsInPackage returns all test names in a package
func (r *runner) listTestsInPackage(pkg string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second) // Shorter timeout for listing
	defer cancel()

	listCmd := exec.CommandContext(ctx, "go", "test", pkg, "-list", "^Test")
	listCmd.Dir = r.workDir
	var listOut, listOutErr bytes.Buffer
	listCmd.Stdout = &listOut
	listCmd.Stderr = &listOutErr

	if err := listCmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("listing tests timed out after 30s")
		}
		return nil, fmt.Errorf("listing tests in package %s: %w\n%s", pkg, err, listOutErr.String())
	}

	var testNames []string
	for _, line := range bytes.Split(listOut.Bytes(), []byte("\n")) {
		testName := string(bytes.TrimSpace(line))
		if isValidTestName(testName) {
			testNames = append(testNames, testName)
		}
	}
	return testNames, nil
}

// isValidTestName returns true if the name represents a valid test
func isValidTestName(name string) bool {
	return name != "" && name != "ok" && !strings.HasPrefix(name, "?")
}

// runTestList runs a list of tests and aggregates their results
func (r *runner) runTestList(metadata types.ValidatorMetadata, testNames []string) (*types.TestResult, error) {
	var result types.TestStatus = types.TestStatusPass
	var testErrors []error

	for _, testName := range testNames {
		testMetadata := metadata
		testMetadata.FuncName = testName

		testResult, err := r.runSingleTest(testMetadata)
		if err != nil {
			return nil, err
		}

		if testResult.Status == types.TestStatusFail {
			result = types.TestStatusFail
			if testResult.Error != nil {
				testErrors = append(testErrors, fmt.Errorf("%s: %w", testName, testResult.Error))
			}
		}
	}

	return &types.TestResult{
		Metadata: metadata,
		Status:   result,
		Error:    errors.Join(testErrors...),
	}, nil
}

// runSingleTest runs a specific test
func (r *runner) runSingleTest(metadata types.ValidatorMetadata) (*types.TestResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()

	args := r.buildTestArgs(metadata)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = r.workDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	r.log.Debug("Running test command",
		"dir", cmd.Dir,
		"package", metadata.Package,
		"command", cmd.String(),
		"timeout", r.timeout)

	result := types.TestResult{
		Metadata: metadata,
		Status:   types.TestStatusPass,
	}

	err := cmd.Run()

	// Check for timeout
	if ctx.Err() == context.DeadlineExceeded {
		result.Status = types.TestStatusFail
		result.Error = fmt.Errorf("test timed out after %v", r.timeout)
		return &result, nil
	}

	// Check if the command failed to run
	if err != nil {
		r.log.Debug("runSingleTest - test did not complete successfully", "name", metadata.FuncName, "error", err)
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.Status = types.TestStatusFail
			result.Error = fmt.Errorf("%s\n%s", exitErr.Error(), stderr.String())
			return &result, nil
		}
		return &result, fmt.Errorf("running test %s: %w", metadata.FuncName, err)
	}

	r.log.Debug("runSingleTest",
		"gate", metadata.Gate,
		"suite", metadata.Suite,
		"pkg", metadata.Package,
		"testName", metadata.FuncName,
		"stdout", stdout.String(),
		"stderr", stderr.String(),
	)

	// Check for skipped tests in output
	if strings.Contains(stdout.String(), "--- SKIP:") {
		result.Status = types.TestStatusSkip
	}

	return &result, nil
}

// buildTestArgs constructs the command line arguments for running a test
func (r *runner) buildTestArgs(metadata types.ValidatorMetadata) []string {
	var args []string = []string{"test"}

	// Run all tests in a package, or a particular test in a package
	if metadata.Package != "" {
		args = append(args, metadata.Package)
	} else {
		args = append(args, "./...")
	}

	// Run a specific test in a package
	if !metadata.RunAll {
		args = append(args, "-run", fmt.Sprintf("^%s$", metadata.FuncName))
	}

	// Disable caching
	args = append(args, "-count", "1")

	// Enable verbose output
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
			b.WriteString(fmt.Sprintf("├── Test: %s (%s) [status=%s]\n",
				testName, formatDuration(test.Duration), test.Status))
			if test.Error != nil {
				b.WriteString(fmt.Sprintf("│       └── Error: %s\n", test.Error.Error()))
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
				b.WriteString(fmt.Sprintf("    ├── Test: %s (%s) [status=%s]\n",
					testName, formatDuration(test.Duration), test.Status))
				if test.Error != nil {
					b.WriteString(fmt.Sprintf("    │       └── Error: %s\n", test.Error.Error()))
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

	if allSkipped {
		return types.TestStatusSkip
	}
	if anyFailed {
		return types.TestStatusFail
	}
	return types.TestStatusPass
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
