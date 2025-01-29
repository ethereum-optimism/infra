package tests

import (
	"context"
	"fmt"
	"math/big"

	nat "github.com/ethereum-optimism/infra/op-nat"
	"github.com/ethereum-optimism/infra/op-nat/network"
	"github.com/ethereum/go-ethereum/log"
)

// NetworkGetChainID is a test that checks if the RPC call `ChainID` is working.
var NetworkGetChainID = nat.Test{
	ID: "network-get-chainid",
	Fn: func(ctx context.Context, log log.Logger, config nat.Config, _ interface{}) (bool, error) {

		for _, network := range config.GetNetworks() {

			if (network.ChainID).Cmp(big.NewInt(0)) < 0 {
				log.Info("chain_id is not set skipping/failing chain_id test",
					"chain_id", network.ChainID,
					"network", network.Name,
				)
				return false, nil
			}
			log.Info("validating network",
				"addr", network.Addr,
				"chain_id", network.ChainID,
			)
			if ok, err := ValidateChainID(ctx, network); !ok || err != nil {
				return false, err
			}

		}
		return true, nil

	},
}

func ValidateChainID(ctx context.Context, network *network.Network) (bool, error) {
	chainID, err := network.RPC.ChainID(ctx)
	if err != nil {
		return false, fmt.Errorf("error requesting chain id from network %s. Error: %w",
			network.Name,
			err,
		)
	}
	if chainID == nil || (*network.ChainID).Cmp(chainID) != 0 {
		return false, fmt.Errorf("failed to get expected chain id for network %s. Expected: %s. Got: %s",
			network.Name,
			network.ChainID.String(),
			chainID.String(),
		)
	}
	log.Debug("successfully got the chain id", "chain_id", chainID.String())

	return true, nil
}
