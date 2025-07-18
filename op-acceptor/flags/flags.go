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
		Required: true,
		EnvVars:  opservice.PrefixEnvVar(EnvVarPrefix, "VALIDATORS"),
		Usage:    "Path to validator config file (eg. 'validators.yaml')",
	}
	Gate = &cli.StringFlag{
		Name:     "gate",
		Value:    "",
		Required: true,
		EnvVars:  opservice.PrefixEnvVar(EnvVarPrefix, "GATE"),
		Usage:    "Gate to run (eg. 'alphanet')",
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
)

var requiredFlags = []cli.Flag{
	TestDir,
	ValidatorConfig,
	Gate,
}

var optionalFlags = []cli.Flag{
	GoBinary,
	RunInterval,
	AllowSkips,
	DefaultTimeout,
	LogDir,
	TestLogLevel,
	OutputRealtimeLogs,
	Orchestrator,
	DevnetEnvURL,
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
