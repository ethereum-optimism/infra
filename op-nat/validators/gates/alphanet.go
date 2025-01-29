package gates

import (
	nat "github.com/ethereum-optimism/infra/op-nat"
	"github.com/ethereum-optimism/infra/op-nat/validators/suites"
)

var Alphanet = nat.Gate{
	ID: "alphanet",
	Validators: []nat.Validator{
		suites.LoadTest,
		suites.Network,
		suites.DepositSuite,
		suites.SimpleTransfer,
	},
}
