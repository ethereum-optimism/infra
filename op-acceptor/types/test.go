package types

import (
	"fmt"
	"strings"
	"time"
)

// TestStatus represents the possible states of a test execution
type TestStatus string

const (
	TestStatusPass  TestStatus = "pass"
	TestStatusFail  TestStatus = "fail"
	TestStatusSkip  TestStatus = "skip"
	TestStatusError TestStatus = "error"
)

// TestResult captures the outcome of a single test run
type TestResult struct {
	Metadata ValidatorMetadata
	Status   TestStatus
	Error    error                  // Changed from string to error
	Duration time.Duration          // Track test execution time
	SubTests map[string]*TestResult // Store individual test results when running a package
	Stdout   string                 // Capture stdout for failing tests
	TimedOut bool                   // Track if this test timed out

	// Hierarchy tracking
	Depth         int      // Nesting depth (0=top-level, 1=first subtest, etc.)
	HierarchyPath []string // Full path from root to this test (e.g., ["TestParent", "SubTest1", "SubSubTest"])
	IsSubTest     bool     // Whether this is a subtest (derived from Depth > 0)
}

// TestConfig represents a test configuration
type TestConfig struct {
	Name    string         `yaml:"name,omitempty"`
	Package string         `yaml:"package"`
	RunAll  bool           `yaml:"run_all,omitempty"`
	Timeout *time.Duration `yaml:"timeout,omitempty"`
}

// GetTestDisplayName returns a formatted display name for a test based on its name and metadata
// If the test name is empty but a package is specified, it will return the package name in a readable format
func GetTestDisplayName(testName string, metadata ValidatorMetadata) string {
	displayName := testName
	if displayName == "" && metadata.Package != "" {
		// For package names, use a shortened version to avoid wrapping
		pkgParts := strings.Split(metadata.Package, "/")
		if len(pkgParts) > 0 {
			displayName = pkgParts[len(pkgParts)-1] + " (package)"
		} else {
			displayName = metadata.Package
		}
	}
	return displayName
}

// SetHierarchyInfo sets the hierarchy information for a test result
// It validates the input and ensures consistency between depth and path
func (tr *TestResult) SetHierarchyInfo(depth int, path []string) error {
	// Validate input
	if err := ValidateHierarchyPath(path); err != nil {
		return fmt.Errorf("invalid hierarchy path: %w", err)
	}

	expectedDepth := CalculateDepthFromPath(path)
	if depth != expectedDepth {
		return fmt.Errorf("depth %d does not match path length (expected %d for path %v)", depth, expectedDepth, path)
	}

	tr.Depth = depth
	tr.HierarchyPath = make([]string, len(path))
	copy(tr.HierarchyPath, path)
	tr.IsSubTest = depth > 0

	return nil
}

// SetHierarchyInfoUnsafe sets the hierarchy information without validation
// Use this only when you're certain the input is valid (e.g., from trusted sources)
func (tr *TestResult) SetHierarchyInfoUnsafe(depth int, path []string) {
	tr.Depth = depth
	tr.HierarchyPath = make([]string, len(path))
	copy(tr.HierarchyPath, path)
	tr.IsSubTest = depth > 0
}

// SetHierarchyFromTestName sets the hierarchy information by parsing a test name
// This is a convenience method for the most common use case
func (tr *TestResult) SetHierarchyFromTestName(testName string) {
	depth, path := ParseTestNameHierarchy(testName)
	tr.SetHierarchyInfoUnsafe(depth, path)
}

// GetParentPath returns the hierarchy path of the parent test
func (tr *TestResult) GetParentPath() []string {
	if len(tr.HierarchyPath) <= 1 {
		return nil
	}
	return tr.HierarchyPath[:len(tr.HierarchyPath)-1]
}

// GetParentName returns the name of the immediate parent test
func (tr *TestResult) GetParentName() string {
	if len(tr.HierarchyPath) <= 1 {
		return ""
	}
	return tr.HierarchyPath[len(tr.HierarchyPath)-2]
}

// GetFullTestPath returns the full hierarchical path as a string
func (tr *TestResult) GetFullTestPath() string {
	return strings.Join(tr.HierarchyPath, "/")
}

// ParseTestNameHierarchy parses a Go test name and extracts hierarchy information
// Handles names like "TestParent/SubTest1/SubSubTest2"
// Returns depth (0=top-level, 1=first subtest, etc.) and the full hierarchy path
func ParseTestNameHierarchy(testName string) (depth int, path []string) {
	if testName == "" {
		return 0, []string{}
	}

	path = strings.Split(testName, "/")
	// Clean up any empty path elements
	cleanPath := make([]string, 0, len(path))
	for _, element := range path {
		if element != "" {
			cleanPath = append(cleanPath, element)
		}
	}

	if len(cleanPath) == 0 {
		return 0, []string{}
	}

	depth = len(cleanPath) - 1
	return depth, cleanPath
}

// BuildHierarchyPath creates a hierarchy path from test names
// This is useful when constructing test results programmatically
func BuildHierarchyPath(testNames ...string) []string {
	path := make([]string, 0, len(testNames))
	for _, name := range testNames {
		if name != "" {
			path = append(path, name)
		}
	}
	return path
}

// ValidateHierarchyPath checks if a hierarchy path is valid
// Returns an error if the path is invalid
func ValidateHierarchyPath(path []string) error {
	if len(path) == 0 {
		return fmt.Errorf("hierarchy path cannot be empty")
	}

	for i, element := range path {
		if element == "" {
			return fmt.Errorf("hierarchy path element at index %d cannot be empty", i)
		}
		if strings.Contains(element, "/") {
			return fmt.Errorf("hierarchy path element '%s' at index %d cannot contain '/' character", element, i)
		}
	}

	return nil
}

// CalculateDepthFromPath calculates the depth from a hierarchy path
// Depth is always len(path) - 1 (0 for top-level tests)
func CalculateDepthFromPath(path []string) int {
	if len(path) <= 1 {
		return 0
	}
	return len(path) - 1
}
