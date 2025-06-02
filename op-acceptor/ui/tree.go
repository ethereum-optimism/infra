package ui

import (
	"strings"
	"unicode/utf8"
)

// Tree hierarchy symbols using box drawing characters
const (
	// Basic tree connectors
	TreeBranch     = "├── " // Branch connector (tee right + horizontal line + space)
	TreeLastBranch = "└── " // Last branch connector (bottom left corner + horizontal line + space)
	TreeVertical   = "│"    // Vertical line for continuing hierarchy

	// Spacing patterns for different indentation levels
	TreeContinue   = "│   "     // Vertical line + 3 spaces (parent has more siblings)
	TreeIndent     = "    "     // 4 spaces (parent was last, no vertical line needed)
	TreeDeepIndent = "│       " // Vertical line + 7 spaces (deeper nesting)
	TreeMultiLevel = "│   │   " // Multiple vertical lines for complex hierarchy

	// Box drawing characters for borders/containers
	BoxTopLeft     = "┌"
	BoxTopRight    = "┐"
	BoxBottomLeft  = "└"
	BoxBottomRight = "┘"
	BoxVertical    = "│"
	BoxHorizontal  = "─"
	BoxTeeRight    = "├"
	BoxTeeLeft     = "┤"

	// Composite patterns for common use cases
	TreeSubTestBranch     = "│       ├──"         // For subtests under tests
	TreeSubTestLastBranch = "│       └──"         // For last subtest under tests
	TreeSubTestError      = "│       │       └──" // For errors under subtests

	// Suite-specific patterns (with additional indentation)
	SuiteBranch        = "    ├── "                // Branch under suite
	SuiteLastBranch    = "    └── "                // Last branch under suite
	SuiteContinue      = "    │   "                // Continue line under suite
	SuiteSubTestBranch = "    │       ├──"         // Subtest under suite test
	SuiteSubTestLast   = "    │       └──"         // Last subtest under suite test
	SuiteSubTestError  = "    │       │       └──" // Error under suite subtest
)

// TreePrefixBuilder helps build consistent tree prefixes based on hierarchy depth and position
type TreePrefixBuilder struct{}

// BuildPrefix generates a tree prefix based on depth, position, and parent positions
func (TreePrefixBuilder) BuildPrefix(depth int, isLast bool, parentIsLast []bool) string {
	if depth == 0 {
		return ""
	}

	var prefix string

	// Build prefix based on parent positions
	for i := 0; i < depth-1; i++ {
		if i < len(parentIsLast) && parentIsLast[i] {
			prefix += TreeIndent // No vertical line if parent was last
		} else {
			prefix += TreeContinue // Vertical line if parent has siblings below
		}
	}

	// Add the current level connector
	if isLast {
		prefix += TreeLastBranch
	} else {
		prefix += TreeBranch
	}

	return prefix
}

// Common convenience functions for building tree displays
func BuildTreePrefix(depth int, isLast bool, parentIsLast []bool) string {
	builder := TreePrefixBuilder{}
	return builder.BuildPrefix(depth, isLast, parentIsLast)
}

// BuildBoxHeader creates a box header with the given title and width
func BuildBoxHeader(title string, width int) string {
	titleRuneCount := utf8.RuneCountInString(title)
	if width < titleRuneCount+4 { // minimum space for borders and padding
		width = titleRuneCount + 4
	}

	titleLen := utf8.RuneCountInString(title)
	contentWidth := width - 4 // account for "│ " and " │"
	padding := contentWidth - titleLen

	header := BoxTopLeft + repeatString(BoxHorizontal, width-2) + BoxTopRight + "\n"
	header += BoxVertical + " " + title + repeatString(" ", padding+1) + BoxVertical + "\n"
	header += BoxTeeRight + repeatString(BoxHorizontal, width-2) + BoxTeeLeft + "\n"

	return header
}

// BuildBoxFooter creates a box footer with the given width
func BuildBoxFooter(width int) string {
	return BoxBottomLeft + repeatString(BoxHorizontal, width-2) + BoxBottomRight + "\n"
}

// BuildBoxLine creates a content line within a box
func BuildBoxLine(content string, width int) string {
	contentLen := utf8.RuneCountInString(content)
	maxContentLen := width - 4 // account for "│ " and " │"

	if contentLen > maxContentLen { // truncate if too long
		// Truncate by runes, not bytes
		runes := []rune(content)
		content = string(runes[:maxContentLen-3]) + "..."
		contentLen = maxContentLen
	}

	padding := maxContentLen - contentLen
	return BoxVertical + " " + content + repeatString(" ", padding+1) + BoxVertical + "\n"
}

// repeatString repeats a string n times
func repeatString(s string, n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat(s, n)
}
