package integration_tests

import (
	"context"
	"net/http"
	"os"
	"path"
	"testing"
	"time"

	"github.com/ethereum-optimism/optimism/proxyd"
	ms "github.com/ethereum-optimism/optimism/proxyd/tools/mockserver/handler"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/stretchr/testify/require"
)

// ts is a convenient method that must parse a time.Time from a string in format `"2006-01-02 15:04:05"`
func ts(s string) time.Time {
	t, err := time.Parse(time.DateTime, s)
	if err != nil {
		panic(err)
	}
	return t
}

func setupBlockHeightZero(t *testing.T) (map[string]*nodeContext, *proxyd.BackendGroup, *ProxydHTTPClient, func()) {
	// setup mock servers
	node1 := NewMockBackend(nil)
	node2 := NewMockBackend(nil)

	dir, err := os.Getwd()
	require.NoError(t, err)

	responses := path.Join(dir, "testdata/block_height_zero_responses.yml")

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

	// setup proxyd
	config := ReadConfig("block_height_zero")
	svr, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)

	// expose the proxyd client
	client := NewProxydClient("http://127.0.0.1:8545")

	// expose the backend group
	bg := svr.BackendGroups["node"]
	require.NotNil(t, bg)
	require.NotNil(t, bg.Consensus)
	require.Equal(t, 2, len(bg.Backends))

	// convenient mapping to access the nodes
	nodes := map[string]*nodeContext{
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

func TestBlockHeightZero(t *testing.T) {
	nodes, bg, _, shutdown := setupBlockHeightZero(t)
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
			node.backend.ClearNetworkErrorsSlidingWindows()
		}
		bg.Consensus.ClearListeners()
		bg.Consensus.Reset()

		require.False(t, bg.Consensus.IsBanned(nodes["node1"].backend))
		require.False(t, bg.Consensus.IsBanned(nodes["node2"].backend))

	}

	override := func(node string, method string, block string, response string, responseCode int) {
		if _, ok := nodes[node]; !ok {
			t.Fatalf("node %s does not exist in the nodes map", node)
		}
		nodes[node].handler.AddOverride(&ms.MethodTemplate{
			Method:       method,
			Block:        block,
			Response:     response,
			ResponseCode: responseCode,
		})
	}

	overrideBlock := func(node string, blockRequest string, blockResponse string, responseCode int) {
		override(node,
			"eth_getBlockByNumber",
			blockRequest,
			buildResponse(map[string]string{
				"number": blockResponse,
				"hash":   "hash_" + blockResponse,
			}),
			responseCode,
		)
	}

	overridePeerCount := func(node string, count int, responseCode int) {
		override(node, "net_peerCount", "", buildResponse(hexutil.Uint64(count).String()), responseCode)
	}

	type blockHeights struct {
		latestBlockNumber    hexutil.Uint64
		latestBlockHash      string
		safeBlockNumber      hexutil.Uint64
		finalizedBlockNumber hexutil.Uint64
	}

	getBlockHeights := func(node string) blockHeights {
		bs := bg.Consensus.GetBackendState(nodes[node].backend)
		lB, lHash := bs.GetLatestBlock()
		sB := bs.GetSafeBlockNumber()
		fB := bs.GetFinalizedBlockNumber()
		return blockHeights{
			latestBlockNumber:    lB,
			latestBlockHash:      lHash,
			safeBlockNumber:      sB,
			finalizedBlockNumber: fB,
		}
	}

	for _, blockState := range []string{"latest", "finalized", "safe"} {

		t.Run("Test that the backend will not be banned if "+blockState+" responds 429", func(t *testing.T) {
			reset()
			update()
			overrideBlock("node1", blockState, "0x0", 429)
			update()
			require.False(t, bg.Consensus.IsBanned(nodes["node1"].backend))
			require.False(t, bg.Consensus.IsBanned(nodes["node2"].backend))
		})

		t.Run("Test that the backend will not be banned if "+blockState+" responds 500", func(t *testing.T) {
			reset()
			update()
			overrideBlock("node1", blockState, "0x0", 500)
			update()
			require.False(t, bg.Consensus.IsBanned(nodes["node1"].backend))
			require.False(t, bg.Consensus.IsBanned(nodes["node2"].backend))
		})

	}

	t.Run("Test that the backend will not be banned if latest responds 200 with block height zero", func(t *testing.T) {
		reset()
		update()
		overrideBlock("node1", "latest", "0x0", 200)
		update()
		require.False(t, bg.Consensus.IsBanned(nodes["node1"].backend))
		require.False(t, bg.Consensus.IsBanned(nodes["node2"].backend))
	})

	t.Run("Test backend state for latest block will not change on error, but safe and finalized can be updated", func(t *testing.T) {

		update()
		bh1 := getBlockHeights("node1")

		overrideBlock("node1", "latest", "0x0", 200)
		overrideBlock("node1", "safe", "0xe3", 200)
		overrideBlock("node1", "finalized", "0xc3", 200)
		update()

		bh2 := getBlockHeights("node1")
		require.Equal(t, bh1.latestBlockNumber, bh2.latestBlockNumber)
		require.Equal(t, bh1.latestBlockHash, bh2.latestBlockHash)

		require.NotEqual(t, bh1.safeBlockNumber, bh2.safeBlockNumber)
		require.NotEqual(t, bh1.finalizedBlockNumber, bh2.finalizedBlockNumber)

		require.Equal(t, "0xe3", bh2.safeBlockNumber.String())
		require.Equal(t, "0xc3", bh2.finalizedBlockNumber.String())

		require.Equal(t, "0x101", bg.Consensus.GetLatestBlockNumber().String())
		require.Equal(t, "0xe1", bg.Consensus.GetSafeBlockNumber().String())
		require.Equal(t, "0xc1", bg.Consensus.GetFinalizedBlockNumber().String())

		require.False(t, bg.Consensus.IsBanned(nodes["node1"].backend))
		require.False(t, bg.Consensus.IsBanned(nodes["node2"].backend))
	})

	t.Run("Test that if it breaches the network error threshold the node will be banned", func(t *testing.T) {
		reset()
		update()
		overrideBlock("node1", "latest", "0x0", 500)
		overrideBlock("node1", "safe", "0x0", 429)
		overrideBlock("node1", "finalized", "0x0", 403)
		overridePeerCount("node1", 0, 500)

		for i := 0; i < 3; i++ {
			require.False(t, bg.Consensus.IsBanned(nodes["node1"].backend), "Execpted node 1 to be not banned on iteration ", i)
			require.False(t, bg.Consensus.IsBanned(nodes["node2"].backend), "Execpted node 2 to be not banned on iteration ", i)
			update()
		}
		require.True(t, bg.Consensus.IsBanned(nodes["node1"].backend))
		require.False(t, bg.Consensus.IsBanned(nodes["node2"].backend))
	})

}
