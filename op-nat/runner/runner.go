package runner

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"testing"

	"github.com/ethereum-optimism/infra/op-nat/discovery"
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

var (
	validators []types.ValidatorMetadata
	results    = &RunnerResult{
		Gates:  make(map[string]*GateResult),
		Passed: true,
	}
	resultsMu sync.Mutex
)

// TestRunner defines the interface for running acceptance tests
type TestRunner interface {
	RunAllTests() (*RunnerResult, error)
	RunTest(metadata types.ValidatorMetadata) (*TestResult, error)
}

type runner struct {
	registry   *registry.Registry
	validators []types.ValidatorMetadata
}

// NewTestRunner creates a new test runner instance
func NewTestRunner(reg *registry.Registry) (TestRunner, error) {
	// Discover tests once during initialization
	validators, err := discovery.DiscoverTests(discovery.Config{
		ConfigFile: reg.GetConfig().Source.ConfigPath,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to discover tests: %w", err)
	}

	return &runner{
		registry:   reg,
		validators: validators,
	}, nil
}

// RunAllTests implements the TestRunner interface
func (r *runner) RunAllTests() (*RunnerResult, error) {
	log.Debug("Running all tests")
	result := &RunnerResult{
		Gates: make(map[string]*GateResult),
	}

	for _, validator := range r.validators {
		fmt.Printf("Running test: %s, gate: %s, suite: %s\n", validator.ID, validator.Gate, validator.Suite)
		log.Debug("Running test", "validator", validator.ID)
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
			fmt.Printf("Error running test %s: %v\n", validator.ID, err)
			return nil, fmt.Errorf("running test %s: %w", validator.ID, err)
		}
		log.Debug("Test result", "validator", validator.ID, "result", testResult)

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
	fmt.Printf(" > Running test: %s, gate: %s, suite: %s\n", metadata.ID, metadata.Gate, metadata.Suite)
	result := &TestResult{
		Metadata: metadata,
		Passed:   false,
	}

	// Build the test command with specific package
	var args []string
	if metadata.Package != "" {
		// If package is specified, use it directly
		args = []string{"test", metadata.Package}
	} else {
		// Otherwise search in working directory
		args = []string{"test", "./..."}
	}

	// Add test filter
	args = append(args,
		"-run", fmt.Sprintf("^%s$", metadata.FuncName), // Exact match for test name
		"-v",
	)

	cmd := exec.Command("go", args...)
	cmd.Dir = r.registry.GetConfig().WorkDir

	// Capture output
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Log the command being run and its working directory for debugging
	fmt.Printf(" > Running test command: %s, dir: %s, package: %s\n",
		metadata.FuncName, cmd.Dir, metadata.Package)
	log.Debug("Running test command",
		"dir", cmd.Dir,
		"package", metadata.Package,
		"command", cmd.String())

	// Run the test
	err := cmd.Run()
	if err != nil {
		// Test failed but ran successfully
		if _, ok := err.(*exec.ExitError); ok {
			result.Error = stdout.String() + stderr.String()
			return result, nil
		}
		// Something else went wrong
		fmt.Printf(" > Error running test %s: %v\n", metadata.FuncName, err)
		return result, fmt.Errorf("running test %s: %w", metadata.FuncName, err)
	}

	// Test passed
	result.Passed = true
	return result, nil
}

// TestMain is the entry point for our test suite
func TestMain(m *testing.M) {
	var err error
	validators, err = discovery.DiscoverTests(discovery.Config{
		ConfigFile:   "./validators.yaml",
		ValidatorDir: ".",
	})
	if err != nil {
		panic(err)
	}

	// Initialize results structure
	initializeResults()

	// Run the test suite
	code := m.Run()

	// Process and report results
	processResults()

	os.Exit(code)
}

func initializeResults() {
	// Create gate results map
	gateResults := make(map[string]*GateResult)
	suiteResults := make(map[string]*SuiteResult)

	// Initialize structures for each gate and suite
	for _, v := range validators {
		switch v.Type {
		case types.ValidatorTypeGate:
			gateResults[v.ID] = &GateResult{
				ID:     v.ID,
				Tests:  make([]*TestResult, 0),
				Suites: make(map[string]*SuiteResult),
				Passed: true,
			}
		case types.ValidatorTypeSuite:
			suiteResults[v.ID] = &SuiteResult{
				ID:     v.ID,
				Tests:  make([]*TestResult, 0),
				Passed: true,
			}
		}
	}

	// Link suites to gates
	for _, suite := range suiteResults {
		if gate, exists := gateResults[suite.ID]; exists {
			gate.Suites[suite.ID] = suite
		}
	}

	// Store in results
	for _, gate := range gateResults {
		results.Gates[gate.ID] = gate
	}
}

// RunTests is the main test runner function
func RunTests(t *testing.T) {
	for _, v := range validators {
		if v.Type == types.ValidatorTypeTest {
			t.Run(v.FuncName, func(t *testing.T) {
				// Create a test result
				result := TestResult{
					Metadata: v,
					Passed:   true,
				}

				// The test will run automatically since we're using the actual function name
				if t.Failed() {
					result.Passed = false
					result.Error = "test failed"
				}

				// Store the result
				resultsMu.Lock()
				updateResults(result)
				resultsMu.Unlock()
			})
		}
	}
}

func updateResults(result TestResult) {
	// Find the appropriate gate and suite
	for i, gate := range results.Gates {
		if gate.ID == result.Metadata.Gate {
			if result.Metadata.Suite == "" {
				// Test belongs directly to gate
				results.Gates[i].Tests = append(results.Gates[i].Tests, &result)
				results.Gates[i].Passed = results.Gates[i].Passed && result.Passed
			} else {
				// Test belongs to a suite
				for j, suite := range gate.Suites {
					if suite.ID == result.Metadata.Suite {
						results.Gates[i].Suites[j].Tests = append(results.Gates[i].Suites[j].Tests, &result)
						results.Gates[i].Suites[j].Passed = results.Gates[i].Suites[j].Passed && result.Passed
					}
				}
			}
			results.Gates[i].Passed = results.Gates[i].Passed && result.Passed
			results.Passed = results.Passed && result.Passed
			break
		}
	}
}

func processResults() {
	// This could emit metrics, write to a file, etc.
	// For now, we'll just use the results stored in the global variable
}

// RunGate runs all tests in a specific gate
func (r *runner) RunGate(gate string) error {
	cfg := r.registry.GetConfig()

	validators, err := discovery.DiscoverTests(discovery.Config{
		ConfigFile:   cfg.Source.ConfigPath,
		ValidatorDir: cfg.WorkDir,
	})
	if err != nil {
		return fmt.Errorf("failed to discover tests: %w", err)
	}

	// Initialize results structure
	initializeResults()

	// Filter tests for this gate
	var testArgs []string
	for _, v := range validators {
		if v.Gate == gate && v.Type == types.ValidatorTypeTest {
			testArgs = append(testArgs, "-test.run", v.FuncName)
		}
	}

	if len(testArgs) == 0 {
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
