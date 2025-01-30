package network

import (
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/log"
	"golang.org/x/net/context"
)

type Network struct {
	ChainID *big.Int
	Name    string
	// Addr is the Address the client is dialing
	Addr string
	RPC  *ethclient.Client
	log  log.Logger
}

func NewNetwork(ctx context.Context, log log.Logger, name, addr string, secure bool, chainId *big.Int) (*Network, error) {

	prefix := "https://"
	if !secure {
		prefix = "http://"
	}

	rpcEndpoint := fmt.Sprintf("%s%s", prefix, addr)

	log.Debug("Creating new network",
		"name", name,
		"chain_id", chainId.String(),
		"rpc", rpcEndpoint,
		"secure", secure,
	)
	client, err := ethclient.Dial(rpcEndpoint)
	if err != nil {
		log.Error("error creating ethclient for network",
			"network", name,
			"error", err,
		)
		return nil, err
	}
	return &Network{
		RPC:     client,
		Addr:    rpcEndpoint,
		Name:    name,
		log:     log,
		ChainID: chainId,
	}, nil

}

// DumpInfo will print a current networks information
func (n *Network) DumpInfo(ctx context.Context) error {
	block, err := n.RPC.BlockNumber(ctx)
	if err != nil {
		n.log.Error("error retreving block",
			"network", n.Name,
			"err", err)
	}
	chainID, err := n.RPC.ChainID(ctx)
	if err != nil {
		n.log.Error("error retreving block",
			"network", n.Name,
			"err", err)
	}
	n.log.Info("Network Dump", "network", n.Name)
	n.log.Info("Current block", "block", block)
	n.log.Info("ChainID", "chain-id", chainID.String())
	return nil
}

// PollForConfirmations waits until the transaction identified by `txhash` has been confirmed
// at least `confs` times on the network. It continuously polls the network for the transaction's
// confirmation status and returns an error if the context is canceled, the transaction is not found,
// or the required number of confirmations is not achieved within the context's timeout.
//
// Parameters:
//   - ctx:    Context for cancellation and timeout.
//   - confs:  The minimum number of confirmations required for the transaction to be considered confirmed.
//   - txhash: The hash of the transaction to monitor.
//
// Returns:
//   - error:  An error if the transaction fails to achieve the required confirmations, the context is canceled,
//     or the transaction is not found on the network.
func (n *Network) PollForConfirmations(ctx context.Context, confs int, txhash common.Hash) error {
	for i := 0; i < 10; i++ {
		receipt, err := n.RPC.TransactionReceipt(context.Background(), txhash)
		if err != nil {
			n.log.Warn("error getting tx receipt for tx",
				"tx", txhash.String(),
				"error", err,
			)
		}

		// Check if the transaction has been mined
		if receipt != nil {
			// Get the current block number
			blockNumber, err := n.RPC.BlockNumber(context.Background())
			if err != nil {
				n.log.Error("error getting block number",
					"tx", txhash.String(),
					"error", err,
				)
			}

			// Calculate the number of confirmations
			confirmations := blockNumber - receipt.BlockNumber.Uint64()
			n.log.Debug("current confirmations",
				"confirmations", confirmations,
				"required_confirmations", confs,
				"tx", txhash,
			)

			// Check if the required number of confirmations has been reached
			if confirmations >= uint64(confs) {
				n.log.Debug("transaction confirmed",
					"tx", txhash,
					"confs", confirmations,
				)
				return nil
			}
		} else {
			n.log.Debug("transaction not yet mined",
				"tx", txhash,
			)
		}
		// Wait for a certain duration before polling again
		time.Sleep(10 * time.Second)
	}
	return errors.New("tx was not confirmed")
}
