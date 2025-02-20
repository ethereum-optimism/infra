package runner

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

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
}

type Config struct {
	Registry   *registry.Registry
	TargetGate string
	WorkDir    string
	Log        log.Logger
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

	return &runner{
		registry:   cfg.Registry,
		validators: validators,
		workDir:    cfg.WorkDir,
		log:        cfg.Log,
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
	listCmd := exec.Command("go", "test", pkg, "-list", "^Test")
	listCmd.Dir = r.workDir
	var listOut, listOutErr bytes.Buffer
	listCmd.Stdout = &listOut
	listCmd.Stderr = &listOutErr

	if err := listCmd.Run(); err != nil {
		fmt.Println(listOutErr.String())
		return nil, fmt.Errorf("listing tests in package %s: %w", pkg, err)
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
	var errors []string

	for _, testName := range testNames {
		passed, output := r.runIndividualTest(metadata.Package, testName)
		if !passed {
			result = types.TestStatusFail
			errors = append(errors, fmt.Sprintf("%s: %s", testName, output))
		}
	}

	return &types.TestResult{
		Metadata: metadata,
		Status:   result,
		Error:    r.formatErrors(errors),
	}, nil
}

// runIndividualTest runs a single test and returns its result
func (r *runner) runIndividualTest(pkg, testName string) (bool, string) {
	r.log.Debug("Running individual test", "testName", testName)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", "test", pkg,
		"-count", "1", // disable caching
		"-run", fmt.Sprintf("^%s$", testName),
		"-v")
	cmd.Dir = r.workDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	output := stdout.String() + stderr.String()
	r.log.Debug("runIndividualTest",
		"pkg", pkg,
		"testName", testName,
		"stdout", stdout.String(),
		"stderr", stderr.String())

	return err == nil, output
}

// formatErrors combines multiple test errors into a single error message
func (r *runner) formatErrors(errors []string) string {
	if len(errors) == 0 {
		return ""
	}
	return fmt.Sprintf("Failed tests:\n%s", strings.Join(errors, "\n"))
}

// runSingleTest runs a specific test
func (r *runner) runSingleTest(metadata types.ValidatorMetadata) (*types.TestResult, error) {
	args := r.buildTestArgs(metadata)
	cmd := exec.Command("go", args...)
	cmd.Dir = r.workDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	log.Debug("Running test command",
		"dir", cmd.Dir,
		"package", metadata.Package,
		"command", cmd.String())

	err := cmd.Run()
	output := stdout.String() + stderr.String()
	fmt.Printf("----------\nstdout: %s\n----------\nstderr: %s\n----------\n", stdout.String(), stderr.String())

	// Check for skipped tests in output
	if strings.Contains(output, "--- SKIP:") {
		return &types.TestResult{
			Metadata: metadata,
			Status:   types.TestStatusSkip,
			Error:    output,
		}, nil
	}

	if err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return &types.TestResult{
				Metadata: metadata,
				Status:   types.TestStatusFail,
				Error:    output,
			}, nil
		}
		fmt.Printf(" > Error running test %s: %v\n", metadata.FuncName, err)
		return nil, fmt.Errorf("running test %s: %w", metadata.FuncName, err)
	}

	return &types.TestResult{
		Metadata: metadata,
		Status:   types.TestStatusPass,
	}, nil
}

// buildTestArgs constructs the command line arguments for running a test
func (r *runner) buildTestArgs(metadata types.ValidatorMetadata) []string {
	var args []string
	if metadata.Package != "" {
		args = []string{"test", metadata.Package}
	} else {
		args = []string{"test", "./..."}
	}

	if !metadata.RunAll {
		args = append(args, "-run", fmt.Sprintf("^%s$", metadata.FuncName))
	}
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
			if test.Error != "" {
				b.WriteString(fmt.Sprintf("│       └── Error: %s\n", test.Error))
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
				if test.Error != "" {
					b.WriteString(fmt.Sprintf("    │       └── Error: %s\n", test.Error))
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
