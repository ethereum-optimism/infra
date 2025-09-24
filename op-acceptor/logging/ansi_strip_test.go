package logging

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStripANSIEscapeSequences(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "No ANSI sequences",
			input:    "Simple text without colors",
			expected: "Simple text without colors",
		},
		{
			name:     "Basic color sequence",
			input:    "\x1b[32mGreen text\x1b[0m",
			expected: "Green text",
		},
		{
			name:     "Multiple color sequences",
			input:    "\x1b[32mINFO \x1b[0m[09-23|13:15:01.028] Started test \x1b[32mTest\x1b[0m=TestExample",
			expected: "INFO [09-23|13:15:01.028] Started test Test=TestExample",
		},
		{
			name:     "Complex test output",
			input:    "    rpc_connectivity_test.go:25:             \x1b[32mINFO \x1b[0m[09-23|13:15:01.028] \"\\x1b[32mINFO \\x1b[0m[09-23|13:15:01.028] Started L2 RPC connectivity test         \\x1b[32mTest\\x1b[0m=TestRPCConnectivity\"",
			expected: "    rpc_connectivity_test.go:25:             INFO [09-23|13:15:01.028] \"\\x1b[32mINFO \\x1b[0m[09-23|13:15:01.028] Started L2 RPC connectivity test         \\x1b[32mTest\\x1b[0m=TestRPCConnectivity\"",
		},
		{
			name:     "Bold and color sequences",
			input:    "\x1b[1m\x1b[32mBold Green\x1b[0m normal text",
			expected: "Bold Green normal text",
		},
		{
			name:     "Multiple parameters in escape sequence",
			input:    "\x1b[1;32mBold Green\x1b[0m text",
			expected: "Bold Green text",
		},
		{
			name:     "Empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "Only ANSI sequences",
			input:    "\x1b[32m\x1b[0m\x1b[1m\x1b[0m",
			expected: "",
		},
		{
			name:     "Nested quotes with ANSI",
			input:    "\"\\x1b[32mINFO \\x1b[0m\" message",
			expected: "\"\\x1b[32mINFO \\x1b[0m\" message",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := stripANSIEscapeSequences(tc.input)
			assert.Equal(t, tc.expected, actual)
		})
	}
}