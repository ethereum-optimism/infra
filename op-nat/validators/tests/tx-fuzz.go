package tests

import (
	"context"
	"crypto/ecdsa"
	"math/big"
	"math/rand"
	"time"

	nat "github.com/ethereum-optimism/infra/op-nat"
	"github.com/ethereum/go-ethereum/log"
	ethparams "github.com/ethereum/go-ethereum/params"
	"github.com/pkg/errors"
	"github.com/scharissis/tx-fuzz/spammer"
)

type TxFuzzParams struct {
	NSlotsToRunFor     int
	TxPerAccount       uint64
	GenerateAccessList bool
	MinBalance         *big.Int
}

// TxFuzz is a test that runs tx-fuzz.
// It runs 3 slots of spam, with 1 transaction per account.
var TxFuzz = nat.Test{
	ID: "tx-fuzz",
	DefaultParams: TxFuzzParams{
		NSlotsToRunFor:     120, // Duration of the fuzzing
		TxPerAccount:       3,
		GenerateAccessList: false,
		MinBalance:         big.NewInt(10 * ethparams.GWei),
	},
	Fn: func(ctx context.Context, log log.Logger, cfg nat.Config, params interface{}) (bool, error) {
		p := params.(TxFuzzParams)
		err := runBasicSpam(ctx, log, cfg, p)
		if err != nil {
			return false, err
		}
		return true, nil
	},
}

func runBasicSpam(ctx context.Context, log log.Logger, config nat.Config, params TxFuzzParams) error {
	fuzzCfg, err := newConfig(ctx, log, config, params)
	if err != nil {
		return err
	}

	airdropValue := big.NewInt(1 * ethparams.Wei)
	return spam(fuzzCfg, spammer.SendBasicTransactions, airdropValue, params)
}

func spam(config *spammer.Config, spamFn spammer.Spam, airdropValue *big.Int, params TxFuzzParams) error {
	// Make sure the accounts are unstuck before sending any transactions
	if err := spammer.Unstuck(config); err != nil {
		return err
	}

	for nSlots := 0; nSlots < params.NSlotsToRunFor; nSlots++ {
		if err := spammer.Airdrop(config, airdropValue); err != nil {
			return err
		}
		if err := spammer.SpamTransactions(config, spamFn); err != nil {
			return err
		}
		time.Sleep(time.Duration(config.SlotTime) * time.Second)
	}
	return nil
}

func newConfig(ctx context.Context, log log.Logger, c nat.Config, p TxFuzzParams) (*spammer.Config, error) {
	txPerAccount := p.TxPerAccount
	genAccessList := p.GenerateAccessList

	sender, err := c.GetWalletWithBalance(ctx, c.L2A, p.MinBalance)
	if err != nil {
		log.Error("failed unable to find sender for tx spam",
			"network", c.L2A.Name,
			"min_balance", p.MinBalance,
		)
		return nil, errors.Wrap(err, "failed to find sender with min balance")
	}

	cfg, err := spammer.NewDefaultConfig(c.L2A.Addr, txPerAccount, genAccessList, rand.New(rand.NewSource(time.Now().UnixNano())))
	if err != nil {
		return nil, err
	}

	privKeys := []*ecdsa.PrivateKey{}
	for _, w := range c.GetAllWallets() {
		privKeys = append(privKeys, w.PrivateKeyESCDA)

	}
	cfg = cfg.WithFaucet(sender.PrivateKeyESCDA).WithKeys(privKeys)

	return cfg, nil
}
