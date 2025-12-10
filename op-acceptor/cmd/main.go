package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

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
	Version   = "v3.6.6"
	GitCommit = ""
	GitDate   = ""
)

func main() {
	app := buildApp()

	if helpRequested(os.Args[1:]) {
		app.Setup()
		cli.ShowAppHelpAndExit(cli.NewContext(app, nil, nil), 0)
	}

	if versionRequested(os.Args[1:]) {
		app.Setup()
		cli.ShowVersion(cli.NewContext(app, nil, nil))
		return
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

	stopSignalHandler := installShutdownSignalHandler(log, closeApp)
	defer stopSignalHandler()

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

func installShutdownSignalHandler(logger log.Logger, closeApp context.CancelCauseFunc) func() {
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)

	done := make(chan struct{})
	go func() {
		defer signal.Stop(sigs)
		select {
		case sig := <-sigs:
			logger.Warn("Received shutdown signal, attempting graceful stop", "signal", sig)
			if closeApp != nil {
				closeApp(fmt.Errorf("received shutdown signal: %s", sig))
			}
		case <-done:
		}
	}()

	return func() {
		close(done)
	}
}

func buildApp() *cli.App {
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
			cli.HandleExitCoder(exitErr)
			return
		}
		if err == nil {
			return
		}
		switch {
		case nat.IsRuntimeError(err):
			cli.HandleExitCoder(cli.Exit(err.Error(), 2))
		case nat.IsTestFailureError(err):
			cli.HandleExitCoder(cli.Exit(err.Error(), 1))
		default:
			cli.HandleExitCoder(cli.Exit(err.Error(), 1))
		}
	}
	return app
}

func helpRequested(args []string) bool {
	for _, arg := range args {
		if arg == "--help" || arg == "-h" || arg == "help" {
			return true
		}
	}
	return false
}

func versionRequested(args []string) bool {
	for _, arg := range args {
		if arg == "--version" || arg == "-v" || arg == "version" {
			return true
		}
	}
	return false
}
