package runner

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
)

var _ OutputParser = (*outputParser)(nil)

// outputParser implements OutputParser interface
type outputParser struct{}

// NewOutputParser creates a new output parser
func NewOutputParser() OutputParser {
	return &outputParser{}
}

// Parse parses test output into TestResult
func (p *outputParser) Parse(output []byte, metadata types.ValidatorMetadata) *types.TestResult {
	result := &types.TestResult{
		Metadata: metadata,
		Status:   types.TestStatusPass,
		SubTests: make(map[string]*types.TestResult),
	}

	if len(output) == 0 {
		result.Status = types.TestStatusFail
		result.Error = fmt.Errorf("no test output")
		return result
	}

	var testStart, testEnd time.Time
	var errorMsg strings.Builder
	var hasSkip bool
	subTestStartTimes := make(map[string]time.Time)

	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := scanner.Bytes()
		event, err := parseTestEvent(line)
		if err != nil {
			continue
		}

		if isMainTestEvent(event, metadata.FuncName) {
			processMainTestEvent(event, result, &testStart, &testEnd, &errorMsg, &hasSkip)
		} else if isSubTestEvent(event, metadata.FuncName) {
			processSubTestEvent(event, result, subTestStartTimes, &errorMsg)
		}
	}

	// Calculate duration
	if !testStart.IsZero() && !testEnd.IsZero() {
		result.Duration = calculateTestDuration(testStart, testEnd)
	}

	// Set error message if test failed
	if result.Status == types.TestStatusFail && errorMsg.Len() > 0 {
		result.Error = fmt.Errorf("%s", strings.TrimSpace(errorMsg.String()))
	}

	// Handle skip status
	if hasSkip && result.Status != types.TestStatusFail {
		result.Status = types.TestStatusSkip
		// Note: Skip reasons are not stored as errors since they're not failures
		// The skip reason information is available in the test output if needed
	}

	return result
}

// ParseWithTimeout parses test output for tests that exceeded timeout
func (p *outputParser) ParseWithTimeout(output []byte, metadata types.ValidatorMetadata, timeout time.Duration) *types.TestResult {
	// If no output, return a minimal timeout result
	if len(output) == 0 {
		return &types.TestResult{
			Metadata: metadata,
			Status:   types.TestStatusFail,
			Error:    fmt.Errorf("TIMEOUT: Test timed out after %v", timeout),
			SubTests: make(map[string]*types.TestResult),
			TimedOut: true,
		}
	}

	// Build a timeout-focused parse that preserves completed subtests
	result := &types.TestResult{
		Metadata: metadata,
		Status:   types.TestStatusFail,
		SubTests: make(map[string]*types.TestResult),
		Error:    fmt.Errorf("TIMEOUT: Test timed out after %v", timeout),
		TimedOut: true,
	}

	subTestStatuses := make(map[string]types.TestStatus)
	subTestStartTimes := make(map[string]time.Time)

	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := scanner.Bytes()
		event, err := parseTestEvent(line)
		if err != nil {
			// Be lenient on JSON parsing during timeout scenarios
			continue
		}

		// Attach any main test output to the error for context
		if isMainTestEvent(event, metadata.FuncName) {
			if event.Action == ActionOutput {
				output := strings.TrimSpace(event.Output)
				if output != "" && result.Error != nil {
					result.Error = fmt.Errorf("%w\nOutput: %s", result.Error, output)
				}
			}
			continue
		}

		// Handle subtests
		subTest, exists := result.SubTests[event.Test]
		if !exists {
			funcName := event.Test
			parts := strings.Split(event.Test, "/")
			if len(parts) > 1 {
				funcName = strings.Join(parts[1:], "/")
			}

			subTest = &types.TestResult{
				Metadata: types.ValidatorMetadata{
					FuncName: funcName,
					Package:  result.Metadata.Package,
				},
				Status: types.TestStatusFail, // default to fail in timeout scenarios until proven otherwise
			}
			result.SubTests[event.Test] = subTest
		}

		switch event.Action {
		case ActionStart, ActionRun:
			subTestStartTimes[event.Test] = event.Time
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

	// Only mark subtests that started but never completed as timed out
	for name, sub := range result.SubTests {
		if _, completed := subTestStatuses[name]; !completed {
			sub.Status = types.TestStatusFail
			sub.TimedOut = true
			if sub.Error == nil {
				sub.Error = fmt.Errorf("SUBTEST TIMEOUT: Test timed out during execution")
			} else {
				sub.Error = fmt.Errorf("%w (TIMED OUT)", sub.Error)
			}
			if start, ok := subTestStartTimes[name]; ok {
				sub.Duration = start.Add(timeout).Sub(start)
			} else {
				// No start time observed; use full timeout to avoid understating runtime
				sub.Duration = timeout
			}
		}
	}

	return result
}

func parseTestEvent(line []byte) (TestEvent, error) {
	var event TestEvent
	if err := json.Unmarshal(line, &event); err != nil {
		return event, err
	}
	return event, nil
}

func isMainTestEvent(event TestEvent, mainTestName string) bool {
	// Handle main test events:
	// 1. Direct match: event.Test == mainTestName
	// 2. Package mode: both are empty (mainTestName == "" && event.Test == "")
	// 3. Single test mode start/end: event.Test == "" but we want mainTestName (for package-level start/end events)
	return event.Test == mainTestName ||
		(mainTestName == "" && event.Test == "") ||
		(mainTestName != "" && event.Test == "" &&
			(event.Action == ActionStart || event.Action == ActionFail || event.Action == ActionPass))
}

// isSubTestEvent determines if an event should be processed as a subtest
func isSubTestEvent(event TestEvent, mainTestName string) bool {
	// Only process events that have a test name
	if event.Test == "" {
		return false
	}

	// Case 1: Actual subtest (contains "/" separator)
	isActualSubTest := strings.Contains(event.Test, "/")

	// Case 2: Package mode - individual tests when no specific function is targeted
	isPackageModeTest := mainTestName == ""

	return isActualSubTest || isPackageModeTest
}

func processMainTestEvent(event TestEvent, result *types.TestResult, testStart, testEnd *time.Time, errorMsg *strings.Builder, hasSkip *bool) {
	switch event.Action {
	case ActionStart:
		*testStart = event.Time
	case ActionPass:
		*testEnd = event.Time
		result.Status = types.TestStatusPass
	case ActionFail:
		*testEnd = event.Time
		result.Status = types.TestStatusFail
	case ActionSkip:
		*testEnd = event.Time
		result.Status = types.TestStatusSkip
		*hasSkip = true
	case ActionOutput:
		output := strings.TrimSpace(event.Output)
		if output != "" {
			if errorMsg.Len() > 0 {
				errorMsg.WriteString("\n")
			}
			errorMsg.WriteString(output)

			// Check for skip indicators
			if strings.Contains(output, "--- SKIP:") {
				*hasSkip = true
			}
		}
	}
}

func processSubTestEvent(event TestEvent, result *types.TestResult,
	subTestStartTimes map[string]time.Time, errorMsg *strings.Builder) {

	// Extract subtest name
	parts := strings.Split(event.Test, "/")

	// For individual tests in package mode (no "/"), treat as direct subtest
	// For actual subtests (contains "/"), use full path
	subTestName := event.Test

	// Early return only if we have no test name at all
	if event.Test == "" {
		return
	}

	// Get or create subtest result
	subTest, exists := result.SubTests[subTestName]
	if !exists {
		var funcName string
		if len(parts) > 1 {
			// Actual subtest: use just the subtest part
			funcName = strings.Join(parts[1:], "/")
		} else {
			// Individual test in package mode: use full test name
			funcName = event.Test
		}

		subTest = &types.TestResult{
			Metadata: types.ValidatorMetadata{
				FuncName: funcName,
				Package:  result.Metadata.Package,
			},
			Status:   types.TestStatusPass,
			SubTests: make(map[string]*types.TestResult),
		}
		result.SubTests[subTestName] = subTest
	}

	switch event.Action {
	case ActionStart, ActionRun:
		subTestStartTimes[subTestName] = event.Time
	case ActionPass:
		subTest.Status = types.TestStatusPass
		calculateSubTestDuration(subTest, event, subTestStartTimes)
	case ActionFail:
		subTest.Status = types.TestStatusFail
		calculateSubTestDuration(subTest, event, subTestStartTimes)
	case ActionSkip:
		subTest.Status = types.TestStatusSkip
		calculateSubTestDuration(subTest, event, subTestStartTimes)
	case ActionOutput:
		// Store the plain text output in the subtest's Stdout field
		// We store plain text here since subtests don't have their own JSON stream
		// The filelogger will handle plain text appropriately
		if subTest.Stdout == "" {
			subTest.Stdout = event.Output
		} else {
			subTest.Stdout += event.Output
		}
		updateSubTestError(subTest, event.Output)
	}

	// Note: Nested subtests are handled by the processSubTestEvent logic above
	// No additional processing needed - the full test name is used as the key
}

func calculateSubTestDuration(subTest *types.TestResult, event TestEvent, subTestStartTimes map[string]time.Time) {
	// Prefer calculated time difference over Elapsed field when we have a start time
	if startTime, ok := subTestStartTimes[event.Test]; ok {
		subTest.Duration = event.Time.Sub(startTime)
	} else if event.Elapsed > 0 {
		subTest.Duration = time.Duration(event.Elapsed * float64(time.Second))
	}
}

func updateSubTestError(subTest *types.TestResult, output string) {
	output = strings.TrimSpace(output)
	if strings.Contains(output, "Error:") || strings.Contains(output, "panic:") ||
		strings.Contains(output, "--- FAIL:") {
		if subTest.Error == nil {
			subTest.Error = fmt.Errorf("%s", output)
		} else {
			subTest.Error = fmt.Errorf("%w\n%s", subTest.Error, output)
		}
	} else if strings.Contains(output, "--- SKIP:") {
		// Store skip reason in a comment or as part of the error
		if subTest.Error == nil {
			subTest.Error = fmt.Errorf("SKIP: %s", output)
		}
	}
}

func calculateTestDuration(start, end time.Time) time.Duration {
	if start.IsZero() || end.IsZero() {
		return 0
	}
	duration := end.Sub(start)
	if duration < 0 {
		return 0
	}
	return duration
}
