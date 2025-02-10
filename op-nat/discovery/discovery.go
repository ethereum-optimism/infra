package discovery

import (
	"fmt"
	"os"

	"github.com/ethereum-optimism/infra/op-nat/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/pkg/errors"
	"gopkg.in/yaml.v3"
)

// DiscoverTests scans a directory for tests and their metadata
func DiscoverTests(cfg Config) ([]types.ValidatorMetadata, error) {
	log.Info("Discovering tests", "config", cfg.ConfigFile)
	fmt.Println("Discovering tests", "config", cfg.ConfigFile)
	if cfg.ConfigFile == "" {
		return nil, errors.New("config file path is required")
	}

	log.Debug("Loading validator config", "path", cfg.ConfigFile)

	// Read and parse the config file
	data, err := os.ReadFile(cfg.ConfigFile)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var validatorConfig types.ValidatorConfig
	if err := yaml.Unmarshal(data, &validatorConfig); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	// Convert slice to map for inheritance resolution
	gateMap := make(map[string]types.GateConfig)
	for _, gate := range validatorConfig.Gates {
		gateMap[gate.ID] = gate
	}

	var validators []types.ValidatorMetadata

	// Process each gate using index for iteration
	for i := range validatorConfig.Gates {
		gate := &validatorConfig.Gates[i]
		if err := gate.ResolveInherited(gateMap); err != nil {
			return nil, fmt.Errorf("resolving gate %q: %w", gate.ID, err)
		}

		// Process direct gate tests
		tests, err := discoverTests(gate.Tests, gate.ID, "")
		if err != nil {
			return nil, err
		}
		validators = append(validators, tests...)

		// Process suites
		for suiteID, suite := range gate.Suites {
			tests, err := discoverTests(suite.Tests, gate.ID, suiteID)
			if err != nil {
				return nil, err
			}
			validators = append(validators, tests...)
		}
	}

	return validators, nil
}

func discoverTests(configs []types.TestConfig, gateID string, suiteID string) ([]types.ValidatorMetadata, error) {
	var tests []types.ValidatorMetadata

	for _, cfg := range configs {
		// If only package is specified (no name), treat it as run_all
		if cfg.Name == "" {
			tests = append(tests, types.ValidatorMetadata{
				ID:      cfg.Package, // Use package as ID for run-all cases
				Gate:    gateID,
				Suite:   suiteID,
				Package: cfg.Package,
				RunAll:  true,
				Type:    types.ValidatorTypeTest,
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
		})
	}

	return tests, nil
}

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

func loadPackage(path string) (*Package, error) {
	// Implementation here
	return nil, nil
}

func isTestFunction(name string) bool {
	return true // Implement proper check
}

func shouldIncludeTest(name string, cfg types.TestConfig) bool {
	return true // Implement proper check
}

type Package struct {
	Files []*File
}

type File struct {
	Funcs []Function
}

type Function struct {
	Name string
}
