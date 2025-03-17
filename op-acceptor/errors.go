package nat

import (
	"errors"
	"fmt"
)

// RuntimeError represents an operational error that should lead to exit code 2
// Examples include configuration errors, file not found, etc.
type RuntimeError struct {
	Err error
}

func (e *RuntimeError) Error() string {
	return fmt.Sprintf("runtime error: %v", e.Err)
}

// Unwrap implements the errors.Unwrap interface
func (e *RuntimeError) Unwrap() error {
	return e.Err
}

// NewRuntimeError creates a new RuntimeError
func NewRuntimeError(err error) *RuntimeError {
	return &RuntimeError{Err: err}
}

// IsRuntimeError checks if the error is or wraps a RuntimeError
func IsRuntimeError(err error) bool {
	var runtimeErr *RuntimeError
	return err != nil && errors.As(err, &runtimeErr)
}

// TestFailureError represents a failure from test assertions (exit code 1)
type TestFailureError struct {
	Message string
}

func (e *TestFailureError) Error() string {
	return fmt.Sprintf("test failure: %s", e.Message)
}

// NewTestFailureError creates a new TestFailureError
func NewTestFailureError(message string) *TestFailureError {
	return &TestFailureError{Message: message}
}

// IsTestFailureError checks if the error is or wraps a TestFailureError
func IsTestFailureError(err error) bool {
	var testErr *TestFailureError
	return err != nil && errors.As(err, &testErr)
}
