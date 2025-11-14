package logging

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCleanLogOutput(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "simple text without special characters",
			input:    "hello world",
			expected: "hello world",
		},
		{
			name:     "text with leading and trailing whitespace",
			input:    "  hello world  ",
			expected: "hello world",
		},
		{
			name:     "text with multiple spaces",
			input:    "hello    world",
			expected: "hello world",
		},
		{
			name:     "text with tabs",
			input:    "hello\t\tworld",
			expected: "hello world",
		},
		{
			name:     "text with newlines",
			input:    "hello\nworld",
			expected: "hello world",
		},
		{
			name:     "text with mixed whitespace",
			input:    "hello  \t\n  world",
			expected: "hello world",
		},
		{
			name:     "file:line prefix simple",
			input:    "test.go:123: error message",
			expected: "error message",
		},
		{
			name:     "file:line prefix with underscore",
			input:    "da_footprint_test.go:188: some log output",
			expected: "some log output",
		},
		{
			name:     "file:line prefix with leading whitespace",
			input:    "  test_file.go:456: message here",
			expected: "message here",
		},
		{
			name:     "file:line prefix with hyphen",
			input:    "my-file.go:99: content",
			expected: "content",
		},
		{
			name:     "ANSI color codes",
			input:    "\x1b[31mred text\x1b[0m",
			expected: "red text",
		},
		{
			name:     "ANSI codes with other formatting",
			input:    "\x1b[1;32mbold green\x1b[0m normal",
			expected: "bold green normal",
		},
		{
			name:     "combination: file prefix, ANSI codes, and whitespace",
			input:    "  test.go:42:  \x1b[31m  error:   failed\x1b[0m  ",
			expected: "error: failed",
		},
		{
			name:     "combination: multiple whitespace types",
			input:    "test.go:1: hello\t\t  world  \n  foo",
			expected: "hello world foo",
		},
		{
			name:     "text that looks like but isn't a file:line prefix",
			input:    "this is not file.go:123: a prefix because text comes before",
			expected: "this is not file.go:123: a prefix because text comes before",
		},
		{
			name:     "multiple lines with different prefixes",
			input:    "file1.go:10: first line\nfile2.go:20: second line",
			expected: "first line file2.go:20: second line",
		},
		{
			name:     "no file extension in prefix",
			input:    "file:123: should not match",
			expected: "file:123: should not match",
		},
		{
			name:     "wrong extension in prefix",
			input:    "file.txt:123: should not match",
			expected: "file.txt:123: should not match",
		},
		{
			name:     "real-world example with geth logger output",
			input:    "  da_test.go:188:   \x1b[36mINFO\x1b[0m [01-01|00:00:00.000]   Transaction   submitted    hash=0x123",
			expected: "INFO [01-01|00:00:00.000] Transaction submitted hash=0x123",
		},
		{
			name:     "multiple consecutive newlines",
			input:    "line1\n\n\nline2",
			expected: "line1 line2",
		},
		{
			name:     "only whitespace",
			input:    "   \t\n   ",
			expected: "",
		},
		{
			name:     "unicode characters preserved",
			input:    "hello 世界 🌍",
			expected: "hello 世界 🌍",
		},
		{
			name:     "file prefix at start preserves rest of content",
			input:    "test.go:1: key=value another=thing",
			expected: "key=value another=thing",
		},
		{
			name:     "real log from da_footprint_test",
			input:    "    da_footprint_test.go:188:             INFO [10-29|23:42:54.881] \"Block 0xbf9f2a47f13a639f439c3bb04070d3186019972b78d694e56e4e61647d4433a0:228357 has DA footprint (0) <= gasUsed (46218), trying next...\"",
			expected: "INFO [10-29|23:42:54.881] \"Block 0xbf9f2a47f13a639f439c3bb04070d3186019972b78d694e56e4e61647d4433a0:228357 has DA footprint (0) <= gasUsed (46218), trying next...\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CleanLogOutput(tt.input, true, true)
			assert.Equal(t, tt.expected, result, "CleanLogOutput(%q, true, true) = %q, want %q", tt.input, result, tt.expected)
		})
	}
}

func TestCleanLogOutputWithoutStrippingPrefixes(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "file:line prefix preserved",
			input:    "da_footprint_test.go:188: some log output",
			expected: "da_footprint_test.go:188: some log output",
		},
		{
			name:     "file:line prefix preserved with whitespace collapsed",
			input:    "test.go:1: hello\t\t  world  \n  foo",
			expected: "test.go:1: hello world foo",
		},
		{
			name:     "ANSI codes still stripped",
			input:    "test.go:42: \x1b[31mred text\x1b[0m",
			expected: "test.go:42: red text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CleanLogOutput(tt.input, false, true)
			assert.Equal(t, tt.expected, result, "CleanLogOutput(%q, false, true) = %q, want %q", tt.input, result, tt.expected)
		})
	}
}

func TestCleanLogOutputRegexPatterns(t *testing.T) {
	t.Run("fileLinePrefixRegex matches valid patterns", func(t *testing.T) {
		validPatterns := []string{
			"test.go:123: ",
			"  test.go:1: ",
			"my_file.go:999: ",
			"My-File.go:1: ",
			"FILE123.go:456: ",
		}
		for _, pattern := range validPatterns {
			assert.True(t, fileLinePrefixRegex.MatchString(pattern),
				"fileLinePrefixRegex should match %q", pattern)
		}
	})

	t.Run("fileLinePrefixRegex does not match invalid patterns", func(t *testing.T) {
		invalidPatterns := []string{
			"file.txt:123: ",       // not .go file
			"prefix file.go:123: ", // not at start
			"file.go:abc: ",        // not a number
			"file:123: ",           // missing .go
			".go:123: ",            // no filename
			"file.go:",             // missing line number
		}
		for _, pattern := range invalidPatterns {
			assert.False(t, fileLinePrefixRegex.MatchString(pattern),
				"fileLinePrefixRegex should not match %q", pattern)
		}
	})

	t.Run("multipleWhitespaceRegex collapses various whitespace", func(t *testing.T) {
		testCases := []struct {
			input    string
			expected string
		}{
			{"a  b", "a b"},
			{"a\tb", "a b"},
			{"a\nb", "a b"},
			{"a\r\nb", "a b"},
			{"a   \t\n   b", "a b"},
		}
		for _, tc := range testCases {
			result := multipleWhitespaceRegex.ReplaceAllString(tc.input, " ")
			assert.Equal(t, tc.expected, result)
		}
	})
}
