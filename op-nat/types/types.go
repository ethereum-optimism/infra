package types

import (
	"fmt"
	"time"
)

// ValidatorConfig represents the complete test configuration
type ValidatorConfig struct {
	Gates    []GateConfig `yaml:"gates"`
	Metadata struct {
		Timeouts map[string]time.Duration `yaml:"timeouts"`
	} `yaml:"metadata"`
}

// GateConfig represents a collection of tests and suites
type GateConfig struct {
	ID          string                 `yaml:"id"`
	Description string                 `yaml:"description"`
	Inherits    []string               `yaml:"inherits,omitempty"`
	Tests       []TestConfig           `yaml:"tests,omitempty"`
	Suites      map[string]SuiteConfig `yaml:"suites,omitempty"`
}

// ResolveInherited processes inheritance relationships between gates by merging
// test configurations from parent gates into the current gate.
//
// The function implements an inheritance mechanism where a gate can inherit tests
// and suites from other gates specified in its 'Inherits' field. The inheritance
// rules are:
// - Suites: Parent suites are only inherited if they don't exist in the child gate
// - Tests: All parent tests are appended to the child gate's tests
func (g *GateConfig) ResolveInherited(gates map[string]GateConfig) error {
	if len(g.Inherits) == 0 {
		return nil
	}

	// Create new collections to store the merged configurations
	// This prevents modifying maps while iterating over them
	mergedSuites := make(map[string]SuiteConfig)
	var mergedTests []TestConfig

	// First copy the current gate's own configurations
	// This ensures the current gate's configs take precedence
	for k, v := range g.Suites {
		mergedSuites[k] = v
	}
	mergedTests = append(mergedTests, g.Tests...)

	// Process each parent gate specified in the Inherits field
	for _, inheritFrom := range g.Inherits {
		parent, ok := gates[inheritFrom]
		if !ok {
			return fmt.Errorf("gate %q inherits from non-existent gate %q", g.ID, inheritFrom)
		}

		// Merge suites from parent, but only if they don't already exist
		// This implements the "child overrides parent" behavior for suites
		for k, v := range parent.Suites {
			if _, exists := mergedSuites[k]; !exists {
				mergedSuites[k] = v
			}
		}

		// Append all tests from the parent
		// Unlike suites, all parent tests are included
		mergedTests = append(mergedTests, parent.Tests...)
	}

	// Update the gate's configuration with the merged results
	g.Suites = mergedSuites
	g.Tests = mergedTests
	return nil
}

// SuiteConfig represents a collection of related tests
type SuiteConfig struct {
	Description string       `yaml:"description"`
	Tests       []TestConfig `yaml:"tests"`
}

// TestConfig represents a test configuration
type TestConfig struct {
	Name    string `yaml:"name,omitempty"`
	Package string `yaml:"package"`
	RunAll  bool   `yaml:"run_all,omitempty"`
}

type ValidatorMetadata struct {
	ID       string
	Type     string
	Gate     string
	Suite    string
	FuncName string
	Package  string
	Timeout  string
	RunAll   bool
}
