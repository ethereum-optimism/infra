package main

import (
	"context"
	"fmt"
	"os"

	"github.com/ethereum/go-ethereum/log"
	"github.com/urfave/cli/v2"

	nat "github.com/ethereum-optimism/infra/op-nat"
	"github.com/ethereum-optimism/infra/op-nat/flags"
	"github.com/ethereum-optimism/infra/op-nat/service"
	"github.com/ethereum-optimism/optimism/op-service/cliapp"
	"github.com/ethereum-optimism/optimism/op-service/ctxinterrupt"
	oplog "github.com/ethereum-optimism/optimism/op-service/log"
)

var (
	Version   = "v0.1.0"
	GitCommit = ""
	GitDate   = ""
)

func main() {
	app := cli.NewApp()
	app.Version = fmt.Sprintf("%s-%s-%s", Version, GitCommit, GitDate)
	app.Name = "op-nat"
	app.Usage = "Optimism Network Acceptance Tester Service"
	app.Description = "op-nat tests networks"
	app.Flags = cliapp.ProtectFlags(flags.Flags)
	app.Action = cliapp.LifecycleCmd(run)

	// Start server
	svc := service.New()
	svc.Start(context.Background())
	defer svc.Shutdown()

	// Start CLI
	ctx := ctxinterrupt.WithSignalWaiterMain(context.Background())
	err := app.RunContext(ctx, os.Args)
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
		ctx.String("testdir"),
		ctx.String("validator-config"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create config: %w", err)
	}

	cfg.Log.Debug("Config", "config", cfg)

	natService, err := nat.New(ctx.Context, cfg, Version)
	if err != nil {
		return nil, fmt.Errorf("failed to create nat: %w", err)
	}

	return natService, nil
}
