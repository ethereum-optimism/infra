package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
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
	"github.com/ethereum-optimism/optimism/op-service/eth"
	"github.com/ethereum-optimism/optimism/op-service/httputil"
	oplog "github.com/ethereum-optimism/optimism/op-service/log"
	opmetrics "github.com/ethereum-optimism/optimism/op-service/metrics"
	"github.com/ethereum-optimism/optimism/op-service/oppprof"
	oprpc "github.com/ethereum-optimism/optimism/op-service/rpc"
	"github.com/ethereum-optimism/optimism/op-service/signer"
	"github.com/ethereum-optimism/optimism/op-service/tls/certman"

	"github.com/ethereum-optimism/infra/op-signer/provider"
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
		return fmt.Errorf("rpc error: %w", err)
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
	registry.MustRegister(service.MetricSignBlockPayloadTotal)
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
	var httpOptions = []httputil.Option{}

	if cfg.TLSConfig.Enabled {
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
		serverTlsConfig := &httputil.ServerTLSConfig{
			Config:    tlsConfig,
			CLIConfig: &cfg.TLSConfig,
		}

		httpOptions = append(httpOptions, httputil.WithServerTLS(serverTlsConfig))
	} else {
		s.log.Warn("TLS disabled. This is insecure and only supported for local development. Please enable TLS in production environments!")
	}

	rpcCfg := cfg.RPCConfig
	s.rpc = oprpc.ServerFromConfig(
		&oprpc.ServerConfig{
			AppVersion: s.version,
			Host:       rpcCfg.ListenAddr,
			Port:       rpcCfg.ListenPort,
			RpcOptions: []oprpc.Option{
				oprpc.WithMiddleware(service.NewAuthMiddleware()),
				oprpc.WithHTTPRecorder(opmetrics.NewPromHTTPRecorder(s.registry, "signer")),
				oprpc.WithLogger(s.log),
			},
			HttpOptions: httpOptions,
		},
	)

	providerCfg, err := provider.ReadConfig(cfg.ServiceConfigPath)
	if err != nil {
		return fmt.Errorf("failed to read provider config: %w", err)
	}
	s.signer, err = service.NewSignerService(s.log, providerCfg)
	if err != nil {
		return fmt.Errorf("failed to create signer service: %w", err)
	}
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
	SignTransaction    SignActionType = "transaction"
	SignBlockPayload   SignActionType = "block_payload"
	SignBlockPayloadV2 SignActionType = "block_payloadV2"
)

func ClientSign(action SignActionType) func(cliCtx *cli.Context) error {
	return func(cliCtx *cli.Context) error {
		ctx := cliCtx.Context

		cfg := signer.ReadCLIConfig(cliCtx)
		if err := cfg.Check(); err != nil {
			return fmt.Errorf("invalid CLI flags: %w", err)
		}

		logCfg := oplog.ReadCLIConfig(cliCtx)
		l := oplog.NewLogger(os.Stdout, logCfg)
		oplog.SetGlobalLogHandler(l.Handler())

		cl, err := signer.NewSignerClient(l, cfg.Endpoint, cfg.Headers, cfg.TLSConfig)
		if err != nil {
			return err
		}

		switch action {
		case SignTransaction:
			txarg := cliCtx.Args().Get(0)
			if txarg == "" {
				return errors.New("no transaction argument was provided")
			}
			txraw, err := hexutil.Decode(txarg)
			if err != nil {
				return errors.New("failed to decode transaction argument")
			}

			tx := &types.Transaction{}
			if err := tx.UnmarshalBinary(txraw); err != nil {
				return fmt.Errorf("failed to unmarshal transaction argument: %w", err)
			}
			chainID := tx.ChainId()
			sender, err := types.LatestSignerForChainID(chainID).Sender(tx)
			if err != nil {
				return fmt.Errorf("failed to determine tx sender: %w", err)
			}
			tx, err = cl.SignTransaction(ctx, chainID, sender, tx)
			if err != nil {
				return err
			}

			result, _ := json.MarshalIndent(tx, "  ", "  ")
			fmt.Println(string(result))

		case SignBlockPayload, SignBlockPayloadV2:
			if count := cliCtx.Args().Len(); count != 3 {
				return fmt.Errorf("expected 3 arguments, but got: %d", count)
			}
			payloadHashStr := cliCtx.Args().Get(0)
			chainIDStr := cliCtx.Args().Get(1)
			domainStr := cliCtx.Args().Get(2)
			var payloadHash common.Hash
			if err := payloadHash.UnmarshalText([]byte(payloadHashStr)); err != nil {
				return fmt.Errorf("failed to unmarshal block payload-hash argument: %w", err)
			}
			var chainID eth.ChainID
			if err := chainID.UnmarshalText([]byte(chainIDStr)); err != nil {
				return fmt.Errorf("failed to unmarshal block chain-ID argument: %w", err)
			}
			var domain eth.Bytes32
			if err := domain.UnmarshalText([]byte(domainStr)); err != nil {
				return fmt.Errorf("failed to unmarshal block domain argument: %w", err)
			}
			var signature eth.Bytes65
			var err error
			switch action {
			case SignBlockPayload:
				signature, err = cl.SignBlockPayload(ctx, &signer.BlockPayloadArgs{
					Domain:        domain,
					ChainID:       chainID.ToBig(),
					PayloadHash:   payloadHash[:],
					SenderAddress: nil,
				})
			case SignBlockPayloadV2:
				signature, err = cl.SignBlockPayloadV2(ctx, &signer.BlockPayloadArgsV2{
					Domain:        domain,
					ChainID:       chainID,
					PayloadHash:   payloadHash,
					SenderAddress: nil,
				})
			}
			if err != nil {
				return err
			}
			fmt.Println(signature.String())
		case "":
			return errors.New("no action was provided")
		}

		return nil
	}
}
