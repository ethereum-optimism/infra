package types

import "fmt"

// GateConfig represents a collection of tests and suites
type GateConfig struct {
	ID          string                 `yaml:"id"`
	Description string                 `yaml:"description"`
	Inherits    []string               `yaml:"inherits,omitempty"`
	Tests       []TestConfig           `yaml:"tests,omitempty"`
	Suites      map[string]SuiteConfig `yaml:"suites,omitempty"`
}

// ResolveInherited processes inheritance relationships between gates by merging
// test configurations from parent gates into the current gate recursively.
//
// The function implements an inheritance mechanism where a gate can inherit tests
// and suites from other gates specified in its 'Inherits' field. The inheritance
// is recursive, so if gate C inherits from B, and B inherits from A, then C will
// get configurations from both B and A. The inheritance rules are:
// - Suites: Parent suites are only inherited if they don't exist in the child gate
// - Tests: All parent tests are appended to the child gate's tests
// - Inheritance is depth-first: more distant ancestors are processed first
func (g *GateConfig) ResolveInherited(gates map[string]GateConfig) error {
	// Track processed gates to prevent infinite recursion
	processed := make(map[string]bool)
	return g.resolveInheritedRecursive(gates, processed)
}

func (g *GateConfig) resolveInheritedRecursive(gates map[string]GateConfig, processed map[string]bool) error {
	if len(g.Inherits) == 0 {
		return nil
	}

	// Create new collections to store the merged configurations
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
		// Check for circular dependencies
		if processed[inheritFrom] {
			return fmt.Errorf("circular inheritance detected for gate %q", inheritFrom)
		}

		parent, ok := gates[inheritFrom]
		if !ok {
			return fmt.Errorf("gate %q inherits from non-existent gate %q", g.ID, inheritFrom)
		}

		// Mark this gate as being processed
		processed[inheritFrom] = true

		// Recursively resolve parent's inheritance first
		if err := parent.resolveInheritedRecursive(gates, processed); err != nil {
			return fmt.Errorf("resolving inheritance for parent gate %q: %w", inheritFrom, err)
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

		// Unmark this gate after processing
		processed[inheritFrom] = false
	}

	// Update the gate's configuration with the merged results
	g.Suites = mergedSuites
	g.Tests = mergedTests
	return nil
}
