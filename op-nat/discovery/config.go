package discovery

import (
	"github.com/ethereum-optimism/infra/op-nat/types"
)

type Config struct {
	ConfigPath   string
	ConfigFile   string // For backward compatibility
	ValidatorDir string
}

type Override struct {
	Gates    map[string]*types.GateConfig
	Packages map[string]*PackageConfig
}

type PackageConfig struct {
	IncludeAll bool
	Exclude    []string
	Timeout    string
}

type SuiteConfig struct {
	Description string
}

type TestConfig struct {
	Description string
}
