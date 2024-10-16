package main

import (
	"context"
	"fmt"
	"os"

	optxproxy "github.com/ethereum-optimism/infra/op-txproxy"

	"github.com/ethereum-optimism/optimism/op-service/cliapp"
	oplog "github.com/ethereum-optimism/optimism/op-service/log"
	"github.com/ethereum-optimism/optimism/op-service/metrics"
	"github.com/ethereum-optimism/optimism/op-service/opio"
	"github.com/ethereum-optimism/optimism/op-service/rpc"

	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"

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
	app.Version = params.VersionWithCommit(GitCommit, GitDate)
	app.Name = "op-txproxy"
	app.Usage = "Optimism TxProxy Service"
	app.Description = "Auxiliary service to supplement op-stack transaction pool management"
	app.Action = cliapp.LifecycleCmd(TxProxyMain)

	logFlags := oplog.CLIFlags(EnvVarPrefix)
	rpcFlags := rpc.CLIFlags(EnvVarPrefix)
	metricsFlags := metrics.CLIFlags(EnvVarPrefix)
	backendFlags := optxproxy.CLIFlags(EnvVarPrefix)
	app.Flags = append(append(append(backendFlags, rpcFlags...), metricsFlags...), logFlags...)

	ctx := opio.WithInterruptBlocker(context.Background())
	if err := app.RunContext(ctx, os.Args); err != nil {
		log.Crit("Application Failed", "err", err)
	}
}

func TxProxyMain(ctx *cli.Context, closeApp context.CancelCauseFunc) (cliapp.Lifecycle, error) {
	log := oplog.NewLogger(oplog.AppOut(ctx), oplog.ReadCLIConfig(ctx))

	metricsRegistry := metrics.NewRegistry()
	m := metrics.With(metricsRegistry)

	cfg := optxproxy.ReadCLIConfig(ctx)
	txproxy, err := optxproxy.NewTxProxy(ctx.Context, log, m, &cfg)
	if err != nil {
		return nil, fmt.Errorf("unable to start superchain backend: %w", err)
	}

	// Start Metrics SErver
	metricsConfig := rpc.ReadCLIConfig(ctx)
	if err := metricsConfig.Check(); err != nil {
		return nil, fmt.Errorf("invalid metrics config: %w", err)
	}

	// TODO: Spin up metrics server

	// Start RPC Server
	rpcConfig := rpc.ReadCLIConfig(ctx)
	rpcOpts := []rpc.ServerOption{
		rpc.WithAPIs(txproxy.GetAPIs()),
		rpc.WithLogger(log),
		rpc.WithMiddleware(optxproxy.AuthMiddleware(optxproxy.DefaultAuthHeaderKey)),
	}

	log.Info("rpc server configuration", "host", rpcConfig.ListenAddr, "port", rpcConfig.ListenPort)
	rpcServer := rpc.NewServer(rpcConfig.ListenAddr, rpcConfig.ListenPort, ctx.App.Version, rpcOpts...)
	return rpc.NewService(log, rpcServer), nil
}
