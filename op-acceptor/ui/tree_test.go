package ui

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTreeConstants(t *testing.T) {
	tests := []struct {
		name     string
		constant string
		expected string
	}{
		{"TreeBranch", TreeBranch, "├── "},
		{"TreeLastBranch", TreeLastBranch, "└── "},
		{"TreeVertical", TreeVertical, "│"},
		{"TreeContinue", TreeContinue, "│   "},
		{"TreeIndent", TreeIndent, "    "},
		{"TreeDeepIndent", TreeDeepIndent, "│       "},
		{"TreeMultiLevel", TreeMultiLevel, "│   │   "},
		{"BoxTopLeft", BoxTopLeft, "┌"},
		{"BoxTopRight", BoxTopRight, "┐"},
		{"BoxBottomLeft", BoxBottomLeft, "└"},
		{"BoxBottomRight", BoxBottomRight, "┘"},
		{"BoxVertical", BoxVertical, "│"},
		{"BoxHorizontal", BoxHorizontal, "─"},
		{"BoxTeeRight", BoxTeeRight, "├"},
		{"BoxTeeLeft", BoxTeeLeft, "┤"},
		{"TreeSubTestBranch", TreeSubTestBranch, "│       ├──"},
		{"TreeSubTestLastBranch", TreeSubTestLastBranch, "│       └──"},
		{"TreeSubTestError", TreeSubTestError, "│       │       └──"},
		{"SuiteBranch", SuiteBranch, "    ├── "},
		{"SuiteLastBranch", SuiteLastBranch, "    └── "},
		{"SuiteContinue", SuiteContinue, "    │   "},
		{"SuiteSubTestBranch", SuiteSubTestBranch, "    │       ├──"},
		{"SuiteSubTestLast", SuiteSubTestLast, "    │       └──"},
		{"SuiteSubTestError", SuiteSubTestError, "    │       │       └──"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.constant != tt.expected {
				t.Errorf("Constant %s = %q, want %q", tt.name, tt.constant, tt.expected)
			}
		})
	}
}

func TestTreePrefixBuilder_BuildPrefix(t *testing.T) {
	builder := TreePrefixBuilder{}

	tests := []struct {
		name         string
		depth        int
		isLast       bool
		parentIsLast []bool
		expected     string
	}{
		{
			name:         "depth 0",
			depth:        0,
			isLast:       false,
			parentIsLast: []bool{},
			expected:     "",
		},
		{
			name:         "depth 1, not last",
			depth:        1,
			isLast:       false,
			parentIsLast: []bool{},
			expected:     "├── ",
		},
		{
			name:         "depth 1, is last",
			depth:        1,
			isLast:       true,
			parentIsLast: []bool{},
			expected:     "└── ",
		},
		{
			name:         "depth 2, parent not last, not last",
			depth:        2,
			isLast:       false,
			parentIsLast: []bool{false},
			expected:     "│   ├── ",
		},
		{
			name:         "depth 2, parent was last, is last",
			depth:        2,
			isLast:       true,
			parentIsLast: []bool{true},
			expected:     "    └── ",
		},
		{
			name:         "depth 3, complex hierarchy",
			depth:        3,
			isLast:       false,
			parentIsLast: []bool{false, true},
			expected:     "│       ├── ",
		},
		{
			name:         "depth 4, very deep hierarchy",
			depth:        4,
			isLast:       true,
			parentIsLast: []bool{false, false, false},
			expected:     "│   │   │   └── ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := builder.BuildPrefix(tt.depth, tt.isLast, tt.parentIsLast)
			if result != tt.expected {
				t.Errorf("BuildPrefix(%d, %v, %v) = %q, want %q",
					tt.depth, tt.isLast, tt.parentIsLast, result, tt.expected)
			}
		})
	}
}

func TestBuildTreePrefix(t *testing.T) {
	// Test that the convenience function works the same as the builder
	depth := 2
	isLast := true
	parentIsLast := []bool{false}

	builder := TreePrefixBuilder{}
	builderResult := builder.BuildPrefix(depth, isLast, parentIsLast)
	convenienceResult := BuildTreePrefix(depth, isLast, parentIsLast)

	if builderResult != convenienceResult {
		t.Errorf("BuildTreePrefix should match TreePrefixBuilder.BuildPrefix, got %q vs %q",
			convenienceResult, builderResult)
	}
}

func TestBuildBoxHeader(t *testing.T) {
	tests := []struct {
		name     string
		title    string
		width    int
		expected string
	}{
		{
			name:     "simple header",
			title:    "TEST",
			width:    10,
			expected: "┌────────┐\n│ TEST   │\n├────────┤\n",
		},
		{
			name:     "minimum width adjustment",
			title:    "LONG TITLE",
			width:    5, // Too small, should be adjusted
			expected: "┌────────────┐\n│ LONG TITLE │\n├────────────┤\n",
		},
		{
			name:     "exact fit",
			title:    "FIT",
			width:    7, // Exactly fits "│ FIT │"
			expected: "┌─────┐\n│ FIT │\n├─────┤\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildBoxHeader(tt.title, tt.width)
			if result != tt.expected {
				t.Errorf("BuildBoxHeader(%q, %d) =\n%q\nwant:\n%q",
					tt.title, tt.width, result, tt.expected)
			}
		})
	}
}

func TestBuildBoxFooter(t *testing.T) {
	tests := []struct {
		name     string
		width    int
		expected string
	}{
		{
			name:     "width 10",
			width:    10,
			expected: "└────────┘\n",
		},
		{
			name:     "width 5",
			width:    5,
			expected: "└───┘\n",
		},
		{
			name:     "width 3 (minimum)",
			width:    3,
			expected: "└─┘\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildBoxFooter(tt.width)
			if result != tt.expected {
				t.Errorf("BuildBoxFooter(%d) = %q, want %q", tt.width, result, tt.expected)
			}
		})
	}
}

func TestBuildBoxLine(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		width    int
		expected string
	}{
		{
			name:     "short content",
			content:  "TEST",
			width:    10,
			expected: "│ TEST   │\n",
		},
		{
			name:     "exact fit content",
			content:  "EXACT",
			width:    9, // "│ EXACT │" = 9 chars
			expected: "│ EXACT │\n",
		},
		{
			name:     "long content gets truncated",
			content:  "VERY LONG CONTENT THAT EXCEEDS WIDTH",
			width:    15,
			expected: "│ VERY LON... │\n",
		},
		{
			name:     "empty content",
			content:  "",
			width:    8,
			expected: "│      │\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildBoxLine(tt.content, tt.width)
			if result != tt.expected {
				t.Errorf("BuildBoxLine(%q, %d) = %q, want %q",
					tt.content, tt.width, result, tt.expected)
			}
		})
	}
}

func TestRepeatString(t *testing.T) {
	tests := []struct {
		name     string
		s        string
		n        int
		expected string
	}{
		{
			name:     "repeat dash 5 times",
			s:        "─",
			n:        5,
			expected: "─────",
		},
		{
			name:     "repeat nothing",
			s:        "x",
			n:        0,
			expected: "",
		},
		{
			name:     "negative count",
			s:        "y",
			n:        -1,
			expected: "",
		},
		{
			name:     "repeat multi-char string",
			s:        "ab",
			n:        3,
			expected: "ababab",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := repeatString(tt.s, tt.n)
			if result != tt.expected {
				t.Errorf("repeatString(%q, %d) = %q, want %q", tt.s, tt.n, result, tt.expected)
			}
		})
	}
}

func TestCompleteBoxExample(t *testing.T) {
	// Test building a complete box with header, content lines, and footer
	width := 20
	title := "TEST RESULTS"

	box := BuildBoxHeader(title, width)
	box += BuildBoxLine("Status: PASS", width)
	box += BuildBoxLine("Duration: 1.5s", width)
	box += BuildBoxFooter(width)

	lines := strings.Split(strings.TrimRight(box, "\n"), "\n")

	// Verify all lines are the correct width (using rune count for Unicode characters)
	for i, line := range lines {
		runeCount := utf8.RuneCountInString(line)
		if runeCount != width {
			t.Errorf("Line %d has width %d, expected %d: %q (byte length: %d)", i, runeCount, width, line, len(line))
		}
	}

	// Verify structure
	if len(lines) != 6 { // header(3) + content(2) + footer(1) = 6 total lines
		t.Errorf("Expected 6 lines, got %d", len(lines))
	}

	// Verify it starts with top border and ends with bottom border
	if !strings.HasPrefix(lines[0], "┌") || !strings.HasSuffix(lines[0], "┐") {
		t.Errorf("First line should be top border: %q", lines[0])
	}

	lastLine := lines[len(lines)-1]
	if !strings.HasPrefix(lastLine, "└") || !strings.HasSuffix(lastLine, "┘") {
		t.Errorf("Last line should be bottom border: %q", lastLine)
	}
}
