// Package runner provides components for executing Go tests in a structured, organized manner.
//
// The main components are:
//   - TestExecutor: Handles individual test execution and manages test processes
//   - OutputParser: Processes test output from go test -json format into structured results
//   - ResultCollector: Aggregates test results into hierarchical structures (gates/suites/tests)
//   - TestCoordinator: Orchestrates test execution workflows and manages test coordination
//   - JSONStore: Manages storage and retrieval of raw JSON test output
//
// These components work together to provide a clean, testable architecture for running
// acceptance tests with proper error handling, timeout management, and result aggregation.
package runner
