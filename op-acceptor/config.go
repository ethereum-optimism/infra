package nat

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/urfave/cli/v2"

	"github.com/ethereum-optimism/infra/op-acceptor/flags"
	"github.com/ethereum/go-ethereum/log"
)

// Config holds the application configuration
type Config struct {
	TestDir               string
	ValidatorConfig       string
	TargetGate            string
	GatelessMode          bool
	GoBinary              string
	RunInterval           time.Duration          // Interval between test runs
	RunOnce               bool                   // Indicates if the service should exit after one test run
	AllowSkips            bool                   // Allow tests to be skipped instead of failing when preconditions are not met
	DefaultTimeout        time.Duration          // Default timeout for individual tests, can be overridden by test config
	Timeout               time.Duration          // Timeout for gateless mode tests (if specified)
	LogDir                string                 // Directory to store test logs
	OutputRealtimeLogs    bool                   // If enabled, test logs will be outputted in realtime
	TestLogLevel          string                 // Log level to be used for the tests
	Orchestrator          flags.OrchestratorType // Devstack orchestrator type
	DevnetEnvURL          string                 // URL or path to the devnet environment file
	Serial                bool                   // Whether to run tests serially instead of in parallel
	Concurrency           int                    // Number of concurrent test workers (0 = auto-determine)
	ShowProgress          bool                   // Whether to show periodic progress updates during test execution
	ProgressInterval      time.Duration          // Interval between progress updates when ShowProgress is 'true'
	FlakeShake            bool                   // Enable flake-shake mode for test stability validation
	FlakeShakeIterations  int                    // Number of times to run each test in flake-shake mode
	Log                   log.Logger
	ExcludeGates          []string // List of gate IDs whose tests should be excluded
	StripFileLinePrefixes bool     // If enabled, strip file:line prefixes from test output logs
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

	// Determine if we're in gateless mode (gate not specified). Validator config is optional in gateless mode
	gatelessMode := gate == ""

	// In gateless mode, we don't require validator config or gate
	if !gatelessMode {
		if validatorConfig == "" {
			return nil, errors.New("validator configuration file is required when not in gateless mode")
		}
		if gate == "" {
			return nil, errors.New("gate is required when not in gateless mode")
		}
	}

	var absValidatorConfig string
	if validatorConfig != "" {
		var err error
		absValidatorConfig, err = filepath.Abs(validatorConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve absolute path for validator config '%s': %w", validatorConfig, err)
		}
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

	excludeGates := parseExcludeGates(ctx.String(flags.ExcludeGates.Name))

	// Conflict: selected gate is also excluded
	if gate != "" {
		for _, eg := range excludeGates {
			if eg == gate {
				return nil, fmt.Errorf("the selected gate '%s' is also specified in --exclude-gates; remove it from exclusions or choose a different gate", gate)
			}
		}
	}

	return &Config{
		TestDir:               absTestDir,
		ValidatorConfig:       absValidatorConfig,
		TargetGate:            gate,
		GatelessMode:          gatelessMode,
		GoBinary:              ctx.String(flags.GoBinary.Name),
		RunInterval:           runInterval,
		RunOnce:               runOnce,
		AllowSkips:            ctx.Bool(flags.AllowSkips.Name),
		DefaultTimeout:        ctx.Duration(flags.DefaultTimeout.Name),
		Timeout:               ctx.Duration(flags.Timeout.Name),
		OutputRealtimeLogs:    ctx.Bool(flags.OutputRealtimeLogs.Name),
		TestLogLevel:          ctx.String(flags.TestLogLevel.Name),
		Orchestrator:          orchestrator,
		DevnetEnvURL:          devnetEnvURL,
		Serial:                ctx.Bool(flags.Serial.Name),
		Concurrency:           ctx.Int(flags.Concurrency.Name),
		ShowProgress:          ctx.Bool(flags.ShowProgress.Name),
		ProgressInterval:      ctx.Duration(flags.ProgressInterval.Name),
		FlakeShake:            ctx.Bool(flags.FlakeShake.Name),
		FlakeShakeIterations:  ctx.Int(flags.FlakeShakeIterations.Name),
		LogDir:                logDir,
		Log:                   log,
		ExcludeGates:          excludeGates,
		StripFileLinePrefixes: ctx.Bool(flags.StripFileLinePrefixes.Name),
	}, nil
}

// parseExcludeGates determines which gates to exclude based on env/flag.

func parseExcludeGates(value string) []string {
	val := strings.TrimSpace(value)
	if val == "" {
		// No default exclusions: empty means no skip gates
		return nil
	}
	parts := strings.Split(val, ",")
	var gates []string
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			gates = append(gates, s)
		}
	}
	return gates
}
