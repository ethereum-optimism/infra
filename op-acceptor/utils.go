package nat

import (
	"github.com/ethereum-optimism/infra/op-acceptor/types"
)

// TestFailedError is a special error type to indicate that tests failed but the system ran correctly
type TestFailedError struct {
	message string
}

// Error implements the error interface
func (e *TestFailedError) Error() string {
	return e.message
}

// NewTestFailedError creates a new TestFailedError with a given message
func NewTestFailedError(message string) *TestFailedError {
	return &TestFailedError{message: message}
}

// Helper function to convert bool to int
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// getResultString returns a colored string representing the test result
func getResultString(status types.TestStatus) string {
	switch status {
	case types.TestStatusPass:
		return "✓ pass"
	case types.TestStatusSkip:
		return "- skip"
	default:
		return "✗ fail"
	}
}
