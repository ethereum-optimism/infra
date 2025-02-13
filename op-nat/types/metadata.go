// Package types contains shared types used across the nat testing framework
package types

// Validator types
const (
	ValidatorTypeTest  = "test"
	ValidatorTypeSuite = "suite"
	ValidatorTypeGate  = "gate"
)

// Test status types
const (
	TestStatusPass = "pass"
	TestStatusFail = "fail"
	TestStatusSkip = "skip"
)
