package types

import "time"

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
	Status   TestStatus // Must be one of TestStatusPass, TestStatusFail, or TestStatusSkip
	Error    string
	Duration time.Duration // Track test execution time
}

// TestConfig represents a test configuration
type TestConfig struct {
	Name    string `yaml:"name,omitempty"`
	Package string `yaml:"package"`
	RunAll  bool   `yaml:"run_all,omitempty"`
}
