package app

import (
	"github.com/urfave/cli/v2"

	opservice "github.com/ethereum-optimism/optimism/op-service"
	oplog "github.com/ethereum-optimism/optimism/op-service/log"
	opmetrics "github.com/ethereum-optimism/optimism/op-service/metrics"
	"github.com/ethereum-optimism/optimism/op-service/oppprof"
	oprpc "github.com/ethereum-optimism/optimism/op-service/rpc"
	optls "github.com/ethereum-optimism/optimism/op-service/tls"
)

const (
	ServiceConfigPathFlagName = "config"
	ClientEndpointFlagName    = "endpoint"
)

func CLIFlags(envPrefix string) []cli.Flag {
	flags := []cli.Flag{
		&cli.StringFlag{
			Name:    ServiceConfigPathFlagName,
			Usage:   "Signer service configuration file path",
			Value:   "config.yaml",
			EnvVars: opservice.PrefixEnvVar(envPrefix, "SERVICE_CONFIG"),
		},
	}
	flags = append(flags, oprpc.CLIFlags(envPrefix)...)
	flags = append(flags, oplog.CLIFlags(envPrefix)...)
	flags = append(flags, opmetrics.CLIFlags(envPrefix)...)
	flags = append(flags, oppprof.CLIFlags(envPrefix)...)
	flags = append(flags, optls.CLIFlags(envPrefix)...)
	return flags
}

func ClientSignCLIFlags(envPrefix string) []cli.Flag {
	flags := []cli.Flag{
		&cli.StringFlag{
			Name:    ClientEndpointFlagName,
			Usage:   "Signer endpoint the client will connect to",
			Value:   "http://localhost:8080",
			EnvVars: opservice.PrefixEnvVar(envPrefix, "CLIENT_ENDPOINT"),
		},
	}
	return flags
}

type Config struct {
	ClientEndpoint    string
	ServiceConfigPath string

	TLSConfig     optls.CLIConfig
	RPCConfig     oprpc.CLIConfig
	LogConfig     oplog.CLIConfig
	MetricsConfig opmetrics.CLIConfig
	PprofConfig   oppprof.CLIConfig
}

func (c Config) Check() error {
	if err := c.RPCConfig.Check(); err != nil {
		return err
	}
	if err := c.MetricsConfig.Check(); err != nil {
		return err
	}
	if err := c.PprofConfig.Check(); err != nil {
		return err
	}
	if err := c.TLSConfig.Check(); err != nil {
		return err
	}
	return nil
}

func NewConfig(ctx *cli.Context) *Config {
	return &Config{
		ClientEndpoint:    ctx.String(ClientEndpointFlagName),
		ServiceConfigPath: ctx.String(ServiceConfigPathFlagName),
		TLSConfig:         optls.ReadCLIConfig(ctx),
		RPCConfig:         oprpc.ReadCLIConfig(ctx),
		LogConfig:         oplog.ReadCLIConfig(ctx),
		MetricsConfig:     opmetrics.ReadCLIConfig(ctx),
		PprofConfig:       oppprof.ReadCLIConfig(ctx),
	}
}
