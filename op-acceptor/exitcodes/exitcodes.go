// Package exitcodes defines the standard exit codes used by op-acceptor.
package exitcodes

// Exit code constants used by op-acceptor
// These constants define the exit codes that the application uses to indicate
// various states when it exits:
//
// * Success (0): Used when all tests pass successfully
// * TestFailure (1): Used when one or more tests fail
// * RuntimeErr (2): Used for runtime errors such as panics, timeouts or other failures
const (
	Success     = 0 // All tests pass
	TestFailure = 1 // Test failures
	RuntimeErr  = 2 // Runtime errors or timeouts
)
