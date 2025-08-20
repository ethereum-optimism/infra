package reporting

import (
	"testing"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
)

func TestGetStatusString(t *testing.T) {
	tests := []struct {
		name     string
		status   types.TestStatus
		expected string
	}{
		{
			name:     "pass",
			status:   types.TestStatusPass,
			expected: "pass",
		},
		{
			name:     "fail",
			status:   types.TestStatusFail,
			expected: "fail",
		},
		{
			name:     "skip",
			status:   types.TestStatusSkip,
			expected: "skip",
		},
		{
			name:     "error",
			status:   types.TestStatusError,
			expected: "error",
		},
		{
			name:     "unknown",
			status:   types.TestStatus("invalid"),
			expected: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getStatusString(tt.status)
			if result != tt.expected {
				t.Errorf("getStatusString(%v) = %q, expected %q", tt.status, result, tt.expected)
			}
		})
	}
}

func TestTreeTableFormatterGetStatusString(t *testing.T) {
	formatter := NewTreeTableFormatter("test", true, false)

	tests := []struct {
		name     string
		status   types.TestStatus
		expected string
	}{
		{
			name:     "pass",
			status:   types.TestStatusPass,
			expected: "pass",
		},
		{
			name:     "fail",
			status:   types.TestStatusFail,
			expected: "fail",
		},
		{
			name:     "skip",
			status:   types.TestStatusSkip,
			expected: "skip",
		},
		{
			name:     "error",
			status:   types.TestStatusError,
			expected: "error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatter.GetStatusString(tt.status)
			if result != tt.expected {
				t.Errorf("TreeTableFormatter.GetStatusString(%v) = %q, expected %q", tt.status, result, tt.expected)
			}
		})
	}
}
