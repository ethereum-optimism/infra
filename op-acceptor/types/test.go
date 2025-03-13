package types

import (
	"strings"
	"time"
)

// TestStatus represents the possible states of a test execution
type TestStatus string

const (
	TestStatusPass TestStatus = "pass"
	TestStatusFail TestStatus = "fail"
	TestStatusSkip TestStatus = "skip"
)

// TestResult captures the outcome of a single test run
type TestResult struct {
	Metadata ValidatorMetadata
	Status   TestStatus
	Error    error                  // Changed from string to error
	Duration time.Duration          // Track test execution time
	SubTests map[string]*TestResult // Store individual test results when running a package
}

// TestConfig represents a test configuration
type TestConfig struct {
	Name    string `yaml:"name,omitempty"`
	Package string `yaml:"package"`
	RunAll  bool   `yaml:"run_all,omitempty"`
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
