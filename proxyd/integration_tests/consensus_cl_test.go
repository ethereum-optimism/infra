package integration_tests

import (
	"context"
	"encoding/json"
	"fmt"
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
	node3 := NewMockBackend(nil)

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
	h3 := ms.MockedHandler{
		Overrides:    []*ms.MethodTemplate{},
		Autoload:     true,
		AutoloadFile: responses,
	}

	require.NoError(t, os.Setenv("NODE1_URL", node1.URL()))
	require.NoError(t, os.Setenv("NODE2_URL", node2.URL()))
	require.NoError(t, os.Setenv("NODE3_URL", node3.URL()))

	node1.SetHandler(http.HandlerFunc(h1.Handler))
	node2.SetHandler(http.HandlerFunc(h2.Handler))
	node3.SetHandler(http.HandlerFunc(h3.Handler))

	config := ReadConfig("consensus_cl")
	svr, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)

	client := NewProxydClient("http://127.0.0.1:8545")

	bg := svr.BackendGroups["node"]
	require.NotNil(t, bg)
	require.NotNil(t, bg.Consensus)
	require.Equal(t, 3, len(bg.Backends))

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
		"node3": {
			mockBackend: node3,
			backend:     bg.Backends[2],
			handler:     &h3,
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

// clSyncStatusWithL1 builds a syncStatus with a custom current_l1.number for pin selection tests.
func clSyncStatusWithL1(unsafeNum float64, unsafeHash string, currentL1Num float64) map[string]interface{} {
	s := clSyncStatus(unsafeNum, unsafeHash)
	s["current_l1"] = map[string]interface{}{
		"hash":      fmt.Sprintf("hash_l1_%d", int(currentL1Num)),
		"number":    currentL1Num,
		"timestamp": float64(9999999999),
	}
	return s
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
	defer nodes["node3"].mockBackend.Close()
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

	t.Run("initial consensus", func(t *testing.T) {
		reset()

		require.Equal(t, "0x0", bg.Consensus.GetLatestBlockNumber().String())

		update()

		// Default responses: unsafe_l2 at 0x101 (257), safe 0xe1, finalized 0xc1
		require.Equal(t, "0x101", bg.Consensus.GetLatestBlockNumber().String())
		require.Equal(t, "0xe1", bg.Consensus.GetSafeBlockNumber().String())
		require.Equal(t, "0xc1", bg.Consensus.GetFinalizedBlockNumber().String())
		require.Equal(t, 3, len(bg.Consensus.GetConsensusGroup()))
	})

	t.Run("prevent using a backend with low peer count", func(t *testing.T) {
		reset()
		overridePeerCount("node1", 0)
		update()

		consensusGroup := bg.Consensus.GetConsensusGroup()
		require.NotContains(t, consensusGroup, nodes["node1"].backend)
		require.False(t, bg.Consensus.IsBanned(nodes["node1"].backend))
		require.Equal(t, 2, len(consensusGroup))
	})

	t.Run("prevent using a backend not in sync", func(t *testing.T) {
		reset()
		overrideNotInSync("node1")
		update()

		consensusGroup := bg.Consensus.GetConsensusGroup()
		require.NotContains(t, consensusGroup, nodes["node1"].backend)
		require.False(t, bg.Consensus.IsBanned(nodes["node1"].backend))
		require.Equal(t, 2, len(consensusGroup))
	})

	t.Run("prevent using a backend with stale L1 head", func(t *testing.T) {
		reset()
		overrideNotInSyncByTimestamp("node1")
		update()

		consensusGroup := bg.Consensus.GetConsensusGroup()
		require.NotContains(t, consensusGroup, nodes["node1"].backend)
		require.False(t, bg.Consensus.IsBanned(nodes["node1"].backend))
		require.Equal(t, 2, len(consensusGroup))
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
		require.Equal(t, 2, len(consensusGroup))
		// node2 and node3 still healthy; consensus at default values
		require.Equal(t, "0x101", bg.Consensus.GetLatestBlockNumber().String())
	})

	t.Run("one node ahead - consensus resolves to lower", func(t *testing.T) {
		reset()

		// node2 one block ahead; lowest is node1 at 0x101
		overrideSyncStatus("node2", 258, "hash_0x102")
		update()

		require.Equal(t, "0x101", bg.Consensus.GetLatestBlockNumber().String())
		require.Equal(t, 3, len(bg.Consensus.GetConsensusGroup()))
	})

	t.Run("lagging backend excluded", func(t *testing.T) {
		reset()

		// node2 is maxBlockLag+1 (9) blocks ahead of node1 at 0x101 → 0x10a (266)
		overrideSyncStatus("node2", 266, "hash_0x10a")
		update()

		// node1 and node3 are lagging, excluded; only node2 in group
		consensusGroup := bg.Consensus.GetConsensusGroup()
		require.NotContains(t, consensusGroup, nodes["node1"].backend)
		require.NotContains(t, consensusGroup, nodes["node3"].backend)
		require.False(t, bg.Consensus.IsBanned(nodes["node1"].backend))
		require.Equal(t, 1, len(consensusGroup))

		// consensus is at node2's block since node1 and node3 are excluded
		require.Equal(t, "0x10a", bg.Consensus.GetLatestBlockNumber().String())
	})

	t.Run("lagging backend not excluded when within maxBlockLag", func(t *testing.T) {
		reset()

		// node2 is exactly maxBlockLag (8) blocks ahead: 0x101 + 8 = 0x109 (265)
		overrideSyncStatus("node2", 265, "hash_0x109")
		update()

		// all three should be in the consensus group since lag = 8 = maxBlockLag (not exceeded)
		require.Equal(t, 3, len(bg.Consensus.GetConsensusGroup()))
		// consensus at node1's block (lowest)
		require.Equal(t, "0x101", bg.Consensus.GetLatestBlockNumber().String())
	})

	t.Run("hash divergence does not affect consensus in CL mode", func(t *testing.T) {
		// Architecture 4: optimism_syncStatus is served from the pin-backend cache.
		// Hash agreement on the latest (unsafe) block is not required — we don't mix
		// fields across backends. Diverging hashes are irrelevant to the consensus block.
		reset()
		listenerCalled := false
		bg.Consensus.AddListener(func() {
			listenerCalled = true
		})

		update()
		require.Equal(t, "0x101", bg.Consensus.GetLatestBlockNumber().String())

		// advance all nodes to 0x102
		overrideSyncStatus("node1", 258, "hash_0x102")
		overrideSyncStatus("node2", 258, "hash_0x102")
		overrideSyncStatus("node3", 258, "hash_0x102")
		update()
		require.Equal(t, "0x102", bg.Consensus.GetLatestBlockNumber().String())

		// node2 diverges: same block number but different hash
		overrideSyncStatus("node2", 258, "wrong_hash_0x102")
		update()

		// CL mode: no walk-back — consensus stays at the minimum latest block (0x102)
		require.Equal(t, "0x102", bg.Consensus.GetLatestBlockNumber().String())
		// no broken event fired: hash divergence on unsafe block is not a consensus failure
		require.False(t, listenerCalled)
		// all backends still in the consensus group
		require.Equal(t, 3, len(bg.Consensus.GetConsensusGroup()))
	})

	t.Run("optimism_syncStatus served from pin-backend cache", func(t *testing.T) {
		reset()
		update()

		require.Equal(t, "0x101", bg.Consensus.GetLatestBlockNumber().String())
		require.Equal(t, "0xe1", bg.Consensus.GetSafeBlockNumber().String())

		resRaw, statusCode, err := client.SendRPC("optimism_syncStatus", nil)
		require.NoError(t, err)
		require.Equal(t, 200, statusCode)

		var jsonMap map[string]interface{}
		err = json.Unmarshal(resRaw, &jsonMap)
		require.NoError(t, err)

		result, ok := jsonMap["result"].(map[string]interface{})
		require.True(t, ok, "result should be an object")

		// All L2 block fields come from the pin backend's cached response — a single
		// coherent snapshot. Verify number, hash, and passthrough fields all present.
		unsafeL2, ok := result["unsafe_l2"].(map[string]interface{})
		require.True(t, ok, "unsafe_l2 should be an object")
		require.Equal(t, hexutil.Uint64(257).String(), hexutil.Uint64(uint64(unsafeL2["number"].(float64))).String())
		require.Equal(t, "hash_0x101", unsafeL2["hash"])
		require.NotNil(t, unsafeL2["timestamp"], "unsafe_l2 timestamp should pass through")
		require.NotNil(t, unsafeL2["parentHash"], "unsafe_l2 parentHash should pass through")

		safeL2, ok := result["safe_l2"].(map[string]interface{})
		require.True(t, ok, "safe_l2 should be an object")
		require.Equal(t, hexutil.Uint64(225).String(), hexutil.Uint64(uint64(safeL2["number"].(float64))).String())
		require.Equal(t, "hash_0xe1", safeL2["hash"])
		require.NotNil(t, safeL2["timestamp"], "safe_l2 timestamp should pass through")
		require.NotNil(t, safeL2["parentHash"], "safe_l2 parentHash should pass through")

		localSafeL2, ok := result["local_safe_l2"].(map[string]interface{})
		require.True(t, ok, "local_safe_l2 should be an object")
		require.Equal(t, hexutil.Uint64(225).String(), hexutil.Uint64(uint64(localSafeL2["number"].(float64))).String())
		require.Equal(t, "hash_0xe1", localSafeL2["hash"])

		finalizedL2, ok := result["finalized_l2"].(map[string]interface{})
		require.True(t, ok, "finalized_l2 should be an object")
		require.Equal(t, hexutil.Uint64(193).String(), hexutil.Uint64(uint64(finalizedL2["number"].(float64))).String())
		require.Equal(t, "hash_0xc1", finalizedL2["hash"])

		// L1 refs pass through from the pin backend unchanged.
		require.NotNil(t, result["current_l1"])
		require.NotNil(t, result["head_l1"])
	})

	t.Run("pin backend response served when backends have different unsafe values", func(t *testing.T) {
		reset()

		// node2 is one block ahead at 0x102; node1 stays at 0x101
		// Both have current_l1=100 (tied), so node1 is selected as pin (first in group)
		overrideSyncStatus("node2", 258, "hash_0x102")
		update()

		// consensus resolves to node1's lower block
		require.Equal(t, "0x101", bg.Consensus.GetLatestBlockNumber().String())

		// optimism_syncStatus response comes from pin backend (node1) at 0x101
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

		// node2 and node3 advance one block; consensus holds at node1's lower block
		overrideSyncStatus("node2", 258, "hash_0x102")
		overrideSyncStatus("node3", 258, "hash_0x102")
		update()
		require.Equal(t, "0x101", bg.Consensus.GetLatestBlockNumber().String())
		require.Equal(t, 3, len(bg.Consensus.GetConsensusGroup()))

		// node1 also advances; all three agree at 0x102
		overrideSyncStatus("node1", 258, "hash_0x102")
		update()
		require.Equal(t, "0x102", bg.Consensus.GetLatestBlockNumber().String())
		require.Equal(t, 3, len(bg.Consensus.GetConsensusGroup()))
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
		// all nodes advance safe and finalized to higher values
		overrideSyncStatusFull("node1", 257, "hash_0x101", 241, 209) // safe=0xf1, finalized=0xd1
		overrideSyncStatusFull("node2", 257, "hash_0x101", 241, 209)
		overrideSyncStatusFull("node3", 257, "hash_0x101", 241, 209)
		update()

		require.Equal(t, "0xf1", bg.Consensus.GetSafeBlockNumber().String())
		require.Equal(t, "0xd1", bg.Consensus.GetFinalizedBlockNumber().String())
	})

	t.Run("ban backend if tags are messed - safe < finalized", func(t *testing.T) {
		reset()
		update() // establish baseline at finalized=193
		// node1 reports safe (0xa1=161) < finalized (0xc1=193) — ordering violation
		overrideSyncStatusFull("node1", 257, "hash_0x101", 161, 193)
		update()

		require.True(t, bg.Consensus.IsBanned(nodes["node1"].backend))
		consensusGroup := bg.Consensus.GetConsensusGroup()
		require.NotContains(t, consensusGroup, nodes["node1"].backend)
		require.Equal(t, 2, len(consensusGroup))
	})

	t.Run("ban backend if tags are messed - latest < safe", func(t *testing.T) {
		reset()
		update() // establish baseline at finalized=193
		// node1 reports latest (0xa1=161) < safe (0xe1=225) — ordering violation
		overrideSyncStatusFull("node1", 161, "hash_0xa1", 225, 193)
		update()

		require.True(t, bg.Consensus.IsBanned(nodes["node1"].backend))
		consensusGroup := bg.Consensus.GetConsensusGroup()
		require.NotContains(t, consensusGroup, nodes["node1"].backend)
		require.Equal(t, 2, len(consensusGroup))
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
		require.Equal(t, 2, len(consensusGroup))
		// node2 and node3 still healthy; consensus safe unchanged
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
		require.Equal(t, 2, len(consensusGroup))
		require.Equal(t, "0xc1", bg.Consensus.GetFinalizedBlockNumber().String())
	})

	t.Run("recover after ban", func(t *testing.T) {
		reset()
		update() // establish baseline

		// cause node1 to be banned via finalized drop
		overrideSyncStatusFull("node1", 257, "hash_0x101", 225, 161)
		update()
		require.True(t, bg.Consensus.IsBanned(nodes["node1"].backend))
		require.Equal(t, 2, len(bg.Consensus.GetConsensusGroup()))

		// unban and restore node1 to healthy state
		bg.Consensus.Unban(nodes["node1"].backend)
		nodes["node1"].handler.ResetOverrides()
		update()

		require.False(t, bg.Consensus.IsBanned(nodes["node1"].backend))
		require.Equal(t, 3, len(bg.Consensus.GetConsensusGroup()))
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
		require.Equal(t, 2, len(bg.Consensus.GetConsensusGroup()))
	})

	t.Run("deep hash divergence does not affect consensus in CL mode", func(t *testing.T) {
		// Architecture 4: no hash walk-back in CL mode. Even multi-block hash divergence
		// does not change the consensus block — it stays at the minimum latest block.
		reset()
		listenerCalled := false
		bg.Consensus.AddListener(func() { listenerCalled = true })

		update()
		require.Equal(t, "0x101", bg.Consensus.GetLatestBlockNumber().String())

		// all advance to 0x103
		overrideSyncStatus("node1", 259, "hash_0x103")
		overrideSyncStatus("node2", 259, "hash_0x103")
		overrideSyncStatus("node3", 259, "hash_0x103")
		update()
		require.Equal(t, "0x103", bg.Consensus.GetLatestBlockNumber().String())

		// node2 diverges at 0x103 (and hypothetically 0x102 too)
		overrideSyncStatus("node2", 259, "wrong_hash_0x103")
		update()

		// CL mode: no walk-back — consensus stays at 0x103 (minimum latest block)
		require.Equal(t, "0x103", bg.Consensus.GetLatestBlockNumber().String())
		require.False(t, listenerCalled)
		require.Equal(t, 3, len(bg.Consensus.GetConsensusGroup()))
	})

	t.Run("fork at new height does not affect consensus in CL mode", func(t *testing.T) {
		// Architecture 4: hash divergence (even from the start at a new height) is ignored.
		// Consensus uses minimum latest block number regardless of hash agreement.
		reset()
		listenerCalled := false
		bg.Consensus.AddListener(func() { listenerCalled = true })
		update()

		require.Equal(t, "0x101", bg.Consensus.GetLatestBlockNumber().String())

		// all nodes jump to 0x103 but node2 on a different fork
		overrideSyncStatus("node1", 259, "node1_hash_0x103")
		overrideSyncStatus("node2", 259, "node2_hash_0x103")
		overrideSyncStatus("node3", 259, "node1_hash_0x103")
		update()

		// CL mode: no walk-back — consensus is the minimum latest block (0x103)
		require.Equal(t, "0x103", bg.Consensus.GetLatestBlockNumber().String())
		require.Equal(t, 3, len(bg.Consensus.GetConsensusGroup()))
		require.False(t, listenerCalled)
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

		// node1 excluded; consensus finalized holds at 0xc1 backed by node2 and node3
		require.Equal(t, "0xc1", bg.Consensus.GetFinalizedBlockNumber().String())
		require.Equal(t, 2, len(bg.Consensus.GetConsensusGroup()))
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

		// node1 excluded; consensus local_safe holds at 0xe1 backed by node2 and node3
		require.Equal(t, "0xe1", bg.Consensus.GetLocalSafeBlockNumber().String())
		require.Equal(t, 2, len(bg.Consensus.GetConsensusGroup()))
		require.NotContains(t, bg.Consensus.GetConsensusGroup(), nodes["node1"].backend)
		// unsafe and safe consensus unaffected
		require.Equal(t, "0x101", bg.Consensus.GetLatestBlockNumber().String())
		require.Equal(t, "0xe1", bg.Consensus.GetSafeBlockNumber().String())
	})

	t.Run("no consensus when all backends are out of sync", func(t *testing.T) {
		reset()
		overrideNotInSync("node1")
		overrideNotInSync("node2")
		overrideNotInSync("node3")
		update()

		require.Equal(t, 0, len(bg.Consensus.GetConsensusGroup()))
	})

	t.Run("backend recovers from out-of-sync and re-enters consensus", func(t *testing.T) {
		reset()
		overrideNotInSync("node1")
		update()

		require.Equal(t, 2, len(bg.Consensus.GetConsensusGroup()))
		require.NotContains(t, bg.Consensus.GetConsensusGroup(), nodes["node1"].backend)

		// node1 catches up: clear the override, L1 lag is gone
		nodes["node1"].handler.ResetOverrides()
		update()

		require.Equal(t, 3, len(bg.Consensus.GetConsensusGroup()))
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
		require.Equal(t, 2, len(consensusGroup))
	})

	t.Run("rewrite local_safe_l2 uses consensus values", func(t *testing.T) {
		reset()
		// node2 has a higher local_safe; consensus picks the lower (node1's) value
		s2 := clSyncStatus(257, "hash_0x101")
		s2["local_safe_l2"] = map[string]interface{}{"hash": "hash_local_safe_high", "number": float64(240)}
		override("node2", "optimism_syncStatus", "", buildResponse(s2))
		update()

		require.Equal(t, "0xe1", bg.Consensus.GetLocalSafeBlockNumber().String())

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

	t.Run("malformed backend response does not corrupt the cache", func(t *testing.T) {
		reset()
		update() // populate cache with valid pin-backend body

		// Override all backends to return malformed optimism_syncStatus.
		// fetchCLSyncStatus will fail to parse it, so the update cycle stores no new body.
		// The cached body from the previous successful update should still be served.
		malformed := clSyncStatus(257, "hash_0x101")
		malformed["unsafe_l2"] = "bad"
		override("node1", "optimism_syncStatus", "", buildResponse(malformed))
		override("node2", "optimism_syncStatus", "", buildResponse(malformed))
		override("node3", "optimism_syncStatus", "", buildResponse(malformed))
		update() // all backends fail parse; cache is not updated

		resRaw, statusCode, err := client.SendRPC("optimism_syncStatus", nil)
		require.NoError(t, err)
		require.Equal(t, 200, statusCode)

		var jsonMap map[string]interface{}
		err = json.Unmarshal(resRaw, &jsonMap)
		require.NoError(t, err)

		// Cache still serves the last valid body — no error, no corruption.
		require.NotNil(t, jsonMap["result"], "should serve cached body, not an error")
		require.Nil(t, jsonMap["error"])

		result := jsonMap["result"].(map[string]interface{})
		unsafeL2 := result["unsafe_l2"].(map[string]interface{})
		require.Equal(t, "hash_0x101", unsafeL2["hash"])
	})

	t.Run("batch optimism_syncStatus requests all served from cache", func(t *testing.T) {
		reset()
		update() // populate cache at 0x101 / hash_0x101

		// Override backends to return unsafe_l2 at 0x102 — but do NOT call update() again.
		// The cache still holds the 0x101 body from the previous update cycle.
		// Both batch requests should be served from the same cached snapshot.
		overrideSyncStatus("node1", 258, "hash_0x102")
		overrideSyncStatus("node2", 258, "hash_0x102")

		require.Equal(t, "0x101", bg.Consensus.GetLatestBlockNumber().String())

		req1 := NewRPCReq("1", "optimism_syncStatus", nil)
		req2 := NewRPCReq("2", "optimism_syncStatus", nil)
		resRaw, statusCode, err := client.SendBatchRPC(req1, req2)
		require.NoError(t, err)
		require.Equal(t, 200, statusCode)

		var batchRes []map[string]interface{}
		err = json.Unmarshal(resRaw, &batchRes)
		require.NoError(t, err)
		require.Len(t, batchRes, 2)

		for i, r := range batchRes {
			result, ok := r["result"].(map[string]interface{})
			require.True(t, ok, "response %d result should be a map", i)

			unsafeL2, ok := result["unsafe_l2"].(map[string]interface{})
			require.True(t, ok, "response %d unsafe_l2 should be a map", i)
			// Cache was populated at 0x101; backends now return 0x102 but cache is not refreshed.
			require.Equal(t, hexutil.Uint64(257).String(), hexutil.Uint64(uint64(unsafeL2["number"].(float64))).String(), "response %d unsafe_l2 number", i)
			require.Equal(t, "hash_0x101", unsafeL2["hash"], "response %d unsafe_l2 hash", i)

			// L1 fields pass through from the pin-backend snapshot.
			require.NotNil(t, result["current_l1"], "response %d current_l1", i)
			require.NotNil(t, result["head_l1"], "response %d head_l1", i)
		}
	})

	t.Run("pin selection uses backend with lowest current_l1", func(t *testing.T) {
		reset()

		// Use l1 values >= 100 (the default from YAML) so they are above the monotonicity floor
		// that may have been established by a previous test cycle.
		// node1 at current_l1=101, node2 at current_l1=104, node3 at current_l1=106.
		override("node1", "optimism_syncStatus", "", buildResponse(clSyncStatusWithL1(257, "hash_0x101", 101)))
		override("node2", "optimism_syncStatus", "", buildResponse(clSyncStatusWithL1(257, "hash_0x101", 104)))
		override("node3", "optimism_syncStatus", "", buildResponse(clSyncStatusWithL1(257, "hash_0x101", 106)))
		update()

		resRaw, statusCode, err := client.SendRPC("optimism_syncStatus", nil)
		require.NoError(t, err)
		require.Equal(t, 200, statusCode)

		var jsonMap map[string]interface{}
		err = json.Unmarshal(resRaw, &jsonMap)
		require.NoError(t, err)

		result := jsonMap["result"].(map[string]interface{})
		currentL1 := result["current_l1"].(map[string]interface{})
		// node1 (current_l1=101) should be the pin — lower L1 means more conservative view
		require.Equal(t, float64(101), currentL1["number"])
	})

	t.Run("pin monotonicity: does not regress to lower current_l1", func(t *testing.T) {
		reset()

		// Use l1 values well above the floor that may have been set by the previous test.
		// First poll: node1 at current_l1=110 is pin (lowest of 110, 115, 112).
		override("node1", "optimism_syncStatus", "", buildResponse(clSyncStatusWithL1(257, "hash_0x101", 110)))
		override("node2", "optimism_syncStatus", "", buildResponse(clSyncStatusWithL1(257, "hash_0x101", 115)))
		override("node3", "optimism_syncStatus", "", buildResponse(clSyncStatusWithL1(257, "hash_0x101", 112)))
		update()

		resRaw, _, err := client.SendRPC("optimism_syncStatus", nil)
		require.NoError(t, err)
		var jsonMap map[string]interface{}
		require.NoError(t, json.Unmarshal(resRaw, &jsonMap))
		currentL1 := jsonMap["result"].(map[string]interface{})["current_l1"].(map[string]interface{})
		require.Equal(t, float64(110), currentL1["number"], "first poll: node1 at 110 is pin")

		// Second poll: node1 and node3 regress to current_l1=105 (stale or reorg), node2 advances to 120
		// Monotonicity floor is 110, so node1 and node3 (105 < 110) are excluded; node2 (120 >= 110) wins
		override("node1", "optimism_syncStatus", "", buildResponse(clSyncStatusWithL1(257, "hash_0x101", 105)))
		override("node2", "optimism_syncStatus", "", buildResponse(clSyncStatusWithL1(257, "hash_0x101", 120)))
		override("node3", "optimism_syncStatus", "", buildResponse(clSyncStatusWithL1(257, "hash_0x101", 105)))
		update()

		resRaw, _, err = client.SendRPC("optimism_syncStatus", nil)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(resRaw, &jsonMap))
		currentL1 = jsonMap["result"].(map[string]interface{})["current_l1"].(map[string]interface{})
		require.Equal(t, float64(120), currentL1["number"], "second poll: node1 and node3 regressed, node2 at 120 is pin")
	})

	t.Run("output root agreement: all backends agree, all stay in consensus group", func(t *testing.T) {
		// Default YAML fixture has all nodes returning outputRoot "output_root_0xe1" for 0xe1.
		reset()
		update()
		require.Equal(t, 3, len(bg.Consensus.GetConsensusGroup()))
		require.False(t, bg.Consensus.IsBanned(nodes["node1"].backend))
		require.False(t, bg.Consensus.IsBanned(nodes["node2"].backend))
		require.False(t, bg.Consensus.IsBanned(nodes["node3"].backend))
	})

	t.Run("output root mismatch: minority backend banned by majority", func(t *testing.T) {
		// node1 and node3 agree on the correct output root; node2 has a wrong root.
		// 2/3 majority — node2 is identified as the minority and gets banned.
		reset()
		override("node2", "optimism_outputAtBlock", "0xe1", buildResponse(map[string]interface{}{
			"outputRoot": "WRONG_output_root",
			"blockRef":   map[string]interface{}{"hash": "hash_0xe1", "number": float64(225)},
		}))
		update()

		// node2 is the minority — banned
		require.True(t, bg.Consensus.IsBanned(nodes["node2"].backend))
		require.False(t, bg.Consensus.IsBanned(nodes["node1"].backend))
		require.False(t, bg.Consensus.IsBanned(nodes["node3"].backend))
		require.Equal(t, 2, len(bg.Consensus.GetConsensusGroup()))
	})

	t.Run("output root: all backends disagree, no majority — nobody banned", func(t *testing.T) {
		// 3 backends each return a unique output root → no backend has ≥2 votes.
		// Without a majority we cannot determine who is correct, so nobody should be banned.
		reset()
		override("node1", "optimism_outputAtBlock", "0xe1", buildResponse(map[string]interface{}{
			"outputRoot": "root_A",
			"blockRef":   map[string]interface{}{"hash": "hash_0xe1", "number": float64(225)},
		}))
		override("node2", "optimism_outputAtBlock", "0xe1", buildResponse(map[string]interface{}{
			"outputRoot": "root_B",
			"blockRef":   map[string]interface{}{"hash": "hash_0xe1", "number": float64(225)},
		}))
		override("node3", "optimism_outputAtBlock", "0xe1", buildResponse(map[string]interface{}{
			"outputRoot": "root_C",
			"blockRef":   map[string]interface{}{"hash": "hash_0xe1", "number": float64(225)},
		}))
		update()

		require.False(t, bg.Consensus.IsBanned(nodes["node1"].backend))
		require.False(t, bg.Consensus.IsBanned(nodes["node2"].backend))
		require.False(t, bg.Consensus.IsBanned(nodes["node3"].backend))
		require.Equal(t, 3, len(bg.Consensus.GetConsensusGroup()))
	})

	t.Run("output root ban recovery", func(t *testing.T) {
		// Ban a backend via output root mismatch, then unban and verify it re-enters consensus.
		reset()
		override("node2", "optimism_outputAtBlock", "0xe1", buildResponse(map[string]interface{}{
			"outputRoot": "WRONG_output_root",
			"blockRef":   map[string]interface{}{"hash": "hash_0xe1", "number": float64(225)},
		}))
		update()

		require.True(t, bg.Consensus.IsBanned(nodes["node2"].backend))
		require.Equal(t, 2, len(bg.Consensus.GetConsensusGroup()))

		bg.Consensus.Unban(nodes["node2"].backend)
		nodes["node2"].handler.ResetOverrides()
		update()

		require.False(t, bg.Consensus.IsBanned(nodes["node2"].backend))
		require.Equal(t, 3, len(bg.Consensus.GetConsensusGroup()))
	})

	t.Run("output root error: backend that errors is skipped, not banned", func(t *testing.T) {
		// If a backend does not support optimism_outputAtBlock (e.g. old version),
		// it should not be penalised — verification is skipped for that backend.
		reset()
		// Override node2 to return a non-200 / error response for outputAtBlock.
		// When all-but-one error, the single successful response is the agreed root
		// and the erroring backend is only warned, not banned.
		// Here we test the simpler case: node2 errors, node1 succeeds.
		// node2 should remain in consensus (error ≠ mismatch).
		override("node2", "optimism_outputAtBlock", "0xe1", `{"jsonrpc":"2.0","id":67,"error":{"code":-32601,"message":"method not found"}}`)
		update()

		require.False(t, bg.Consensus.IsBanned(nodes["node2"].backend))
		require.Equal(t, 3, len(bg.Consensus.GetConsensusGroup()))
	})

}

// TestConsensusCLFirstCycle verifies that before the first consensus cycle completes
// (cache is nil), optimism_syncStatus returns a JSON-RPC error rather than falling
// through to an arbitrary backend.
func TestConsensusCLFirstCycle(t *testing.T) {
	nodes, bg, client, shutdown := setupCL(t)
	defer nodes["node1"].mockBackend.Close()
	defer nodes["node2"].mockBackend.Close()
	defer nodes["node3"].mockBackend.Close()
	defer shutdown()

	// Do NOT call update() — cache is nil on first boot
	_ = bg

	resRaw, statusCode, err := client.SendRPC("optimism_syncStatus", nil)
	require.NoError(t, err)
	require.Equal(t, 503, statusCode)

	var jsonMap map[string]interface{}
	require.NoError(t, json.Unmarshal(resRaw, &jsonMap))

	require.Nil(t, jsonMap["result"], "should return an error, not a backend result")
	require.NotNil(t, jsonMap["error"], "should return an RPC error when cache is not yet populated")
	rpcErr := jsonMap["error"].(map[string]interface{})
	require.Equal(t, float64(-32025), rpcErr["code"])
}
