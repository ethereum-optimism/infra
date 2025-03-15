package main

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	nat "github.com/ethereum-optimism/infra/op-acceptor"
	"github.com/ethereum-optimism/infra/op-acceptor/types"
)

// Mock implementation of an error with ExitCode method
type mockExitCoder struct {
	errMsg   string
	exitCode int
}

func (m *mockExitCoder) Error() string {
	return m.errMsg
}

func (m *mockExitCoder) ExitCode() int {
	return m.exitCode
}

func TestErrorExitCode(t *testing.T) {
	testCases := []struct {
		name     string
		err      error
		expected int
	}{
		{
			name:     "nil error should return 0",
			err:      nil,
			expected: 0,
		},
		{
			name:     "standard error should return system error code",
			err:      errors.New("standard error"),
			expected: nat.ExitCodeSystemError,
		},
		{
			name: "exit coder error should return its exit code",
			err: &mockExitCoder{
				errMsg:   "test error with exit code",
				exitCode: 3,
			},
			expected: 3,
		},
		{
			name:     "test failure should return test failure code",
			err:      nat.NewTestFailureError(types.TestStatusFail),
			expected: nat.ExitCodeTestFailure,
		},
		{
			name:     "test pass should return success code",
			err:      nat.NewTestFailureError(types.TestStatusPass),
			expected: nat.ExitCodeSuccess,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Get exit code from error
			var exitCode int
			if tc.err == nil {
				exitCode = 0
			} else if coder, ok := tc.err.(interface{ ExitCode() int }); ok {
				exitCode = coder.ExitCode()
			} else {
				exitCode = nat.ExitCodeSystemError
			}

			// Verify exit code is as expected
			assert.Equal(t, tc.expected, exitCode)
		})
	}
}

func TestTestStatusToExitCode(t *testing.T) {
	testCases := []struct {
		name         string
		testStatus   types.TestStatus
		expectedCode int
	}{
		{
			name:         "pass status returns success code",
			testStatus:   types.TestStatusPass,
			expectedCode: nat.ExitCodeSuccess,
		},
		{
			name:         "skip status returns success code",
			testStatus:   types.TestStatusSkip,
			expectedCode: nat.ExitCodeSuccess,
		},
		{
			name:         "fail status returns failure code",
			testStatus:   types.TestStatusFail,
			expectedCode: nat.ExitCodeTestFailure,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a test error
			testErr := nat.NewTestFailureError(tc.testStatus)

			// Verify the error returns the expected exit code
			assert.Equal(t, tc.expectedCode, testErr.ExitCode())

			// Verify error message contains the status
			assert.Contains(t, testErr.Error(), string(tc.testStatus))
		})
	}
}
