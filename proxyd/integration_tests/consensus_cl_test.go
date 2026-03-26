package integration_tests

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path"
	"testing"

	"github.com/ethereum/go-ethereum/common/hexutil"

	"github.com/ethereum-optimism/infra/proxyd"
	ms "github.com/ethereum-optimism/infra/proxyd/tools/mockserver/handler"
	"github.com/stretchr/testify/require"
)

func setupCL(t *testing.T) (map[string]nodeContext, *proxyd.BackendGroup, *ProxydHTTPClient, func()) {
	node1 := NewMockBackend(nil)
	node2 := NewMockBackend(nil)

	dir, err := os.Getwd()
	require.NoError(t, err)

	responses := path.Join(dir, "testdata/consensus_cl_responses.yml")

	h1 := ms.MockedHandler{
		Overrides:    []*ms.MethodTemplate{},
		Autoload:     true,
		AutoloadFile: responses,
	}
	h2 := ms.MockedHandler{
		Overrides:    []*ms.MethodTemplate{},
		Autoload:     true,
		AutoloadFile: responses,
	}

	require.NoError(t, os.Setenv("NODE1_URL", node1.URL()))
	require.NoError(t, os.Setenv("NODE2_URL", node2.URL()))

	node1.SetHandler(http.HandlerFunc(h1.Handler))
	node2.SetHandler(http.HandlerFunc(h2.Handler))

	config := ReadConfig("consensus_cl")
	svr, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)

	client := NewProxydClient("http://127.0.0.1:8545")

	bg := svr.BackendGroups["node"]
	require.NotNil(t, bg)
	require.NotNil(t, bg.Consensus)
	require.Equal(t, 2, len(bg.Backends))

	nodes := map[string]nodeContext{
		"node1": {
			mockBackend: node1,
			backend:     bg.Backends[0],
			handler:     &h1,
		},
		"node2": {
			mockBackend: node2,
			backend:     bg.Backends[1],
			handler:     &h2,
		},
	}

	return nodes, bg, client, shutdown
}

// clSyncStatus builds a full optimism_syncStatus result map with the given unsafe_l2 values.
// safe/finalized/L1 fields use the default test values.
// local_safe_l2 defaults to the same values as safe_l2 (non-interop behaviour).
func clSyncStatus(unsafeNum float64, unsafeHash string) map[string]interface{} {
	return map[string]interface{}{
		"unsafe_l2": map[string]interface{}{
			"hash":   unsafeHash,
			"number": unsafeNum,
		},
		"safe_l2": map[string]interface{}{
			"hash":   "hash_0xe1",
			"number": float64(225),
		},
		"local_safe_l2": map[string]interface{}{
			"hash":   "hash_0xe1",
			"number": float64(225),
		},
		"finalized_l2": map[string]interface{}{
			"hash":   "hash_0xc1",
			"number": float64(193),
		},
		"current_l1": map[string]interface{}{
			"hash":      "hash_l1_100",
			"number":    float64(100),
			"timestamp": float64(9999999999),
		},
		"head_l1": map[string]interface{}{
			"hash":      "hash_l1_100",
			"number":    float64(100),
			"timestamp": float64(9999999999),
		},
	}
}

// clSyncStatusOutOfSync builds a syncStatus where L1 lag exceeds the default threshold (10).
func clSyncStatusOutOfSync(unsafeNum float64, unsafeHash string) map[string]interface{} {
	s := clSyncStatus(unsafeNum, unsafeHash)
	s["head_l1"] = map[string]interface{}{
		"hash":      "hash_l1_111",
		"number":    float64(111), // lag = 111 - 100 = 11 > 10 (default threshold)
		"timestamp": float64(9999999999),
	}
	return s
}

// clSyncStatusStaleL1 builds a syncStatus where head_l1 timestamp is very old, triggering staleness check.
func clSyncStatusStaleL1(unsafeNum float64, unsafeHash string) map[string]interface{} {
	s := clSyncStatus(unsafeNum, unsafeHash)
	s["head_l1"] = map[string]interface{}{
		"hash":      "hash_l1_100",
		"number":    float64(100),
		"timestamp": float64(1000), // Unix timestamp far in the past → stale
	}
	return s
}

// clSyncStatusInitializing builds a syncStatus where all fields are zero,
// as returned by an op-node that has just restarted and hasn't connected to L1 yet.
func clSyncStatusInitializing() map[string]interface{} {
	zero := map[string]interface{}{"hash": "0x0000000000000000000000000000000000000000000000000000000000000000", "number": float64(0), "timestamp": float64(0)}
	return map[string]interface{}{
		"unsafe_l2":     zero,
		"safe_l2":       zero,
		"local_safe_l2": zero,
		"finalized_l2":  zero,
		"current_l1":    zero,
		"head_l1":       zero,
	}
}

func TestConsensusCL(t *testing.T) {
	nodes, bg, client, shutdown := setupCL(t)
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

	reset := func() {
		for _, node := range nodes {
			node.handler.ResetOverrides()
			node.mockBackend.Reset()
			node.backend.ClearSlidingWindows()
		}
		bg.Consensus.ClearListeners()
		bg.Consensus.Reset()
	}

	override := func(node string, method string, block string, response string) {
		if _, ok := nodes[node]; !ok {
			t.Fatalf("node %s does not exist in the nodes map", node)
		}
		nodes[node].handler.AddOverride(&ms.MethodTemplate{
			Method:   method,
			Block:    block,
			Response: response,
		})
	}

	overrideSyncStatus := func(node string, unsafeNum float64, unsafeHash string) {
		override(node, "optimism_syncStatus", "", buildResponse(clSyncStatus(unsafeNum, unsafeHash)))
	}

	overridePeerCount := func(node string, count int) {
		override(node, "opp2p_peerStats", "", buildResponse(map[string]interface{}{
			"connected": float64(count),
		}))
	}

	overrideNotInSync := func(node string) {
		nodes[node].handler.AddOverride(&ms.MethodTemplate{
			Method:   "optimism_syncStatus",
			Block:    "",
			Response: buildResponse(clSyncStatusOutOfSync(257, "hash_0x101")),
		})
	}

	overrideNotInSyncByTimestamp := func(node string) {
		nodes[node].handler.AddOverride(&ms.MethodTemplate{
			Method:   "optimism_syncStatus",
			Block:    "",
			Response: buildResponse(clSyncStatusStaleL1(257, "hash_0x101")),
		})
	}

	// overrideSyncStatusFull overrides safe_l2, local_safe_l2, and finalized_l2 numbers in addition to unsafe_l2.
	// local_safe_l2 defaults to the same values as safe_l2 (non-interop behaviour).
	overrideSyncStatusFull := func(node string, unsafeNum float64, unsafeHash string, safeNum float64, finalizedNum float64) {
		s := clSyncStatus(unsafeNum, unsafeHash)
		s["safe_l2"] = map[string]interface{}{
			"hash":   "hash_safe",
			"number": safeNum,
		}
		s["local_safe_l2"] = map[string]interface{}{
			"hash":   "hash_safe",
			"number": safeNum,
		}
		s["finalized_l2"] = map[string]interface{}{
			"hash":   "hash_finalized",
			"number": finalizedNum,
		}
		override(node, "optimism_syncStatus", "", buildResponse(s))
	}

	// outputAtBlock builds the JSON response for an optimism_outputAtBlock call.
	outputAtBlock := func(num float64, hash string) string {
		return buildResponse(map[string]interface{}{
			"blockRef": map[string]interface{}{"number": num, "hash": hash},
		})
	}

	t.Run("initial consensus", func(t *testing.T) {
		reset()

		require.Equal(t, "0x0", bg.Consensus.GetLatestBlockNumber().String())

		update()

		// Default responses: unsafe_l2 at 0x101 (257), safe 0xe1, finalized 0xc1
		require.Equal(t, "0x101", bg.Consensus.GetLatestBlockNumber().String())
		require.Equal(t, "0xe1", bg.Consensus.GetSafeBlockNumber().String())
		require.Equal(t, "0xc1", bg.Consensus.GetFinalizedBlockNumber().String())
		require.Equal(t, 2, len(bg.Consensus.GetConsensusGroup()))
	})

	t.Run("prevent using a backend with low peer count", func(t *testing.T) {
		reset()
		overridePeerCount("node1", 0)
		update()

		consensusGroup := bg.Consensus.GetConsensusGroup()
		require.NotContains(t, consensusGroup, nodes["node1"].backend)
		require.False(t, bg.Consensus.IsBanned(nodes["node1"].backend))
		require.Equal(t, 1, len(consensusGroup))
	})

	t.Run("prevent using a backend not in sync", func(t *testing.T) {
		reset()
		overrideNotInSync("node1")
		update()

		consensusGroup := bg.Consensus.GetConsensusGroup()
		require.NotContains(t, consensusGroup, nodes["node1"].backend)
		require.False(t, bg.Consensus.IsBanned(nodes["node1"].backend))
		require.Equal(t, 1, len(consensusGroup))
	})

	t.Run("prevent using a backend with stale L1 head", func(t *testing.T) {
		reset()
		overrideNotInSyncByTimestamp("node1")
		update()

		consensusGroup := bg.Consensus.GetConsensusGroup()
		require.NotContains(t, consensusGroup, nodes["node1"].backend)
		require.False(t, bg.Consensus.IsBanned(nodes["node1"].backend))
		require.Equal(t, 1, len(consensusGroup))
	})

	t.Run("prevent using a backend that is initializing (all-zero syncStatus)", func(t *testing.T) {
		reset()
		// Simulate a node that just restarted: all block numbers and timestamps are zero.
		// The backend should be excluded from consensus (not banned) and the metric
		// should reflect out-of-sync, not in-sync.
		override("node1", "optimism_syncStatus", "", buildResponse(clSyncStatusInitializing()))
		update()

		consensusGroup := bg.Consensus.GetConsensusGroup()
		require.NotContains(t, consensusGroup, nodes["node1"].backend)
		require.False(t, bg.Consensus.IsBanned(nodes["node1"].backend))
		require.Equal(t, 1, len(consensusGroup))
		// node2 still healthy; consensus at default values
		require.Equal(t, "0x101", bg.Consensus.GetLatestBlockNumber().String())
	})

	t.Run("one node ahead - consensus resolves to lower", func(t *testing.T) {
		reset()

		// node2 one block ahead; lowest is node1 at 0x101
		overrideSyncStatus("node2", 258, "hash_0x102")
		update()

		require.Equal(t, "0x101", bg.Consensus.GetLatestBlockNumber().String())
		require.Equal(t, 2, len(bg.Consensus.GetConsensusGroup()))
	})

	t.Run("lagging backend excluded", func(t *testing.T) {
		reset()

		// node2 is maxBlockLag+1 (9) blocks ahead of node1 at 0x101 → 0x10a (266)
		overrideSyncStatus("node2", 266, "hash_0x10a")
		update()

		// node1 is lagging, excluded
		consensusGroup := bg.Consensus.GetConsensusGroup()
		require.NotContains(t, consensusGroup, nodes["node1"].backend)
		require.False(t, bg.Consensus.IsBanned(nodes["node1"].backend))
		require.Equal(t, 1, len(consensusGroup))

		// consensus is at node2's block since node1 is excluded
		require.Equal(t, "0x10a", bg.Consensus.GetLatestBlockNumber().String())
	})

	t.Run("lagging backend not excluded when within maxBlockLag", func(t *testing.T) {
		reset()

		// node2 is exactly maxBlockLag (8) blocks ahead: 0x101 + 8 = 0x109 (265)
		overrideSyncStatus("node2", 265, "hash_0x109")
		update()

		// both should be in the consensus group since lag = 8 = maxBlockLag (not exceeded)
		require.Equal(t, 2, len(bg.Consensus.GetConsensusGroup()))
		// consensus at node1's block (lowest)
		require.Equal(t, "0x101", bg.Consensus.GetLatestBlockNumber().String())
	})

	t.Run("broken consensus - hash divergence walks back to agreeable block", func(t *testing.T) {
		reset()
		listenerCalled := false
		bg.Consensus.AddListener(func() {
			listenerCalled = true
		})

		update()
		require.Equal(t, "0x101", bg.Consensus.GetLatestBlockNumber().String())

		// advance both nodes to 0x102
		overrideSyncStatus("node1", 258, "hash_0x102")
		overrideSyncStatus("node2", 258, "hash_0x102")
		// provide outputAtBlock for 0x101 so walk-back can verify agreement there
		override("node1", "optimism_outputAtBlock", "0x102", buildResponse(map[string]interface{}{
			"blockRef": map[string]interface{}{"number": float64(258), "hash": "hash_0x102"},
		}))
		override("node2", "optimism_outputAtBlock", "0x102", buildResponse(map[string]interface{}{
			"blockRef": map[string]interface{}{"number": float64(258), "hash": "hash_0x102"},
		}))
		update()
		require.Equal(t, "0x102", bg.Consensus.GetLatestBlockNumber().String())

		// node2 diverges: same block number but different hash
		overrideSyncStatus("node2", 258, "wrong_hash_0x102")
		// walk-back will query optimism_outputAtBlock("0x101") on both nodes;
		// the default YAML response returns "hash_0x101" — so both agree at 0x101
		update()

		// consensus walks back to 0x101 where hashes agree
		require.Equal(t, "0x101", bg.Consensus.GetLatestBlockNumber().String())
		require.True(t, listenerCalled)

		// both backends still in the consensus group (no ban for hash divergence)
		consensusGroup := bg.Consensus.GetConsensusGroup()
		require.Equal(t, 2, len(consensusGroup))
	})

	t.Run("rewrite response of optimism_syncStatus", func(t *testing.T) {
		reset()
		update()

		require.Equal(t, "0x101", bg.Consensus.GetLatestBlockNumber().String())
		require.Equal(t, "hash_0x101", bg.Consensus.GetLatestBlockHash())
		require.Equal(t, "0xe1", bg.Consensus.GetSafeBlockNumber().String())
		require.Equal(t, "hash_0xe1", bg.Consensus.GetSafeBlockHash())

		resRaw, statusCode, err := client.SendRPC("optimism_syncStatus", nil)
		require.NoError(t, err)
		require.Equal(t, 200, statusCode)

		var jsonMap map[string]interface{}
		err = json.Unmarshal(resRaw, &jsonMap)
		require.NoError(t, err)

		result, ok := jsonMap["result"].(map[string]interface{})
		require.True(t, ok, "result should be an object")

		unsafeL2, ok := result["unsafe_l2"].(map[string]interface{})
		require.True(t, ok, "unsafe_l2 should be an object")
		require.Equal(t, hexutil.Uint64(257).String(), hexutil.Uint64(uint64(unsafeL2["number"].(float64))).String())
		require.Equal(t, "hash_0x101", unsafeL2["hash"])

		safeL2, ok := result["safe_l2"].(map[string]interface{})
		require.True(t, ok, "safe_l2 should be an object")
		require.Equal(t, hexutil.Uint64(225).String(), hexutil.Uint64(uint64(safeL2["number"].(float64))).String())
		require.Equal(t, "hash_0xe1", safeL2["hash"])

		localSafeL2, ok := result["local_safe_l2"].(map[string]interface{})
		require.True(t, ok, "local_safe_l2 should be an object")
		require.Equal(t, hexutil.Uint64(225).String(), hexutil.Uint64(uint64(localSafeL2["number"].(float64))).String())
		require.Equal(t, "hash_0xe1", localSafeL2["hash"])

		finalizedL2, ok := result["finalized_l2"].(map[string]interface{})
		require.True(t, ok, "finalized_l2 should be an object")
		require.Equal(t, hexutil.Uint64(193).String(), hexutil.Uint64(uint64(finalizedL2["number"].(float64))).String())
		require.Equal(t, "hash_0xc1", finalizedL2["hash"])

		// L1 refs pass through from backend
		require.NotNil(t, result["current_l1"])
		require.NotNil(t, result["head_l1"])
	})

	t.Run("rewrite uses consensus values even when individual backends differ", func(t *testing.T) {
		reset()

		// node2 is one block ahead at 0x102
		overrideSyncStatus("node2", 258, "hash_0x102")
		update()

		// consensus is at 0x101 (the lower of the two)
		require.Equal(t, "0x101", bg.Consensus.GetLatestBlockNumber().String())

		// optimism_syncStatus response should reflect consensus (0x101), not individual backend value
		resRaw, statusCode, err := client.SendRPC("optimism_syncStatus", nil)
		require.NoError(t, err)
		require.Equal(t, 200, statusCode)

		var jsonMap map[string]interface{}
		err = json.Unmarshal(resRaw, &jsonMap)
		require.NoError(t, err)

		result := jsonMap["result"].(map[string]interface{})
		unsafeL2 := result["unsafe_l2"].(map[string]interface{})
		require.Equal(t, hexutil.Uint64(257).String(), hexutil.Uint64(uint64(unsafeL2["number"].(float64))).String())
		require.Equal(t, "hash_0x101", unsafeL2["hash"])

		safeL2 := result["safe_l2"].(map[string]interface{})
		require.Equal(t, hexutil.Uint64(225).String(), hexutil.Uint64(uint64(safeL2["number"].(float64))).String())
		require.Equal(t, "hash_0xe1", safeL2["hash"])
	})

	t.Run("advance consensus", func(t *testing.T) {
		reset()
		update()

		require.Equal(t, "0x101", bg.Consensus.GetLatestBlockNumber().String())

		// node2 advances one block; consensus holds at node1's lower block
		overrideSyncStatus("node2", 258, "hash_0x102")
		update()
		require.Equal(t, "0x101", bg.Consensus.GetLatestBlockNumber().String())
		require.Equal(t, 2, len(bg.Consensus.GetConsensusGroup()))

		// node1 also advances; both agree at 0x102
		overrideSyncStatus("node1", 258, "hash_0x102")
		update()
		require.Equal(t, "0x102", bg.Consensus.GetLatestBlockNumber().String())
		require.Equal(t, 2, len(bg.Consensus.GetConsensusGroup()))
	})

	t.Run("should use lowest safe and finalized", func(t *testing.T) {
		reset()
		// node2 has higher safe/finalized; consensus uses node1's lower values
		overrideSyncStatusFull("node2", 257, "hash_0x101", 241, 209) // safe=0xf1, finalized=0xd1
		update()

		require.Equal(t, "0xe1", bg.Consensus.GetSafeBlockNumber().String())
		require.Equal(t, "0xc1", bg.Consensus.GetFinalizedBlockNumber().String())
	})

	t.Run("advance safe and finalized", func(t *testing.T) {
		reset()
		// both nodes advance safe and finalized to higher values
		overrideSyncStatusFull("node1", 257, "hash_0x101", 241, 209) // safe=0xf1, finalized=0xd1
		overrideSyncStatusFull("node2", 257, "hash_0x101", 241, 209)
		update()

		require.Equal(t, "0xf1", bg.Consensus.GetSafeBlockNumber().String())
		require.Equal(t, "0xd1", bg.Consensus.GetFinalizedBlockNumber().String())
	})

	t.Run("ban backend if tags are messed - safe < finalized", func(t *testing.T) {
		reset()
		// node1 reports safe (0xa1=161) < finalized (0xc1=193) — ordering violation
		overrideSyncStatusFull("node1", 257, "hash_0x101", 161, 193)
		update()

		require.True(t, bg.Consensus.IsBanned(nodes["node1"].backend))
		consensusGroup := bg.Consensus.GetConsensusGroup()
		require.NotContains(t, consensusGroup, nodes["node1"].backend)
		require.Equal(t, 1, len(consensusGroup))
	})

	t.Run("ban backend if tags are messed - latest < safe", func(t *testing.T) {
		reset()
		// node1 reports latest (0xa1=161) < safe (0xe1=225) — ordering violation
		overrideSyncStatusFull("node1", 161, "hash_0xa1", 225, 193)
		update()

		require.True(t, bg.Consensus.IsBanned(nodes["node1"].backend))
		consensusGroup := bg.Consensus.GetConsensusGroup()
		require.NotContains(t, consensusGroup, nodes["node1"].backend)
		require.Equal(t, 1, len(consensusGroup))
	})

	t.Run("ban backend if safe dropped", func(t *testing.T) {
		reset()
		update() // establishes safe baseline at 0xe1 (225)

		// safe drops from 0xe1 (225) to 0xb1 (177)
		overrideSyncStatusFull("node1", 257, "hash_0x101", 177, 193)
		update()

		require.True(t, bg.Consensus.IsBanned(nodes["node1"].backend))
		consensusGroup := bg.Consensus.GetConsensusGroup()
		require.NotContains(t, consensusGroup, nodes["node1"].backend)
		require.Equal(t, 1, len(consensusGroup))
		// node2 still healthy; consensus safe unchanged
		require.Equal(t, "0xe1", bg.Consensus.GetSafeBlockNumber().String())
	})

	t.Run("ban backend if finalized dropped", func(t *testing.T) {
		reset()
		update() // establishes finalized baseline at 0xc1 (193)

		// finalized drops from 0xc1 (193) to 0xa1 (161)
		overrideSyncStatusFull("node1", 257, "hash_0x101", 225, 161)
		update()

		require.True(t, bg.Consensus.IsBanned(nodes["node1"].backend))
		consensusGroup := bg.Consensus.GetConsensusGroup()
		require.NotContains(t, consensusGroup, nodes["node1"].backend)
		require.Equal(t, 1, len(consensusGroup))
		require.Equal(t, "0xc1", bg.Consensus.GetFinalizedBlockNumber().String())
	})

	t.Run("recover after ban", func(t *testing.T) {
		reset()
		update() // establish baseline

		// cause node1 to be banned via finalized drop
		overrideSyncStatusFull("node1", 257, "hash_0x101", 225, 161)
		update()
		require.True(t, bg.Consensus.IsBanned(nodes["node1"].backend))
		require.Equal(t, 1, len(bg.Consensus.GetConsensusGroup()))

		// unban and restore node1 to healthy state
		bg.Consensus.Unban(nodes["node1"].backend)
		nodes["node1"].handler.ResetOverrides()
		update()

		require.False(t, bg.Consensus.IsBanned(nodes["node1"].backend))
		require.Equal(t, 2, len(bg.Consensus.GetConsensusGroup()))
	})

	t.Run("stays banned if unhealthy state persists after unban", func(t *testing.T) {
		reset()

		// safe (161) < finalized (193): static ordering violation — always fails
		// regardless of prior state, so re-banning works even after Ban() clears block state.
		overrideSyncStatusFull("node1", 257, "hash_0x101", 161, 193)
		update()
		require.True(t, bg.Consensus.IsBanned(nodes["node1"].backend))

		// unban but leave the bad state in place
		bg.Consensus.Unban(nodes["node1"].backend)
		update()

		// should be immediately re-banned
		require.True(t, bg.Consensus.IsBanned(nodes["node1"].backend))
		require.Equal(t, 1, len(bg.Consensus.GetConsensusGroup()))
	})

	t.Run("broken consensus with depth 2", func(t *testing.T) {
		reset()
		listenerCalled := false
		bg.Consensus.AddListener(func() { listenerCalled = true })

		update()
		require.Equal(t, "0x101", bg.Consensus.GetLatestBlockNumber().String())

		// both advance to 0x103
		overrideSyncStatus("node1", 259, "hash_0x103")
		overrideSyncStatus("node2", 259, "hash_0x103")
		override("node1", "optimism_outputAtBlock", "0x101", outputAtBlock(257, "hash_0x101"))
		override("node2", "optimism_outputAtBlock", "0x101", outputAtBlock(257, "hash_0x101"))
		update()
		require.Equal(t, "0x103", bg.Consensus.GetLatestBlockNumber().String())

		// node2 diverges at both 0x103 and 0x102; 0x101 still agrees (YAML default)
		overrideSyncStatus("node2", 259, "wrong_hash_0x103")
		override("node1", "optimism_outputAtBlock", "0x103", outputAtBlock(259, "hash_0x103"))
		override("node2", "optimism_outputAtBlock", "0x103", outputAtBlock(259, "wrong_hash_0x103"))
		override("node1", "optimism_outputAtBlock", "0x102", outputAtBlock(258, "hash_0x102"))
		override("node2", "optimism_outputAtBlock", "0x102", outputAtBlock(258, "wrong_hash_0x102"))
		update()

		// walk-back finds agreement at 0x101
		require.Equal(t, "0x101", bg.Consensus.GetLatestBlockNumber().String())
		require.True(t, listenerCalled)
		require.Equal(t, 2, len(bg.Consensus.GetConsensusGroup()))
	})

	t.Run("fork in advanced block - no prior consensus at new height", func(t *testing.T) {
		reset()
		listenerCalled := false
		bg.Consensus.AddListener(func() { listenerCalled = true })
		update()

		require.Equal(t, "0x101", bg.Consensus.GetLatestBlockNumber().String())

		// both nodes jump to 0x103 but on different forks (never agreed at 0x102 or 0x103)
		overrideSyncStatus("node1", 259, "node1_hash_0x103")
		overrideSyncStatus("node2", 259, "node2_hash_0x103")
		override("node1", "optimism_outputAtBlock", "0x103", outputAtBlock(259, "node1_hash_0x103"))
		override("node2", "optimism_outputAtBlock", "0x103", outputAtBlock(259, "node2_hash_0x103"))
		override("node1", "optimism_outputAtBlock", "0x102", outputAtBlock(258, "node1_hash_0x102"))
		override("node2", "optimism_outputAtBlock", "0x102", outputAtBlock(258, "node2_hash_0x102"))
		// 0x101 uses YAML default — both agree
		update()

		// resolves to last common ancestor (0x101)
		require.Equal(t, "0x101", bg.Consensus.GetLatestBlockNumber().String())
		require.Equal(t, 2, len(bg.Consensus.GetConsensusGroup()))
		// listener NOT fired: we never had prior consensus at 0x103 or 0x102
		require.False(t, listenerCalled)
	})

	t.Run("safe hash divergence - walks back to agreed safe block", func(t *testing.T) {
		reset()

		// node2 reports a different hash at safe block 0xe1 (225)
		s2 := clSyncStatus(257, "hash_0x101")
		s2["safe_l2"] = map[string]interface{}{
			"hash":   "wrong_safe_hash",
			"number": float64(225),
		}
		override("node2", "optimism_syncStatus", "", buildResponse(s2))

		// both agree at 0xe0 (224) one block back
		override("node1", "optimism_outputAtBlock", "0xe0", outputAtBlock(224, "agreed_safe_hash"))
		override("node2", "optimism_outputAtBlock", "0xe0", outputAtBlock(224, "agreed_safe_hash"))

		update()

		// safe walks back to last agreed block
		require.Equal(t, "0xe0", bg.Consensus.GetSafeBlockNumber().String())
		require.Equal(t, "agreed_safe_hash", bg.Consensus.GetSafeBlockHash())
		// unsafe consensus unaffected
		require.Equal(t, "0x101", bg.Consensus.GetLatestBlockNumber().String())
		require.Equal(t, 2, len(bg.Consensus.GetConsensusGroup()))
	})

	t.Run("finalized below consensus - lagging backend excluded from candidates", func(t *testing.T) {
		reset()
		update() // establishes finalized consensus at 0xc1 (193)

		// node1 drops to finalized 0xc0 (192), below the established consensus of 0xc1 (193).
		// FilterCandidates excludes node1 to prevent dragging consensus backward,
		// which would cause unnecessary EL sync cycles on downstream light nodes.
		s1 := clSyncStatus(257, "hash_0x101")
		s1["finalized_l2"] = map[string]interface{}{"hash": "hash_0xc0", "number": float64(192)}
		override("node1", "optimism_syncStatus", "", buildResponse(s1))

		update()

		// node1 excluded; consensus finalized holds at 0xc1 backed by node2 alone
		require.Equal(t, "0xc1", bg.Consensus.GetFinalizedBlockNumber().String())
		require.Equal(t, "hash_0xc1", bg.Consensus.GetFinalizedBlockHash())
		require.Equal(t, 1, len(bg.Consensus.GetConsensusGroup()))
		require.NotContains(t, bg.Consensus.GetConsensusGroup(), nodes["node1"].backend)
		// unsafe and safe consensus unaffected
		require.Equal(t, "0x101", bg.Consensus.GetLatestBlockNumber().String())
		require.Equal(t, "0xe1", bg.Consensus.GetSafeBlockNumber().String())
	})

	t.Run("local_safe below consensus - lagging backend excluded from candidates", func(t *testing.T) {
		reset()
		update() // establishes local_safe consensus at 0xe1 (225)

		// node1 drops to local_safe 0xe0 (224), below the established consensus of 0xe1 (225).
		// FilterCandidates excludes node1 to prevent local_safe from going backward,
		// which would trigger unnecessary EL sync cycles in downstream light nodes via FollowSource.
		s1 := clSyncStatus(257, "hash_0x101")
		s1["local_safe_l2"] = map[string]interface{}{"hash": "hash_0xe0", "number": float64(224)}
		override("node1", "optimism_syncStatus", "", buildResponse(s1))

		update()

		// node1 excluded; consensus local_safe holds at 0xe1 backed by node2 alone
		require.Equal(t, "0xe1", bg.Consensus.GetLocalSafeBlockNumber().String())
		require.Equal(t, "hash_0xe1", bg.Consensus.GetLocalSafeBlockHash())
		require.Equal(t, 1, len(bg.Consensus.GetConsensusGroup()))
		require.NotContains(t, bg.Consensus.GetConsensusGroup(), nodes["node1"].backend)
		// unsafe and safe consensus unaffected
		require.Equal(t, "0x101", bg.Consensus.GetLatestBlockNumber().String())
		require.Equal(t, "0xe1", bg.Consensus.GetSafeBlockNumber().String())
	})

	t.Run("no consensus when both backends are out of sync", func(t *testing.T) {
		reset()
		overrideNotInSync("node1")
		overrideNotInSync("node2")
		update()

		require.Equal(t, 0, len(bg.Consensus.GetConsensusGroup()))
	})

	t.Run("backend recovers from out-of-sync and re-enters consensus", func(t *testing.T) {
		reset()
		overrideNotInSync("node1")
		update()

		require.Equal(t, 1, len(bg.Consensus.GetConsensusGroup()))
		require.NotContains(t, bg.Consensus.GetConsensusGroup(), nodes["node1"].backend)

		// node1 catches up: clear the override, L1 lag is gone
		nodes["node1"].handler.ResetOverrides()
		update()

		require.Equal(t, 2, len(bg.Consensus.GetConsensusGroup()))
		require.Contains(t, bg.Consensus.GetConsensusGroup(), nodes["node1"].backend)
	})

	t.Run("ban backend if safe > local_safe (invalid interop state)", func(t *testing.T) {
		reset()
		// node1 reports safe (0xf1=241) > local_safe (0xe1=225) — invalid on interop chains
		s := clSyncStatus(257, "hash_0x101")
		s["safe_l2"] = map[string]interface{}{"hash": "hash_safe", "number": float64(241)}
		s["local_safe_l2"] = map[string]interface{}{"hash": "hash_local_safe", "number": float64(225)}
		override("node1", "optimism_syncStatus", "", buildResponse(s))
		update()

		require.True(t, bg.Consensus.IsBanned(nodes["node1"].backend))
		consensusGroup := bg.Consensus.GetConsensusGroup()
		require.NotContains(t, consensusGroup, nodes["node1"].backend)
		require.Equal(t, 1, len(consensusGroup))
	})

	t.Run("rewrite local_safe_l2 uses consensus values", func(t *testing.T) {
		reset()
		// node2 has a higher local_safe; consensus picks the lower (node1's) value
		s2 := clSyncStatus(257, "hash_0x101")
		s2["local_safe_l2"] = map[string]interface{}{"hash": "hash_local_safe_high", "number": float64(240)}
		override("node2", "optimism_syncStatus", "", buildResponse(s2))
		update()

		require.Equal(t, "0xe1", bg.Consensus.GetLocalSafeBlockNumber().String())
		require.Equal(t, "hash_0xe1", bg.Consensus.GetLocalSafeBlockHash())

		resRaw, statusCode, err := client.SendRPC("optimism_syncStatus", nil)
		require.NoError(t, err)
		require.Equal(t, 200, statusCode)

		var jsonMap map[string]interface{}
		err = json.Unmarshal(resRaw, &jsonMap)
		require.NoError(t, err)

		result := jsonMap["result"].(map[string]interface{})
		localSafeL2, ok := result["local_safe_l2"].(map[string]interface{})
		require.True(t, ok)
		require.Equal(t, hexutil.Uint64(225).String(), hexutil.Uint64(uint64(localSafeL2["number"].(float64))).String())
		require.Equal(t, "hash_0xe1", localSafeL2["hash"])
	})

	t.Run("rewrite finalized_l2 uses consensus values", func(t *testing.T) {
		reset()
		// node2 has a higher finalized; consensus picks node1's lower value
		s2 := clSyncStatus(257, "hash_0x101")
		s2["finalized_l2"] = map[string]interface{}{"hash": "hash_finalized_high", "number": float64(210)}
		override("node2", "optimism_syncStatus", "", buildResponse(s2))
		update()

		require.Equal(t, "0xc1", bg.Consensus.GetFinalizedBlockNumber().String())
		require.Equal(t, "hash_0xc1", bg.Consensus.GetFinalizedBlockHash())

		resRaw, statusCode, err := client.SendRPC("optimism_syncStatus", nil)
		require.NoError(t, err)
		require.Equal(t, 200, statusCode)

		var jsonMap map[string]interface{}
		err = json.Unmarshal(resRaw, &jsonMap)
		require.NoError(t, err)

		result := jsonMap["result"].(map[string]interface{})
		finalizedL2, ok := result["finalized_l2"].(map[string]interface{})
		require.True(t, ok)
		require.Equal(t, hexutil.Uint64(193).String(), hexutil.Uint64(uint64(finalizedL2["number"].(float64))).String())
		require.Equal(t, "hash_0xc1", finalizedL2["hash"])
	})
}
