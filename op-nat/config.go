package nat

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"math/rand"
	"os"
	"strconv"

	"github.com/urfave/cli/v2"

	"github.com/ethereum-optimism/infra/op-nat/flags"
	"github.com/ethereum-optimism/infra/op-nat/network"
	"github.com/ethereum-optimism/infra/op-nat/wallet"
	"github.com/ethereum/go-ethereum/log"
)

type Config struct {
	// Network config
	SC         SuperchainManifest
	Validators []Validator

	Wallets []*wallet.Wallet

	// Networks
	L1  *network.Network
	L2A *network.Network

	Log log.Logger
}

func NewConfig(ctx *cli.Context, log log.Logger, validators []Validator) (*Config, error) {
	// Parse flags
	if err := flags.CheckRequired(ctx); err != nil {
		return nil, fmt.Errorf("missing required flags: %w", err)
	}

	// Parse kurtosis-devnet manifest
	manifest, err := parseManifest(ctx.String(flags.KurtosisDevnetManifest.Name))
	if err != nil {
		return nil, fmt.Errorf("failed to parse kurtosis-devnet manifest: %w", err)
	}

	wallets := []*wallet.Wallet{}
	for i, w := range manifest.L1.Wallets {
		w, err := wallet.NewWallet(w.PrivateKey, fmt.Sprintf("user-key-%s", i))
		if err != nil {
			log.Warn("error creating wallet: %w", err)
		}
		wallets = append(wallets, w)
	}

	l1ID, err := strconv.Atoi(manifest.L1.ID)
	if err != nil {
		log.Warn("L1 Chain ID was not supplied, will skip l1 chain-id test")
		l1ID = -1
	}

	l1, err := network.NewNetwork(
		ctx.Context,
		log,
		manifest.L1.Name,
		manifest.L1.Nodes[0].Services.EL.Endpoints["rpc"].Host,
		manifest.L1.Nodes[0].Services.EL.Endpoints["rpc"].Secure,
		big.NewInt(int64(l1ID)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to setup l1 network: %w", err)
	}

	l2AID, err := strconv.Atoi(manifest.L2[0].ID)
	if err != nil {
		log.Warn("L2A Chain ID was not supplied, will skip l2A chain-id test")
		l2AID = -1
	}

	l2A, err := network.NewNetwork(
		ctx.Context,
		log,
		manifest.L2[0].Name,
		manifest.L2[0].Nodes[0].Services.EL.Endpoints["rpc"].Host,
		manifest.L2[0].Nodes[0].Services.EL.Endpoints["rpc"].Secure,
		big.NewInt(int64(l2AID)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to setup l2A network: %w", err)
	}

	return &Config{
		SC:         *manifest,
		Validators: validators,
		L1:         l1,
		L2A:        l2A,
		Wallets:    wallets,
		Log:        log,
	}, nil
}

func parseManifest(manifestPath string) (*SuperchainManifest, error) {
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest file: %w", err)
	}

	var superchainManifest SuperchainManifest
	if err := json.Unmarshal(manifest, &superchainManifest); err != nil {
		return nil, fmt.Errorf("failed to unmarshal manifest: %w", err)
	}
	return &superchainManifest, nil

}

// GetNetworks returns all of the networks in an array
func (c *Config) GetNetworks() []*network.Network {
	return []*network.Network{
		c.L1,
		c.L2A,
	}
}

func (c *Config) GetRandomWallet() *wallet.Wallet {
	randomIndex := rand.Intn(len(c.Wallets))
	return c.Wallets[randomIndex]
}

// GetWalletWithBalance is used to find a wallet with a balance of atleast amount on a network
func (c *Config) GetWalletWithBalance(ctx context.Context, network *network.Network, amount *big.Int) (*wallet.Wallet, error) {
	for _, w := range c.Wallets {
		balance, err := w.GetBalance(ctx, network)
		if err != nil {
			log.Error("error getting wallet balance",
				"wallet", w.Address(),
				"network", network.Name,
			)
			return nil, err
		}
		log.Info("",
			"wallet", w.Address(),
			"balance", balance,
			"network", network.Name,
		)
		// balance >= amount will return 0 or 1
		if balance.Cmp(amount) >= 0 {
			return w, nil
		}
	}
	return nil, fmt.Errorf("no wallet found with balance %d on network %s",
		amount,
		network.Name,
	)
}
