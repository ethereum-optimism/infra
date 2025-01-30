package tests

import (
	"context"
	"fmt"

	nat "github.com/ethereum-optimism/infra/op-nat"
)

// NetworkGetBlockNumber is a test that checks if RPC call `BlockNumber` is working.
var NetworkGetBlockNumber = nat.Test{
	ID: "network-get-block-number",
	Fn: func(ctx context.Context, config nat.Config, _ interface{}) (bool, error) {

		for _, network := range config.GetNetworks() {
			config.Log.Debug("getting block number on network",
				"network", network.Name,
			)
			blockID, err := network.RPC.BlockNumber(ctx)
			if err != nil {
				return false, fmt.Errorf("failed to get block number")
			}
			config.Log.Debug("successfully got the block number", "blockNumber", blockID)
		}
		return true, nil
	},
}
