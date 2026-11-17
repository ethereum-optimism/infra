package integration_tests

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/stretchr/testify/require"

	ms "github.com/ethereum-optimism/infra/proxyd/tools/mockserver/handler"
)

// TestConsensusOutOfRangeWait covers consensus_out_of_range_wait: a request for
// a block just above the consensus latest block (as a client that saw the block
// on a websocket subscription sends) waits for consensus to reach it instead of
// getting null straight away.
func TestConsensusOutOfRangeWait(t *testing.T) {
	nodes, bg, client, shutdown := setupCustomConfig(t, "consensus_out_of_range_wait")
	defer nodes["node1"].mockBackend.Close()
	defer nodes["node2"].mockBackend.Close()
	defer shutdown()

	ctx := context.Background()

	update := func() {
		for _, be := range bg.Backends {
			bg.Consensus.UpdateBackend(ctx, be)
		}
		bg.Consensus.UpdateBackendGroupConsensus(ctx)
	}

	override := func(node string, method string, block string, response string) {
		nodes[node].handler.AddOverride(&ms.MethodTemplate{
			Method:   method,
			Block:    block,
			Response: response,
		})
	}

	reset := func() {
		for _, node := range nodes {
			node.handler.ResetOverrides()
			node.mockBackend.Reset()
			node.backend.ClearSlidingWindows()
		}
		bg.Consensus.ClearListeners()
		bg.Consensus.Reset()

		// node1 alone in consensus, at latest 0x101
		override("node2", "net_peerCount", "", buildResponse(hexutil.Uint64(0).String()))
		update()
		require.Equal(t, "0x101", bg.Consensus.GetLatestBlockNumber().String())
		require.Equal(t, 1, len(bg.Consensus.GetConsensusGroup()))
		nodes["node1"].mockBackend.Reset()
	}

	// advance moves node1's latest block to 0x102 and polls consensus.
	advance := func() {
		override("node1", "eth_getBlockByNumber", "latest", buildResponse(map[string]string{
			"number": "0x102",
			"hash":   "hash_0x102",
		}))
		update()
	}

	getBlock := func(block string) (map[string]interface{}, time.Duration) {
		start := time.Now()
		resRaw, statusCode, err := client.SendRPC("eth_getBlockByNumber", []interface{}{block, false})
		elapsed := time.Since(start)
		require.NoError(t, err)
		require.Equal(t, 200, statusCode)
		var res map[string]interface{}
		require.NoError(t, json.Unmarshal(resRaw, &res))
		return res, elapsed
	}

	t.Run("serves the block once consensus reaches it", func(t *testing.T) {
		reset()

		go func() {
			time.Sleep(200 * time.Millisecond)
			advance()
		}()

		res, elapsed := getBlock("0x102")
		require.Nil(t, res["error"])
		require.Equal(t, "0x102", res["result"].(map[string]interface{})["number"])
		require.GreaterOrEqual(t, elapsed, 200*time.Millisecond)
		require.Less(t, elapsed, time.Second)
		require.Equal(t, "0x102", bg.Consensus.GetLatestBlockNumber().String())
	})

	t.Run("answers null after the wait when consensus does not reach the block", func(t *testing.T) {
		reset()

		res, elapsed := getBlock("0x102")
		require.Nil(t, res["error"])
		require.Nil(t, res["result"])
		require.GreaterOrEqual(t, elapsed, time.Second)
		require.Equal(t, 0, len(nodes["node1"].mockBackend.Requests()))
	})

	t.Run("does not wait for a block beyond consensus_max_block_lag", func(t *testing.T) {
		reset()

		// 0x10a is 9 blocks above 0x101; consensus_max_block_lag is 8
		res, elapsed := getBlock("0x10a")
		require.Nil(t, res["error"])
		require.Nil(t, res["result"])
		require.Less(t, elapsed, 500*time.Millisecond)
		require.Equal(t, 0, len(nodes["node1"].mockBackend.Requests()))
	})

	t.Run("does not delay requests within consensus", func(t *testing.T) {
		reset()

		res, elapsed := getBlock("latest")
		require.Equal(t, "0x101", res["result"].(map[string]interface{})["number"])
		require.Less(t, elapsed, 500*time.Millisecond)
	})

	t.Run("a batch serves every element once consensus reaches its blocks", func(t *testing.T) {
		reset()

		go func() {
			time.Sleep(200 * time.Millisecond)
			advance()
		}()

		resRaw, statusCode, err := client.SendBatchRPC(
			NewRPCReq("1", "eth_getBlockByNumber", []interface{}{"0x101", false}),
			NewRPCReq("2", "eth_getBlockByNumber", []interface{}{"0x102", false}))
		require.NoError(t, err)
		require.Equal(t, 200, statusCode)

		var res []map[string]interface{}
		require.NoError(t, json.Unmarshal(resRaw, &res))
		require.Equal(t, 2, len(res))
		require.Equal(t, "0x101", res[0]["result"].(map[string]interface{})["number"])
		require.Equal(t, "0x102", res[1]["result"].(map[string]interface{})["number"])
	})

	t.Run("a batch spends one wait across its minibatches", func(t *testing.T) {
		reset()

		start := time.Now()
		resRaw, statusCode, err := client.SendBatchRPC(
			NewRPCReq("1", "eth_getBlockByNumber", []interface{}{"0x102", false}),
			NewRPCReq("2", "eth_getBlockByNumber", []interface{}{"0x103", false}),
			NewRPCReq("3", "eth_getBlockByNumber", []interface{}{"0x104", false}))
		elapsed := time.Since(start)
		require.NoError(t, err)
		require.Equal(t, 200, statusCode)

		var res []map[string]interface{}
		require.NoError(t, json.Unmarshal(resRaw, &res))
		require.Equal(t, 3, len(res))
		for _, r := range res {
			require.Nil(t, r["error"])
			require.Nil(t, r["result"])
		}
		// three minibatches, one 1s budget
		require.GreaterOrEqual(t, elapsed, time.Second)
		require.Less(t, elapsed, 2*time.Second)
	})
}
