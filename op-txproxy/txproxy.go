package op_txproxy

import (
	"context"
	"errors"
	"fmt"

	"github.com/ethereum-optimism/optimism/op-service/httputil"
	opmetrics "github.com/ethereum-optimism/optimism/op-service/metrics"
	oprpc "github.com/ethereum-optimism/optimism/op-service/rpc"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rpc"
)

var (
	MetricsNameSpace = "op_txproxy"
)

type TxProxy struct {
	log     log.Logger
	version string

	rpcSrv          *oprpc.Server
	metricsSrv      *httputil.HTTPServer
	metricsRegistry *prometheus.Registry

	conditionalTxService *ConditionalTxService
}

func NewTxProxy(ctx context.Context, log log.Logger, version string, cfg *CLIConfig) (*TxProxy, error) {
	metricsRegistry := opmetrics.NewRegistry()
	metricsFactory := opmetrics.With(metricsRegistry)

	conditionalTxService, err := NewConditionalTxService(ctx, log, metricsFactory, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create conditional tx service: %w", err)
	}

	txp := &TxProxy{
		log:                  log,
		version:              version,
		metricsRegistry:      metricsRegistry,
		conditionalTxService: conditionalTxService,
	}

	if err := txp.init(cfg); err != nil {
		return nil, fmt.Errorf("failed initialization: %w", err)
	}

	return txp, nil
}

func (txp *TxProxy) Start(_ context.Context) error {
	return nil // nothing else required
}

func (txp *TxProxy) Stop(ctx context.Context) error {
	var result error

	if txp.rpcSrv != nil {
		txp.log.Info("stopping rpc server")
		if err := txp.rpcSrv.Stop(); err != nil {
			result = errors.Join(result, fmt.Errorf("failed to stop metrics server: %w", err))
		}
	}
	if txp.metricsSrv != nil {
		txp.log.Info("stopping metrics server")
		if err := txp.metricsSrv.Stop(ctx); err != nil {
			result = errors.Join(result, fmt.Errorf("failed to stop metrics server: %w", err))
		}
	}

	return result
}

func (s *TxProxy) Stopped() bool {
	return false // no-op lifecycle api
}

func (txp *TxProxy) init(cfg *CLIConfig) error {
	if err := txp.initRPC(cfg); err != nil {
		return err
	}
	if err := txp.initMetrics(cfg); err != nil {
		return err
	}
	return nil
}

func (txp *TxProxy) initRPC(cfg *CLIConfig) error {
	apis := []rpc.API{{Namespace: "eth", Service: txp.conditionalTxService}}

	rpcCfg := cfg.rpcConfig
	rpcOpts := []oprpc.ServerOption{
		oprpc.WithAPIs(apis),
		oprpc.WithLogger(txp.log),
		oprpc.WithMiddleware(AuthMiddleware(DefaultAuthHeaderKey)),
	}

	txp.log.Info("starting rpc server", "addr", rpcCfg.ListenAddr, "port", rpcCfg.ListenPort)
	txp.rpcSrv = oprpc.NewServer(rpcCfg.ListenAddr, rpcCfg.ListenPort, txp.version, rpcOpts...)
	if err := txp.rpcSrv.Start(); err != nil {
		return fmt.Errorf("failed to start rpc server: %w", err)
	}

	return nil
}

func (txp *TxProxy) initMetrics(cfg *CLIConfig) error {
	metricsCfg := cfg.metricsConfig

	txp.log.Info("starting metrics server", "addr", metricsCfg.ListenAddr, "port", metricsCfg.ListenPort)
	metricsServer, err := opmetrics.StartServer(txp.metricsRegistry, metricsCfg.ListenAddr, metricsCfg.ListenPort)
	if err != nil {
		return fmt.Errorf("failed to start metrics server: %w", err)
	}

	txp.metricsSrv = metricsServer
	return nil
}
