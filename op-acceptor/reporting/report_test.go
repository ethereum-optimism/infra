package reporting

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReportBuilder_BuildFromTestResults(t *testing.T) {
	tests := []struct {
		name           string
		testResults    []*types.TestResult
		runID          string
		networkName    string
		gateName       string
		expectedStats  ReportStats
		expectedGates  int
		expectedSuites int
		expectedTests  int
		expectedFailed int
	}{
		{
			name:        "empty test results",
			testResults: []*types.TestResult{},
			runID:       "test-run-1",
			networkName: "devnet",
			gateName:    "test-gate",
			expectedStats: ReportStats{
				Total:   0,
				Passed:  0,
				Failed:  0,
				Skipped: 0,
				Errored: 0,
			},
			expectedGates:  0,
			expectedSuites: 0,
			expectedTests:  0,
			expectedFailed: 0,
		},
		{
			name: "single passing test",
			testResults: []*types.TestResult{
				{
					Metadata: types.ValidatorMetadata{
						ID:       "test1",
						Gate:     "gate1",
						Suite:    "suite1",
						FuncName: "TestPassing",
						Package:  "github.com/example/test",
					},
					Status:   types.TestStatusPass,
					Duration: 100 * time.Millisecond,
				},
			},
			runID:       "test-run-1",
			networkName: "devnet",
			gateName:    "test-gate",
			expectedStats: ReportStats{
				Total:    1,
				Passed:   1,
				Failed:   0,
				Skipped:  0,
				Errored:  0,
				PassRate: 100,
			},
			expectedGates:  1,
			expectedSuites: 1,
			expectedTests:  1,
			expectedFailed: 0,
		},
		{
			name: "mixed test results with subtests",
			testResults: []*types.TestResult{
				{
					Metadata: types.ValidatorMetadata{
						ID:       "test1",
						Gate:     "gate1",
						Suite:    "suite1",
						FuncName: "TestPassing",
						Package:  "github.com/example/test",
					},
					Status:   types.TestStatusPass,
					Duration: 100 * time.Millisecond,
				},
				{
					Metadata: types.ValidatorMetadata{
						ID:       "test2",
						Gate:     "gate1",
						Suite:    "suite1",
						FuncName: "TestFailing",
						Package:  "github.com/example/test",
					},
					Status:   types.TestStatusFail,
					Duration: 200 * time.Millisecond,
					Error:    errors.New("test failed"),
					SubTests: map[string]*types.TestResult{
						"SubTest1": {
							Status:   types.TestStatusPass,
							Duration: 50 * time.Millisecond,
						},
						"SubTest2": {
							Status:   types.TestStatusFail,
							Duration: 75 * time.Millisecond,
							Error:    errors.New("subtest failed"),
						},
					},
				},
				{
					Metadata: types.ValidatorMetadata{
						ID:       "test3",
						Gate:     "gate2",
						FuncName: "TestSkipped",
						Package:  "github.com/example/test2",
					},
					Status:   types.TestStatusSkip,
					Duration: 10 * time.Millisecond,
				},
			},
			runID:       "test-run-1",
			networkName: "devnet",
			gateName:    "test-gate",
			expectedStats: ReportStats{
				Total:    5, // 3 main tests + 2 subtests
				Passed:   2, // TestPassing + SubTest1
				Failed:   2, // TestFailing + SubTest2
				Skipped:  1, // TestSkipped
				Errored:  0,
				PassRate: 40, // 2 passed out of 5 total = 40%
			},
			expectedGates:  2,
			expectedSuites: 1, // Only suite1 has a suite name
			expectedTests:  4, // TestPassing, SubTest1, SubTest2, TestSkipped (TestFailing excluded since it has subtests)
			expectedFailed: 1, // Only SubTest2 (TestFailing excluded from failed list since it has subtests)
		},
		{
			name: "test with timeout",
			testResults: []*types.TestResult{
				{
					Metadata: types.ValidatorMetadata{
						ID:       "test1",
						Gate:     "gate1",
						FuncName: "TestTimeout",
						Package:  "github.com/example/test",
					},
					Status:   types.TestStatusFail,
					Duration: 5 * time.Second,
					TimedOut: true,
					Error:    errors.New("test timed out"),
				},
			},
			runID:       "test-run-1",
			networkName: "devnet",
			gateName:    "test-gate",
			expectedStats: ReportStats{
				Total:    1,
				Passed:   0,
				Failed:   1,
				Skipped:  0,
				Errored:  0,
				Timeouts: 1,
				PassRate: 0, // 0 passed out of 1 total = 0%
			},
			expectedGates:  1,
			expectedSuites: 0,
			expectedTests:  1,
			expectedFailed: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := NewReportBuilder()
			report := builder.BuildFromTestResults(tt.testResults, tt.runID, tt.networkName, tt.gateName)

			// Check basic report metadata
			assert.Equal(t, tt.runID, report.RunID)
			assert.Equal(t, tt.networkName, report.NetworkName)
			assert.Equal(t, tt.gateName, report.GateName)

			// Check overall statistics
			assert.Equal(t, tt.expectedStats, report.Stats)
			assert.Equal(t, tt.expectedGates, len(report.Gates))
			assert.Equal(t, tt.expectedTests, len(report.AllTests))
			assert.Equal(t, tt.expectedFailed, len(report.FailedTests))

			// Check pass rate calculation
			if tt.expectedStats.Total > 0 {
				expectedPassRate := float64(tt.expectedStats.Passed) / float64(tt.expectedStats.Total) * 100
				assert.InDelta(t, expectedPassRate, report.PassRate, 0.1)
			}

			// Count actual suites across all gates
			actualSuites := 0
			for _, gate := range report.Gates {
				actualSuites += len(gate.Suites)
			}
			assert.Equal(t, tt.expectedSuites, actualSuites)

			// Check boolean flags
			assert.Equal(t, tt.expectedStats.Failed+tt.expectedStats.Errored > 0, report.HasFailures)
			assert.Equal(t, tt.expectedStats.Timeouts > 0, report.HasTimeouts)
		})
	}
}

func TestReportBuilder_WithSubTestsDisabled(t *testing.T) {
	testResults := []*types.TestResult{
		{
			Metadata: types.ValidatorMetadata{
				ID:       "test1",
				Gate:     "gate1",
				FuncName: "TestWithSubtests",
				Package:  "github.com/example/test",
			},
			Status:   types.TestStatusPass,
			Duration: 100 * time.Millisecond,
			SubTests: map[string]*types.TestResult{
				"SubTest1": {
					Status:   types.TestStatusPass,
					Duration: 50 * time.Millisecond,
				},
				"SubTest2": {
					Status:   types.TestStatusFail,
					Duration: 75 * time.Millisecond,
				},
			},
		},
	}

	// With subtests enabled (default)
	builderWithSubtests := NewReportBuilder()
	reportWithSubtests := builderWithSubtests.BuildFromTestResults(testResults, "run1", "net", "gate")
	assert.Equal(t, 3, reportWithSubtests.Stats.Total) // 1 main + 2 subtests

	// With subtests disabled
	builderWithoutSubtests := NewReportBuilder().WithSubTestsEnabled(false)
	reportWithoutSubtests := builderWithoutSubtests.BuildFromTestResults(testResults, "run1", "net", "gate")
	assert.Equal(t, 1, reportWithoutSubtests.Stats.Total) // Only main test
}

func TestReportBuilder_WithLogPathGenerator(t *testing.T) {
	customLogPathGenerator := func(test *types.TestResult, isSubTest bool, parentName string) string {
		if isSubTest {
			return "logs/subtests/" + parentName + "_" + test.Metadata.FuncName + ".log"
		}
		return "logs/" + test.Metadata.FuncName + ".log"
	}

	testResults := []*types.TestResult{
		{
			Metadata: types.ValidatorMetadata{
				ID:       "test1",
				Gate:     "gate1",
				FuncName: "TestMain",
				Package:  "github.com/example/test",
			},
			Status:   types.TestStatusPass,
			Duration: 100 * time.Millisecond,
			SubTests: map[string]*types.TestResult{
				"SubTest1": {
					Metadata: types.ValidatorMetadata{
						ID:       "subtest1",
						FuncName: "SubTest1",
					},
					Status:   types.TestStatusPass,
					Duration: 50 * time.Millisecond,
				},
			},
		},
	}

	builder := NewReportBuilder().WithLogPathGenerator(customLogPathGenerator)
	report := builder.BuildFromTestResults(testResults, "run1", "net", "gate")

	// Find the subtest (main test with subtests is not in AllTests)
	var subTest *ReportTestItem
	for i := range report.AllTests {
		if report.AllTests[i].IsSubTest {
			subTest = &report.AllTests[i]
		}
	}

	// The main test should be in the hierarchical structure but not in AllTests
	require.NotNil(t, subTest, "Subtest should be found")
	require.Len(t, report.AllTests, 1, "Only subtest should be in AllTests (parent test excluded)")

	// Find the main test in the gate structure
	var mainTestFound bool
	var mainTestLogPath string
	for _, gate := range report.Gates {
		for _, test := range gate.Tests {
			if test.Name == "TestMain" && !test.IsSubTest {
				mainTestFound = true
				mainTestLogPath = test.LogPath
				break
			}
		}
	}

	require.True(t, mainTestFound, "Main test should be found in gate structure")
	assert.Equal(t, "logs/TestMain.log", mainTestLogPath)
	assert.Equal(t, "logs/subtests/TestMain_SubTest1.log", subTest.LogPath)
	assert.True(t, subTest.HasLogFile)
}

func TestReportItem_StatusDisplay(t *testing.T) {
	tests := []struct {
		status        types.TestStatus
		expectedText  string
		expectedClass string
	}{
		{types.TestStatusPass, "PASS", "pass"},
		{types.TestStatusFail, "FAIL", "fail"},
		{types.TestStatusSkip, "SKIP", "skip"},
		{types.TestStatusError, "ERROR", "error"},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			statusDisplay := getStatusDisplay(tt.status)
			assert.Equal(t, tt.expectedText, statusDisplay.Text)
			assert.Equal(t, tt.expectedClass, statusDisplay.Class)
		})
	}
}

func TestReportBuilder_FailedTestNames(t *testing.T) {
	testResults := []*types.TestResult{
		{
			Metadata: types.ValidatorMetadata{
				ID:       "test1",
				Gate:     "gate1",
				FuncName: "TestPassing",
				Package:  "github.com/example/test",
			},
			Status: types.TestStatusPass,
		},
		{
			Metadata: types.ValidatorMetadata{
				ID:       "test2",
				Gate:     "gate1",
				FuncName: "TestFailing",
				Package:  "github.com/example/test",
			},
			Status: types.TestStatusFail,
			Error:  errors.New("test failed"),
		},
		{
			Metadata: types.ValidatorMetadata{
				ID:       "test3",
				Gate:     "gate1",
				FuncName: "TestError",
				Package:  "github.com/example/other",
			},
			Status: types.TestStatusError,
			Error:  errors.New("test error"),
		},
	}

	builder := NewReportBuilder()
	report := builder.BuildFromTestResults(testResults, "run1", "net", "gate")

	expectedFailedNames := []string{
		"github.com/example/test.TestFailing",
		"github.com/example/other.TestError",
	}

	assert.Equal(t, expectedFailedNames, report.FailedTestNames)
	assert.Equal(t, 2, len(report.FailedTests))
}

func TestReportBuilder_TimeoutTestNames(t *testing.T) {
	testResults := []*types.TestResult{
		{
			Metadata: types.ValidatorMetadata{
				ID:       "test1",
				Gate:     "gate1",
				FuncName: "TestNormal",
				Package:  "github.com/example/test",
			},
			Status: types.TestStatusPass,
		},
		{
			Metadata: types.ValidatorMetadata{
				ID:       "test2",
				Gate:     "gate1",
				FuncName: "TestTimeout",
				Package:  "github.com/example/test",
			},
			Status:   types.TestStatusFail,
			TimedOut: true,
			Error:    errors.New("test timed out"),
		},
	}

	builder := NewReportBuilder()
	report := builder.BuildFromTestResults(testResults, "run1", "net", "gate")

	expectedTimeoutNames := []string{
		"github.com/example/test.TestTimeout",
	}

	assert.Equal(t, expectedTimeoutNames, report.TimeoutTestNames)
	assert.Equal(t, 1, len(report.TimeoutTests))
	assert.True(t, report.HasTimeouts)
	assert.Equal(t, 1, report.Stats.Timeouts)
}

func TestFormatDuration(t *testing.T) {
	testDuration := []struct {
		duration time.Duration
		expected string
	}{
		{100 * time.Millisecond, "100ms"},
		{500 * time.Millisecond, "500ms"},
		{1 * time.Second, "1s"},
		{1*time.Second + 500*time.Millisecond, "1.5s"},
		{2*time.Minute + 30*time.Second, "2m30s"},
	}

	for _, tt := range testDuration {
		t.Run(fmt.Sprintf("duration_%v", tt.duration), func(t *testing.T) {
			result := formatDuration(tt.duration)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestReportBuilder_TestNameFallbacks(t *testing.T) {
	tests := []struct {
		name         string
		testResult   *types.TestResult
		expectedName string
	}{
		{
			name: "normal test with function name",
			testResult: &types.TestResult{
				Metadata: types.ValidatorMetadata{
					ID:       "test1",
					FuncName: "TestFunction",
					Package:  "github.com/example/test",
				},
			},
			expectedName: "TestFunction",
		},
		{
			name: "test with RunAll true",
			testResult: &types.TestResult{
				Metadata: types.ValidatorMetadata{
					ID:      "test1",
					RunAll:  true,
					Package: "github.com/example/test",
				},
			},
			expectedName: "test (package)",
		},
		{
			name: "test fallback to ID",
			testResult: &types.TestResult{
				Metadata: types.ValidatorMetadata{
					ID:      "test-id-123",
					Package: "github.com/example/test",
				},
			},
			expectedName: "test-id-123",
		},
		{
			name: "test fallback to package name",
			testResult: &types.TestResult{
				Metadata: types.ValidatorMetadata{
					Package: "github.com/example/mypackage",
				},
			},
			expectedName: "mypackage (package)",
		},
		{
			name: "test fallback to full package path",
			testResult: &types.TestResult{
				Metadata: types.ValidatorMetadata{
					Package: "singlepackage",
				},
			},
			expectedName: "singlepackage (package)",
		},
	}

	builder := NewReportBuilder()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := builder.createTestItem(tt.testResult, false, "", 1)
			assert.Equal(t, tt.expectedName, item.Name)
		})
	}
}

func TestReportBuilder_FilterPackageTests(t *testing.T) {
	builder := NewReportBuilder().WithLogPathGenerator(func(test *types.TestResult, isSubTest bool, parentName string) string {
		// Simple test log path generator
		if test.Metadata.RunAll {
			return "package-logs.log"
		}
		return test.Metadata.FuncName + ".log"
	})

	testResults := []*types.TestResult{
		// Package-level test (should now appear in AllTests as header row)
		{
			Metadata: types.ValidatorMetadata{
				ID:      "package-test",
				Package: "github.com/example/base",
				RunAll:  true,
			},
			Status:   types.TestStatusPass,
			Duration: 100 * time.Millisecond,
		},
		// Individual test (should remain in AllTests)
		{
			Metadata: types.ValidatorMetadata{
				ID:       "individual-test",
				FuncName: "TestCLAdvance",
				Package:  "github.com/example/base",
			},
			Status:   types.TestStatusPass,
			Duration: 200 * time.Millisecond,
		},
	}

	report := builder.BuildFromTestResults(testResults, "test-run", "test-network", "test-gate")

	// NEW BEHAVIOR: Should have both tests in AllTests (package test now appears as header row)
	assert.Len(t, report.AllTests, 2)
	assert.Equal(t, "base (package)", report.AllTests[0].Name) // Package test appears first
	assert.Equal(t, "TestCLAdvance", report.AllTests[1].Name)  // Individual test appears second

	// Should have correct statistics including both tests
	assert.Equal(t, 2, report.Stats.Total)
	assert.Equal(t, 2, report.Stats.Passed)

	// Should have captured the package log path
	assert.Equal(t, "package-logs.log", report.PackageLogPath)
}

func TestReportBuilder_NamedSubtestFiltering(t *testing.T) {
	builder := NewReportBuilder().WithLogPathGenerator(func(test *types.TestResult, isSubTest bool, parentName string) string {
		return test.Metadata.FuncName + ".log"
	})

	testResults := []*types.TestResult{
		// Parent test (should be excluded because it has named children)
		{
			Metadata: types.ValidatorMetadata{
				ID:       "test1",
				FuncName: "TestChainFork",
				Package:  "github.com/example/test",
				Gate:     "gate1",
			},
			Status:   types.TestStatusPass,
			Duration: 100 * time.Millisecond,
		},
		// Named subtest 1 (should be included)
		{
			Metadata: types.ValidatorMetadata{
				ID:       "test2",
				FuncName: "TestChainFork/Chain_0",
				Package:  "github.com/example/test",
				Gate:     "gate1",
			},
			Status:   types.TestStatusPass,
			Duration: 50 * time.Millisecond,
		},
		// Named subtest 2 (should be included)
		{
			Metadata: types.ValidatorMetadata{
				ID:       "test3",
				FuncName: "TestChainFork/Chain_1",
				Package:  "github.com/example/test",
				Gate:     "gate1",
			},
			Status:   types.TestStatusPass,
			Duration: 60 * time.Millisecond,
		},
		// Standalone test without children (should be included)
		{
			Metadata: types.ValidatorMetadata{
				ID:       "test4",
				FuncName: "TestCLAdvance",
				Package:  "github.com/example/test",
				Gate:     "gate1",
			},
			Status:   types.TestStatusPass,
			Duration: 200 * time.Millisecond,
		},
	}

	report := builder.BuildFromTestResults(testResults, "test-run", "test-network", "test-gate")

	// Should have 3 tests in AllTests: TestCLAdvance, Chain_0, Chain_1 (TestChainFork parent excluded)
	assert.Len(t, report.AllTests, 3)

	// Verify the test names
	testNames := make([]string, len(report.AllTests))
	for i, test := range report.AllTests {
		testNames[i] = test.Name
	}
	assert.Contains(t, testNames, "TestCLAdvance")
	assert.Contains(t, testNames, "Chain_0")          // Extracted from "TestChainFork/Chain_0"
	assert.Contains(t, testNames, "Chain_1")          // Extracted from "TestChainFork/Chain_1"
	assert.NotContains(t, testNames, "TestChainFork") // Parent should be excluded

	// Verify that named subtests are marked as subtests
	var subtestCount int
	var standaloneCount int
	for _, test := range report.AllTests {
		if test.IsSubTest {
			subtestCount++
			assert.Equal(t, "TestChainFork", test.ParentTest)
		} else {
			standaloneCount++
		}
	}
	assert.Equal(t, 2, subtestCount)    // Chain_0 and Chain_1
	assert.Equal(t, 1, standaloneCount) // TestCLAdvance

	// Should have correct statistics including all tests
	assert.Equal(t, 4, report.Stats.Total)
	assert.Equal(t, 4, report.Stats.Passed)
}

func TestReportBuilder_DuplicateTestDeduplication(t *testing.T) {
	builder := NewReportBuilder().WithLogPathGenerator(func(test *types.TestResult, isSubTest bool, parentName string) string {
		return test.Metadata.FuncName + ".log"
	})

	// Simulate the scenario where the same test appears multiple times:
	// 1. As an individual test result
	// 2. As part of a package test result with subtests
	testResults := []*types.TestResult{
		// Individual test result (should be deduplicated if package version exists)
		{
			Metadata: types.ValidatorMetadata{
				ID:       "individual-test",
				FuncName: "TestCLAdvance",
				Package:  "github.com/example/base",
				Gate:     "holocene",
			},
			Status:   types.TestStatusPass,
			Duration: 100 * time.Millisecond,
		},
		// Package test result containing the same test as a subtest (should be preferred)
		{
			Metadata: types.ValidatorMetadata{
				ID:      "package-test",
				Package: "github.com/example/base",
				Gate:    "holocene",
				RunAll:  true,
			},
			Status:   types.TestStatusPass,
			Duration: 200 * time.Millisecond,
			SubTests: map[string]*types.TestResult{
				"TestCLAdvance": {
					Metadata: types.ValidatorMetadata{
						ID:       "subtest-testcladvance",
						FuncName: "TestCLAdvance",
						Package:  "github.com/example/base",
						Gate:     "holocene",
					},
					Status:   types.TestStatusPass,
					Duration: 50 * time.Millisecond,
				},
				"TestOtherTest": {
					Metadata: types.ValidatorMetadata{
						ID:       "subtest-othertest",
						FuncName: "TestOtherTest",
						Package:  "github.com/example/base",
						Gate:     "holocene",
					},
					Status:   types.TestStatusFail,
					Duration: 60 * time.Millisecond,
				},
			},
		},
		// Another individual test (unique, should be included)
		{
			Metadata: types.ValidatorMetadata{
				ID:       "unique-test",
				FuncName: "TestUniqueFunction",
				Package:  "github.com/example/base",
				Gate:     "holocene",
			},
			Status:   types.TestStatusPass,
			Duration: 80 * time.Millisecond,
		},
	}

	report := builder.BuildFromTestResults(testResults, "test-run", "test-network", "holocene")

	// NEW BEHAVIOR: Should have package test + unique tests in AllTests:
	// - base (package) (package test header)
	// - TestCLAdvance (as subtest from package test, not individual)
	// - TestOtherTest (as subtest from package test)
	// - TestUniqueFunction (individual test)
	assert.Len(t, report.AllTests, 4)

	// Verify test names
	testNames := make([]string, len(report.AllTests))
	for i, test := range report.AllTests {
		testNames[i] = test.Name
	}
	assert.Contains(t, testNames, "base (package)")     // Package test header
	assert.Contains(t, testNames, "TestCLAdvance")      // From package subtest
	assert.Contains(t, testNames, "TestOtherTest")      // From package subtest
	assert.Contains(t, testNames, "TestUniqueFunction") // Individual test

	// Verify that TestCLAdvance appears as a subtest (from package), not as individual test
	var testCLAdvanceTest *ReportTestItem
	for i := range report.AllTests {
		if report.AllTests[i].Name == "TestCLAdvance" {
			testCLAdvanceTest = &report.AllTests[i]
			break
		}
	}
	require.NotNil(t, testCLAdvanceTest, "TestCLAdvance should be found in AllTests")
	assert.True(t, testCLAdvanceTest.IsSubTest, "TestCLAdvance should be marked as subtest from package test")

	// Statistics should count package test + subtests + individual test
	assert.Equal(t, 4, report.Stats.Total)  // Package test + 2 subtests + 1 individual
	assert.Equal(t, 3, report.Stats.Passed) // Package test + TestCLAdvance + TestUniqueFunction
	assert.Equal(t, 1, report.Stats.Failed) // TestOtherTest

	// Failed test names should not have duplicates
	expectedFailedNames := []string{
		"github.com/example/base.TestOtherTest",
	}
	assert.Equal(t, expectedFailedNames, report.FailedTestNames)
}
