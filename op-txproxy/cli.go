package op_txproxy

import (
	opservice "github.com/ethereum-optimism/optimism/op-service"

	"github.com/urfave/cli/v2"
)

const (
	SendRawTransactionConditionalEnabledFlagName   = "sendRawTxConditional.enabled"
	SendRawTransactionConditionalBackendFlagName   = "sendRawTxConditional.backend"
	SendRawTransactionConditionalRateLimitFlagName = "sendRawTxConditional.ratelimit"
)

type CLIConfig struct {
	SendRawTransactionConditionalEnabled   bool
	SendRawTransactionConditionalBackend   string
	SendRawTransactionConditionalRateLimit uint64
}

func CLIFlags(envPrefix string) []cli.Flag {
	return []cli.Flag{
		&cli.BoolFlag{
			Name:    SendRawTransactionConditionalEnabledFlagName,
			Usage:   "Decider if eth_sendRawTransactionConditional requests should passthrough or be rejected",
			Value:   true,
			EnvVars: opservice.PrefixEnvVar(envPrefix, "SENDRAWTXCONDITIONAL_ENABLED"),
		},
		&cli.StringFlag{
			Name:     SendRawTransactionConditionalBackendFlagName,
			Usage:    "block builder to broadcast conditional transactions",
			Required: true,
			EnvVars:  opservice.PrefixEnvVar(envPrefix, "SENDRAWTXCONDITIONAL_BACKEND"),
		},
		&cli.Uint64Flag{
			Name:    SendRawTransactionConditionalRateLimitFlagName,
			Usage:   "Maximum cost -- storage lookups -- allowed for conditional transactions in a given second",
			Value:   5000,
			EnvVars: opservice.PrefixEnvVar(envPrefix, "SENDRAWTXCONDITIONAL_RATELIMIT"),
		},
	}
}

func ReadCLIConfig(ctx *cli.Context) CLIConfig {
	return CLIConfig{
		SendRawTransactionConditionalEnabled:   ctx.Bool(SendRawTransactionConditionalEnabledFlagName),
		SendRawTransactionConditionalBackend:   ctx.String(SendRawTransactionConditionalBackendFlagName),
		SendRawTransactionConditionalRateLimit: ctx.Uint64(SendRawTransactionConditionalRateLimitFlagName),
	}
}
