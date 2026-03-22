package service

import (
	"errors"
	"testing"
)

func TestIsNotFoundError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "not found - lowercase",
			err:      errors.New("not found"),
			expected: true,
		},
		{
			name:     "not found - mixed case",
			err:      errors.New("Not Found"),
			expected: true,
		},
		{
			name:     "not found - uppercase",
			err:      errors.New("NOT FOUND"),
			expected: true,
		},
		{
			name:     "not found - with prefix",
			err:      errors.New("failed to list test metadata: not found"),
			expected: true,
		},
		{
			name:     "not found - with suffix",
			err:      errors.New("resource not found"),
			expected: true,
		},
		{
			name:     "other error - rate limit",
			err:      errors.New("rate limit exceeded"),
			expected: false,
		},
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isNotFoundError(tt.err)
			if got != tt.expected {
				t.Errorf("isNotFoundError() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestFlakyStatusMapping(t *testing.T) {
	tests := []struct {
		name           string
		result         string
		message        string
		expectedResult string
		filtered       bool
	}{
		{
			name:           "flaky fail becomes failed",
			result:         "skipped",
			message:        "FLAKY_FAIL: test-reason: assertion failed",
			expectedResult: "failed",
		},
		{
			name:           "flaky pass becomes flaky_pass",
			result:         "skipped",
			message:        "FLAKY_PASS: test-reason",
			expectedResult: "flaky_pass",
		},
		{
			name:     "regular skip is filtered",
			result:   "skipped",
			message:  "precondition not met",
			filtered: true,
		},
		{
			name:     "empty skip is filtered",
			result:   "skipped",
			message:  "",
			filtered: true,
		},
		{
			name:           "regular failure unchanged",
			result:         "failed",
			message:        "test failed",
			expectedResult: "failed",
		},
		{
			name:           "success unchanged",
			result:         "success",
			message:        "",
			expectedResult: "success",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, keep := mapFlakyStatus(tt.result, tt.message)
			if keep == tt.filtered {
				t.Errorf("mapFlakyStatus() filtered = %v, want %v", !keep, tt.filtered)
			}
			if !tt.filtered && got != tt.expectedResult {
				t.Errorf("mapFlakyStatus() result = %q, want %q", got, tt.expectedResult)
			}
		})
	}
}
