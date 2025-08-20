package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/ethereum/go-ethereum/log"
	"github.com/honeycombio/otel-config-go/otelconfig"
	"github.com/urfave/cli/v2"

	nat "github.com/ethereum-optimism/infra/op-acceptor"
	"github.com/ethereum-optimism/infra/op-acceptor/flags"
	"github.com/ethereum-optimism/infra/op-acceptor/service"
	"github.com/ethereum-optimism/optimism/devnet-sdk/telemetry"
	"github.com/ethereum-optimism/optimism/op-service/cliapp"
	"github.com/ethereum-optimism/optimism/op-service/ctxinterrupt"
	oplog "github.com/ethereum-optimism/optimism/op-service/log"
)

var (
	Version   = "v0.5.0"
	GitCommit = ""
	GitDate   = ""
)

func main() {
	app := cli.NewApp()
	app.Version = fmt.Sprintf("%s-%s-%s", Version, GitCommit, GitDate)
	app.Name = "op-acceptor"
	app.Usage = "Optimism Network Acceptance Tester Service"
	app.Description = "op-acceptor tests networks"
	app.Flags = cliapp.ProtectFlags(flags.Flags)
	app.Action = cliapp.LifecycleCmd(run)
	app.ExitErrHandler = func(c *cli.Context, err error) {
		var exitErr cli.ExitCoder
		if errors.As(err, &exitErr) {
			// Use the exit code from the ExitCoder
			cli.HandleExitCoder(exitErr)
		} else if err != nil {
			// Check for typed runtime errors
			if nat.IsRuntimeError(err) {
				// For runtime errors, use exit code 2
				cli.HandleExitCoder(cli.Exit(err.Error(), 2))
			} else if nat.IsTestFailureError(err) {
				// For test failures, use exit code 1
				cli.HandleExitCoder(cli.Exit(err.Error(), 1))
			} else {
				// For other unspecified errors, default to exit code 1
				cli.HandleExitCoder(cli.Exit(err.Error(), 1))
			}
		}
	}

	// Start telemetry
	ctx, shutdown, err := telemetry.SetupOpenTelemetry(
		context.Background(),
		otelconfig.WithServiceName(app.Name),
		otelconfig.WithServiceVersion(app.Version),
	)
	if err != nil {
		log.Crit("Failed to setup open telemetry", "message", err)
	}
	defer shutdown()

	// Start server
	svc := service.New()
	svc.Start(ctx)
	defer svc.Shutdown()

	// Start CLI
	ctx = ctxinterrupt.WithSignalWaiterMain(ctx)
	err = app.RunContext(ctx, os.Args)
	if err != nil {
		log.Crit("Application failed", "message", err)
	}
}

func run(ctx *cli.Context, closeApp context.CancelCauseFunc) (cliapp.Lifecycle, error) {
	logCfg := oplog.ReadCLIConfig(ctx)
	log := oplog.NewLogger(oplog.AppOut(ctx), logCfg)
	oplog.SetGlobalLogHandler(log.Handler())
	oplog.SetupDefaults()

	// Initialize the service with both paths
	cfg, err := nat.NewConfig(
		ctx,
		log,
		ctx.String(flags.TestDir.Name),
		ctx.String(flags.ValidatorConfig.Name),
		ctx.String(flags.Gate.Name),
	)
	if err != nil {
		// Wrap in RuntimeError to signal this should exit with code 2
		return nil, nat.NewRuntimeError(fmt.Errorf("failed to create config: %w", err))
	}

	cfg.Log.Debug("Config", "config", cfg)

	// Create the NAT service
	natService, err := nat.New(ctx.Context, cfg, Version, closeApp)
	if err != nil {
		// Wrap in RuntimeError to signal this should exit with code 2
		return nil, nat.NewRuntimeError(fmt.Errorf("failed to create nat: %w", err))
	}

	return natService, nil
}
