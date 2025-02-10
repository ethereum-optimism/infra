package registry

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/ethereum-optimism/infra/op-nat/types"
	"github.com/ethereum/go-ethereum/log"
	"gopkg.in/yaml.v3"
)

// Registry manages test sources and their configurations
type Registry struct {
	config  Config
	sources map[string]*types.TestSource
	mu      sync.RWMutex
}

// Config contains registry configuration
type Config struct {
	Source  types.SourceConfig
	Gate    string
	WorkDir string
}

// NewRegistry creates a new registry instance
func NewRegistry(cfg Config) (*Registry, error) {
	// Load validator config
	configPath := filepath.Join(cfg.Source.Location, cfg.Source.ConfigPath)
	if _, err := os.Stat(configPath); err != nil {
		return nil, fmt.Errorf("validator config not found at %s: %w", cfg.Source.ConfigPath, err)
	}

	// Add debug logging for path resolution
	log.Debug("Creating registry with config",
		"source.Location", cfg.Source.Location,
		"source.ConfigPath", cfg.Source.ConfigPath,
		"workDir", cfg.WorkDir)

	// Create registry instance
	r := &Registry{
		config:  cfg,
		sources: make(map[string]*types.TestSource),
	}

	// Load the source immediately
	if err := r.loadSource(cfg.Source); err != nil {
		return nil, fmt.Errorf("failed to load source: %w", err)
	}

	return r, nil
}

// loadSource loads a test source and its configuration
func (r *Registry) loadSource(cfg types.SourceConfig) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Use the full path when reading the config file
	configPath := filepath.Join(cfg.Location, cfg.ConfigPath)

	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read config file at path %s: %w", configPath, err)
	}

	var validatorConfig types.ValidatorConfig
	if err := yaml.Unmarshal(data, &validatorConfig); err != nil {
		return fmt.Errorf("failed to parse config file: %w", err)
	}

	// Store the source
	r.sources[cfg.Location] = &types.TestSource{
		Location: cfg.Location,
		Version:  cfg.Version,
		Config:   &validatorConfig,
	}

	// Resolve gate inheritance
	if validatorConfig.Gates != nil {
		gateMap := make(map[string]types.GateConfig)
		for _, gate := range validatorConfig.Gates {
			gateMap[gate.ID] = gate
		}

		for i := range validatorConfig.Gates {
			if err := validatorConfig.Gates[i].ResolveInherited(gateMap); err != nil {
				return fmt.Errorf("invalid gate inheritance: %w", err)
			}
		}
	}

	return nil
}

// Validate checks that all configured tests exist and are valid
func (r *Registry) Validate() error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, source := range r.sources {
		if source.Config == nil {
			continue
		}

		// Check for circular gate inheritance
		gateMap := make(map[string]types.GateConfig)
		for _, gate := range source.Config.Gates {
			gateMap[gate.ID] = gate
		}

		for _, gate := range source.Config.Gates {
			if err := gate.ResolveInherited(gateMap); err != nil {
				return fmt.Errorf("invalid gate inheritance: %w", err)
			}
		}
	}

	return nil
}

// GetConfig returns the registry configuration
func (r *Registry) GetConfig() Config {
	return r.config
}

// AddGate creates a new gate and adds it to the registry
func (r *Registry) AddGate(id string) *types.GateConfig {
	gate := &types.GateConfig{
		ID:     id,
		Tests:  []types.TestConfig{},
		Suites: make(map[string]types.SuiteConfig),
	}
	return gate
}

// GetGate retrieves a gate by ID
func (r *Registry) GetGate(id string) *types.GateConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, source := range r.sources {
		if source.Config != nil {
			for i := range source.Config.Gates {
				if source.Config.Gates[i].ID == id {
					return &source.Config.Gates[i]
				}
			}
		}
	}
	return nil
}

// Gate represents a collection of tests and suites
type Gate struct {
	name   string
	tests  []string
	suites map[string]*Suite
}

// Suite represents a collection of related tests
type Suite struct {
	name  string
	tests []string
}

func mustGetwd() string {
	pwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	return pwd
}
