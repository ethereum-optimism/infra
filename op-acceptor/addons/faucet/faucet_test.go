package faucet

import (
	"testing"

	"github.com/ethereum-optimism/optimism/devnet-sdk/descriptors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetELRPC(t *testing.T) {
	t.Run("proxyd scenario", func(t *testing.T) {
		chain := &descriptors.Chain{
			Services: map[string][]*descriptors.Service{
				"proxyd": {
					{
						Name: "proxyd",
						Endpoints: map[string]*descriptors.PortInfo{
							"http": {
								Host: "localhost",
								Port: 8545,
							},
						},
					},
				},
			},
		}

		// Call getELRPC
		rpcURL, err := getELRPC(chain)

		// Check results
		require.NoError(t, err)
		assert.Equal(t, "http://localhost:8545", rpcURL)
	})

	t.Run("el node scenario", func(t *testing.T) {
		node := descriptors.Node{
			Services: map[string]*descriptors.Service{
				"el": {
					Name: "geth",
					Endpoints: map[string]*descriptors.PortInfo{
						"rpc": {
							Host: "127.0.0.1",
							Port: 8545,
						},
					},
				},
			},
		}

		chain := &descriptors.Chain{
			Nodes: []descriptors.Node{node},
		}

		// Call getELRPC
		rpcURL, err := getELRPC(chain)

		// Check results
		require.NoError(t, err)
		assert.Equal(t, "http://127.0.0.1:8545", rpcURL)
	})
}
