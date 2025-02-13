package runner

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/ethereum-optimism/infra/op-nat/registry"
	"github.com/ethereum-optimism/infra/op-nat/types"
	"github.com/ethereum/go-ethereum/log"
)

// TestResult captures the outcome of a single test run
type TestResult struct {
	Metadata types.ValidatorMetadata
	Passed   bool
	Error    string
	Duration time.Duration // Track test execution time
}

// SuiteResult captures aggregated results for a test suite
type SuiteResult struct {
	ID          string
	Description string
	Tests       map[string]*TestResult // Map test names to results
	Passed      bool
	Duration    time.Duration
	Stats       ResultStats
}

// GateResult captures aggregated results for a gate
type GateResult struct {
	ID          string
	Description string
	Tests       map[string]*TestResult  // Direct gate tests
	Suites      map[string]*SuiteResult // Test suites
	Passed      bool
	Duration    time.Duration
	Stats       ResultStats
	Inherited   []string // Track which gates this inherits from
}

// RunnerResult captures the complete test run results
type RunnerResult struct {
	Gates    map[string]*GateResult
	Passed   bool
	Duration time.Duration
	Stats    ResultStats
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
	RunTest(metadata types.ValidatorMetadata) (*TestResult, error)
}

type runner struct {
	registry   *registry.Registry
	validators []types.ValidatorMetadata
	workDir    string // Directory for running tests
	log        log.Logger
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
	start := time.Now()
	r.log.Debug("Running all tests")

	result := &RunnerResult{
		Gates: make(map[string]*GateResult),
		Stats: ResultStats{StartTime: start},
	}

	if err := r.processAllGates(result); err != nil {
		return nil, err
	}

	result.Duration = time.Since(start)
	result.Passed = result.Stats.Failed == 0
	result.Stats.EndTime = time.Now()

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
		Tests:  make(map[string]*TestResult),
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
	gateResult.Passed = gateResult.Stats.Failed == 0
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
		Tests: make(map[string]*TestResult),
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
	suiteResult.Passed = suiteResult.Stats.Failed == 0
	suiteResult.Stats.EndTime = time.Now()

	return nil
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
func (r *runner) RunTest(metadata types.ValidatorMetadata) (*TestResult, error) {
	start := time.Now()
	r.log.Info("Running test", "validator", metadata.ID)

	var result *TestResult
	var err error
	if metadata.RunAll {
		result, err = r.runAllTestsInPackage(metadata)
	} else {
		result, err = r.runSingleTest(metadata)
	}

	if result != nil {
		result.Duration = time.Since(start)
	}
	return result, err
}

// runAllTestsInPackage discovers and runs all tests in a package
func (r *runner) runAllTestsInPackage(metadata types.ValidatorMetadata) (*TestResult, error) {
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
func (r *runner) runTestList(metadata types.ValidatorMetadata, testNames []string) (*TestResult, error) {
	allPassed := true
	var errors []string

	for _, testName := range testNames {
		passed, output := r.runIndividualTest(metadata.Package, testName)
		if !passed {
			allPassed = false
			errors = append(errors, fmt.Sprintf("%s: %s", testName, output))
		}
	}

	return &TestResult{
		Metadata: metadata,
		Passed:   allPassed,
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
	r.log.Debug("runIndividualTest()", "output", output)

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
func (r *runner) runSingleTest(metadata types.ValidatorMetadata) (*TestResult, error) {
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
	fmt.Print(output)

	if err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return &TestResult{
				Metadata: metadata,
				Passed:   false,
				Error:    output,
			}, nil
		}
		fmt.Printf(" > Error running test %s: %v\n", metadata.FuncName, err)
		return nil, fmt.Errorf("running test %s: %w", metadata.FuncName, err)
	}

	return &TestResult{
		Metadata: metadata,
		Passed:   true,
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
			if !g.Passed {
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
		b.WriteString(fmt.Sprintf("├── Passed: %v\n", gate.Passed))
		b.WriteString(fmt.Sprintf("├── Tests: %d passed, %d failed\n",
			gate.Stats.Passed, gate.Stats.Failed))

		// Print direct gate tests
		for testName, test := range gate.Tests {
			b.WriteString(fmt.Sprintf("├── Test: %s (%s) [pass=%v]\n", testName, formatDuration(test.Duration), test.Passed))
			if test.Error != "" {
				b.WriteString(fmt.Sprintf("│       └── Error: %s\n", test.Error))
			}
		}

		// Print suites
		for suiteName, suite := range gate.Suites {
			b.WriteString(fmt.Sprintf("└── Suite: %s (%s)\n", suiteName, formatDuration(suite.Duration)))
			b.WriteString(fmt.Sprintf("    ├── Passed: %v\n", suite.Passed))
			b.WriteString(fmt.Sprintf("    ├── Tests: %d passed, %d failed\n",
				suite.Stats.Passed, suite.Stats.Failed))

			// Print suite tests
			for testName, test := range suite.Tests {
				b.WriteString(fmt.Sprintf("    ├── Test: %s (%s) [pass=%v]\n", testName, formatDuration(test.Duration), test.Passed))
				if test.Error != "" {
					b.WriteString(fmt.Sprintf("    │       └── Error: %s\n", test.Error))
				}
			}
		}
	}
	return b.String()
}

// Helper method to update stats at all levels
func (r *RunnerResult) updateStats(gate *GateResult, suite *SuiteResult, test *TestResult) {
	// Update test suite stats if applicable
	if suite != nil {
		// Only increment total for actual tests
		suite.Stats.Total++
		if test.Passed {
			suite.Stats.Passed++
		} else {
			suite.Stats.Failed++
		}
		suite.Duration += test.Duration
	}

	// Update gate stats - only count actual tests
	gate.Stats.Total++
	if test.Passed {
		gate.Stats.Passed++
	} else {
		gate.Stats.Failed++
	}
	gate.Duration += test.Duration

	// Update overall stats - only count actual tests
	r.Stats.Total++
	if test.Passed {
		r.Stats.Passed++
	} else {
		r.Stats.Failed++
	}
	r.Duration += test.Duration
}
