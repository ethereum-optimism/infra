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
		name     string
		result   string
		message  string
		expected string
	}{
		{
			name:     "flaky fail becomes failed",
			result:   "skipped",
			message:  "FLAKY_FAIL: test-reason: assertion failed",
			expected: "failed",
		},
		{
			name:     "flaky pass becomes flaky_pass",
			result:   "skipped",
			message:  "FLAKY_PASS: test-reason",
			expected: "flaky_pass",
		},
		{
			name:     "regular skip stays skipped",
			result:   "skipped",
			message:  "precondition not met",
			expected: "skipped",
		},
		{
			name:     "empty skip stays skipped",
			result:   "skipped",
			message:  "",
			expected: "skipped",
		},
		{
			name:     "regular failure unchanged",
			result:   "failed",
			message:  "test failed",
			expected: "failed",
		},
		{
			name:     "success unchanged",
			result:   "success",
			message:  "",
			expected: "success",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapFlakyStatus(tt.result, tt.message)
			if got != tt.expected {
				t.Errorf("mapFlakyStatus(%q, %q) = %q, want %q", tt.result, tt.message, got, tt.expected)
			}
		})
	}
}
