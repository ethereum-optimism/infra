package flags

import (
	"fmt"

	"github.com/urfave/cli/v2"

	opservice "github.com/ethereum-optimism/optimism/op-service"
	opflags "github.com/ethereum-optimism/optimism/op-service/flags"
	oplog "github.com/ethereum-optimism/optimism/op-service/log"
	opmetrics "github.com/ethereum-optimism/optimism/op-service/metrics"
	oprpc "github.com/ethereum-optimism/optimism/op-service/rpc"
)

const EnvVarPrefix = "OP_ACCEPTOR"

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
)

var requiredFlags = []cli.Flag{
	TestDir,
	ValidatorConfig,
	Gate,
}

var optionalFlags = []cli.Flag{
	GoBinary,
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
