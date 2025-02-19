package nat

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/urfave/cli/v2"

	"github.com/ethereum-optimism/infra/op-nat/flags"
	"github.com/ethereum/go-ethereum/log"
)

type Config struct {
	TestDir         string
	ValidatorConfig string
	TargetGate      string

	Log log.Logger
}

// NewConfig creates a new Config instance
func NewConfig(ctx *cli.Context, log log.Logger, testDir string, validatorConfig string, gate string) (*Config, error) {
	// Parse flags
	if err := flags.CheckRequired(ctx); err != nil {
		return nil, fmt.Errorf("missing required flags: %w", err)
	}
	if testDir == "" {
		return nil, errors.New("test directory is required")
	}
	if validatorConfig == "" {
		return nil, errors.New("validator config path is required")
	}
	if gate == "" {
		return nil, errors.New("gate is required")
	}

	// Get absolute paths
	absTestDir, err := filepath.Abs(testDir)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for test dir: %w", err)
	}
	absValidatorConfig, err := filepath.Abs(validatorConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for validator config: %w", err)
	}

	return &Config{
		TestDir:         absTestDir,
		ValidatorConfig: absValidatorConfig,
		TargetGate:      gate,
		Log:             log,
	}, nil
}
