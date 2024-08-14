package main

import (
	"context"
	"fmt"
	"os"

	"github.com/ethereum-optimism/optimism/op-service/cliapp"
	oplog "github.com/ethereum-optimism/optimism/op-service/log"
	"github.com/ethereum-optimism/optimism/op-service/metrics"
	"github.com/ethereum-optimism/optimism/op-service/opio"
	"github.com/ethereum-optimism/optimism/op-service/rpc"
	optxproxy "github.com/ethereum-optimism/optimism/op-txproxy"

	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"

	"github.com/urfave/cli/v2"
)

var (
	GitCommit    = ""
	GitDate      = ""
	EnvVarPrefix = "op_txproxy"
)

func main() {
	oplog.SetupDefaults()

	app := cli.NewApp()
	app.Version = params.VersionWithCommit(GitCommit, GitDate)
	app.Name = "op-txproxy"
	app.Usage = "Optimism TxProxy Service"
	app.Description = "Auxiliary service to supplement op-stack transaction pool management"
	app.Action = cliapp.LifecycleCmd(TxProxyMain)

	logFlags := oplog.CLIFlags(EnvVarPrefix)
	rpcFlags := rpc.CLIFlags(EnvVarPrefix)
	backendFlags := optxproxy.CLIFlags(EnvVarPrefix)
	app.Flags = append(append(backendFlags, rpcFlags...), logFlags...)

	ctx := opio.WithInterruptBlocker(context.Background())
	if err := app.RunContext(ctx, os.Args); err != nil {
		log.Crit("Application Failed", "err", err)
	}
}

func TxProxyMain(ctx *cli.Context, closeApp context.CancelCauseFunc) (cliapp.Lifecycle, error) {
	log := oplog.NewLogger(oplog.AppOut(ctx), oplog.ReadCLIConfig(ctx))
	m := metrics.With(metrics.NewRegistry())

	cfg := optxproxy.ReadCLIConfig(ctx)
	txproxy, err := optxproxy.NewTxProxy(ctx.Context, log, m, &cfg)
	if err != nil {
		return nil, fmt.Errorf("unable to start superchain backend: %w", err)
	}

	rpcConfig := rpc.ReadCLIConfig(ctx)
	rpcOpts := []rpc.ServerOption{
		rpc.WithAPIs(txproxy.GetAPIs()),
		rpc.WithLogger(log),
		rpc.WithMiddleware(optxproxy.AuthMiddleware(optxproxy.DefaultAuthHeaderKey)),
	}

	rpcServer := rpc.NewServer(rpcConfig.ListenAddr, rpcConfig.ListenPort, ctx.App.Version, rpcOpts...)
	return rpc.NewService(log, rpcServer), nil
}
