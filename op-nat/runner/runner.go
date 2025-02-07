package runner

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ethereum-optimism/infra/op-nat/discovery"
	"github.com/ethereum-optimism/infra/op-nat/types"
)

// TestResult captures the outcome of a single test run
type TestResult struct {
	Metadata types.ValidatorMetadata
	Passed   bool
	Error    string
}

// SuiteResult captures aggregated results for a test suite
type SuiteResult struct {
	Metadata types.ValidatorMetadata
	Tests    []TestResult
	Passed   bool
}

// GateResult captures aggregated results for a gate
type GateResult struct {
	Metadata types.ValidatorMetadata
	Suites   []SuiteResult
	Tests    []TestResult
	Passed   bool
}

// RunnerResult captures the complete test run results
type RunnerResult struct {
	Gates  []GateResult
	Passed bool
}

var (
	validators []types.ValidatorMetadata
	results    = &RunnerResult{
		Gates:  make([]GateResult, 0),
		Passed: true,
	}
	resultsMu sync.Mutex
)

// TestRunner defines the interface for running acceptance tests
type TestRunner interface {
	RunAllTests() (*RunnerResult, error)
}

type testRunner struct {
	testDir string
}

// NewTestRunner creates a new test runner instance
func NewTestRunner(testDir string) (TestRunner, error) {
	if testDir == "" {
		return nil, errors.New("testDir is required")
	}
	return &testRunner{
		testDir: testDir,
	}, nil
}

// RunAllTests implements the TestRunner interface
func (r *testRunner) RunAllTests() (*RunnerResult, error) {
	var err error
	validators, err = discovery.DiscoverTests(r.testDir)
	if err != nil {
		return nil, fmt.Errorf("failed to discover tests: %w", err)
	}

	// Initialize results structure
	initializeResults()

	// Create test arguments to run only our discovered tests
	var testArgs []string
	for _, v := range validators {
		if v.Type == types.ValidatorTypeTest {
			testArgs = append(testArgs, "-test.run", v.FuncName)
		}
	}

	// Get absolute path to test directory
	absTestDir, err := filepath.Abs(r.testDir)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}

	// Set up the test command with the full package path
	cmd := exec.Command("go", append([]string{"test", "-v", "./..."}, testArgs...)...)
	cmd.Dir = absTestDir

	// Capture output
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Run the tests
	err = cmd.Run()
	if err != nil {
		// Print stderr for debugging
		fmt.Printf("Test stderr output:\n%s\n", stderr.String())

		// Check if it's just test failures (exit status 1) or a more serious error
		if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 1 {
			return nil, fmt.Errorf("failed to run tests: %w\nstderr: %s", err, stderr.String())
		}
	}

	// Parse test output and update results
	scanner := bufio.NewScanner(&stdout)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "--- PASS:") || strings.Contains(line, "--- FAIL:") {
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				testName := parts[2]
				passed := strings.Contains(line, "--- PASS:")

				// Find corresponding validator and update results
				for _, v := range validators {
					if v.FuncName == testName {
						result := TestResult{
							Metadata: v,
							Passed:   passed,
							Error:    "", // Could parse error from output if needed
						}
						resultsMu.Lock()
						updateResults(result)
						resultsMu.Unlock()
						break
					}
				}
			}
		}
	}

	// Check for scanner errors
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading test output: %w", err)
	}

	// Process results
	processResults()

	return results, nil
}

// TestMain is the entry point for our test suite
func TestMain(m *testing.M) {
	var err error
	validators, err = discovery.DiscoverTests(".")
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
				Metadata: v,
				Suites:   make([]SuiteResult, 0),
				Tests:    make([]TestResult, 0),
				Passed:   true,
			}
		case types.ValidatorTypeSuite:
			suiteResults[v.ID] = &SuiteResult{
				Metadata: v,
				Tests:    make([]TestResult, 0),
				Passed:   true,
			}
		}
	}

	// Link suites to gates
	for _, suite := range suiteResults {
		if gate, exists := gateResults[suite.Metadata.Gate]; exists {
			gate.Suites = append(gate.Suites, *suite)
		}
	}

	// Store in results
	for _, gate := range gateResults {
		results.Gates = append(results.Gates, *gate)
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
		if gate.Metadata.ID == result.Metadata.Gate {
			if result.Metadata.Suite == "" {
				// Test belongs directly to gate
				results.Gates[i].Tests = append(results.Gates[i].Tests, result)
				results.Gates[i].Passed = results.Gates[i].Passed && result.Passed
			} else {
				// Test belongs to a suite
				for j, suite := range gate.Suites {
					if suite.Metadata.ID == result.Metadata.Suite {
						results.Gates[i].Suites[j].Tests = append(results.Gates[i].Suites[j].Tests, result)
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
