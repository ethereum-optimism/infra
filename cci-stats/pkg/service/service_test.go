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
