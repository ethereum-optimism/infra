package flags

import (
	"fmt"
	"time"

	"github.com/urfave/cli/v2"

	opservice "github.com/ethereum-optimism/optimism/op-service"
	opflags "github.com/ethereum-optimism/optimism/op-service/flags"
	oplog "github.com/ethereum-optimism/optimism/op-service/log"
	opmetrics "github.com/ethereum-optimism/optimism/op-service/metrics"
	oprpc "github.com/ethereum-optimism/optimism/op-service/rpc"
)

const EnvVarPrefix = "OP_ACCEPTOR"

// OrchestratorType represents the type of devstack orchestrator
type OrchestratorType string

// Orchestrator type constants
const (
	OrchestratorSysgo  OrchestratorType = "sysgo"
	OrchestratorSysext OrchestratorType = "sysext"
)

// String returns the string representation of the orchestrator type
func (o OrchestratorType) String() string {
	return string(o)
}

// ValidOrchestratorTypes returns a slice of all valid orchestrator types
func ValidOrchestratorTypes() []OrchestratorType {
	return []OrchestratorType{OrchestratorSysgo, OrchestratorSysext}
}

// IsValid checks if the orchestrator type is valid
func (o OrchestratorType) IsValid() bool {
	for _, valid := range ValidOrchestratorTypes() {
		if o == valid {
			return true
		}
	}
	return false
}

// validateOrchestrator validates that the orchestrator value is one of the allowed types
func validateOrchestrator(value string) error {
	orchestrator := OrchestratorType(value)
	if !orchestrator.IsValid() {
		return fmt.Errorf("orchestrator must be one of: %s, %s", OrchestratorSysgo, OrchestratorSysext)
	}
	return nil
}

var (
	TestDir = &cli.StringFlag{
		Name:    "testdir",
		Value:   "",
		EnvVars: opservice.PrefixEnvVar(EnvVarPrefix, "TESTDIR"),
		Usage:   "Path to the test directory from which to discover tests",
	}
	ValidatorConfig = &cli.StringFlag{
		Name:     "validators",
		Value:    "",
		Required: false,
		EnvVars:  opservice.PrefixEnvVar(EnvVarPrefix, "VALIDATORS"),
		Usage:    "Path to validator config file (eg. 'validators.yaml'). Optional - if not provided, gateless mode will auto-discover tests in the test directory.",
	}
	Gate = &cli.StringFlag{
		Name:     "gate",
		Value:    "",
		Required: false,
		EnvVars:  opservice.PrefixEnvVar(EnvVarPrefix, "GATE"),
		Usage:    "Gate to run (eg. 'alphanet'). Optional - if not provided, gateless mode will run all discovered tests.",
	}
	GoBinary = &cli.StringFlag{
		Name:    "go-binary",
		Value:   "go",
		EnvVars: opservice.PrefixEnvVar(EnvVarPrefix, "GO_BINARY"),
		Usage:   "Path to the Go binary to use for running tests",
	}
	RunInterval = &cli.DurationFlag{
		Name:    "run-interval",
		Value:   0,
		EnvVars: opservice.PrefixEnvVar(EnvVarPrefix, "RUN_INTERVAL"),
		Usage:   "Interval between test runs (e.g. '1h', '30m'). Set to 0 or omit for run-once mode.",
	}
	AllowSkips = &cli.BoolFlag{
		Name:    "allow-skips",
		Value:   false,
		EnvVars: opservice.PrefixEnvVar(EnvVarPrefix, "ALLOW_SKIPS"),
		Usage:   "Allow tests to be skipped instead of failing when preconditions are not met.",
	}
	DefaultTimeout = &cli.DurationFlag{
		Name:    "default-timeout",
		Value:   5 * time.Minute,
		EnvVars: opservice.PrefixEnvVar(EnvVarPrefix, "DEFAULT_TIMEOUT"),
		Usage:   "Default timeout of an individual test (e.g. '30s', '5m', etc.). This setting is superseded by test or suite level timeout configuration. Set to '0' to disable any default timeout. Defaults to '5m'.",
	}
	Timeout = &cli.DurationFlag{
		Name:    "timeout",
		EnvVars: opservice.PrefixEnvVar(EnvVarPrefix, "TIMEOUT"),
		Usage:   "Timeout for all tests in gateless mode (e.g. '30s', '5m', etc.). If not specified, falls back to --default-timeout. Only applies in gateless mode.",
	}
	LogDir = &cli.StringFlag{
		Name:    "logdir",
		Value:   "logs",
		EnvVars: opservice.PrefixEnvVar(EnvVarPrefix, "LOGDIR"),
		Usage:   "Directory to store test logs. Defaults to 'logs' if not specified.",
	}
	TestLogLevel = &cli.StringFlag{
		Name:    "test-log-level",
		Value:   "info",
		EnvVars: opservice.PrefixEnvVar(EnvVarPrefix, "TEST_LOG_LEVEL"),
		Usage:   "Log level to be used for the tests. Defaults to 'info'.",
	}
	OutputRealtimeLogs = &cli.BoolFlag{
		Name:    "output-realtime-logs",
		Value:   false,
		EnvVars: opservice.PrefixEnvVar(EnvVarPrefix, "OUTPUT_REALTIME_LOGS"),
		Usage:   "If enabled, test logs will be outputted to the console in realtime. Defaults to false.",
	}
	StripCodeLinePrefixes = &cli.BoolFlag{
		Name:    "strip-code-line-prefixes",
		Value:   true,
		EnvVars: opservice.PrefixEnvVar(EnvVarPrefix, "STRIP_CODE_LINE_PREFIXES"),
		Usage:   "Strip file:line prefixes (e.g. 'test.go:123:') from test logs. Defaults to true.",
	}
	ShowProgress = &cli.BoolFlag{
		Name:    "show-progress",
		Value:   false,
		EnvVars: opservice.PrefixEnvVar(EnvVarPrefix, "SHOW_PROGRESS"),
		Usage:   "Show periodic progress updates during test execution. Defaults to false.",
	}
	ProgressInterval = &cli.DurationFlag{
		Name:    "progress-interval",
		Value:   30 * time.Second,
		EnvVars: opservice.PrefixEnvVar(EnvVarPrefix, "PROGRESS_INTERVAL"),
		Usage:   "Interval between progress updates when --show-progress is enabled. Defaults to 30s.",
	}
	Orchestrator = &cli.StringFlag{
		Name:    "orchestrator",
		Value:   OrchestratorSysext.String(),
		EnvVars: []string{"DEVSTACK_ORCHESTRATOR"},
		Usage:   "Devstack orchestrator type: 'sysext' (external provider) or 'sysgo' (in-memory Go). Defaults to 'sysext'.",
		Action: func(ctx *cli.Context, value string) error {
			return validateOrchestrator(value)
		},
	}
	DevnetEnvURL = &cli.StringFlag{
		Name:    "devnet-env-url",
		Value:   "",
		EnvVars: []string{"DEVNET_ENV_URL"},
		Usage:   "URL or path to the devnet environment file. Required for sysext orchestrator.",
	}
	Serial = &cli.BoolFlag{
		Name:    "serial",
		Value:   false,
		EnvVars: opservice.PrefixEnvVar(EnvVarPrefix, "SERIAL"),
		Usage:   "Run tests serially instead of in parallel. By default, tests run in parallel across packages.",
	}

	Concurrency = &cli.IntFlag{
		Name:    "concurrency",
		Value:   0, // 0 means auto-determine
		EnvVars: opservice.PrefixEnvVar(EnvVarPrefix, "CONCURRENCY"),
		Usage:   "Number of concurrent test workers. 0 (default) auto-determines based on system capabilities.",
	}
	FlakeShake = &cli.BoolFlag{
		Name:    "flake-shake",
		Value:   false,
		EnvVars: opservice.PrefixEnvVar(EnvVarPrefix, "FLAKE_SHAKE"),
		Usage:   "Enable flake-shake mode to run tests multiple times for stability validation.",
	}
	FlakeShakeIterations = &cli.IntFlag{
		Name:    "flake-shake-iterations",
		Value:   100,
		EnvVars: opservice.PrefixEnvVar(EnvVarPrefix, "FLAKE_SHAKE_ITERATIONS"),
		Usage:   "Number of times to run each test in flake-shake mode. Defaults to 100.",
	}
	ExcludeGates = &cli.StringFlag{
		Name:    "exclude-gates",
		Value:   "",
		EnvVars: opservice.PrefixEnvVar(EnvVarPrefix, "EXCLUDE_GATES"),
		Usage:   "Comma-separated list of gate IDs to blacklist globally across all modes.",
	}
)

var requiredFlags = []cli.Flag{
	TestDir,
}

var optionalFlags = []cli.Flag{
	ValidatorConfig,
	Gate,
	GoBinary,
	RunInterval,
	AllowSkips,
	DefaultTimeout,
	Timeout,
	LogDir,
	TestLogLevel,
	OutputRealtimeLogs,
	StripCodeLinePrefixes,
	ShowProgress,
	ProgressInterval,
	Orchestrator,
	DevnetEnvURL,
	Serial,
	Concurrency,
	FlakeShake,
	FlakeShakeIterations,
	ExcludeGates,
}
var Flags []cli.Flag

func init() {
	optionalFlags = append(optionalFlags, oprpc.CLIFlags(EnvVarPrefix)...)
	optionalFlags = append(optionalFlags, oplog.CLIFlags(EnvVarPrefix)...)
	optionalFlags = append(optionalFlags, opmetrics.CLIFlags(EnvVarPrefix)...)
	// optionalFlags = append(optionalFlags, oppprof.CLIFlags(EnvVarPrefix)...)
	// optionalFlags = append(optionalFlags, opflags.CLIFlags(EnvVarPrefix, "")...)

	Flags = append(requiredFlags, optionalFlags...)
}

func CheckRequired(ctx *cli.Context) error {
	for _, f := range requiredFlags {
		if !ctx.IsSet(f.Names()[0]) {
			return fmt.Errorf("flag %s is required", f.Names()[0])
		}
	}
	return opflags.CheckRequiredXor(ctx)
}
