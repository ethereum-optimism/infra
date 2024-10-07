package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/urfave/cli/v2"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"

	"github.com/ethereum-optimism/optimism/op-service/cliapp"
	"github.com/ethereum-optimism/optimism/op-service/httputil"
	oplog "github.com/ethereum-optimism/optimism/op-service/log"
	opmetrics "github.com/ethereum-optimism/optimism/op-service/metrics"
	"github.com/ethereum-optimism/optimism/op-service/oppprof"
	oprpc "github.com/ethereum-optimism/optimism/op-service/rpc"
	"github.com/ethereum-optimism/optimism/op-service/tls/certman"

	"github.com/ethereum-optimism/infra/op-signer/client"
	"github.com/ethereum-optimism/infra/op-signer/service"
)

type SignerApp struct {
	log log.Logger

	version string

	pprofServer   *oppprof.Service
	metricsServer *httputil.HTTPServer
	registry      *prometheus.Registry

	signer *service.SignerService

	rpc *oprpc.Server

	stopped atomic.Bool
}

func InitFromConfig(ctx context.Context, log log.Logger, cfg *Config, version string) (*SignerApp, error) {
	if err := cfg.Check(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	app := &SignerApp{log: log, version: version}
	if err := app.init(cfg); err != nil {
		return nil, errors.Join(err, app.Stop(ctx)) // clean up the failed init attempt
	}
	return app, nil
}

func (s *SignerApp) init(cfg *Config) error {
	if err := s.initPprof(cfg); err != nil {
		return fmt.Errorf("pprof error: %w", err)
	}
	if err := s.initMetrics(cfg); err != nil {
		return fmt.Errorf("metrics error: %w", err)
	}
	if err := s.initRPC(cfg); err != nil {
		return fmt.Errorf("metrics error: %w", err)
	}
	return nil
}

func (s *SignerApp) initPprof(cfg *Config) error {
	if !cfg.PprofConfig.ListenEnabled {
		return nil
	}
	s.pprofServer = oppprof.New(
		cfg.PprofConfig.ListenEnabled,
		cfg.PprofConfig.ListenAddr,
		cfg.PprofConfig.ListenPort,
		cfg.PprofConfig.ProfileType,
		cfg.PprofConfig.ProfileDir,
		cfg.PprofConfig.ProfileFilename,
	)
	s.log.Info("Starting pprof server", "addr", cfg.PprofConfig.ListenAddr, "port", cfg.PprofConfig.ListenPort)
	if err := s.pprofServer.Start(); err != nil {
		return fmt.Errorf("failed to start pprof server: %w", err)
	}
	return nil
}

func (s *SignerApp) initMetrics(cfg *Config) error {
	registry := opmetrics.NewRegistry()
	registry.MustRegister(service.MetricSignTransactionTotal)
	s.registry = registry // some things require metrics registry

	if !cfg.MetricsConfig.Enabled {
		return nil
	}

	metricsCfg := cfg.MetricsConfig
	s.log.Info("Starting metrics server", "addr", metricsCfg.ListenAddr, "port", metricsCfg.ListenPort)
	metricsServer, err := opmetrics.StartServer(registry, metricsCfg.ListenAddr, metricsCfg.ListenPort)
	if err != nil {
		return fmt.Errorf("failed to start metrics server: %w", err)
	}
	s.log.Info("Started metrics server", "endpoint", metricsServer.Addr())
	s.metricsServer = metricsServer
	return nil
}

func (s *SignerApp) initRPC(cfg *Config) error {
	caCert, err := os.ReadFile(cfg.TLSConfig.TLSCaCert)
	if err != nil {
		return fmt.Errorf("failed to read tls ca cert: %s", string(caCert))
	}
	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCert)

	cm, err := certman.New(s.log, cfg.TLSConfig.TLSCert, cfg.TLSConfig.TLSKey)
	if err != nil {
		return fmt.Errorf("failed to read tls cert or key: %w", err)
	}
	if err := cm.Watch(); err != nil {
		return fmt.Errorf("failed to start certman watcher: %w", err)
	}

	tlsConfig := &tls.Config{
		GetCertificate: cm.GetCertificate,
		ClientCAs:      caCertPool,
		ClientAuth:     tls.VerifyClientCertIfGiven, // necessary for k8s healthz probes, but we check the cert in service/auth.go
	}
	serverTlsConfig := &oprpc.ServerTLSConfig{
		Config:    tlsConfig,
		CLIConfig: &cfg.TLSConfig,
	}

	rpcCfg := cfg.RPCConfig
	s.rpc = oprpc.NewServer(
		rpcCfg.ListenAddr,
		rpcCfg.ListenPort,
		s.version,
		oprpc.WithLogger(s.log),
		oprpc.WithTLSConfig(serverTlsConfig),
		oprpc.WithMiddleware(service.NewAuthMiddleware()),
		oprpc.WithHTTPRecorder(opmetrics.NewPromHTTPRecorder(s.registry, "signer")),
	)

	serviceCfg, err := service.ReadConfig(cfg.ServiceConfigPath)
	if err != nil {
		return fmt.Errorf("failed to read service config: %w", err)
	}
	s.signer = service.NewSignerService(s.log, serviceCfg)
	s.signer.RegisterAPIs(s.rpc)

	if err := s.rpc.Start(); err != nil {
		return fmt.Errorf("error starting RPC server: %w", err)
	}
	s.log.Info("Started op-signer RPC server", "addr", s.rpc.Endpoint())

	return nil
}

func (s *SignerApp) Start(ctx context.Context) error {
	return nil
}

func (s *SignerApp) Stop(ctx context.Context) error {
	var result error
	if s.rpc != nil {
		if err := s.rpc.Stop(); err != nil {
			result = errors.Join(result, fmt.Errorf("failed to stop RPC server: %w", err))
		}
	}
	if s.pprofServer != nil {
		if err := s.pprofServer.Stop(ctx); err != nil {
			result = errors.Join(result, fmt.Errorf("failed to stop pprof server: %w", err))
		}
	}
	if s.metricsServer != nil {
		if err := s.metricsServer.Stop(ctx); err != nil {
			result = errors.Join(result, fmt.Errorf("failed to stop metrics server: %w", err))
		}
	}
	return result
}

func (s *SignerApp) Stopped() bool {
	return s.stopped.Load()
}

var _ cliapp.Lifecycle = (*SignerApp)(nil)

func MainAppAction(version string) cliapp.LifecycleAction {
	return func(cliCtx *cli.Context, _ context.CancelCauseFunc) (cliapp.Lifecycle, error) {
		cfg := NewConfig(cliCtx)
		logger := oplog.NewLogger(cliCtx.App.Writer, cfg.LogConfig)
		return InitFromConfig(cliCtx.Context, logger, cfg, version)
	}
}

type SignActionType string

const (
	SignTransaction  SignActionType = "transaction"
	SignBlockPayload SignActionType = "block_payload"
)

func ClientSign(version string) func(cliCtx *cli.Context) error {
	return func(cliCtx *cli.Context) error {
		cfg := NewConfig(cliCtx)
		if err := cfg.Check(); err != nil {
			return fmt.Errorf("invalid CLI flags: %w", err)
		}

		l := oplog.NewLogger(os.Stdout, cfg.LogConfig)
		log.Root().SetHandler(l.GetHandler())

		actionStr := cliCtx.Args().Get(0)
		action := SignActionType(actionStr)

		switch action {
		case SignTransaction:
			txarg := cliCtx.Args().Get(1)
			if txarg == "" {
				return errors.New("no transaction argument was provided")
			}
			txraw, err := hexutil.Decode(txarg)
			if err != nil {
				return errors.New("failed to decode transaction argument")
			}

			client, err := client.NewSignerClient(l, cfg.ClientEndpoint, cfg.TLSConfig)
			if err != nil {
				return err
			}

			tx := &types.Transaction{}
			if err := tx.UnmarshalBinary(txraw); err != nil {
				return fmt.Errorf("failed to unmarshal transaction argument: %w", err)
			}

			tx, err = client.SignTransaction(context.Background(), tx)
			if err != nil {
				return err
			}

			result, _ := tx.MarshalJSON()
			fmt.Println(string(result))

		case SignBlockPayload:
			blockPayloadHash := cliCtx.Args().Get(1)
			if blockPayloadHash == "" {
				return errors.New("no block payload argument was provided")
			}

			client, err := client.NewSignerClient(l, cfg.ClientEndpoint, cfg.TLSConfig)
			if err != nil {
				return err
			}

			signingHash := common.Hash{}
			if err := signingHash.UnmarshalText([]byte(blockPayloadHash)); err != nil {
				return fmt.Errorf("failed to unmarshal block payload argument: %w", err)
			}

			signature, err := client.SignBlockPayload(context.Background(), signingHash)
			if err != nil {
				return err
			}

			fmt.Println(string(signature[:]))

		case "":
			return errors.New("no action was provided")
		}

		return nil
	}
}
