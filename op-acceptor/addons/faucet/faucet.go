package faucet

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strconv"

	"github.com/ethereum-optimism/optimism/devnet-sdk/descriptors"
	"github.com/ethereum-optimism/optimism/devnet-sdk/shell/env"
	"github.com/ethereum-optimism/optimism/op-faucet/config"
	"github.com/ethereum-optimism/optimism/op-faucet/faucet"
	fconf "github.com/ethereum-optimism/optimism/op-faucet/faucet/backend/config"
	"github.com/ethereum-optimism/optimism/op-faucet/faucet/backend/types"
	"github.com/ethereum-optimism/optimism/op-service/endpoint"
	"github.com/ethereum-optimism/optimism/op-service/eth"
	oplog "github.com/ethereum-optimism/optimism/op-service/log"
	opmetrics "github.com/ethereum-optimism/optimism/op-service/metrics"
	"github.com/ethereum-optimism/optimism/op-service/oppprof"
	oprpc "github.com/ethereum-optimism/optimism/op-service/rpc"
)

type Service struct {
	env *env.DevnetEnv
	svc *faucet.Service
}

func NewFaucet(env *env.DevnetEnv) *Service {
	return &Service{
		env: env,
	}
}

func (s *Service) Start(ctx context.Context) error {
	rpcCfg := oprpc.DefaultCLIConfig()
	rpcCfg.ListenPort = 0 // use dynamic port

	cfg := &config.Config{
		Version:       "embedded",
		LogConfig:     oplog.DefaultCLIConfig(),
		MetricsConfig: opmetrics.DefaultCLIConfig(),
		PprofConfig:   oppprof.DefaultCLIConfig(),
		RPC:           rpcCfg,
	}
	cfg.Faucets = s.faucetsConfig(s.env)

	logger := oplog.NewLogger(os.Stdout, cfg.LogConfig)
	svc, err := faucet.FromConfig(ctx, cfg, logger)
	if err != nil {
		return fmt.Errorf("failed to create faucet: %w", err)
	}

	if err := svc.Start(ctx); err != nil {
		return fmt.Errorf("failed to start faucet: %w", err)
	}

	rpc := svc.RPC()
	rpcURL, err := url.Parse(rpc)
	if err != nil {
		return fmt.Errorf("failed to parse rpc url: %w", err)
	}
	port, err := strconv.Atoi(rpcURL.Port())
	if err != nil {
		return fmt.Errorf("failed to parse rpc url port: %w", err)
	}

	faucetService := &descriptors.Service{
		Name: "op-faucet",
		Endpoints: descriptors.EndpointMap{
			"rpc": &descriptors.PortInfo{
				Port: port,
				Host: "127.0.0.1", // we're running the test locally to the faucet
			},
		},
	}

	if s.env.Env.L1.Services == nil {
		s.env.Env.L1.Services = make(map[string][]*descriptors.Service)
	}

	s.env.Env.L1.Services["faucet"] = []*descriptors.Service{faucetService}
	for _, l2 := range s.env.Env.L2 {
		if l2.Services == nil {
			l2.Services = make(map[string][]*descriptors.Service)
		}
		l2.Services["faucet"] = []*descriptors.Service{faucetService}
	}

	s.svc = svc
	return nil
}

func (s *Service) Stop(ctx context.Context) error {
	return s.svc.Stop(ctx)
}

func (s *Service) faucetsConfig(env *env.DevnetEnv) *fconf.Config {
	cfg := &fconf.Config{
		Faucets:  make(map[types.FaucetID]*fconf.FaucetEntry),
		Defaults: make(map[eth.ChainID]types.FaucetID),
	}

	for _, l2 := range env.Env.L2 {
		// TODO: we might need something else for persistent devnets, but that's what can be expected from a kurtosis one.
		wallet, ok := l2.L1Wallets["l2Faucet"]
		if !ok {
			continue
		}
		chainID, err := eth.ParseDecimalChainID(l2.ID)
		if err != nil {
			continue
		}
		faucetID := types.FaucetID(fmt.Sprintf("%s-faucet", l2.ID))
		elRPC, err := getELRPC(l2.Chain)
		if err != nil {
			continue
		}
		faucetEntry := &fconf.FaucetEntry{
			ChainID: chainID,
			ELRPC: endpoint.MustRPC{
				Value: endpoint.URL(elRPC),
			},
			TxCfg: fconf.TxManagerConfig{
				PrivateKey: wallet.PrivateKey,
			},
		}
		cfg.Faucets[faucetID] = faucetEntry
		cfg.Defaults[chainID] = faucetID
	}

	return cfg
}

func getELRPC(c *descriptors.Chain) (string, error) {
	if c.Services != nil {
		if proxyd, ok := c.Services["proxyd"]; ok {
			ep, ok := proxyd[0].Endpoints["http"]
			if !ok {
				return "", fmt.Errorf("rpc endpoint not found")
			}
			return fmt.Sprintf("http://%s:%d", ep.Host, ep.Port), nil
		}
	}

	// if no proxyd, fallback to the first el node
	el, ok := c.Nodes[0].Services["el"]
	if !ok {
		return "", fmt.Errorf("el service not found")
	}
	ep, ok := el.Endpoints["rpc"]
	if !ok {
		return "", fmt.Errorf("rpc endpoint not found")
	}
	return fmt.Sprintf("http://%s:%d", ep.Host, ep.Port), nil
}
