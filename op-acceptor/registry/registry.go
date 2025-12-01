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
	config       Config
	validators   []types.ValidatorMetadata
	gateInherits map[string][]string // gateID -> list of directly inherited gate IDs
	mu           sync.RWMutex
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

	// Resolve inheritance so that excluded gates include tests inherited from parents
	if err := r.validateGateInheritance(cfg); err != nil {
		r.config.Log.Warn("Failed to resolve gate inheritance for exclude-gates; skipping filter", "error", err)
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
		if _, ok := s.byTuple[TestRef{Package: v.Package, Name: v.FuncName}]; ok || packagePrefixBlacklisted(v.Package, s.byPackage) {
			excludedCount++
			name := v.FuncName
			excludedPrev = append(excludedPrev, formatRef(v.Package, name))
			r.config.Log.Info("Excluded by blacklist", "package", v.Package, "name", v.FuncName)
			continue
		}
		filtered = append(filtered, v)
	}
	r.validators = filtered

	if excludedCount > 0 {
		r.config.Log.Info("Blacklist removed tests", "count", excludedCount, "excluded_gates", gates)
		r.config.Log.Debug("Excluded tests preview", "tests", excludedPrev)
		r.config.Log.Debug("Validators filtered", "before", original, "after", len(filtered))
	}
}

// packagePrefixBlacklisted returns true if pkg matches or is a subpackage of any blacklisted package entry.
// It matches exact import path prefix on segment boundaries: A blocks A and A/sub, but not Afoo.
func packagePrefixBlacklisted(pkg string, byPackage map[string]struct{}) bool {
	for p := range byPackage {
		if pkg == p || (strings.HasPrefix(pkg, p) && (len(pkg) == len(p) || pkg[len(p)] == '/')) {
			return true
		}
	}
	return false
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

	// Create a deep copy of gates before inheritance resolution to preserve direct tests
	originalGates := make([]types.GateConfig, len(validatorConfig.Gates))
	r.gateInherits = make(map[string][]string)
	for i := range validatorConfig.Gates {
		originalGates[i] = validatorConfig.Gates[i]
		originalGates[i].Tests = make([]types.TestConfig, len(validatorConfig.Gates[i].Tests))
		copy(originalGates[i].Tests, validatorConfig.Gates[i].Tests)
		originalGates[i].Suites = make(map[string]types.SuiteConfig)
		for k, v := range validatorConfig.Gates[i].Suites {
			originalGates[i].Suites[k] = v
		}
		r.gateInherits[validatorConfig.Gates[i].ID] = validatorConfig.Gates[i].Inherits
	}

	// Resolve gate inheritance
	if err := r.validateGateInheritance(validatorConfig); err != nil {
		return fmt.Errorf("failed to resolve gate inheritance: %w", err)
	}

	// Convert config into test metadata using resolved gates (for running all tests)
	// but track which gates originally defined each test
	validators, err := r.discoverTestsWithOriginalGates(validatorConfig, originalGates)
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

// GetValidatorsByGates returns validators for multiple gates, including validators from inherited gates
func (r *Registry) GetValidatorsByGates(gateIDs []string) []types.ValidatorMetadata {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var validators []types.ValidatorMetadata
	gateSet := make(map[string]bool)
	for _, gateID := range gateIDs {
		gateSet[gateID] = true
	}

	// Also include gates that are inherited by the target gates
	inheritedGateSet := make(map[string]bool)
	for _, gateID := range gateIDs {
		inheritedGates := r.gateInherits[gateID]
		for _, inheritedGateID := range inheritedGates {
			r.collectInheritedGates(inheritedGateID, inheritedGateSet)
			inheritedGateSet[inheritedGateID] = true
		}
	}

	for gateID := range gateSet {
		inheritedGateSet[gateID] = true
	}
	for gateID := range inheritedGateSet {
		gateSet[gateID] = true
	}

	for _, validator := range r.validators {
		if gateSet[validator.Gate] {
			validators = append(validators, validator)
		}
	}
	return validators
}

// collectInheritedGates recursively collects all gates inherited by a gate
func (r *Registry) collectInheritedGates(gateID string, collected map[string]bool) {
	if collected[gateID] {
		return
	}
	inheritedGates := r.gateInherits[gateID]
	for _, inheritedGateID := range inheritedGates {
		collected[inheritedGateID] = true
		r.collectInheritedGates(inheritedGateID, collected)
	}
}

// GetConfig returns the registry configuration
func (r *Registry) GetConfig() Config {
	return r.config
}

// GetGateInherits returns the list of gates that a gate directly inherits from
func (r *Registry) GetGateInherits(gateID string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.gateInherits[gateID]
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

// discoverTestsWithOriginalGates creates validators from resolved gates but tracks original gate ownership
func (r *Registry) discoverTestsWithOriginalGates(validatorConfig *types.ValidatorConfig, originalGates []types.GateConfig) ([]types.ValidatorMetadata, error) {
	var validators []types.ValidatorMetadata

	// Create maps of original gate tests and suites for quick lookup
	originalGateTests := make(map[string]map[string]bool)
	originalGateSuites := make(map[string]map[string]map[string]bool)
	for _, gate := range originalGates {
		testSet := make(map[string]bool)
		for _, test := range gate.Tests {
			key := test.Package
			if test.Name != "" {
				key += ":" + test.Name
			}
			testSet[key] = true
		}
		originalGateTests[gate.ID] = testSet

		suiteMap := make(map[string]map[string]bool)
		for suiteID, suite := range gate.Suites {
			suiteTestSet := make(map[string]bool)
			for _, test := range suite.Tests {
				key := test.Package
				if test.Name != "" {
					key += ":" + test.Name
				}
				suiteTestSet[key] = true
			}
			suiteMap[suiteID] = suiteTestSet
		}
		originalGateSuites[gate.ID] = suiteMap
	}

	// Process resolved gates - create validators with original gate IDs
	// For each resolved gate, create validators for tests that were originally defined in that gate
	for i := range validatorConfig.Gates {
		gate := &validatorConfig.Gates[i]

		for _, test := range gate.Tests {
			testKey := test.Package
			if test.Name != "" {
				testKey += ":" + test.Name
			}

			if testSet, exists := originalGateTests[gate.ID]; exists && testSet[testKey] {
				tests, err := r.discoverTestsInConfig([]types.TestConfig{test}, gate.ID, "")
				if err != nil {
					return nil, err
				}
				validators = append(validators, tests...)
			}
		}

		for suiteID, suite := range gate.Suites {
			for _, test := range suite.Tests {
				testKey := test.Package
				if test.Name != "" {
					testKey += ":" + test.Name
				}

				if suiteMap, exists := originalGateSuites[gate.ID]; exists {
					if suiteTestSet, exists := suiteMap[suiteID]; exists && suiteTestSet[testKey] {
						tests, err := r.discoverTestsInConfig([]types.TestConfig{test}, gate.ID, suiteID)
						if err != nil {
							return nil, err
						}
						validators = append(validators, tests...)
					}
				}
			}
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
