package registry

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/ethereum/go-ethereum/log"
	"gopkg.in/yaml.v3"
)

// Registry manages test sources and their configurations
type Registry struct {
	config     Config
	validators []types.ValidatorMetadata
	mu         sync.RWMutex
}

// Config contains registry configuration
type Config struct {
	Log                 log.Logger
	ValidatorConfigFile string
	DefaultTimeout      time.Duration
}

// NewRegistry creates a new registry instance
func NewRegistry(cfg Config) (*Registry, error) {
	if cfg.ValidatorConfigFile == "" {
		return nil, fmt.Errorf("validator config file is required")
	}
	if cfg.Log == nil {
		cfg.Log = log.New()
		cfg.Log.Error("No logger provided, using default")
	}

	// Create registry instance
	r := &Registry{
		config: cfg,
	}

	// Load the source
	if err := r.loadValidators(cfg.ValidatorConfigFile); err != nil {
		return nil, fmt.Errorf("failed to load source: %w", err)
	}

	cfg.Log.Debug("Registry loaded", "len(validators)", len(r.validators))

	return r, nil
}

// loadValidators loads a test source and its configuration
func (r *Registry) loadValidators(cfgPath string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Load the validator config
	validatorConfig, err := loadConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Resolve gate inheritance
	if err := r.validateGateInheritance(validatorConfig); err != nil {
		return fmt.Errorf("failed to resolve gate inheritance: %w", err)
	}

	// Convert config into test metadata (moved from discovery)
	validators, err := r.discoverTests(validatorConfig)
	if err != nil {
		return fmt.Errorf("failed to discover tests: %w", err)
	}

	r.validators = validators

	return nil
}

// validateGateInheritance checks gate inheritance resolution
func (r *Registry) validateGateInheritance(config *types.ValidatorConfig) error {
	if config.Gates == nil {
		return nil
	}

	gateMap := make(map[string]types.GateConfig)
	for _, gate := range config.Gates {
		gateMap[gate.ID] = gate
	}

	// Check for circular inheritance before resolving
	for _, gate := range config.Gates {
		if err := r.checkCircularInheritance(gate.ID, gate.Inherits, gateMap, make(map[string]bool)); err != nil {
			return fmt.Errorf("circular inheritance detected: %w", err)
		}
	}

	// Resolve inheritance
	for i := range config.Gates {
		if err := config.Gates[i].ResolveInherited(gateMap); err != nil {
			return fmt.Errorf("invalid gate inheritance: %w", err)
		}
	}

	return nil
}

// checkCircularInheritance detects circular dependencies in gate inheritance
func (r *Registry) checkCircularInheritance(currentID string, inherits []string, gateMap map[string]types.GateConfig, visited map[string]bool) error {
	if visited[currentID] {
		return fmt.Errorf("circular inheritance detected at gate %s", currentID)
	}

	visited[currentID] = true
	defer delete(visited, currentID) // Clean up after checking this branch

	for _, inheritedID := range inherits {
		inherited, exists := gateMap[inheritedID]
		if !exists {
			return fmt.Errorf("gate %s inherits from non-existent gate %s", currentID, inheritedID)
		}

		if err := r.checkCircularInheritance(inheritedID, inherited.Inherits, gateMap, visited); err != nil {
			return err
		}
	}

	return nil
}

// GetValidators returns all discovered validators
func (r *Registry) GetValidators() []types.ValidatorMetadata {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.validators
}

// GetValidatorsByGate returns validators for a specific gate
func (r *Registry) GetValidatorsByGate(gateID string) []types.ValidatorMetadata {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var validators []types.ValidatorMetadata
	for _, validator := range r.validators {
		if validator.Gate == gateID {
			validators = append(validators, validator)
		}
	}
	return validators
}

// GetConfig returns the registry configuration
func (r *Registry) GetConfig() Config {
	return r.config
}

// loadConfig loads a validator config from a file
func loadConfig(path string) (*types.ValidatorConfig, error) {
	log.Debug("Reading validator config file", "path", path)

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var cfg types.ValidatorConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	return &cfg, nil
}

// Move discovery.DiscoverTests to be a private method
func (r *Registry) discoverTests(validatorConfig *types.ValidatorConfig) ([]types.ValidatorMetadata, error) {
	var validators []types.ValidatorMetadata

	for i := range validatorConfig.Gates {
		gate := &validatorConfig.Gates[i]

		// Process direct gate tests
		tests, err := r.discoverTestsInConfig(gate.Tests, gate.ID, "")
		if err != nil {
			return nil, err
		}
		validators = append(validators, tests...)

		// Process suites
		for suiteID, suite := range gate.Suites {
			tests, err := r.discoverTestsInConfig(suite.Tests, gate.ID, suiteID)
			if err != nil {
				return nil, err
			}
			validators = append(validators, tests...)
		}
	}

	return validators, nil
}

func (r *Registry) discoverTestsInConfig(configs []types.TestConfig, gateID string, suiteID string) ([]types.ValidatorMetadata, error) {
	var tests []types.ValidatorMetadata

	for _, cfg := range configs {
		var timeout time.Duration
		if cfg.Timeout != nil {
			timeout = *cfg.Timeout
		} else {
			timeout = r.config.DefaultTimeout
		}

		// If only package is specified (no name), treat it as "run all"
		if cfg.Name == "" {
			tests = append(tests, types.ValidatorMetadata{
				ID:      cfg.Package, // Use package as ID for run-all cases
				Gate:    gateID,
				Suite:   suiteID,
				Package: cfg.Package,
				RunAll:  true,
				Type:    types.ValidatorTypeTest,
				Timeout: timeout,
			})
			continue
		}

		// Normal case with specific test name
		tests = append(tests, types.ValidatorMetadata{
			ID:       cfg.Name,
			Gate:     gateID,
			Suite:    suiteID,
			FuncName: cfg.Name,
			Package:  cfg.Package,
			Type:     types.ValidatorTypeTest,
			Timeout:  timeout,
		})
	}

	return tests, nil
}
