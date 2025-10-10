package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ethereum-optimism/optimism/devnet-sdk/shell/env"
	"github.com/ethereum-optimism/optimism/devnet-sdk/telemetry"
	"github.com/ethereum-optimism/optimism/op-devstack/dsl"
	"github.com/ethereum/go-ethereum/log"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"

	"math"
	"runtime"

	"github.com/ethereum-optimism/infra/op-acceptor/flags"
	"github.com/ethereum-optimism/infra/op-acceptor/logging"
	"github.com/ethereum-optimism/infra/op-acceptor/metrics"
	"github.com/ethereum-optimism/infra/op-acceptor/registry"
	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/ethereum-optimism/infra/op-acceptor/ui"
)

// Go test2json (TestEvent)action constants for JSON test output
// See https://cs.opensource.google/go/go/+/master:src/cmd/test2json/main.go;l=34-60
const (
	ActionStart  = "start"
	ActionRun    = "run"
	ActionPass   = "pass"
	ActionFail   = "fail"
	ActionSkip   = "skip"
	ActionOutput = "output"
)

// SuiteResult captures aggregated results for a test suite
type SuiteResult struct {
	ID            string
	Description   string
	Tests         map[string]*types.TestResult
	Status        types.TestStatus
	Duration      time.Duration // Total test time (sum of all test durations)
	WallClockTime time.Duration // Actual elapsed time for this suite
	Stats         ResultStats
}

// GateResult captures aggregated results for a gate
type GateResult struct {
	ID            string
	Description   string
	Tests         map[string]*types.TestResult
	Suites        map[string]*SuiteResult
	Status        types.TestStatus
	Duration      time.Duration // Total test time (sum of all test durations)
	WallClockTime time.Duration // Actual elapsed time for this gate
	Stats         ResultStats
	Inherited     []string
}

// RunnerResult captures the complete test run results
type RunnerResult struct {
	Gates         map[string]*GateResult
	Status        types.TestStatus
	Duration      time.Duration // Total test time (sum of all test durations)
	WallClockTime time.Duration // Actual elapsed time for the entire run
	Stats         ResultStats
	RunID         string
	IsParallel    bool // Indicates if this run used parallel execution
	// Skip summary (optional)
	SkipCounts *SkipCounts
}

// SkipCounts captures exclusion stats from skip gates filtering
type SkipCounts struct {
	TotalExcluded  int `json:"total_excluded"`
	ExcludedByPkg  int `json:"excluded_by_package"`
	ExcludedByName int `json:"excluded_by_name"`
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
	RunAllTests(ctx context.Context) (*RunnerResult, error)
	RunTest(ctx context.Context, metadata types.ValidatorMetadata) (*types.TestResult, error)
	ReproducibleEnv() Env
}

// TestRunnerWithFileLogger extends the TestRunner interface with a method
// to set the file logger after creation
type TestRunnerWithFileLogger interface {
	TestRunner
	SetFileLogger(logger *logging.FileLogger)
}

// runner struct implements TestRunner interface
type runner struct {
	registry           *registry.Registry
	validators         []types.ValidatorMetadata
	workDir            string // Directory for running tests
	log                log.Logger
	runID              string
	goBinary           string              // Path to the Go binary
	allowSkips         bool                // Whether to allow skipping tests when preconditions are not met
	outputRealtimeLogs bool                // If enabled, test logs will be outputted in realtime
	testLogLevel       string              // Log level to be used for the tests
	fileLogger         *logging.FileLogger // Logger for storing test results
	networkName        string              // Name of the network being tested
	env                *env.DevnetEnv
	tracer             trace.Tracer
	serial             bool // Whether to run tests serially instead of in parallel
	concurrency        int  // Number of concurrent test workers (0 = auto-determine)

	// New component fields
	executor     TestExecutor
	coordinator  TestCoordinator
	collector    ResultCollector
	outputParser OutputParser
	jsonStore    JSONStore
}

// Config holds configuration for creating a new runner
type Config struct {
	Registry           *registry.Registry
	TargetGate         string
	WorkDir            string
	Log                log.Logger
	GoBinary           string              // path to the Go binary
	AllowSkips         bool                // Whether to allow skipping tests when preconditions are not met
	OutputRealtimeLogs bool                // Whether to output test logs to the console
	TestLogLevel       string              // Log level to be used for the tests
	FileLogger         *logging.FileLogger // Logger for storing test results
	NetworkName        string              // Name of the network being tested
	DevnetEnv          *env.DevnetEnv
	Serial             bool          // Whether to run tests serially instead of in parallel
	Concurrency        int           // Number of concurrent test workers (0 = auto-determine)
	ShowProgress       bool          // Whether to show periodic progress updates during test execution
	ProgressInterval   time.Duration // Interval between progress updates when ShowProgress is 'true'
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

	var validators []types.ValidatorMetadata
	if len(cfg.TargetGate) > 0 {
		validators = cfg.Registry.GetValidatorsByGate(cfg.TargetGate)
	} else {
		validators = cfg.Registry.GetValidators()
	}
	if len(validators) == 0 {
		return nil, fmt.Errorf("no validators found")
	}

	if cfg.GoBinary == "" {
		cfg.GoBinary = "go" // Default to "go" if not specified
	}

	// Default network name if not specified
	networkName := cfg.NetworkName
	if networkName == "" {
		networkName = "unknown"
	}

	cfg.Log.Debug("NewTestRunner()", "targetGate", cfg.TargetGate, "workDir", cfg.WorkDir,
		"allowSkips", cfg.AllowSkips, "goBinary", cfg.GoBinary, "networkName", networkName, "serial", cfg.Serial)

	r := &runner{
		registry:           cfg.Registry,
		validators:         validators,
		workDir:            cfg.WorkDir,
		log:                cfg.Log,
		goBinary:           cfg.GoBinary,
		allowSkips:         cfg.AllowSkips,
		outputRealtimeLogs: cfg.OutputRealtimeLogs,
		testLogLevel:       cfg.TestLogLevel,
		fileLogger:         cfg.FileLogger,
		networkName:        networkName,
		env:                cfg.DevnetEnv,
		tracer:             otel.Tracer("test runner"),
		serial:             cfg.Serial,
		concurrency:        cfg.Concurrency,
	}

	// Initialize new components
	r.outputParser = NewOutputParser()
	r.jsonStore = NewJSONStore(cfg.FileLogger)
	r.collector = NewResultCollector()

	// Create a timeout value
	timeout := DefaultTestTimeout

	executor, err := NewTestExecutor(
		cfg.WorkDir,
		timeout,
		r.goBinary,
		r.ReproducibleEnv,
		r.testCommandContext,
		r.outputParser,
		r.jsonStore,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create test executor: %w", err)
	}
	r.executor = executor

	// Create progress indicator if ShowProgress is true
	var progressIndicator ProgressIndicator
	if cfg.ShowProgress {
		progressIndicator = NewConsoleProgressIndicator(cfg.Log, cfg.ProgressInterval)
	} else {
		progressIndicator = NewNoOpProgressIndicator()
	}

	// Create parallel runner first if needed, then coordinator once
	var parallelRunner ParallelRunner
	if !cfg.Serial && cfg.Concurrency > 0 {
		parallelExecutor := NewParallelExecutor(r, cfg.Concurrency)
		parallelRunner = NewParallelRunnerAdapter(parallelExecutor)
	}

	// Initialize coordinator once with correct parallel runner
	r.coordinator = NewTestCoordinator(r.executor, r.collector, parallelRunner, progressIndicator)

	return r, nil
}

// GetUI implements UIProvider interface
// Returns the progress indicator from the coordinator if available.
//
// Coordinator Lifecycle Contract:
// - The coordinator MUST be initialized before parallel test execution begins
// - The coordinator SHOULD NOT be modified during active test execution
// - When coordinator is nil, progress tracking is gracefully disabled
func (r *runner) GetUI() ProgressIndicator {
	if r.coordinator != nil {
		return r.coordinator.GetUI()
	}
	return nil
}

// RunAllTests implements the TestRunner interface
func (r *runner) RunAllTests(ctx context.Context) (*RunnerResult, error) {
	// Use fileLogger's runID if available, otherwise generate new
	if r.fileLogger != nil {
		r.runID = r.fileLogger.GetRunID()
	} else {
		r.runID = uuid.New().String()
	}

	r.log.Debug("Running all tests", "run_id", r.runID, "parallel", !r.serial)

	if r.serial {
		return r.runAllTestsSerial(ctx)
	} else {
		return r.runAllTestsParallel(ctx)
	}
}

// runAllTestsSerial runs all tests serially
func (r *runner) runAllTestsSerial(ctx context.Context) (*RunnerResult, error) {
	start := time.Now()
	result := &RunnerResult{
		Gates:      make(map[string]*GateResult),
		Stats:      ResultStats{StartTime: start},
		IsParallel: false,
	}

	if err := r.processAllGates(ctx, result); err != nil {
		return nil, err
	}

	wallClockTime := time.Since(start)
	result.WallClockTime = wallClockTime
	result.Duration = wallClockTime // In serial mode, these are the same
	result.Status = determineRunnerStatus(result)
	result.Stats.EndTime = time.Now()
	result.RunID = r.runID
	return result, nil
}

// runAllTestsParallel implements the new parallel test execution logic
func (r *runner) runAllTestsParallel(ctx context.Context) (*RunnerResult, error) {
	start := time.Now()

	// Collect all test work to be executed in parallel
	workItems := r.collectTestWork()

	if len(workItems) == 0 {
		r.log.Warn("No test work items found")
		resultMgr := NewResultHierarchyManager()
		result := resultMgr.CreateEmptyResult(r.runID, start)
		result.IsParallel = true
		resultMgr.FinalizeResults(result, start)
		result.WallClockTime = time.Since(start)
		return result, nil
	}

	// Determine optimal concurrency based on system capabilities and workload
	concurrency := r.determineConcurrency(len(workItems))

	r.log.Info("Executing tests in parallel", "totalWorkItems", len(workItems), "runID", r.runID, "concurrency", concurrency)
	r.log.Debug("Work items", "workItems", workItems)

	// Create parallel executor with reasonable concurrency
	executor := NewParallelExecutor(r, concurrency)

	// Execute tests in parallel
	result, err := executor.ExecuteTests(ctx, workItems)
	if err != nil {
		return nil, fmt.Errorf("parallel test execution failed: %w", err)
	}

	// Set parallel execution metadata
	result.IsParallel = true
	result.WallClockTime = time.Since(start)
	// Note: result.Duration is already set by ParallelExecutor as sum of test durations

	// Finalize gate and suite statuses
	r.finalizeParallelResults(result)

	return result, nil
}

// finalizeParallelResults updates the final status of gates and suites after parallel execution
func (r *runner) finalizeParallelResults(result *RunnerResult) {
	// Use the shared result manager for consistent finalization
	resultMgr := NewResultHierarchyManager()
	resultMgr.FinalizeResults(result, result.Stats.StartTime)
}

// processAllGates handles the execution of all gates
func (r *runner) processAllGates(ctx context.Context, result *RunnerResult) error {
	// Group validators by gate
	gateValidators := r.groupValidatorsByGate()

	// Process each gate
	for gateName, validators := range gateValidators {
		if err := r.processGate(ctx, gateName, validators, result); err != nil {
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
func (r *runner) processGate(ctx context.Context, gateName string, validators []types.ValidatorMetadata, result *RunnerResult) error {
	ctx, span := r.tracer.Start(ctx, fmt.Sprintf("gate %s", gateName))
	defer span.End()

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
	if err := r.processSuites(ctx, suiteValidators, gateResult, result); err != nil {
		return err
	}

	// Then process direct gate tests
	if err := r.processDirectTests(ctx, directTests, gateResult, result); err != nil {
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
func (r *runner) processSuites(ctx context.Context, suiteValidators map[string][]types.ValidatorMetadata, gateResult *GateResult, result *RunnerResult) error {
	for suiteName, suiteTests := range suiteValidators {
		if err := r.processSuite(ctx, suiteName, suiteTests, gateResult, result); err != nil {
			return fmt.Errorf("processing suite %s: %w", suiteName, err)
		}
	}
	return nil
}

// processSuite handles the execution of a single suite
func (r *runner) processSuite(ctx context.Context, suiteName string, suiteTests []types.ValidatorMetadata, gateResult *GateResult, result *RunnerResult) error {
	ctx, span := r.tracer.Start(ctx, fmt.Sprintf("suite %s", suiteName))
	defer span.End()

	suiteStart := time.Now()
	suiteResult := &SuiteResult{
		ID:    suiteName,
		Tests: make(map[string]*types.TestResult),
		Stats: ResultStats{StartTime: suiteStart},
	}
	gateResult.Suites[suiteName] = suiteResult

	// Run all tests in the suite
	for _, validator := range suiteTests {
		if err := r.processTestAndAddToResults(ctx, validator, gateResult, suiteResult, result); err != nil {
			return err
		}
	}

	suiteResult.Duration = time.Since(suiteStart)
	suiteResult.Status = determineSuiteStatus(suiteResult)
	suiteResult.Stats.EndTime = time.Now()

	return nil
}

// processDirectTests handles the execution of direct gate tests
func (r *runner) processDirectTests(ctx context.Context, directTests []types.ValidatorMetadata, gateResult *GateResult, result *RunnerResult) error {
	for _, validator := range directTests {
		if err := r.processTestAndAddToResults(ctx, validator, gateResult, nil, result); err != nil {
			return err
		}
	}
	return nil
}

// processTestAndAddToResults runs a single test and adds its results to the appropriate result containers
func (r *runner) processTestAndAddToResults(ctx context.Context, validator types.ValidatorMetadata, gateResult *GateResult, suiteResult *SuiteResult, result *RunnerResult) error {
	testResult, err := r.RunTest(ctx, validator)
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

func isLocalPath(pkg string) bool {
	return strings.HasPrefix(pkg, "./") || strings.HasPrefix(pkg, "/") || strings.HasPrefix(pkg, "../")
}

// RunTest implements the TestRunner interface
func (r *runner) RunTest(ctx context.Context, metadata types.ValidatorMetadata) (*types.TestResult, error) {
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

	// Check if the path is available locally
	if isLocalPath(metadata.Package) {
		fullPath := filepath.Join(r.workDir, metadata.Package)
		if _, statErr := os.Stat(fullPath); os.IsNotExist(statErr) {
			r.log.Error("Local package path does not exist, failing test", "validator", metadata.ID, "package", metadata.Package, "fullPath", fullPath)
			return &types.TestResult{
				Metadata: metadata,
				Status:   types.TestStatusFail,
				Error:    fmt.Errorf("local package path does not exist: %s", fullPath),
			}, nil
		}
	}

	r.log.Info("Running validator", "validator", metadata.ID)

	start := time.Now()
	if metadata.RunAll {
		result, err = r.runAllTestsInPackage(ctx, metadata)
	} else {
		result, err = r.runSingleTest(ctx, metadata)
	}

	var status types.TestStatus
	if result != nil {
		result.Duration = time.Since(start)
		status = result.Status
	} else {
		status = types.TestStatusError
	}
	metrics.RecordValidation(r.networkName, r.runID, metadata.ID, metadata.Type.String(), status)

	return result, err
}

// runAllTestsInPackage discovers and runs all tests in a package
// Executes the entire package as a single go test process to preserve intra-package parallelism.
func (r *runner) runAllTestsInPackage(ctx context.Context, metadata types.ValidatorMetadata) (*types.TestResult, error) {
	pkgMeta := metadata
	pkgMeta.RunAll = false
	pkgMeta.FuncName = ""

	r.log.Debug("Running package as single process", "package", pkgMeta.Package)
	res, err := r.runSingleTest(ctx, pkgMeta)
	if res != nil {
		// Preserve the caller intent for reporting
		res.Metadata.RunAll = true
	}
	return res, err
}

// runTestList runs a list of tests and aggregates their results
func (r *runner) runTestList(ctx context.Context, metadata types.ValidatorMetadata, testNames []string) (*types.TestResult, error) {
	if len(testNames) == 0 {
		r.log.Warn("No tests found to run in package", "package", metadata.Package)
		return &types.TestResult{
			Metadata: metadata,
			Status:   types.TestStatusSkip,
			Duration: 0,
			SubTests: make(map[string]*types.TestResult),
		}, nil
	}

	var result = types.TestStatusPass
	var testErrors []error
	var totalDuration time.Duration
	testResults := make(map[string]*types.TestResult)
	var failedTestsStdout strings.Builder
	var aggregatedRawJSON []byte          // Store aggregated raw JSON for the test list
	var timeoutCount int                  // Track how many tests timed out
	var timedOutTests = make([]string, 0) // Track which tests timed out

	r.log.Info("Running test package", "package", metadata.Package, "testCount", len(testNames))

	// Run each test in the list
	for _, testName := range testNames {
		// Create a new metadata with the specific test name
		testMetadata := metadata
		testMetadata.RunAll = false
		testMetadata.FuncName = testName

		r.log.Debug("Running individual test in package", "test", testName, "package", metadata.Package)

		// Run the individual test
		testResult, err := r.runSingleTest(ctx, testMetadata)
		if err != nil {
			return nil, fmt.Errorf("running test %s: %w", testName, err)
		}

		// Store the individual test result
		testResults[testName] = testResult
		totalDuration += testResult.Duration

		// Check if this test timed out
		if testResult.TimedOut {
			timeoutCount++
			timedOutTests = append(timedOutTests, testName)
			r.log.Error("Test in package timed out",
				"test", testName,
				"package", metadata.Package,
				"error", testResult.Error.Error())
		}

		// Aggregate raw JSON from individual tests for the aggregated result
		if individualRawJSON, exists := r.getRawJSON(testMetadata.ID); exists {
			// Append this test's raw JSON to the aggregated JSON
			aggregatedRawJSON = append(aggregatedRawJSON, individualRawJSON...)
		}

		// Update overall status based on individual test result
		if testResult.Status == types.TestStatusFail {
			result = types.TestStatusFail

			if testResult.Error != nil {
				testErrors = append(testErrors, fmt.Errorf("%s: %w", testName, testResult.Error))
			}

			// Collect stdout from failing tests
			if testResult.Stdout != "" {
				if testResult.TimedOut {
					failedTestsStdout.WriteString(fmt.Sprintf("\n--- Test: %s (TIMED OUT) ---\n", testName))
				} else {
					failedTestsStdout.WriteString(fmt.Sprintf("\n--- Test: %s ---\n", testName))
				}
				failedTestsStdout.WriteString(testResult.Stdout)
			}
		}
	}

	// Store the aggregated raw JSON for the package-level test result
	if len(aggregatedRawJSON) > 0 {
		r.storeRawJSON(metadata.ID, aggregatedRawJSON)
	}

	// Create an appropriate error message that includes timeout information
	var finalError error
	if len(testErrors) > 0 {
		if timeoutCount > 0 {
			finalError = fmt.Errorf("package test failures include timeouts: %v", timedOutTests)
		} else {
			finalError = errors.Join(testErrors...)
		}
	}

	// If any tests failed, log the collected stdout
	failedStdout := failedTestsStdout.String()
	if result == types.TestStatusFail && failedStdout != "" {
		r.log.Debug("Package test failed",
			"package", metadata.Package,
			"timeouts", timeoutCount,
			"stdout_from_failed_tests", failedStdout)
	}

	// Log summary of package test execution
	passed := 0
	failed := 0
	for _, testResult := range testResults {
		switch testResult.Status {
		case types.TestStatusPass:
			passed++
		case types.TestStatusFail:
			failed++
		}
	}
	r.log.Info("Package test completed",
		"package", metadata.Package,
		"total", len(testNames),
		"passed", passed,
		"failed", failed,
		"timeouts", timeoutCount,
		"duration", totalDuration)

	// Create the aggregate result
	packageResult := &types.TestResult{
		Metadata: metadata,
		Status:   result,
		Error:    finalError,
		Duration: totalDuration,
		SubTests: testResults,
		Stdout:   failedStdout,
	}

	// Log the package result
	if r.fileLogger != nil {
		if logErr := r.fileLogger.LogTestResult(packageResult, r.runID); logErr != nil {
			r.log.Error("Failed to log package result", "error", logErr, "package", metadata.Package)
		}
	}
	r.log.Info("Package test result",
		"package", metadata.Package,
		"status", packageResult.Status,
		"duration", packageResult.Duration,
		"subtests", len(packageResult.SubTests),
		"error", packageResult.Error)

	return packageResult, nil
}

// runSingleTest runs a specific test
func (r *runner) runSingleTest(ctx context.Context, metadata types.ValidatorMetadata) (*types.TestResult, error) {
	ctx, span := r.tracer.Start(ctx, fmt.Sprintf("test %s", metadata.FuncName))
	defer span.End()

	var timeoutDuration time.Duration
	if metadata.Timeout != 0 {
		timeoutDuration = metadata.Timeout
	} else if metadata.FuncName == "" {
		// Apply default timeout for package-mode runs when none provided
		timeoutDuration = DefaultTestTimeout
	}
	if timeoutDuration != 0 {
		var cancel func()
		// This parent process timeout is redundant, add 200ms to allow child process
		// to trigger timeout before parent process.
		ctx, cancel = context.WithTimeout(ctx, timeoutDuration+200*time.Millisecond)
		defer cancel()
	}

	args := r.buildTestArgs(metadata)
	cmd, cleanup := r.testCommandContext(ctx, r.goBinary, args...)
	defer cleanup()

	var stdout, stderr bytes.Buffer
	var timeoutOccurred bool
	var testStartTime = time.Now()

	if r.outputRealtimeLogs {
		stdoutLogger := &logWriter{logFn: func(msg string) {
			r.log.Info("Test output", "test", metadata.FuncName, "output", msg)
		}}
		stderrLogger := &logWriter{logFn: func(msg string) {
			r.log.Error("Test error output", "test", metadata.FuncName, "error", msg)
		}}

		cmd.Stdout = io.MultiWriter(&stdout, stdoutLogger)
		cmd.Stderr = io.MultiWriter(&stderr, stderrLogger)
	} else {
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
	}

	// If there's no function name use package name
	testLabel := metadata.FuncName
	if testLabel == "" {
		testLabel = metadata.Package
	}

	r.log.Info("Running test", "test", testLabel)
	r.log.Debug("Running test command",
		"dir", cmd.Dir,
		"package", metadata.Package,
		"test", testLabel,
		"command", cmd.String(),
		"timeout", timeoutDuration,
		"allowSkips", r.allowSkips)

	// Run the command
	err := cmd.Run()
	testDuration := time.Since(testStartTime)

	// Check for timeout first and set flag
	if ctx.Err() == context.DeadlineExceeded {
		timeoutOccurred = true
		r.log.Error("Test timed out",
			"test", metadata.FuncName,
			"timeout", timeoutDuration,
			"duration", testDuration,
			"partialStdout", len(stdout.Bytes()),
			"partialStderr", len(stderr.Bytes()))
	}

	// ALWAYS store the raw JSON output for the RawJSONSink if we have a file logger
	// This ensures partial output is captured even on timeout
	if stdout.Len() > 0 {
		r.storeRawJSON(metadata.ID, stdout.Bytes())
		r.log.Debug("Stored partial output", "test", metadata.FuncName, "bytes", stdout.Len())
	} else if timeoutOccurred {
		// Even if no stdout, store a timeout marker in raw JSON for debugging
		timeoutInfo := fmt.Sprintf(`{"Time":"%s","Action":"timeout","Package":"%s","Test":"%s","Output":"TEST TIMED OUT after %v - no JSON output captured\n"}`,
			time.Now().Format(time.RFC3339), metadata.Package, metadata.FuncName, timeoutDuration)
		r.storeRawJSON(metadata.ID, []byte(timeoutInfo))
		r.log.Debug("Stored timeout marker", "test", metadata.FuncName)
	}

	// Handle timeout case with enhanced error messaging
	if timeoutOccurred {
		timeoutMsg := fmt.Sprintf("TIMEOUT: Test timed out after %v", timeoutDuration)
		if testDuration > 0 {
			timeoutMsg += fmt.Sprintf(" (actual duration: %v)", testDuration)
		}

		result := &types.TestResult{
			Metadata: metadata,
			Status:   types.TestStatusFail,
			Error:    fmt.Errorf("%s", timeoutMsg),
			Duration: testDuration,
			SubTests: make(map[string]*types.TestResult),
			TimedOut: true, // Set the timeout flag
		}

		// If we have partial stdout, include it for analysis
		if stdout.Len() > 0 {
			result.Stdout = stdout.String()
			// Try to parse any partial output to extract subtest information
			if partialResult := r.parseTestOutputWithTimeout(stdout.Bytes(), metadata, timeoutDuration); partialResult != nil {
				result.SubTests = partialResult.SubTests
				// Update the error to include subtest information if available
				if len(result.SubTests) > 0 {
					timeoutMsg += fmt.Sprintf(" - %d subtests detected in partial output", len(result.SubTests))
					result.Error = fmt.Errorf("%s", timeoutMsg)
				}
			}
		}

		// Include stderr in the result if present
		if stderr.Len() > 0 {
			if result.Error != nil {
				result.Error = fmt.Errorf("%w\nstderr: %s", result.Error, stderr.String())
			} else {
				result.Error = fmt.Errorf("timeout stderr: %s", stderr.String())
			}
		}

		// Force logging of timeout result to ensure it's captured
		if r.fileLogger != nil {
			if logErr := r.fileLogger.LogTestResult(result, r.runID); logErr != nil {
				r.log.Error("Failed to log timeout result", "error", logErr, "test", metadata.FuncName)
			}
		}
		r.log.Info("Timeout result",
			"test", metadata.FuncName,
			"status", result.Status,
			"duration", result.Duration,
			"subtests", len(result.SubTests),
			"error", result.Error)

		return result, nil
	}

	// Parse the JSON output for non-timeout cases
	parsedResult := r.parseTestOutput(stdout.Bytes(), metadata)

	// If we couldn't parse the output for some reason, create a minimal failing result
	if parsedResult == nil {
		r.log.Error("test exited with non-zero exit code", "exitCode", cmd.ProcessState.ExitCode())
		parsedResult = &types.TestResult{
			Metadata: metadata,
			Status:   types.TestStatusFail,
			Error:    fmt.Errorf("failed to parse test output"),
			Stdout:   stdout.String(),
			SubTests: make(map[string]*types.TestResult),
		}
	}

	// Capture stdout in the test result for all tests
	if stdout.Len() > 0 {
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

	// Log the individual test result
	if r.fileLogger != nil {
		if logErr := r.fileLogger.LogTestResult(parsedResult, r.runID); logErr != nil {
			r.log.Error("Failed to log individual test result", "error", logErr, "test", metadata.FuncName)
		}
	}

	return parsedResult, nil
}

// parseTestOutput parses the JSON test output and extracts test result information
func (r *runner) parseTestOutput(output []byte, metadata types.ValidatorMetadata) *types.TestResult {
	return r.outputParser.Parse(output, metadata)
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

	// Add timeout: use provided value, otherwise in package-mode apply default
	if metadata.Timeout != 0 {
		args = append(args, "-timeout", metadata.Timeout.String())
	} else if metadata.FuncName == "" { // package-mode
		args = append(args, "-timeout", DefaultTestTimeout.String())
	}

	// Always use verbose output
	args = append(args, "-v")

	// Always use JSON output for more reliable parsing
	args = append(args, "-json")

	return args
}

// RunGate runs all tests in a specific gate
func (r *runner) RunGate(ctx context.Context, gate string) error {
	ctx, span := r.tracer.Start(ctx, fmt.Sprintf("gate %s", gate))
	defer span.End()

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
	result, err := r.RunAllTests(ctx)
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
		b.WriteString(fmt.Sprintf("%sStatus: %s\n", ui.TreeBranch, gate.Status))
		b.WriteString(fmt.Sprintf("%sTests: %d passed, %d failed, %d skipped\n", ui.TreeBranch,
			gate.Stats.Passed, gate.Stats.Failed, gate.Stats.Skipped))

		// Print direct gate tests
		for testName, test := range gate.Tests {
			// Get a display name for the test
			displayName := types.GetTestDisplayName(testName, test.Metadata)

			b.WriteString(fmt.Sprintf("%sTest: %s (%s) [status=%s]\n", ui.TreeBranch,
				displayName, formatDuration(test.Duration), test.Status))
			if test.Error != nil {
				b.WriteString(fmt.Sprintf("%sError: %s\n", ui.BuildTreePrefix(2, true, []bool{false}), test.Error.Error()))
			}

			// Print subtests recursively
			r.printSubTests(&b, test.SubTests, 2, []bool{false})
		}

		// Print suites
		for suiteName, suite := range gate.Suites {
			b.WriteString(fmt.Sprintf("%sSuite: %s (%s)\n", ui.TreeLastBranch, suiteName, formatDuration(suite.Duration)))
			b.WriteString(fmt.Sprintf("%sStatus: %s\n", ui.SuiteBranch, suite.Status))
			b.WriteString(fmt.Sprintf("%sTests: %d passed, %d failed, %d skipped\n", ui.SuiteBranch,
				suite.Stats.Passed, suite.Stats.Failed, suite.Stats.Skipped))

			// Print suite tests
			for testName, test := range suite.Tests {
				// Get a display name for the test
				displayName := types.GetTestDisplayName(testName, test.Metadata)

				b.WriteString(fmt.Sprintf("%sTest: %s (%s) [status=%s]\n", ui.SuiteBranch,
					displayName, formatDuration(test.Duration), test.Status))
				if test.Error != nil {
					b.WriteString(fmt.Sprintf("%sError: %s\n", ui.BuildTreePrefix(3, true, []bool{true, false}), test.Error.Error()))
				}

				// Print subtests recursively with suite indentation
				r.printSubTests(&b, test.SubTests, 3, []bool{true, false})
			}
		}
	}
	return b.String()
}

// printSubTests recursively prints subtests at any depth using dynamic tree prefixes
func (r *RunnerResult) printSubTests(b *strings.Builder, subTests map[string]*types.TestResult, baseDepth int, parentIsLast []bool) {
	if len(subTests) == 0 {
		return
	}

	// Convert map to slice and sort for consistent output
	subTestNames := make([]string, 0, len(subTests))
	for name := range subTests {
		subTestNames = append(subTestNames, name)
	}

	// Sort to ensure consistent output order
	for i := 0; i < len(subTestNames); i++ {
		for j := i + 1; j < len(subTestNames); j++ {
			if subTestNames[i] > subTestNames[j] {
				subTestNames[i], subTestNames[j] = subTestNames[j], subTestNames[i]
			}
		}
	}

	for i, subTestName := range subTestNames {
		subTest := subTests[subTestName]
		isLast := i == len(subTestNames)-1

		// Build the prefix for this depth level
		prefix := ui.BuildTreePrefix(baseDepth, isLast, parentIsLast)

		fmt.Fprintf(b, "%s Test: %s (%s) [status=%s]\n",
			prefix, subTestName, formatDuration(subTest.Duration), subTest.Status)

		if subTest.Error != nil {
			// Create error prefix (one level deeper, always last)
			errorParentIsLast := make([]bool, len(parentIsLast)+1)
			copy(errorParentIsLast, parentIsLast)
			errorParentIsLast[len(parentIsLast)] = isLast
			errorPrefix := ui.BuildTreePrefix(baseDepth+1, true, errorParentIsLast)
			fmt.Fprintf(b, "%sError: %s\n", errorPrefix, subTest.Error.Error())
		}

		// Recursively print nested subtests
		if len(subTest.SubTests) > 0 {
			nestedParentIsLast := make([]bool, len(parentIsLast)+1)
			copy(nestedParentIsLast, parentIsLast)
			nestedParentIsLast[len(parentIsLast)] = isLast
			r.printSubTests(b, subTest.SubTests, baseDepth+1, nestedParentIsLast)
		}
	}
}

// updateStats updates statistics at all levels
func (r *RunnerResult) updateStats(gate *GateResult, suite *SuiteResult, test *types.TestResult) {
	// Update test suite stats if applicable
	if suite != nil {
		r.updateStatsForContainer(&suite.Stats, &suite.Duration, test.Status, test.Duration)
	}

	// Update gate stats
	r.updateStatsForContainer(&gate.Stats, &gate.Duration, test.Status, test.Duration)

	// Update overall stats
	r.updateStatsForContainer(&r.Stats, &r.Duration, test.Status, test.Duration)

	// Update stats for SubTests if they exist
	if len(test.SubTests) > 0 {
		for _, subTest := range test.SubTests {
			// Update the global stats with this sub-test
			r.updateStatsForContainer(&r.Stats, &r.Duration, subTest.Status, subTest.Duration)

			// Update gate stats
			r.updateStatsForContainer(&gate.Stats, &gate.Duration, subTest.Status, subTest.Duration)

			// Update suite stats
			if suite != nil {
				r.updateStatsForContainer(&suite.Stats, &suite.Duration, subTest.Status, subTest.Duration)
			}
		}
	}
}

// updateStatsForContainer is a helper function that updates stats for any container (runner, gate, or suite)
func (r *RunnerResult) updateStatsForContainer(stats *ResultStats, duration *time.Duration, status types.TestStatus, testDuration time.Duration) {
	stats.Total++
	switch status {
	case types.TestStatusPass:
		stats.Passed++
	case types.TestStatusFail:
		stats.Failed++
	case types.TestStatusSkip:
		stats.Skipped++
	}
	*duration += testDuration
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

// formatErrors combines multiple test errors into a single error message

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

// SetFileLogger sets the file logger for the runner
func (r *runner) SetFileLogger(logger *logging.FileLogger) {
	r.fileLogger = logger
}

// getRawJSONSink is a helper method to get the RawJSONSink from the file logger
// Returns the sink and a boolean indicating if it was found and properly typed
func (r *runner) getRawJSONSink() (*logging.RawJSONSink, bool) {
	if r.fileLogger == nil {
		return nil, false
	}

	sink, ok := r.fileLogger.GetSinkByType("RawJSONSink")
	if !ok {
		r.log.Error("Failed to get RawJSONSink")
		return nil, false
	}

	rawSink, ok := sink.(*logging.RawJSONSink)
	if !ok {
		r.log.Error("Failed to get RawJSONSink: wrong type", "type", fmt.Sprintf("%T", sink))
		return nil, false
	}

	return rawSink, true
}

// storeRawJSON is a helper method to store raw JSON for a test
func (r *runner) storeRawJSON(testID string, rawJSON []byte) {
	if rawSink, ok := r.getRawJSONSink(); ok {
		rawSink.StoreRawJSON(testID, rawJSON)
	} else {
		r.log.Debug("No raw JSON sink available, not storing raw JSON output")
	}
}

// getRawJSON is a helper method to retrieve raw JSON for a test
func (r *runner) getRawJSON(testID string) ([]byte, bool) {
	if rawSink, ok := r.getRawJSONSink(); ok {
		return rawSink.GetRawJSON(testID)
	}
	return nil, false
}

func (r *runner) testCommandContext(ctx context.Context, name string, arg ...string) (*exec.Cmd, func()) {
	cmd := exec.CommandContext(ctx, name, arg...)
	cmd.Dir = r.workDir

	// Always set the TEST_LOG_LEVEL environment variable
	runEnv := append([]string{fmt.Sprintf("TEST_LOG_LEVEL=%s", r.testLogLevel)}, os.Environ()...)
	runEnv = append(runEnv, r.ReproducibleEnv()...)
	runEnv = telemetry.InstrumentEnvironment(ctx, runEnv)

	if r.env == nil {
		// For sysgo orchestrator, just add the orchestrator type to the environment
		runEnv = append(runEnv, fmt.Sprintf("DEVSTACK_ORCHESTRATOR=%s", flags.OrchestratorSysgo))
		// Disable color output in test logs to avoid ANSI escape sequences
		runEnv = append(runEnv, "NO_COLOR=1")
		cmd.Env = runEnv
		return cmd, func() {}
	}

	// For sysext orchestrator, use the existing devnet environment logic
	// at this point we've already parsed the URL once before to load the environment, so no need to check for errors
	url, _ := url.Parse(r.env.URL)

	// Create a temporary file for the devnet environment.
	// We can't rely on the environment that has been passed to the runner as addons may have modified it.
	envFile, err := os.CreateTemp("", "test-env-*.json")
	if err != nil {
		r.log.Error("Failed to create temp env file", "error", err)
	} else {
		if err := json.NewEncoder(envFile).Encode(r.env.Env); err != nil {
			r.log.Error("Failed to write env to temp file", "error", err)
		}

		runEnv = append(
			runEnv,
			// Add the orchestrator type
			fmt.Sprintf("DEVSTACK_ORCHESTRATOR=%s", flags.OrchestratorSysext),
			// override the env URL with the one from the temp file
			fmt.Sprintf("%s=%s", env.EnvURLVar, envFile.Name()),
			// override the control resolution scheme with the original one
			fmt.Sprintf("%s=%s", env.EnvCtrlVar, url.Scheme),
			// Disable color output in test logs to avoid ANSI escape sequences
			"NO_COLOR=1",
		)
		cmd.Env = runEnv
	}
	cleanup := func() {
		if envFile != nil {
			// not elegant, but how bad is it, really? A temp file might escape...
			_ = os.Remove(envFile.Name())
		}
	}
	return cmd, cleanup
}

func (r *runner) ReproducibleEnv() Env {
	orchestrator := flags.OrchestratorSysgo
	if r.env != nil {
		orchestrator = flags.OrchestratorSysext
	}

	// Prefer the runner's runID; fall back to the file logger's runID if not set
	seedRunID := r.runID
	if seedRunID == "" && r.fileLogger != nil {
		seedRunID = r.fileLogger.GetRunID()
	}

	base := Env{
		// Set the orchestrator type
		fmt.Sprintf("DEVSTACK_ORCHESTRATOR=%s", orchestrator),
		// salt the funder abstraction with DEVSTACK_KEYS_SALT=$runID
		fmt.Sprintf("%s=%s", dsl.SaltEnvVar, seedRunID),
		// align test logging level for reproduction
		fmt.Sprintf("TEST_LOG_LEVEL=%s", r.testLogLevel),
	}
	// Only set DEVNET_EXPECT_PRECONDITIONS_MET when we DO expect preconditions to be met.
	// op-devstack treats the mere presence of this variable as "enforce preconditions".
	// Therefore, when allowSkips=true we must NOT set the variable at all.
	if !r.allowSkips {
		base = append(base, fmt.Sprintf("%s=%t", env.ExpectPreconditionsMet, true))
	}
	// For sysext, include original ENV URL and control scheme (if available)
	if r.env != nil && r.env.URL != "" {
		if u, err := url.Parse(r.env.URL); err == nil {
			base = append(base,
				fmt.Sprintf("%s=%s", env.EnvURLVar, r.env.URL),
				fmt.Sprintf("%s=%s", env.EnvCtrlVar, u.Scheme),
			)
		}
	}
	return base
}

type Env []string

func (e Env) String() string {
	return strings.Join(e, "\n")
}

// Make sure the runner type implements both interfaces
var _ TestRunner = &runner{}
var _ TestRunnerWithFileLogger = &runner{}

type logWriter struct {
	logFn func(msg string)
	buf   []byte
}

func (w *logWriter) Write(p []byte) (n int, err error) {
	w.buf = append(w.buf, p...)
	for {
		idx := bytes.IndexByte(w.buf, '\n')
		if idx == -1 {
			break
		}
		line := w.buf[:idx]
		w.buf = w.buf[idx+1:]

		// Try to parse as a test event
		event, err := parseTestEvent(line)
		if err == nil && event.Action == ActionOutput {
			// If it's a valid test event with output action, use the Output field
			w.logFn(event.Output)
		} else {
			// If not a valid test event or not an output action, use the raw line
			w.logFn(string(line))
		}
	}
	return len(p), nil
}

// parseTestOutputWithTimeout parses partial test output from timed-out tests
// It's more lenient than parseTestOutput and focuses on extracting any available subtest information
func (r *runner) parseTestOutputWithTimeout(output []byte, metadata types.ValidatorMetadata, timeoutDuration time.Duration) *types.TestResult {
	if len(output) == 0 {
		r.log.Debug("Empty test output in timeout scenario", "test", metadata.FuncName, "package", metadata.Package)
		return nil
	}

	result := &types.TestResult{
		Metadata: metadata,
		Status:   types.TestStatusFail, // Always fail for timeout
		SubTests: make(map[string]*types.TestResult),
		Error:    fmt.Errorf("TIMEOUT: Test timed out after %v", timeoutDuration),
		TimedOut: true, // Mark as timed out
	}

	subTestStatuses := make(map[string]types.TestStatus)
	subTestStartTimes := make(map[string]time.Time)
	lines := bytes.Split(output, []byte("\n"))

	validEventsFound := 0

	for _, line := range lines {
		if len(line) == 0 {
			continue
		}

		event, err := parseTestEvent(line)
		if err != nil {
			// In timeout scenarios, be more lenient with parsing errors
			r.log.Debug("Failed to parse test JSON output line in timeout scenario", "error", err, "line", string(line))
			continue
		}

		validEventsFound++

		if isMainTestEvent(event, metadata.FuncName) {
			switch event.Action {
			case ActionOutput:
				// Store any output from the main test, might be useful for debugging timeouts
				if result.Error != nil {
					result.Error = fmt.Errorf("%w\nOutput: %s", result.Error, event.Output)
				}
			}
		} else {
			// Process subtest events
			subTest, exists := result.SubTests[event.Test]
			if !exists {
				subTest = &types.TestResult{
					Metadata: types.ValidatorMetadata{
						FuncName: event.Test,
						Package:  result.Metadata.Package,
					},
					Status: types.TestStatusFail, // Default to fail in timeout scenarios
				}
				result.SubTests[event.Test] = subTest
			}

			switch event.Action {
			case ActionStart:
				subTestStartTimes[event.Test] = event.Time
				subTest.Status = types.TestStatusFail // Assume failed due to timeout unless we see completion
			case ActionPass:
				subTest.Status = types.TestStatusPass
				subTestStatuses[event.Test] = types.TestStatusPass
				calculateSubTestDuration(subTest, event, subTestStartTimes)
			case ActionFail:
				subTest.Status = types.TestStatusFail
				subTestStatuses[event.Test] = types.TestStatusFail
				calculateSubTestDuration(subTest, event, subTestStartTimes)
			case ActionSkip:
				subTest.Status = types.TestStatusSkip
				subTestStatuses[event.Test] = types.TestStatusSkip
				calculateSubTestDuration(subTest, event, subTestStartTimes)
			case ActionOutput:
				updateSubTestError(subTest, event.Output)
			}
		}
	}

	// Mark any subtests that started but didn't complete as timed out
	for testName, subTest := range result.SubTests {
		if _, hasStatus := subTestStatuses[testName]; !hasStatus {
			// This subtest started but never completed - mark as timed out
			subTest.Status = types.TestStatusFail
			subTest.TimedOut = true // Mark subtest as timed out
			if subTest.Error == nil {
				subTest.Error = fmt.Errorf("SUBTEST TIMEOUT: Test timed out during execution")
			} else {
				subTest.Error = fmt.Errorf("%w (TIMED OUT)", subTest.Error)
			}

			// Calculate duration based on when the timeout actually occurred, not current time
			if startTime, hasStart := subTestStartTimes[testName]; hasStart {
				// Use the timeout duration as the maximum time this subtest could have run
				// This is more accurate than time.Since(startTime) which could be much later
				actualTimeout := startTime.Add(timeoutDuration)
				subTest.Duration = actualTimeout.Sub(startTime)
			} else {
				// If we don't have a start time, use a fraction of the timeout as estimate
				subTest.Duration = timeoutDuration / 2
			}
		}
	}

	r.log.Debug("Parsed partial timeout output",
		"test", metadata.FuncName,
		"package", metadata.Package,
		"subtests", len(result.SubTests),
		"validEvents", validEventsFound,
		"timeout", timeoutDuration,
	)

	return result
}

// GetSpeedup returns the speedup factor (total test time / wall clock time)
func (r *RunnerResult) GetSpeedup() float64 {
	if r.WallClockTime == 0 {
		return 1.0
	}
	return float64(r.Duration) / float64(r.WallClockTime)
}

// GetEfficiencyDisplayString returns a formatted efficiency description
func (r *RunnerResult) GetEfficiencyDisplayString() string {
	if !r.IsParallel || r.WallClockTime == 0 {
		return ""
	}
	speedup := r.GetSpeedup()
	if speedup > 1.1 { // Only show if meaningful speedup
		return fmt.Sprintf(" (%.1fx speedup)", speedup)
	}
	return ""
}

// Interface methods for enhanced timing display
func (r *RunnerResult) GetDuration() time.Duration {
	return r.Duration
}

func (r *RunnerResult) GetWallClockTime() time.Duration {
	return r.WallClockTime
}

func (r *RunnerResult) IsParallelRun() bool {
	return r.IsParallel
}

// determineConcurrency intelligently determines the optimal concurrency level
// based on system capabilities, workload characteristics, and user preferences.
// we assume that the tests are I/O-bound and that the system is capable
// of handling more concurrent workers than CPU cores.
func (r *runner) determineConcurrency(numWorkItems int) int {
	// Handle edge case: no work items means no concurrency needed
	if numWorkItems == 0 {
		r.log.Debug("No work items, returning zero concurrency")
		return 0
	}

	// If user explicitly set concurrency, use it (unless it's 0 which means auto)
	if r.concurrency > 0 {
		requestedConcurrency := r.concurrency
		// Cap at number of work items - no point having more workers than work
		effectiveConcurrency := int(math.Min(float64(requestedConcurrency), float64(numWorkItems)))
		r.log.Info("Using user-specified concurrency",
			"requested", requestedConcurrency,
			"effective", effectiveConcurrency,
			"workItems", numWorkItems)
		return effectiveConcurrency
	}

	numCPU := runtime.NumCPU()
	baseConcurrency := numCPU

	var targetConcurrency int
	if numCPU <= 2 {
		// On low-core systems, be conservative
		targetConcurrency = numCPU
	} else if numCPU <= 4 {
		// On mid-range systems, modest increase
		targetConcurrency = int(math.Ceil(float64(numCPU) * 1.25))
	} else {
		// On high-core systems, more aggressive for I/O-bound workloads
		targetConcurrency = int(math.Ceil(float64(numCPU) * 1.5))
	}

	// Apply constraints in correct order:
	// 1. Ensure minimum of 1 worker for non-zero work
	if targetConcurrency < 1 {
		targetConcurrency = 1
	}

	// 2. Cap at reasonable upper bound to avoid resource exhaustion
	maxReasonableConcurrency := 32 // Reasonable upper limit for most systems
	if targetConcurrency > maxReasonableConcurrency {
		targetConcurrency = maxReasonableConcurrency
	}

	// 3. Finally, never exceed number of work items (most important constraint)
	if targetConcurrency > numWorkItems {
		targetConcurrency = numWorkItems
	}

	r.log.Info("Auto-determined concurrency",
		"cpuCores", numCPU,
		"baseConcurrency", baseConcurrency,
		"targetConcurrency", targetConcurrency,
		"workItems", numWorkItems,
	)

	return targetConcurrency
}
