package suites

import (
	nat "github.com/ethereum-optimism/infra/op-nat"
	"github.com/ethereum-optimism/infra/op-nat/validators/tests"
)

var Network = nat.Suite{
	ID: "network",
	Tests: []nat.Test{
		tests.NetworkGetBlockNumber,
		tests.NetworkGetChainID,
	},
}
