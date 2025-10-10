package registry

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"strings"

	"github.com/ethereum-optimism/infra/op-acceptor/testlist"
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
	Timeout             time.Duration // Timeout for gateless mode tests (if specified)
	// Gateless mode fields
	GatelessMode bool
	TestDir      string
	// Skip behavior
	ExcludeGates []string
}

// NewRegistry creates a new registry instance
func NewRegistry(cfg Config) (*Registry, error) {
	if cfg.Log == nil {
		cfg.Log = log.New()
		cfg.Log.Error("No logger provided, using default")
	}

	// Create registry instance
	r := &Registry{
		config: cfg,
	}

	// Load validators based on mode
	if cfg.GatelessMode {
		if cfg.TestDir == "" {
			return nil, fmt.Errorf("test directory is required for gateless mode")
		}
		if err := r.loadGatelessValidators(); err != nil {
			return nil, fmt.Errorf("failed to load gateless validators: %w", err)
		}
	} else {
		if cfg.ValidatorConfigFile == "" {
			return nil, fmt.Errorf("validator config file is required")
		}
		if err := r.loadValidators(cfg.ValidatorConfigFile); err != nil {
			return nil, fmt.Errorf("failed to load validators: %w", err)
		}
	}

	// Apply skip filters if any exclude gates were specified and we have a validator config
	if len(cfg.ExcludeGates) > 0 && cfg.ValidatorConfigFile != "" {
		r.applyExcludeGates(cfg.ValidatorConfigFile, cfg.ExcludeGates)
	}

	cfg.Log.Debug("Registry loaded", "len(validators)", len(r.validators), "gatelessMode", cfg.GatelessMode)

	return r, nil
}

// loadGatelessValidators auto-discovers test packages and creates synthetic validators
func (r *Registry) loadGatelessValidators() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.config.Log.Info("Auto-discovering test packages in gateless mode", "testDir", r.config.TestDir)

	// Use the test directory (sans "/...") as the working root for discovery
	workingRoot := strings.TrimSuffix(r.config.TestDir, "/...")
	if wdAbs, err := filepath.Abs(workingRoot); err == nil {
		workingRoot = wdAbs
	}

	// Discover test packages rooted at workingRoot
	packages, err := testlist.FindTestPackages(".", workingRoot)
	if err != nil {
		return fmt.Errorf("failed to discover test packages: %w", err)
	}

	if len(packages) == 0 {
		return fmt.Errorf("no test packages found in directory: %s", r.config.TestDir)
	}

	r.config.Log.Debug("Found test packages", "count", len(packages), "packages", packages)

	// Create synthetic validators for each discovered package
	var validators []types.ValidatorMetadata
	for _, pkg := range packages {
		// For gateless mode, use the discovered package paths directly
		adjustedPkg := pkg

		// Determine which timeout to use: prefer Timeout flag, fall back to DefaultTimeout
		timeout := r.config.DefaultTimeout
		if r.config.Timeout > 0 {
			timeout = r.config.Timeout
		}

		validator := types.ValidatorMetadata{
			ID:      adjustedPkg, // Use the discovered package path as ID
			Gate:    "gateless",  // Use a synthetic gate name
			Suite:   "",          // No suite in gateless mode
			Package: adjustedPkg,
			RunAll:  true, // Always run all tests in package for gateless mode
			Type:    types.ValidatorTypeTest,
			Timeout: timeout,
		}
		validators = append(validators, validator)
	}

	r.validators = validators
	r.config.Log.Info("Created synthetic validators for gateless mode", "count", len(validators))

	return nil
}

// TestRef uniquely identifies a test by package/name pair. Name may be empty
// for package-level entries.
type TestRef struct {
	Package string
	Name    string
}

type skipSet struct {
	byTuple   map[TestRef]struct{}
	byPackage map[string]struct{}
}

// applyExcludeGates builds and applies a skip filter set based on provided gates.
// It logs warnings for unknown gates and malformed entries.
func (r *Registry) applyExcludeGates(cfgPath string, gates []string) {
	if len(gates) == 0 {
		return
	}

	// Load config to inspect gates
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		r.config.Log.Warn("Failed to load validators for exclude-gates; skipping filter", "error", err)
		return
	}

	// Index gates by ID
	gateIndex := make(map[string]types.GateConfig)
	for _, g := range cfg.Gates {
		gateIndex[g.ID] = g
	}

	// Build skip set
	s := skipSet{byTuple: map[TestRef]struct{}{}, byPackage: map[string]struct{}{}}
	for _, gid := range gates {
		g, ok := gateIndex[gid]
		if !ok {
			r.config.Log.Warn("exclude gate not found; ignoring", "gate", gid)
			continue
		}
		// Direct tests
		for _, t := range g.Tests {
			pkg := strings.TrimSpace(t.Package)
			name := strings.TrimSpace(t.Name)
			switch {
			case pkg != "" && name != "":
				s.byTuple[TestRef{Package: pkg, Name: name}] = struct{}{}
			case pkg != "":
				s.byPackage[pkg] = struct{}{}
			default:
				r.config.Log.Warn("malformed test in skip gate; missing package", "gate", gid)
			}
		}
		// Suite tests
		for _, suite := range g.Suites {
			for _, t := range suite.Tests {
				pkg := strings.TrimSpace(t.Package)
				name := strings.TrimSpace(t.Name)
				switch {
				case pkg != "" && name != "":
					s.byTuple[TestRef{Package: pkg, Name: name}] = struct{}{}
				case pkg != "":
					s.byPackage[pkg] = struct{}{}
				default:
					r.config.Log.Warn("malformed test in skip gate suite; missing package", "gate", gid)
				}
			}
		}
	}

	// Filter validators
	original := len(r.validators)
	var excludedPrev []string
	filtered := r.validators[:0]
	excludedCount := 0
	for _, v := range r.validators {
		// Only tests are considered
		if v.Type != types.ValidatorTypeTest {
			filtered = append(filtered, v)
			continue
		}
		// Match by tuple or package
		if _, ok := s.byTuple[TestRef{Package: v.Package, Name: v.FuncName}]; ok {
			excludedCount++
			if len(excludedPrev) < 20 {
				excludedPrev = append(excludedPrev, formatRef(v.Package, v.FuncName))
			}
			continue
		}
		if _, ok := s.byPackage[v.Package]; ok {
			excludedCount++
			if len(excludedPrev) < 20 {
				excludedPrev = append(excludedPrev, formatRef(v.Package, ""))
			}
			continue
		}
		filtered = append(filtered, v)
	}
	r.validators = filtered

	if excludedCount > 0 {
		r.config.Log.Info("Excluding tests from gates", "count", excludedCount, "gates", gates)
		r.config.Log.Debug("Excluded tests preview", "tests", excludedPrev)
		r.config.Log.Debug("Validators filtered", "before", original, "after", len(filtered))
	}
}

func formatRef(pkg string, name string) string {
	if name == "" {
		return pkg + "::*"
	}
	return pkg + "::" + name
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
