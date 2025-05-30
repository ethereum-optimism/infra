package reporting

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
)

// ReportStats contains aggregated statistics for a test run
type ReportStats struct {
	Total    int
	Passed   int
	Failed   int
	Skipped  int
	Errored  int
	Timeouts int
	PassRate float64
}

// ReportTestItem represents a single test or subtest in the report
type ReportTestItem struct {
	// Identity
	ID         string // Unique identifier
	Name       string // Display name
	Package    string // Package name
	Gate       string // Gate name
	Suite      string // Suite name
	IsSubTest  bool   // Whether this is a subtest
	ParentTest string // Name of parent test for subtests

	// Status and Results
	Status   types.TestStatus
	Error    error // Error if failed
	Duration time.Duration

	// Timing
	StartTime      time.Time // When the test started
	ExecutionOrder int       // Order in which tests were executed

	// Output and Logs
	LogPath    string // Path to log file
	HasLogFile bool   // Whether a log file exists

	// Hierarchy
	Level int // Nesting level (0=gate, 1=suite, 2=test, 3=subtest)
}

// ReportSuite represents a suite of tests in the report
type ReportSuite struct {
	Name     string
	Gate     string
	Status   types.TestStatus
	Duration time.Duration
	Stats    ReportStats
	Tests    []ReportTestItem
}

// ReportGate represents a gate containing suites and direct tests
type ReportGate struct {
	Name     string
	Status   types.TestStatus
	Duration time.Duration
	Stats    ReportStats
	Suites   []ReportSuite
	Tests    []ReportTestItem // Direct gate tests
}

// ReportData contains all the structured data needed for any report format
type ReportData struct {
	// Run Information
	RunID        string
	NetworkName  string
	GateName     string
	Timestamp    time.Time
	Duration     time.Duration
	DurationText string

	// Overall Statistics
	Stats        ReportStats
	PassRate     float64
	PassRateText string
	HasFailures  bool
	HasTimeouts  bool

	// Hierarchical Data
	Gates []ReportGate

	// Flat Lists (for table-style outputs)
	AllTests     []ReportTestItem // All tests and subtests in flat list
	FailedTests  []ReportTestItem // Only failed tests
	TimeoutTests []ReportTestItem // Only timed out tests

	// Summary Lists
	FailedTestNames  []string
	TimeoutTestNames []string

	// Package-level information
	PackageLogPath string // Path to package-level logs for header link
}

// ReportBuilder constructs ReportData from various input sources
type ReportBuilder struct {
	showSubTests     bool
	logPathGenerator func(test *types.TestResult, isSubTest bool, parentName string) string
}

// NewReportBuilder creates a new report builder
func NewReportBuilder() *ReportBuilder {
	return &ReportBuilder{
		showSubTests: true,
		logPathGenerator: func(test *types.TestResult, isSubTest bool, parentName string) string {
			// Default implementation - can be overridden
			return ""
		},
	}
}

// WithSubTestsEnabled controls whether subtests are included in the report
func (rb *ReportBuilder) WithSubTestsEnabled(enabled bool) *ReportBuilder {
	rb.showSubTests = enabled
	return rb
}

// WithLogPathGenerator sets a custom function for generating log file paths
func (rb *ReportBuilder) WithLogPathGenerator(generator func(test *types.TestResult, isSubTest bool, parentName string) string) *ReportBuilder {
	rb.logPathGenerator = generator
	return rb
}

// BuildFromRunnerResult creates a ReportData from a runner.RunnerResult
func (rb *ReportBuilder) BuildFromRunnerResult(result interface{}, runID, networkName, gateName string) *ReportData {
	// This will be implemented to work with runner.RunnerResult
	// For now, return empty structure
	return &ReportData{
		RunID:       runID,
		NetworkName: networkName,
		GateName:    gateName,
		Timestamp:   time.Now(),
	}
}

// BuildFromTestResults creates a ReportData from a collection of TestResults
func (rb *ReportBuilder) BuildFromTestResults(testResults []*types.TestResult, runID, networkName, gateName string) *ReportData {
	report := &ReportData{
		RunID:            runID,
		NetworkName:      networkName,
		GateName:         gateName,
		Timestamp:        time.Now(),
		Gates:            make([]ReportGate, 0),
		AllTests:         make([]ReportTestItem, 0),
		FailedTests:      make([]ReportTestItem, 0),
		TimeoutTests:     make([]ReportTestItem, 0),
		FailedTestNames:  make([]string, 0),
		TimeoutTestNames: make([]string, 0),
	}

	// Group tests by gate and suite
	gateMap := make(map[string]*ReportGate)
	suiteMap := make(map[string]*ReportSuite) // key: "gate:suite"

	var executionOrder int
	var totalDuration time.Duration

	// Track which tests are covered by package tests with subtests
	// This helps us avoid duplicating individual tests that are also subtests
	subtestCoverage := make(map[string]bool) // key: "package:funcname"

	// Track all individual test results to detect duplicates
	individualTests := make(map[string]*types.TestResult) // key: "package:funcname"

	// First pass: collect all test names to detect parent/child relationships
	// and identify which tests are covered by package subtests
	testNames := make(map[string]bool)
	for _, testResult := range testResults {
		if testResult.Metadata.FuncName != "" {
			testNames[testResult.Metadata.FuncName] = true
		}

		// Track individual tests (non-package tests)
		if !testResult.Metadata.RunAll && testResult.Metadata.FuncName != "" {
			key := fmt.Sprintf("%s:%s", testResult.Metadata.Package, testResult.Metadata.FuncName)
			individualTests[key] = testResult
		}
	}

	// Second pass: identify actual duplicates by checking if individual tests also appear as subtests
	for _, testResult := range testResults {
		if testResult.Metadata.RunAll && len(testResult.SubTests) > 0 {
			for subtestName := range testResult.SubTests {
				key := fmt.Sprintf("%s:%s", testResult.Metadata.Package, subtestName)
				// Only mark as covered if there's actually an individual test with the same key
				if _, exists := individualTests[key]; exists {
					subtestCoverage[key] = true
				}
			}
		}
	}

	// Helper function to check if a test has child tests (based on naming pattern)
	hasChildTests := func(testName string) bool {
		if testName == "" {
			return false
		}
		for name := range testNames {
			// Check if another test starts with this test name followed by "/"
			if strings.HasPrefix(name, testName+"/") {
				return true
			}
		}
		return false
	}

	// Helper function to create a unique key for a test
	createTestKey := func(pkg, funcName string) string {
		if pkg == "" && funcName == "" {
			return ""
		}
		return fmt.Sprintf("%s:%s", pkg, funcName)
	}

	// Process test results in order to preserve execution sequence
	for _, testResult := range testResults {
		executionOrder++

		// Detect if this is a subtest based on naming pattern (contains "/")
		isNamedSubtest := strings.Contains(testResult.Metadata.FuncName, "/")
		var parentTestName string
		if isNamedSubtest {
			// Extract parent test name (everything before the first "/")
			parts := strings.SplitN(testResult.Metadata.FuncName, "/", 2)
			if len(parts) > 0 {
				parentTestName = parts[0]
			}
		}

		// Process main test
		testItem := rb.createTestItem(testResult, isNamedSubtest, parentTestName, 0, executionOrder)

		// Check if this is a package-level test and capture its log path for the header
		if testResult.Metadata.RunAll && report.PackageLogPath == "" {
			report.PackageLogPath = testItem.LogPath
		}

		// Determine if this test has subtests (either in SubTests map or based on naming pattern)
		hasSubtests := len(testResult.SubTests) > 0
		hasNamedChildren := hasChildTests(testResult.Metadata.FuncName)

		// Create unique key for this test
		testKey := createTestKey(testResult.Metadata.Package, testResult.Metadata.FuncName)

		// Check if this individual test is covered by a package test subtest
		// Only skip individual tests if they're covered by subtests AND this is not a package test itself
		isCoveredBySubtest := !testResult.Metadata.RunAll && subtestCoverage[testKey]

		// Package-level tests should appear in AllTests as header rows, even if they have subtests
		// Individual tests should appear unless they're covered by package subtests
		// Named subtests should always appear
		shouldIncludeInAllTests := testResult.Metadata.RunAll || // Include package tests
			isNamedSubtest || // Include named subtests
			(!hasSubtests && !hasNamedChildren && !isCoveredBySubtest) // Include standalone tests not covered by subtests

		if shouldIncludeInAllTests {
			report.AllTests = append(report.AllTests, testItem)
		}

		// Add test duration to total
		totalDuration += testResult.Duration

		// Update statistics for all tests except individual tests that are covered by subtests (to avoid duplicates)
		// Package tests should always count in stats
		shouldCountInStats := testResult.Metadata.RunAll || !isCoveredBySubtest
		if shouldCountInStats {
			rb.updateStats(&report.Stats, testResult.Status, testResult.TimedOut)
		}

		// Add to failed/timeout lists if applicable (but only for tests that appear in AllTests)
		if shouldIncludeInAllTests {
			if testResult.Status == types.TestStatusFail || testResult.Status == types.TestStatusError {
				report.FailedTests = append(report.FailedTests, testItem)
				if testResult.TimedOut {
					report.TimeoutTests = append(report.TimeoutTests, testItem)
					// Include package name in timeout test name for backward compatibility
					timeoutTestName := testResult.Metadata.FuncName
					if testResult.Metadata.Package != "" {
						timeoutTestName = fmt.Sprintf("%s.%s", testResult.Metadata.Package, timeoutTestName)
					}
					report.TimeoutTestNames = append(report.TimeoutTestNames, timeoutTestName)
				} else {
					// Include package name in failed test name for backward compatibility
					failedTestName := testResult.Metadata.FuncName
					if testResult.Metadata.Package != "" {
						failedTestName = fmt.Sprintf("%s.%s", testResult.Metadata.Package, failedTestName)
					}
					report.FailedTestNames = append(report.FailedTestNames, failedTestName)
				}
			}
		}

		// Process subtests if enabled
		if rb.showSubTests {
			for subtestName, subtest := range testResult.SubTests {
				executionOrder++
				// For subtests, if FuncName is empty, use the map key (subtestName)
				if subtest.Metadata.FuncName == "" {
					subtest.Metadata.FuncName = subtestName
				}
				// Inherit parent metadata if not set in subtest
				if subtest.Metadata.Gate == "" {
					subtest.Metadata.Gate = testResult.Metadata.Gate
				}
				if subtest.Metadata.Suite == "" {
					subtest.Metadata.Suite = testResult.Metadata.Suite
				}
				if subtest.Metadata.Package == "" {
					subtest.Metadata.Package = testResult.Metadata.Package
				}
				// Use the display name of the parent test, not the function name
				subtestItem := rb.createTestItem(subtest, true, testItem.Name, 1, executionOrder)

				// Always add subtests to AllTests since they're not package-level tests
				report.AllTests = append(report.AllTests, subtestItem)

				// Add subtest duration to total
				totalDuration += subtest.Duration

				// Update statistics for subtest only if the parent test is a package test or not covered
				// This prevents double-counting when individual tests are replaced by package subtests
				shouldCountSubtestInStats := testResult.Metadata.RunAll || !isCoveredBySubtest
				if shouldCountSubtestInStats {
					rb.updateStats(&report.Stats, subtest.Status, subtest.TimedOut)
				}

				// Add to failed/timeout lists if applicable
				if subtest.Status == types.TestStatusFail || subtest.Status == types.TestStatusError {
					report.FailedTests = append(report.FailedTests, subtestItem)
					if subtest.TimedOut {
						report.TimeoutTests = append(report.TimeoutTests, subtestItem)
						// Include package name in timeout subtest name for backward compatibility
						timeoutSubTestName := subtest.Metadata.FuncName
						if testResult.Metadata.Package != "" {
							timeoutSubTestName = fmt.Sprintf("%s.%s", testResult.Metadata.Package, timeoutSubTestName)
						}
						report.TimeoutTestNames = append(report.TimeoutTestNames, timeoutSubTestName)
					} else {
						// Include package name in failed subtest name for backward compatibility
						failedSubTestName := subtest.Metadata.FuncName
						if testResult.Metadata.Package != "" {
							failedSubTestName = fmt.Sprintf("%s.%s", testResult.Metadata.Package, failedSubTestName)
						}
						report.FailedTestNames = append(report.FailedTestNames, failedSubTestName)
					}
				}

				// Add subtest to hierarchical structure
				rb.addTestItemToHierarchy(subtestItem, gateMap, suiteMap)
			}
		}

		// Add main test to hierarchical structure (always add for hierarchy, even if it has subtests)
		rb.addTestItemToHierarchy(testItem, gateMap, suiteMap)
	}

	// Set total duration
	report.Duration = totalDuration

	// Calculate gate and suite statistics and statuses
	rb.calculateHierarchicalStats(gateMap, suiteMap)

	// Convert gate map to slice and populate suites
	for _, gate := range gateMap {
		report.Gates = append(report.Gates, *gate)
	}

	// Add suites to their respective gates
	for _, suite := range suiteMap {
		for i := range report.Gates {
			if report.Gates[i].Name == suite.Gate {
				report.Gates[i].Suites = append(report.Gates[i].Suites, *suite)
				break
			}
		}
	}

	// Calculate pass rate
	if report.Stats.Total > 0 {
		report.Stats.PassRate = float64(report.Stats.Passed) / float64(report.Stats.Total) * 100
		report.PassRate = report.Stats.PassRate
		report.PassRateText = fmt.Sprintf("%.1f", report.PassRate)
	}

	// Set boolean flags
	report.HasFailures = report.Stats.Failed+report.Stats.Errored > 0
	report.HasTimeouts = report.Stats.Timeouts > 0

	return report
}

// calculateHierarchicalStats calculates statistics and statuses for gates and suites
func (rb *ReportBuilder) calculateHierarchicalStats(gateMap map[string]*ReportGate, suiteMap map[string]*ReportSuite) {
	// Calculate suite statistics
	for _, suite := range suiteMap {
		suite.Stats = ReportStats{}
		var suiteDuration time.Duration

		for _, test := range suite.Tests {
			rb.updateStats(&suite.Stats, test.Status, false) // TimedOut is already reflected in Status
			suiteDuration += test.Duration
		}

		suite.Duration = suiteDuration
		suite.Status = rb.determineStatus(suite.Stats)
	}

	// Calculate gate statistics
	for _, gate := range gateMap {
		gate.Stats = ReportStats{}
		var gateDuration time.Duration

		// Add direct gate tests
		for _, test := range gate.Tests {
			rb.updateStats(&gate.Stats, test.Status, false) // TimedOut is already reflected in Status
			gateDuration += test.Duration
		}

		// Add suite tests to gate statistics
		for suiteKey, suite := range suiteMap {
			parts := strings.Split(suiteKey, ":")
			if len(parts) == 2 && parts[0] == gate.Name {
				gate.Stats.Total += suite.Stats.Total
				gate.Stats.Passed += suite.Stats.Passed
				gate.Stats.Failed += suite.Stats.Failed
				gate.Stats.Skipped += suite.Stats.Skipped
				gate.Stats.Errored += suite.Stats.Errored
				gate.Stats.Timeouts += suite.Stats.Timeouts
				gateDuration += suite.Duration
			}
		}

		gate.Duration = gateDuration
		gate.Status = rb.determineStatus(gate.Stats)
	}
}

// determineStatus determines the overall status based on statistics
func (rb *ReportBuilder) determineStatus(stats ReportStats) types.TestStatus {
	if stats.Failed > 0 || stats.Errored > 0 {
		return types.TestStatusFail
	}
	if stats.Skipped > 0 && stats.Passed == 0 {
		return types.TestStatusSkip
	}
	if stats.Total == 0 {
		return types.TestStatusPass
	}
	return types.TestStatusPass
}

// extractStartTimeFromJSON attempts to extract the actual start time from JSON test output
func (rb *ReportBuilder) extractStartTimeFromJSON(stdout string) (time.Time, bool) {
	// Look for the first "run" action which indicates test start
	lines := strings.Split(stdout, "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}

		// Try to parse as JSON
		var logEntry struct {
			Time   string `json:"Time"`
			Action string `json:"Action"`
		}

		if err := json.Unmarshal([]byte(line), &logEntry); err == nil {
			if logEntry.Action == "run" && logEntry.Time != "" {
				if startTime, err := time.Parse(time.RFC3339Nano, logEntry.Time); err == nil {
					return startTime, true
				}
			}
		}
	}
	return time.Time{}, false
}

// createTestItem creates a ReportTestItem from a TestResult
func (rb *ReportBuilder) createTestItem(testResult *types.TestResult, isSubTest bool, parentTest string, level int, executionOrder int) ReportTestItem {
	name := testResult.Metadata.FuncName

	// Handle named subtests (e.g., "TestChainFork/Chain_0" -> "Chain_0")
	if isSubTest && strings.Contains(name, "/") {
		parts := strings.SplitN(name, "/", 2)
		if len(parts) > 1 {
			name = parts[1] // Use the part after the "/"
		}
	}

	if name == "" {
		if testResult.Metadata.RunAll {
			// Use package basename for package-level tests instead of "AllTests"
			if testResult.Metadata.Package != "" {
				packageParts := strings.Split(testResult.Metadata.Package, "/")
				// Find the last non-empty part
				for i := len(packageParts) - 1; i >= 0; i-- {
					if packageParts[i] != "" {
						name = packageParts[i]
						break
					}
				}
				// If we found a package name, format it nicely
				if name != "" {
					name = fmt.Sprintf("%s (package)", name)
				} else {
					name = "Package Suite"
				}
			} else {
				name = "Package Suite"
			}
		} else {
			name = testResult.Metadata.ID
		}
	}

	// Fallback to package name if still empty
	if name == "" && testResult.Metadata.Package != "" {
		parts := strings.Split(testResult.Metadata.Package, "/")
		if len(parts) > 0 {
			name = parts[len(parts)-1] + " (package)"
		} else {
			name = testResult.Metadata.Package + " (package)"
		}
	}

	// Include package context in the display name for better readability
	// Only add package context for tests without clear function names or when it adds value
	if !isSubTest && testResult.Metadata.Package != "" && !strings.Contains(name, "(package)") && !strings.Contains(name, "Package Suite") {
		// Only add package context if:
		// 1. The name is just an ID (no clear test function name)
		// 2. The name doesn't already contain package information
		if testResult.Metadata.FuncName == "" || strings.HasPrefix(name, "test-") || strings.HasPrefix(name, "id-") {
			packageParts := strings.Split(testResult.Metadata.Package, "/")
			if len(packageParts) > 0 {
				packageName := packageParts[len(packageParts)-1]
				// Only add package context if the test name doesn't already contain it
				if !strings.Contains(name, packageName) && packageName != name {
					name = fmt.Sprintf("%s (%s)", name, packageName)
				}
			}
		}
	}

	// Determine start time
	var startTime time.Time
	if extractedTime, found := rb.extractStartTimeFromJSON(testResult.Stdout); found {
		startTime = extractedTime
	} else {
		// Use execution order to estimate timing relative to report generation
		// Space tests 1 second apart based on execution order for consistent ordering
		baseTime := time.Now().Add(-time.Duration(executionOrder) * time.Second)
		startTime = baseTime
	}

	// Generate log path
	logPath := rb.logPathGenerator(testResult, isSubTest, parentTest)

	return ReportTestItem{
		// Identity
		ID:         testResult.Metadata.ID,
		Name:       name,
		Package:    testResult.Metadata.Package,
		Gate:       testResult.Metadata.Gate,
		Suite:      testResult.Metadata.Suite,
		IsSubTest:  isSubTest,
		ParentTest: parentTest,

		// Status and Results
		Status:   testResult.Status,
		Error:    testResult.Error,
		Duration: testResult.Duration,

		// Timing
		StartTime:      startTime,
		ExecutionOrder: executionOrder,

		// Hierarchy
		Level: level,

		// Paths
		LogPath:    logPath,
		HasLogFile: logPath != "",
	}
}

// updateStats updates statistics counters
func (rb *ReportBuilder) updateStats(stats *ReportStats, status types.TestStatus, timedOut bool) {
	stats.Total++

	switch status {
	case types.TestStatusPass:
		stats.Passed++
	case types.TestStatusFail:
		stats.Failed++
	case types.TestStatusSkip:
		stats.Skipped++
	case types.TestStatusError:
		stats.Errored++
	}

	if timedOut {
		stats.Timeouts++
	}
}

// addTestItemToHierarchy adds a test item to the appropriate gate and suite structures
func (rb *ReportBuilder) addTestItemToHierarchy(testItem ReportTestItem, gateMap map[string]*ReportGate, suiteMap map[string]*ReportSuite) {
	gate := testItem.Gate
	suite := testItem.Suite

	// Ensure gate exists
	if _, exists := gateMap[gate]; !exists {
		gateMap[gate] = &ReportGate{
			Name:   gate,
			Status: types.TestStatusPass, // Will be updated based on test results
			Suites: make([]ReportSuite, 0),
			Tests:  make([]ReportTestItem, 0),
		}
	}

	// Handle suite if specified
	if suite != "" {
		suiteKey := fmt.Sprintf("%s:%s", gate, suite)
		if _, exists := suiteMap[suiteKey]; !exists {
			suiteMap[suiteKey] = &ReportSuite{
				Name:   suite,
				Gate:   gate,
				Status: types.TestStatusPass,
				Tests:  make([]ReportTestItem, 0),
			}
		}
		// Add test to suite
		suiteMap[suiteKey].Tests = append(suiteMap[suiteKey].Tests, testItem)
	} else {
		// Add test directly to gate
		gateMap[gate].Tests = append(gateMap[gate].Tests, testItem)
	}
}
