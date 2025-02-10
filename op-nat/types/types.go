package types

import (
	"fmt"
	"time"
)

// SourceConfig defines where to find tests
type SourceConfig struct {
	Location   string
	Version    string
	ConfigPath string
}

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

func (g *GateConfig) ResolveInherited(gates map[string]GateConfig) error {
	if len(g.Inherits) == 0 {
		return nil
	}

	// Create new maps to avoid modifying during iteration
	mergedSuites := make(map[string]SuiteConfig)
	var mergedTests []TestConfig

	// First copy our own config
	for k, v := range g.Suites {
		mergedSuites[k] = v
	}
	mergedTests = append(mergedTests, g.Tests...)

	// Then merge in inherited configs
	for _, inheritFrom := range g.Inherits {
		parent, ok := gates[inheritFrom]
		if !ok {
			return fmt.Errorf("gate %q inherits from non-existent gate %q", g.ID, inheritFrom)
		}

		// Merge suites (parent suites don't override our own)
		for k, v := range parent.Suites {
			if _, exists := mergedSuites[k]; !exists {
				mergedSuites[k] = v
			}
		}

		// Append tests
		mergedTests = append(mergedTests, parent.Tests...)
	}

	g.Suites = mergedSuites
	g.Tests = mergedTests
	return nil
}

// SuiteConfig represents a collection of related tests
type SuiteConfig struct {
	Description string       `yaml:"description"`
	Tests       []TestConfig `yaml:"tests"`
}

// TestConfig represents a single test or group of tests
type TestConfig struct {
	Name     string            // The name of the test
	Package  string            // The package containing the test
	Funcs    []string          `yaml:"funcs,omitempty"`
	All      bool              `yaml:"all,omitempty"`
	Exclude  []string          `yaml:"exclude,omitempty"`
	Prefix   string            `yaml:"prefix,omitempty"`
	Alias    string            `yaml:"alias,omitempty"`
	Timeouts map[string]string `yaml:"timeouts,omitempty"`
}

// TestSource represents a source of tests (local or remote)
type TestSource struct {
	Location string
	Version  string
	Config   *ValidatorConfig
}

type ValidatorMetadata struct {
	ID       string
	Type     string
	Gate     string
	Suite    string
	FuncName string
	Package  string
	Timeout  string
}
