// Package types contains shared types used across the nat testing framework
package types

// ValidatorType represents the type of validator (test, suite, or gate)
type ValidatorType string

const (
	ValidatorTypeTest  ValidatorType = "test"
	ValidatorTypeSuite ValidatorType = "suite"
	ValidatorTypeGate  ValidatorType = "gate"
)

// ValidatorMetadata contains metadata about a discovered validator
type ValidatorMetadata struct {
	ID       string
	Type     ValidatorType
	Gate     string
	Suite    string
	FuncName string
}
