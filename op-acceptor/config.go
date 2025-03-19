package nat

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/urfave/cli/v2"

	"github.com/ethereum-optimism/infra/op-acceptor/flags"
	"github.com/ethereum/go-ethereum/log"
)

// Config holds the configuration for the Network Acceptance Tester.
type Config struct {
	Log             log.Logger
	TestDir         string
	ValidatorConfig string
	RunInterval     time.Duration
	RunOnce         bool
	GoBinary        string
	TargetGate      string
	AllowSkips      bool
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

	runInterval := ctx.Duration(flags.RunInterval.Name)
	runOnce := runInterval == 0

	return &Config{
		TestDir:         absTestDir,
		ValidatorConfig: absValidatorConfig,
		TargetGate:      gate,
		GoBinary:        ctx.String(flags.GoBinary.Name),
		RunInterval:     runInterval,
		RunOnce:         runOnce,
		AllowSkips:      ctx.Bool(flags.AllowSkips.Name),
		Log:             log,
	}, nil
}
