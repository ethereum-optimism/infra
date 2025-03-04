// Package types contains shared types used across the nat testing framework
package types

import "time"

// ValidatorType represents the type of validator
type ValidatorType string

// String implements the Stringer interface for ValidatorType
func (v ValidatorType) String() string {
	return string(v)
}

// ValidatorType enum values
const (
	ValidatorTypeTest  ValidatorType = "test"
	ValidatorTypeSuite ValidatorType = "suite"
	ValidatorTypeGate  ValidatorType = "gate"
)

// ValidatorConfig represents the complete test configuration
type ValidatorConfig struct {
	Gates    []GateConfig `yaml:"gates"`
	Metadata struct {
		Timeouts map[string]time.Duration `yaml:"timeouts"`
	} `yaml:"metadata"`
}

type ValidatorMetadata struct {
	ID       string
	Type     ValidatorType
	Gate     string
	Suite    string
	FuncName string
	Package  string
	Timeout  string
	RunAll   bool
}
