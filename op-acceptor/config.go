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

type Config struct {
	TestDir         string
	ValidatorConfig string
	TargetGate      string
	GoBinary        string
	RunInterval     time.Duration // Interval between test runs
	RunOnce         bool          // Indicates if the service should exit after one test run
	AllowSkips      bool          // Allow tests to be skipped instead of failing when preconditions are not met
	DefaultTimeout  time.Duration // Default timeout for individual tests, can be overridden by test config
	LogDir          string        // Directory to store test logs
	OutputTestLogs  bool          // Whether to output test logs to the console
	TestLogLevel    string        // Log level to be used for the tests
	Log             log.Logger
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
		return nil, errors.New("validator configuration file is required")
	}

	absValidatorConfig, err := filepath.Abs(validatorConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve absolute path for validator config '%s': %w", validatorConfig, err)
	}

	runInterval := ctx.Duration(flags.RunInterval.Name)
	runOnce := runInterval == 0

	// Get log directory, default to "logs" if not specified
	logDir := ctx.String(flags.LogDir.Name)
	if logDir == "" {
		logDir = "logs"
	}

	// Resolve the absolute paths
	absTestDir, err := filepath.Abs(testDir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve absolute path for test directory '%s': %w", testDir, err)
	}
	logDir, err = filepath.Abs(logDir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve absolute path for log directory '%s': %w", logDir, err)
	}

	return &Config{
		TestDir:         absTestDir,
		ValidatorConfig: absValidatorConfig,
		TargetGate:      gate,
		GoBinary:        ctx.String(flags.GoBinary.Name),
		RunInterval:     runInterval,
		RunOnce:         runOnce,
		AllowSkips:      ctx.Bool(flags.AllowSkips.Name),
		DefaultTimeout:  ctx.Duration(flags.DefaultTimeout.Name),
		OutputTestLogs:  ctx.Bool(flags.OutputTestLogs.Name),
		TestLogLevel:    ctx.String(flags.TestLogLevel.Name),
		LogDir:          logDir,
		Log:             log,
	}, nil
}
