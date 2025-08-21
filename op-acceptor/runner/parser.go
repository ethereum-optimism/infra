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
		} else if event.Test != "" && (strings.Contains(event.Test, "/") || metadata.FuncName == "") {
			// Process as subtest if:
			// 1. It contains "/" (actual subtest), OR
			// 2. We're in package mode (metadata.FuncName == "") and it's an individual test
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
	result := p.Parse(output, metadata)

	// Mark as timeout if duration exceeds timeout
	if result.Duration >= timeout {
		result.Status = types.TestStatusFail
		result.Error = fmt.Errorf("test exceeded timeout of %v", timeout)
		result.TimedOut = true

		// Mark all incomplete subtests as timed out
		for _, subTest := range result.SubTests {
			if subTest.Status == types.TestStatusPass {
				subTest.Status = types.TestStatusFail
				subTest.Error = fmt.Errorf("subtest timed out")
				subTest.TimedOut = true
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
