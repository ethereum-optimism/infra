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
}

// SuiteResult captures aggregated results for a test suite
type SuiteResult struct {
	ID     string
	Tests  []*TestResult
	Passed bool
}

// GateResult captures aggregated results for a gate
type GateResult struct {
	ID     string
	Tests  []*TestResult
	Suites map[string]*SuiteResult
	Passed bool
}

// RunnerResult captures the complete test run results
type RunnerResult struct {
	Gates  map[string]*GateResult
	Passed bool
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
	r.log.Debug("Running all tests")
	result := &RunnerResult{
		Gates: make(map[string]*GateResult),
	}

	for _, validator := range r.validators {
		r.log.Debug("Running test", "validator", validator.ID)
		// Process test based on its gate
		gateResult, exists := result.Gates[validator.Gate]
		if !exists {
			gateResult = &GateResult{
				ID:     validator.Gate,
				Tests:  make([]*TestResult, 0),
				Suites: make(map[string]*SuiteResult),
				Passed: true,
			}
			result.Gates[validator.Gate] = gateResult
		}

		testResult, err := r.RunTest(validator)
		if err != nil {
			r.log.Error("Error running test", "validator", validator.ID, "error", err)
			return nil, fmt.Errorf("running test %s: %w", validator.ID, err)
		}
		r.log.Debug("Test result", "validator", validator.ID, "result", testResult)

		// Add test to appropriate collection
		if validator.Suite != "" {
			suiteResult, exists := gateResult.Suites[validator.Suite]
			if !exists {
				suiteResult = &SuiteResult{
					ID:     validator.Suite,
					Tests:  make([]*TestResult, 0),
					Passed: true,
				}
				gateResult.Suites[validator.Suite] = suiteResult
			}
			suiteResult.Tests = append(suiteResult.Tests, testResult)
			suiteResult.Passed = suiteResult.Passed && testResult.Passed
		} else {
			gateResult.Tests = append(gateResult.Tests, testResult)
		}
		gateResult.Passed = gateResult.Passed && testResult.Passed
	}

	// Calculate final pass/fail
	result.Passed = true
	for _, gate := range result.Gates {
		result.Passed = result.Passed && gate.Passed
	}

	return result, nil
}

// RunTest implements the TestRunner interface
func (r *runner) RunTest(metadata types.ValidatorMetadata) (*TestResult, error) {
	r.log.Info("Running test", "validator", metadata.ID, "gate", metadata.Gate, "suite", metadata.Suite)

	if metadata.RunAll {
		return r.runAllTestsInPackage(metadata)
	}
	return r.runSingleTest(metadata)
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
