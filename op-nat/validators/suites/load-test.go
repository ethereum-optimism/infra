package suites

import (
	"math/big"

	nat "github.com/ethereum-optimism/infra/op-nat"
	"github.com/ethereum-optimism/infra/op-nat/validators/tests"
	ethparams "github.com/ethereum/go-ethereum/params"
)

var LoadTest = nat.Suite{
	ID: "load-test",
	Tests: []nat.Test{
		tests.TxFuzz,
	},
	TestsParams: map[string]interface{}{
		"tx-fuzz": tests.TxFuzzParams{
			NSlotsToRunFor:     3,
			TxPerAccount:       2,
			GenerateAccessList: false,
			MinBalance:         big.NewInt(10 * ethparams.GWei),
		},
	},
}
