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

// Config holds the application configuration
type Config struct {
	TestDir            string
	ValidatorConfig    string
	TargetGate         string
	GoBinary           string
	RunInterval        time.Duration          // Interval between test runs
	RunOnce            bool                   // Indicates if the service should exit after one test run
	AllowSkips         bool                   // Allow tests to be skipped instead of failing when preconditions are not met
	DefaultTimeout     time.Duration          // Default timeout for individual tests, can be overridden by test config
	LogDir             string                 // Directory to store test logs
	OutputRealtimeLogs bool                   // If enabled, test logs will be outputted in realtime
	TestLogLevel       string                 // Log level to be used for the tests
	Orchestrator       flags.OrchestratorType // Devstack orchestrator type
	DevnetEnvURL       string                 // URL or path to the devnet environment file
	Log                log.Logger
}

// NewConfig creates a new Config from cli context
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

	orchestratorStr := ctx.String(flags.Orchestrator.Name)
	orchestrator := flags.OrchestratorType(orchestratorStr)

	// Validate orchestrator type (this should already be validated by the CLI flag, but double-check)
	if !orchestrator.IsValid() {
		return nil, fmt.Errorf("invalid orchestrator type: %s. Must be one of: %s, %s",
			orchestratorStr, flags.OrchestratorSysgo, flags.OrchestratorSysext)
	}

	devnetEnvURL := ctx.String(flags.DevnetEnvURL.Name)

	return &Config{
		TestDir:            absTestDir,
		ValidatorConfig:    absValidatorConfig,
		TargetGate:         gate,
		GoBinary:           ctx.String(flags.GoBinary.Name),
		RunInterval:        runInterval,
		RunOnce:            runOnce,
		AllowSkips:         ctx.Bool(flags.AllowSkips.Name),
		DefaultTimeout:     ctx.Duration(flags.DefaultTimeout.Name),
		OutputRealtimeLogs: ctx.Bool(flags.OutputRealtimeLogs.Name),
		TestLogLevel:       ctx.String(flags.TestLogLevel.Name),
		Orchestrator:       orchestrator,
		DevnetEnvURL:       devnetEnvURL,
		LogDir:             logDir,
		Log:                log,
	}, nil
}
