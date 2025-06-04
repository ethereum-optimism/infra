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

func chainFaucet(chain *descriptors.Chain, wallet *descriptors.Wallet) *fconf.FaucetEntry {
	chainID, err := eth.ParseDecimalChainID(chain.ID)
	if err != nil {
		return nil
	}
	elRPC, err := getELRPC(chain)
	if err != nil {
		return nil
	}
	return &fconf.FaucetEntry{
		ChainID: chainID,
		ELRPC: endpoint.MustRPC{
			Value: endpoint.URL(elRPC),
		},
		TxCfg: fconf.TxManagerConfig{
			PrivateKey: wallet.PrivateKey,
		},
	}
}

// This tries to generate a faucet config for each chain, based on the known wallets.
// The conventions, inherited from op-deployer are:
// For L1:
// - if we have a wallet named l1Faucet, we use it
// - otherwise, if we have a wallet named user-key-20 (by convention that's the devkey we use for the faucet in local environments), we use it
// - otherwise, if any of the L2s has a wallet named l1Faucet, we use it
// For L2:
// - if we have a wallet named l2Faucet, we use it
func (s *Service) faucetsConfig(env *env.DevnetEnv) *fconf.Config {
	cfg := &fconf.Config{
		Faucets:  make(map[types.FaucetID]*fconf.FaucetEntry),
		Defaults: make(map[eth.ChainID]types.FaucetID),
	}

	l1Wallet := env.Env.L1.Wallets["l1Faucet"]
	if l1Wallet == nil {
		l1Wallet = env.Env.L1.Wallets["user-key-20"]
	}

	for _, l2 := range env.Env.L2 {
		// TODO: this is awful, but normally op-deployer registers the same l1Faucet for all L2s.
		if w, ok := l2.L1Wallets["l1Faucet"]; ok && l1Wallet == nil {
			l1Wallet = w
		}

		// TODO: we might need something else for persistent devnets, but that's what can be expected from a kurtosis one.
		wallet, ok := l2.L1Wallets["l2Faucet"]
		if !ok {
			continue
		}
		faucetID := types.FaucetID(fmt.Sprintf("%s-faucet", l2.ID))
		faucetEntry := chainFaucet(l2.Chain, wallet)
		cfg.Faucets[faucetID] = faucetEntry
		cfg.Defaults[faucetEntry.ChainID] = faucetID
	}

	if l1Wallet != nil {
		l1 := env.Env.L1
		faucetEntry := chainFaucet(l1, l1Wallet)
		cfg.Faucets[types.FaucetID("l1-faucet")] = faucetEntry
		cfg.Defaults[faucetEntry.ChainID] = types.FaucetID("l1-faucet")
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
