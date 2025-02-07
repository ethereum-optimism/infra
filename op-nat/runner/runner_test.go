package runner

import (
	"testing"

	"github.com/ethereum-optimism/infra/op-nat/types"
	"github.com/stretchr/testify/assert"
)

// Test validators
//
//nat:validator id:test-gate type:gate
func TestGate(t *testing.T) {}

//nat:validator id:test-suite type:suite gate:test-gate
func TestSuite(t *testing.T) {}

//nat:validator id:direct-test type:test gate:test-gate
func TestDirectToGate(t *testing.T) {
	assert.True(t, true, "this test should pass")
}

//nat:validator id:suite-test type:test gate:test-gate suite:test-suite
func TestInSuite(t *testing.T) {
	assert.True(t, true, "this test should pass")
}

func TestRunTests(t *testing.T) {
	// Reset validators for testing
	validators = []types.ValidatorMetadata{
		{
			ID:   "test-gate",
			Type: types.ValidatorTypeGate,
		},
		{
			ID:   "test-suite",
			Type: types.ValidatorTypeSuite,
			Gate: "test-gate",
		},
		{
			ID:       "test1",
			Type:     types.ValidatorTypeTest,
			Gate:     "test-gate",
			FuncName: "TestDirectToGate",
		},
		{
			ID:       "test2",
			Type:     types.ValidatorTypeTest,
			Gate:     "test-gate",
			Suite:    "test-suite",
			FuncName: "TestInSuite",
		},
	}

	// Initialize results
	results = &RunnerResult{
		Gates:  make([]GateResult, 0),
		Passed: true,
	}
	initializeResults()

	// Run tests
	RunTests(t)

	// Verify results were collected
	assert.True(t, results.Passed, "all tests should pass")
	assert.Len(t, results.Gates, 1, "should have one gate")
}

func TestUpdateResults(t *testing.T) {
	// Reset results for testing
	results = &RunnerResult{
		Gates:  make([]GateResult, 0),
		Passed: true,
	}

	// Initialize with a test gate and suite
	gateMetadata := types.ValidatorMetadata{
		ID:   "test-gate",
		Type: types.ValidatorTypeGate,
	}
	suiteMetadata := types.ValidatorMetadata{
		ID:   "test-suite",
		Type: types.ValidatorTypeSuite,
		Gate: "test-gate",
	}

	results.Gates = append(results.Gates, GateResult{
		Metadata: gateMetadata,
		Suites: []SuiteResult{{
			Metadata: suiteMetadata,
			Tests:    make([]TestResult, 0),
			Passed:   true,
		}},
		Tests:  make([]TestResult, 0),
		Passed: true,
	})

	tests := []struct {
		name     string
		result   TestResult
		wantPass bool
	}{
		{
			name: "direct gate test pass",
			result: TestResult{
				Metadata: types.ValidatorMetadata{
					ID:   "direct-test",
					Type: types.ValidatorTypeTest,
					Gate: "test-gate",
				},
				Passed: true,
			},
			wantPass: true,
		},
		{
			name: "suite test fail",
			result: TestResult{
				Metadata: types.ValidatorMetadata{
					ID:    "suite-test",
					Type:  types.ValidatorTypeTest,
					Gate:  "test-gate",
					Suite: "test-suite",
				},
				Passed: false,
				Error:  "test failed",
			},
			wantPass: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			updateResults(tt.result)

			// Find the result in our structure
			for _, gate := range results.Gates {
				if gate.Metadata.ID == tt.result.Metadata.Gate {
					if tt.result.Metadata.Suite == "" {
						// Direct gate test
						found := false
						for _, test := range gate.Tests {
							if test.Metadata.ID == tt.result.Metadata.ID {
								assert.Equal(t, tt.wantPass, test.Passed)
								found = true
								break
							}
						}
						assert.True(t, found, "test result not found in gate")
					} else {
						// Suite test
						for _, suite := range gate.Suites {
							if suite.Metadata.ID == tt.result.Metadata.Suite {
								found := false
								for _, test := range suite.Tests {
									if test.Metadata.ID == tt.result.Metadata.ID {
										assert.Equal(t, tt.wantPass, test.Passed)
										found = true
										break
									}
								}
								assert.True(t, found, "test result not found in suite")
							}
						}
					}
				}
			}
		})
	}
}

func TestInitializeResults(t *testing.T) {
	// Reset validators and results
	validators = []types.ValidatorMetadata{
		{
			ID:   "test-gate",
			Type: types.ValidatorTypeGate,
		},
		{
			ID:   "test-suite",
			Type: types.ValidatorTypeSuite,
			Gate: "test-gate",
		},
	}
	results = &RunnerResult{
		Gates:  make([]GateResult, 0),
		Passed: true,
	}

	initializeResults()

	assert.Len(t, results.Gates, 1, "should have one gate")
	gate := results.Gates[0]
	assert.Equal(t, "test-gate", gate.Metadata.ID)
	assert.Len(t, gate.Suites, 1, "gate should have one suite")
	assert.Equal(t, "test-suite", gate.Suites[0].Metadata.ID)
}
