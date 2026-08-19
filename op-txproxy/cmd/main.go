package main

import (
	"context"
	"fmt"
	"os"

	optxproxy "github.com/ethereum-optimism/infra/op-txproxy"

	opservice "github.com/ethereum-optimism/optimism/op-service"
	"github.com/ethereum-optimism/optimism/op-service/cliapp"
	"github.com/ethereum-optimism/optimism/op-service/ctxinterrupt"
	oplog "github.com/ethereum-optimism/optimism/op-service/log"
	"github.com/ethereum-optimism/optimism/op-service/metrics"
	"github.com/ethereum-optimism/optimism/op-service/rpc"

	"github.com/ethereum/go-ethereum/log"
	gethversion "github.com/ethereum/go-ethereum/version"

	"github.com/urfave/cli/v2"
)

var (
	GitCommit    = ""
	GitDate      = ""
	EnvVarPrefix = "OP_TXPROXY"
)

func main() {
	oplog.SetupDefaults()

	app := cli.NewApp()
	version := fmt.Sprintf("%d.%d.%d", gethversion.Major, gethversion.Minor, gethversion.Patch)
	app.Version = opservice.FormatVersion(version, GitCommit, GitDate, gethversion.Meta)
	app.Name = "op-txproxy"
	app.Usage = "Optimism TxProxy Service"
	app.Description = "Auxiliary service to supplement op-stack transaction pool management"
	app.Action = cliapp.LifecycleCmd(TxProxyMain)

	logFlags := oplog.CLIFlags(EnvVarPrefix)
	rpcFlags := rpc.CLIFlags(EnvVarPrefix)
	metricsFlags := metrics.CLIFlags(EnvVarPrefix)
	backendFlags := optxproxy.CLIFlags(EnvVarPrefix)
	app.Flags = append(append(append(backendFlags, rpcFlags...), metricsFlags...), logFlags...)

	ctx := ctxinterrupt.WithSignalWaiterMain(context.Background())
	if err := app.RunContext(ctx, os.Args); err != nil {
		log.Crit("Application Failed", "err", err)
	}
}

func TxProxyMain(ctx *cli.Context, closeApp context.CancelCauseFunc) (cliapp.Lifecycle, error) {
	log := oplog.NewLogger(oplog.AppOut(ctx), oplog.ReadCLIConfig(ctx))

	cfg := optxproxy.ReadCLIConfig(ctx)
	txproxy, err := optxproxy.NewTxProxy(ctx.Context, log, ctx.App.Version, &cfg)
	if err != nil {
		return nil, fmt.Errorf("unable to start superchain backend: %w", err)
	}

	return txproxy, nil
}
