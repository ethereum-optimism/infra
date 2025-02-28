package types

// SuiteConfig represents a collection of related tests
type SuiteConfig struct {
	Description string       `yaml:"description"`
	Tests       []TestConfig `yaml:"tests"`
}
