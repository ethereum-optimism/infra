package tests

import (
	"context"
	"fmt"
	"math/big"

	nat "github.com/ethereum-optimism/infra/op-nat"
	"github.com/ethereum-optimism/infra/op-nat/network"
	"github.com/ethereum-optimism/infra/op-nat/wallet"
	"github.com/ethereum/go-ethereum/log"
	ethparams "github.com/ethereum/go-ethereum/params"
	"github.com/pkg/errors"
)

type SimpleTranferParams struct {
	// TransferAmount is the amount of eth transferred
	TransferAmount big.Int
	// MinBalance is how much eth is required to run the test
	MinBalance big.Int
}

// SimpleTransfer is a test that runs a transfer on a network
var SimpleTransfer = nat.Test{
	ID: "simple-transfer",
	DefaultParams: SimpleTranferParams{
		TransferAmount: *big.NewInt(1 * ethparams.GWei),
		MinBalance:     *big.NewInt(10 * ethparams.GWei),
	},
	Fn: func(ctx context.Context, cfg nat.Config, params interface{}) (bool, error) {

		p := params.(SimpleTranferParams)
		for _, network := range cfg.GetNetworks() {
			sender, receiver, err := SetupSimpleTransferTest(ctx, network, cfg, p)
			if err != nil {
				return false, err
			}
			pass, err := SimpleTransferTest(ctx, cfg.Log, network, sender, receiver, p)
			if err != nil {
				return false, errors.Wrapf(err, "error executing simple transfer for network: %s", network.Name)
			}

			if !pass {
				return pass, nil
			}
		}
		return true, nil
	},
}

func SetupSimpleTransferTest(ctx context.Context, network *network.Network, cfg nat.Config, p SimpleTranferParams) (*wallet.Wallet, *wallet.Wallet, error) {

	sender, err := cfg.GetWalletWithBalance(ctx, network, &p.MinBalance)
	if err != nil {
		return nil, nil, err
	}

	// Ensure receiver is not equal to sender
	for i := 0; i < 5; i++ {
		receiver := cfg.GetRandomWallet()
		if receiver.Address() == sender.Address() {
			continue
		}
		return sender, receiver, nil
	}
	return nil, nil, errors.New("unable to find valid receiver wallet that did not match sender wallet")

}

func SimpleTransferTest(ctx context.Context, log log.Logger, network *network.Network, sender, receiver *wallet.Wallet, p SimpleTranferParams) (bool, error) {
	// Make sure the accounts are unstuck before sending any transactions
	if network == nil || sender == nil || receiver == nil {
		return false, errors.New("error empty arguments provided for SimpleTransferTest")
	}

	senderBalancePre, err := sender.GetBalance(ctx, network)
	if err != nil {
		return false, errors.Wrap(err, "error getting sender balance")
	}

	receiverBalancePre, err := receiver.GetBalance(ctx, network)
	if err != nil {
		return false, errors.Wrap(err, "error getting receiver balance")
	}

	log.Debug("user balances pre simple transfer test",
		"sender", senderBalancePre.String(),
		"sender_addr", sender.Address(),
		"receiver", receiverBalancePre.String(),
		"receiver_addr", receiver.Address(),
		"network", network.Name,
		"transfer_value", p.TransferAmount.String(),
	)

	tx, err := sender.Send(ctx, network, &p.TransferAmount, receiver.Address())
	if err != nil {
		return false, errors.Wrap(err, fmt.Sprintf("error sending simple transfer"+
			"network: %s"+
			"sender: %s"+
			"receiver: %s",
			network.Name,
			sender.Address(),
			receiver.Address(),
		))
	}

	if err := network.PollForConfirmations(ctx, 2, tx.Hash()); err != nil {
		return false, errors.Wrap(err, "error polling for tx confirmation")
	}

	senderBalancePost, err := sender.GetBalance(ctx, network)
	if err != nil {
		return false, errors.Wrap(err, "error getting sender balance")
	}

	receiverBalancePost, err := receiver.GetBalance(ctx, network)
	if err != nil {
		return false, errors.Wrap(err, "error getting receiver balance")
	}

	receiverDiff := new(big.Int)
	receiverDiff.Sub(receiverBalancePost, receiverBalancePre)

	senderPostExpected := new(big.Int)
	senderPostExpected.Sub(senderBalancePre, &p.TransferAmount)

	log.Debug("user balances post simple transfer test",
		"sender_post", senderBalancePost.String(),
		"sender_post_expected", senderPostExpected.String(),
		"receiver_post", receiverBalancePost.String(),
		"receiver_diff", receiverDiff.String(),
		"transfer_amount", p.TransferAmount.String(),
	)

	// TODO: Improve the clarity of these checks
	// If the difference is not the same return error
	// Maybe wrap these in readable functions
	if p.TransferAmount.Cmp(receiverDiff) != 0 {
		return false, errors.New("error receiver balance post transfer was incorrect")
	}

	// If sender post to be greater than senderPostExpected fail the test
	if senderBalancePost.Cmp(senderPostExpected) >= 0 {
		return false, errors.New("error sender balance post transfer was greater than expected")
	}

	return true, nil
}
