package logging

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestJSONOutputParser(t *testing.T) {
	// Test with standard JSON output from go test -json
	jsonOutput := `{"Time":"2025-05-09T16:31:48.748553+10:00","Action":"output","Package":"simple","Test":"TestExample","Output":"=== RUN   TestExample\n"}
{"Time":"2025-05-09T16:31:48.748563+10:00","Action":"output","Package":"simple","Test":"TestExample","Output":"This is line 1\n"}
{"Time":"2025-05-09T16:31:48.748570+10:00","Action":"run","Package":"simple","Test":"TestExample"}
{"Time":"2025-05-09T16:31:48.748575+10:00","Action":"output","Package":"simple","Test":"TestExample","Output":"This is line 2\n"}`

	parser := NewJSONOutputParser(jsonOutput)

	// Collect output using a handler
	var outputs []string
	var tests []string

	parser.ProcessJSONOutput(func(jsonData map[string]interface{}, outputText string) {
		outputs = append(outputs, outputText)
		if test, ok := jsonData["Test"].(string); ok {
			tests = append(tests, test)
		}
	})

	// Verify the correct outputs were collected
	assert.Equal(t, 3, len(outputs), "Should have collected 3 output lines")
	assert.Equal(t, "=== RUN   TestExample\n", outputs[0])
	assert.Equal(t, "This is line 1\n", outputs[1])
	assert.Equal(t, "This is line 2\n", outputs[2])

	// Verify test names were collected
	assert.Equal(t, 3, len(tests), "Should have collected 3 test names")
	for _, test := range tests {
		assert.Equal(t, "TestExample", test)
	}

	// Test with mixed content
	mixedOutput := `This is not JSON
{"Time":"2025-05-09T16:31:48.748553+10:00","Action":"output","Package":"simple","Test":"TestExample","Output":"=== RUN   TestExample\n"}
{"Time":"2025-05-09T16:31:48.748575+10:00","Action":"output","Package":"simple","Test":"TestExample","Output":"Test output\n"}`

	mixedParser := NewJSONOutputParser(mixedOutput)
	var mixedOutputs []string

	mixedParser.ProcessJSONOutput(func(jsonData map[string]interface{}, outputText string) {
		mixedOutputs = append(mixedOutputs, outputText)
	})

	// Verify it correctly handles mixed content
	assert.Equal(t, 2, len(mixedOutputs), "Should skip non-JSON lines")
	assert.Equal(t, "=== RUN   TestExample\n", mixedOutputs[0])
	assert.Equal(t, "Test output\n", mixedOutputs[1])

	// Test with empty input
	emptyParser := NewJSONOutputParser("")
	var emptyOutputs []string

	emptyParser.ProcessJSONOutput(func(jsonData map[string]interface{}, outputText string) {
		emptyOutputs = append(emptyOutputs, outputText)
	})

	assert.Empty(t, emptyOutputs, "Should not process any data for empty input")

	// Create a more enhanced version that can filter by test name
	testJSON := `{"Time":"2025-05-09T16:31:48.748553+10:00","Action":"output","Package":"simple","Test":"TestOne","Output":"Output from TestOne\n"}
{"Time":"2025-05-09T16:31:48.748563+10:00","Action":"output","Package":"simple","Test":"TestTwo","Output":"Output from TestTwo\n"}
{"Time":"2025-05-09T16:31:48.748575+10:00","Action":"output","Package":"simple","Test":"TestOne","Output":"More output from TestOne\n"}`

	testParser := NewJSONOutputParser(testJSON)

	// Only collect output from TestOne
	var testOneOutput strings.Builder
	testParser.ProcessJSONOutput(func(jsonData map[string]interface{}, outputText string) {
		if test, ok := jsonData["Test"].(string); ok && test == "TestOne" {
			testOneOutput.WriteString(outputText)
		}
	})

	expectedTestOneOutput := "Output from TestOne\nMore output from TestOne\n"
	assert.Equal(t, expectedTestOneOutput, testOneOutput.String(), "Should filter by test name")
}

func TestJSONOutputParserWithReader(t *testing.T) {
	jsonData := `{"Time":"2025-05-09T16:31:48.748553+10:00","Action":"output","Package":"reader","Test":"TestReader","Output":"Reader output 1\n"}
{"Time":"2025-05-09T16:31:48.748563+10:00","Action":"output","Package":"reader","Test":"TestReader","Output":"Reader output 2\n"}`

	// Create parser from string
	parser := NewJSONOutputParser(jsonData)

	// Collect output
	var outputs []string
	parser.ProcessJSONOutput(func(_ map[string]interface{}, outputText string) {
		outputs = append(outputs, outputText)
	})

	// Verify output
	assert.Equal(t, 2, len(outputs), "Should process lines from string")
	assert.Equal(t, "Reader output 1\n", outputs[0])
	assert.Equal(t, "Reader output 2\n", outputs[1])
}

// Test various edge cases in JSON parsing with individual lines
func TestJSONParsingEdgeCases(t *testing.T) {
	// Test with valid single line
	validLine := `{"Time":"2025-05-09T16:31:48.748553+10:00","Action":"output","Package":"simple","Test":"TestLine","Output":"Line output\n"}`
	validParser := NewJSONOutputParser(validLine)

	var validOutput string
	var validTestName string
	validParser.ProcessJSONOutput(func(jsonData map[string]interface{}, outputText string) {
		validOutput = outputText
		validTestName = jsonData["Test"].(string)
	})

	// Verify valid JSON is processed
	assert.Equal(t, "Line output\n", validOutput)
	assert.Equal(t, "TestLine", validTestName)

	// Test with invalid JSON
	invalidJSON := `{"This is not valid JSON`
	invalidParser := NewJSONOutputParser(invalidJSON)
	invalidCalled := false
	invalidParser.ProcessJSONOutput(func(_ map[string]interface{}, _ string) {
		invalidCalled = true
	})
	assert.False(t, invalidCalled, "Handler should not be called for invalid JSON")

	// Test with non-output action
	nonOutputLine := `{"Time":"2025-05-09T16:31:48.748553+10:00","Action":"run","Package":"simple","Test":"TestRun"}`
	nonOutputParser := NewJSONOutputParser(nonOutputLine)
	nonOutputCalled := false
	nonOutputParser.ProcessJSONOutput(func(_ map[string]interface{}, _ string) {
		nonOutputCalled = true
	})
	assert.False(t, nonOutputCalled, "Handler should not be called for non-output action")

	// Test with empty input
	emptyParser := NewJSONOutputParser("")
	emptyCalled := false
	emptyParser.ProcessJSONOutput(func(_ map[string]interface{}, _ string) {
		emptyCalled = true
	})
	assert.False(t, emptyCalled, "Handler should not be called for empty input")

	// Test with non-JSON text
	nonJSONParser := NewJSONOutputParser("This is not JSON")
	nonJSONCalled := false
	nonJSONParser.ProcessJSONOutput(func(_ map[string]interface{}, _ string) {
		nonJSONCalled = true
	})
	assert.False(t, nonJSONCalled, "Handler should not be called for non-JSON text")

	// Test with multiple mixed lines including one valid line
	mixedContent := `Not JSON
{"Time":"2025-05-09T16:31:48.748553+10:00","Action":"output","Package":"simple","Test":"TestMixed","Output":"Mixed output\n"}
{"Action":"run"}`

	mixedParser := NewJSONOutputParser(mixedContent)
	var mixedOutput string
	mixedParser.ProcessJSONOutput(func(_ map[string]interface{}, outputText string) {
		mixedOutput = outputText
	})

	assert.Equal(t, "Mixed output\n", mixedOutput, "Should process only the valid output line in mixed content")
}

func TestGetErrorInfo(t *testing.T) {
	// Test output with error information
	errorJSON := `{"Time":"2025-05-09T16:31:48.748553+10:00","Action":"output","Package":"simple","Test":"TestExample","Output":"=== RUN   TestExample\n"}
{"Time":"2025-05-09T16:31:48.748563+10:00","Action":"output","Package":"simple","Test":"TestExample","Output":"    Error Trace:    /path/to/file.go:123\n"}
{"Time":"2025-05-09T16:31:48.748570+10:00","Action":"output","Package":"simple","Test":"TestExample","Output":"    Error:          Expected values to match\n"}
{"Time":"2025-05-09T16:31:48.748571+10:00","Action":"output","Package":"simple","Test":"TestExample","Output":"                    expected: 42\n"}
{"Time":"2025-05-09T16:31:48.748572+10:00","Action":"output","Package":"simple","Test":"TestExample","Output":"                    actual  : 43\n"}
{"Time":"2025-05-09T16:31:48.748573+10:00","Action":"output","Package":"simple","Test":"TestExample","Output":"    Messages:       Values should be equal\n"}`

	// Test using the parser's method directly
	parser := NewJSONOutputParser(errorJSON)
	errorInfo := parser.GetErrorInfo()

	// Verify all fields were properly extracted
	assert.Equal(t, "TestExample", errorInfo.TestName)
	assert.Contains(t, errorInfo.ErrorTrace, "/path/to/file.go:123")
	assert.Contains(t, errorInfo.ErrorMessage, "Expected values to match")
	assert.Contains(t, errorInfo.Expected, "42")
	assert.Contains(t, errorInfo.Actual, "43")
	assert.Contains(t, errorInfo.Messages, "Values should be equal")

	// Test using the helper function
	helperErrorInfo := extractErrorData(errorJSON)
	assert.Equal(t, errorInfo, helperErrorInfo, "Helper function should produce the same result")

	// Test with empty input
	emptyErrorInfo := extractErrorData("")
	assert.Empty(t, emptyErrorInfo.TestName)
	assert.Empty(t, emptyErrorInfo.ErrorMessage)
}
