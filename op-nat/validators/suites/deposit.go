package suites

import (
	nat "github.com/ethereum-optimism/infra/op-nat"
	"github.com/ethereum-optimism/infra/op-nat/validators/tests"
)

var DepositSuite = nat.Suite{
	ID: "simple-deposit",
	Tests: []nat.Test{
		tests.SimpleDeposit,
	},
	TestsParams: map[string]interface{}{
		tests.SimpleDeposit.ID: tests.SimpleDepositParams{
			MaxBalanceChecks: 24,
		},
	},
}
