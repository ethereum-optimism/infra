package integration_tests

import (
	"context"
	"net/http"
	"os"
	"path"
	"testing"

	"time"

	"github.com/ethereum-optimism/optimism/proxyd"
	sw "github.com/ethereum-optimism/optimism/proxyd/pkg/avg-sliding-window"
	ms "github.com/ethereum-optimism/optimism/proxyd/tools/mockserver/handler"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/stretchr/testify/require"
)

type bhZeroNodeContext struct {
	backend                    *proxyd.Backend   // this is the actual backend impl in proxyd
	mockBackend                *MockBackend      // this is the fake backend that we can use to mock responses
	handler                    *ms.MockedHandler // this is where we control the state of mocked responses
	intermittentNetErrorWindow *sw.AvgSlidingWindow
	clock                      *sw.AdjustableClock // this is where we control backend time
}

// ts is a convenient method that must parse a time.Time from a string in format `"2006-01-02 15:04:05"`
func ts(s string) time.Time {
	t, err := time.Parse(time.DateTime, s)
	if err != nil {
		panic(err)
	}
	return t
}

func setupBlockHeightZero(t *testing.T) (map[string]*bhZeroNodeContext, *proxyd.BackendGroup, *ProxydHTTPClient, func()) {
	// setup mock servers
	node1 := NewMockBackend(nil)
	node2 := NewMockBackend(nil)

	dir, err := os.Getwd()
	require.NoError(t, err)

	responses := path.Join(dir, "testdata/block_height_zero_and_network_errors_responses.yaml")

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
	config := ReadConfig("block_height_zero_and_network_errors")
	svr, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)

	// expose the proxyd client
	client := NewProxydClient("http://127.0.0.1:8545")

	// expose the backend group
	bg := svr.BackendGroups["node"]

	require.NotNil(t, bg)
	require.NotNil(t, bg.Consensus, "Expected Consenus Poller to be intialized")
	require.Equal(t, 2, len(bg.Backends))

	// convenient mapping to access the nodes
	nodes := map[string]*bhZeroNodeContext{
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

	addTimeToBackend := func(node string, ts time.Duration) {
		mockBackend, ok := nodes[node]
		require.True(t, ok, "Fatal error bad node key for override clock")
		mockBackend.clock.Set(mockBackend.clock.Now().Add(ts))
	}

	// poll for updated consensus
	update := func() {
		for _, be := range bg.Backends {
			bg.Consensus.UpdateBackend(ctx, be)
		}
		bg.Consensus.UpdateBackendGroupConsensus(ctx)
		addTimeToBackend("node1", 3*time.Second)
		addTimeToBackend("node2", 3*time.Second)
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

		require.False(t, bg.Consensus.IsBanned(nodes["node1"].backend))
		require.False(t, bg.Consensus.IsBanned(nodes["node2"].backend))

		now := ts("2023-04-21 15:00:00")
		clock := sw.NewAdjustableClock(now)
		b1 := nodes["node1"]
		b2 := nodes["node2"]
		b1.intermittentNetErrorWindow = sw.NewSlidingWindow(
			sw.WithWindowLength(5*time.Minute),
			sw.WithBucketSize(time.Second),
			sw.WithClock(clock))

		b2.intermittentNetErrorWindow = sw.NewSlidingWindow(
			sw.WithWindowLength(5*time.Minute),
			sw.WithBucketSize(time.Second),
			sw.WithClock(clock))

		b1.clock = clock
		b2.clock = clock
		b1.backend.Override(proxyd.WithIntermittentNetworkErrorSlidingWindow(b1.intermittentNetErrorWindow))
		b2.backend.Override(proxyd.WithIntermittentNetworkErrorSlidingWindow(b2.intermittentNetErrorWindow))
		nodes["node1"] = b1
		nodes["node2"] = b2

		require.Zero(t, nodes["node1"].intermittentNetErrorWindow.Count())
		require.Zero(t, nodes["node2"].intermittentNetErrorWindow.Count())

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
			bh1 := getBlockHeights("node1")
			overrideBlock("node1", blockState, "0x101", 429)
			update()
			bh2 := getBlockHeights("node1")
			require.False(t, bg.Consensus.IsBanned(nodes["node1"].backend))
			require.False(t, bg.Consensus.IsBanned(nodes["node2"].backend))
			require.Equal(t, bh1.latestBlockNumber.String(), bh2.latestBlockNumber.String())
			require.Equal(t, bh1.safeBlockNumber.String(), bh2.safeBlockNumber.String())
			require.Equal(t, bh1.finalizedBlockNumber.String(), bh2.finalizedBlockNumber.String())
			require.Equal(t, nodes["node1"].intermittentNetErrorWindow.Count(), uint(1))
			require.Equal(t, nodes["node2"].intermittentNetErrorWindow.Count(), uint(0))
		})

		// Write  a test which will check the sliding window increments each time by one
		t.Run("Test that the backend will not be banned and single increment of window if "+blockState+" responds 500", func(t *testing.T) {
			reset()
			update()
			bh1 := getBlockHeights("node1")
			overrideBlock("node1", blockState, "0x101", 500)
			update()
			bh2 := getBlockHeights("node1")
			require.False(t, bg.Consensus.IsBanned(nodes["node1"].backend))
			require.False(t, bg.Consensus.IsBanned(nodes["node2"].backend))
			require.Equal(t, bh1.latestBlockNumber.String(), bh2.latestBlockNumber.String())
			require.Equal(t, bh1.safeBlockNumber.String(), bh2.safeBlockNumber.String())
			require.Equal(t, bh1.finalizedBlockNumber.String(), bh2.finalizedBlockNumber.String())
			require.Equal(t, nodes["node1"].intermittentNetErrorWindow.Count(), uint(1))
			require.Equal(t, nodes["node2"].intermittentNetErrorWindow.Count(), uint(0))
		})

		t.Run("Test that the backend will not be banned and single increment of window if "+blockState+" responds 0 and 200", func(t *testing.T) {
			reset()
			update()
			bh1 := getBlockHeights("node2")
			overrideBlock("node2", blockState, "0x0", 200)
			update()
			bh2 := getBlockHeights("node2")
			require.False(t, bg.Consensus.IsBanned(nodes["node1"].backend))
			require.False(t, bg.Consensus.IsBanned(nodes["node2"].backend))

			require.Equal(t, bh1.latestBlockNumber.String(), bh2.latestBlockNumber.String())
			require.Equal(t, bh1.safeBlockNumber.String(), bh2.safeBlockNumber.String())
			require.Equal(t, bh1.finalizedBlockNumber.String(), bh2.finalizedBlockNumber.String())
			require.Equal(t, nodes["node1"].intermittentNetErrorWindow.Count(), uint(0))
			require.Equal(t, nodes["node2"].intermittentNetErrorWindow.Count(), uint(1))
		})

	}

	t.Run("Test that the backend will not be banned and single increment of window if latest responds 200 with block height zero", func(t *testing.T) {
		reset()
		update()
		overrideBlock("node1", "latest", "0x0", 200)
		bh1 := getBlockHeights("node1")
		update()
		bh2 := getBlockHeights("node1")
		require.False(t, bg.Consensus.IsBanned(nodes["node1"].backend))
		require.False(t, bg.Consensus.IsBanned(nodes["node2"].backend))

		require.Equal(t, bh1.latestBlockNumber.String(), bh2.latestBlockNumber.String())
		require.Equal(t, bh1.safeBlockNumber.String(), bh2.safeBlockNumber.String())
		require.Equal(t, bh1.finalizedBlockNumber.String(), bh2.finalizedBlockNumber.String())
		require.Equal(t, nodes["node1"].intermittentNetErrorWindow.Count(), uint(1))
		require.Equal(t, nodes["node2"].intermittentNetErrorWindow.Count(), uint(0))
	})

	t.Run("Test that the backend will not be banned if latest responds 5xx for peer count", func(t *testing.T) {
		reset()
		update()
		overridePeerCount("node2", 59, 500)
		bh1 := getBlockHeights("node2")
		update()
		bh2 := getBlockHeights("node2")
		require.False(t, bg.Consensus.IsBanned(nodes["node1"].backend))
		require.False(t, bg.Consensus.IsBanned(nodes["node2"].backend))

		require.Equal(t, bh1.latestBlockNumber.String(), bh2.latestBlockNumber.String())
		require.Equal(t, bh1.safeBlockNumber.String(), bh2.safeBlockNumber.String())
		require.Equal(t, bh1.finalizedBlockNumber.String(), bh2.finalizedBlockNumber.String())

		require.Equal(t, nodes["node1"].intermittentNetErrorWindow.Count(), uint(0))
		require.Equal(t, nodes["node2"].intermittentNetErrorWindow.Count(), uint(1))
	})

	t.Run("Test that the backend will not be banned if latest responds 4xx for peer count", func(t *testing.T) {
		reset()
		update()
		overridePeerCount("node1", 59, 429)
		bh1 := getBlockHeights("node1")
		update()
		bh2 := getBlockHeights("node1")
		require.False(t, bg.Consensus.IsBanned(nodes["node1"].backend))
		require.False(t, bg.Consensus.IsBanned(nodes["node2"].backend))

		require.Equal(t, bh1.latestBlockNumber.String(), bh2.latestBlockNumber.String())
		require.Equal(t, bh1.safeBlockNumber.String(), bh2.safeBlockNumber.String())
		require.Equal(t, bh1.finalizedBlockNumber.String(), bh2.finalizedBlockNumber.String())

		require.Equal(t, nodes["node1"].intermittentNetErrorWindow.Count(), uint(1))
		require.Equal(t, nodes["node2"].intermittentNetErrorWindow.Count(), uint(0))
	})

	t.Run("Test that the backend will not be banned if latest responds 200 and 0 for peer count", func(t *testing.T) {
		reset()
		update()
		bh1 := getBlockHeights("node1")
		overridePeerCount("node1", 0, 200)
		update()
		bh2 := getBlockHeights("node1")
		require.False(t, bg.Consensus.IsBanned(nodes["node1"].backend))
		require.False(t, bg.Consensus.IsBanned(nodes["node2"].backend))

		require.Equal(t, bh1.latestBlockNumber.String(), bh2.latestBlockNumber.String())
		require.Equal(t, bh1.safeBlockNumber.String(), bh2.safeBlockNumber.String())
		require.Equal(t, bh1.finalizedBlockNumber.String(), bh2.finalizedBlockNumber.String())

		require.Equal(t, nodes["node1"].intermittentNetErrorWindow.Count(), uint(1))
		require.Equal(t, nodes["node2"].intermittentNetErrorWindow.Count(), uint(0))
	})

	t.Run("Test that if it breaches the network error threshold the node will be banned", func(t *testing.T) {
		reset()
		update()
		overrideBlock("node1", "latest", "0x0", 500)
		overrideBlock("node1", "safe", "0x0", 429)
		overrideBlock("node1", "finalized", "0x0", 403)
		overridePeerCount("node1", 0, 500)

		for i := 1; i < 7; i++ {
			require.False(t, bg.Consensus.IsBanned(nodes["node1"].backend), "Execpted node 1 to be not banned on iteration ", i)
			require.False(t, bg.Consensus.IsBanned(nodes["node2"].backend), "Execpted node 2 to be not banned on iteration ", i)
			update()
			// On the 5th update (i=6), node 1 will be banned due to error rate and not increment window
			if i < 6 {
				require.Equal(t, nodes["node1"].intermittentNetErrorWindow.Count(), uint(i))
			}
			require.Equal(t, nodes["node2"].intermittentNetErrorWindow.Count(), uint(0))
		}
		require.True(t, bg.Consensus.IsBanned(nodes["node1"].backend))
		require.False(t, bg.Consensus.IsBanned(nodes["node2"].backend))
	})

}
