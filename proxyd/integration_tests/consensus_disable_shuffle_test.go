package integration_tests

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConsensusDisableShuffle(t *testing.T) {
	nodes, bg, client, shutdown := setupConsensusTest(t, "consensus_disable_shuffle")
	defer nodes["node1"].mockBackend.Close()
	defer nodes["node2"].mockBackend.Close()
	defer shutdown()

	ctx := context.Background()

	// poll for updated consensus
	update := func() {
		for _, be := range bg.Backends {
			bg.Consensus.UpdateBackend(ctx, be)
		}
		bg.Consensus.UpdateBackendGroupConsensus(ctx)
	}

	// convenient methods to manipulate state and mock responses
	reset := func() {
		for _, node := range nodes {
			node.handler.ResetOverrides()
			node.mockBackend.Reset()
			node.backend.ClearSlidingWindows()
		}
		bg.Consensus.ClearListeners()
		bg.Consensus.Reset()

	}

	t.Run("test shuffling is disabled in consensus mode", func(t *testing.T) {
		reset()
		update()

		// reset request counts
		nodes["node1"].mockBackend.Reset()
		nodes["node2"].mockBackend.Reset()

		require.Equal(t, 0, len(nodes["node1"].mockBackend.Requests()))
		require.Equal(t, 0, len(nodes["node2"].mockBackend.Requests()))

		numberReqs := 20
		for numberReqs > 0 {
			_, statusCode, err := client.SendRPC("eth_getBlockByNumber", []interface{}{"0x101", false})
			require.NoError(t, err)
			require.Equal(t, 200, statusCode)
			numberReqs--
		}

		msg := fmt.Sprintf("n1 %d, n2 %d",
			len(nodes["node1"].mockBackend.Requests()), len(nodes["node2"].mockBackend.Requests()))

		// odds of 20 requests going to one node is 1 in 2^20 if shuffling is enabled, thus must be not shuffling
		require.Equal(t, 20, len(nodes["node1"].mockBackend.Requests()), msg)
		require.Equal(t, 0, len(nodes["node2"].mockBackend.Requests()), msg)
	})
}
