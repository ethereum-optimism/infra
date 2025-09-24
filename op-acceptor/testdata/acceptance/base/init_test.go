package base

import (
	"testing"

	apreset "github.com/ethereum-optimism/infra/op-acceptor/testdata/acceptance/internal/preset"
	"github.com/ethereum-optimism/optimism/op-devstack/presets"
)

func TestMain(m *testing.M) {
	// Use an embedded minimal preset that never registers local contract sources
	presets.DoMain(m, apreset.WithMinimalEmbedded())
}
