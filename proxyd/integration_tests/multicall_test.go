package integration_tests

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"testing"

	"github.com/ethereum/go-ethereum/common/hexutil"

	"github.com/ethereum-optimism/optimism/proxyd"
	ms "github.com/ethereum-optimism/optimism/proxyd/tools/mockserver/handler"
	"github.com/stretchr/testify/require"
)

// func makeSendRawTransaction(dataHex string) []byte {
// 	return []byte(`{"jsonrpc":"2.0","method":"eth_sendRawTransaction","params":["` + dataHex + `"],"id":1}`)
// }

func setupMulticall(t *testing.T) (map[string]nodeContext, *proxyd.BackendGroup, *ProxydHTTPClient, func(), *proxyd.Server) {
	// setup mock servers
	node1 := NewMockBackend(nil)
	node2 := NewMockBackend(nil)

	dir, err := os.Getwd()
	require.NoError(t, err)

	responses := path.Join(dir, "testdata/consensus_responses.yml")

	h1 := ms.MockedHandler{
		Overrides:    []*ms.MethodTemplate{},
		Autoload:     true,
		AutoloadFile: responses,
	}
	h2 := ms.MockedHandler{
		Overrides:    []*ms.MethodTemplate{},
		Autoload:     true,
		AutoloadFile: "",
	}

	require.NoError(t, os.Setenv("NODE1_URL", node1.URL()))
	require.NoError(t, os.Setenv("NODE2_URL", node2.URL()))

	node1.SetHandler(http.HandlerFunc(h1.Handler))
	node2.SetHandler(SingleResponseHandler(200, dummyRes))

	// setup proxyd
	config := ReadConfig("multicall")
	svr, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)

	// expose the proxyd client
	client := NewProxydClient("http://127.0.0.1:8545")

	// expose the backend group
	bg := svr.BackendGroups["node"]
	require.NotNil(t, bg)
	require.NotNil(t, bg.Consensus)
	require.Equal(t, 2, len(bg.Backends)) // should match config

	// convenient mapping to access the nodes by name
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

	return nodes, bg, client, shutdown, svr
}

func TestMulticall(t *testing.T) {
	nodes, bg, _, shutdown, svr := setupMulticall(t)
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
			require.Zero(t, len(node.mockBackend.requests))
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

	overrideBlock := func(node string, blockRequest string, blockResponse string) {
		override(node,
			"eth_getBlockByNumber",
			blockRequest,
			buildResponse(map[string]string{
				"number": blockResponse,
				"hash":   "hash_" + blockResponse,
			}))
	}

	overridePeerCount := func(node string, count int) {
		override(node, "net_peerCount", "", buildResponse(hexutil.Uint64(count).String()))
	}

	nodeBackendRequestCount := func(node string) int {
		return len(nodes[node].mockBackend.requests)
	}
	// TODO: Maybe add a function that verifies the backend request is the dummy raw tx

	// force ban node2 and make sure node1 is the only one in consensus
	// useOnlyNode1 := func() {
	// 	overridePeerCount("node2", 0)
	// 	update()
	//
	// 	consensusGroup := bg.Consensus.GetConsensusGroup()
	// 	require.Equal(t, 1, len(consensusGroup))
	// 	require.Contains(t, consensusGroup, nodes["node1"].backend)
	// 	nodes["node1"].mockBackend.Reset()
	// }

	t.Run("initial multi-call test", func(t *testing.T) {
		reset()

		body := makeSendRawTransaction(txHex1)
		req, _ := http.NewRequest("POST", "https://1.1.1.1:8080", bytes.NewReader(body))
		req.Header.Set("X-Forwarded-For", "203.0.113.1")
		rr := httptest.NewRecorder()
		svr.HandleRPC(rr, req)
		resp := rr.Result()
		require.NotNil(t, resp.Body)
		require.Equal(t, 200, resp.StatusCode)
		require.Equal(t, resp.Header["X-Served-By"], []string{"node/node2"})
		rpcRes := &proxyd.RPCRes{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(rpcRes))
		require.False(t, rpcRes.IsError())
		// unknown consensus at inik

		require.Equal(t, 1, nodeBackendRequestCount("node1"))
		require.Equal(t, 1, nodeBackendRequestCount("node2"))
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

	t.Run("prevent using a backend lagging behind", func(t *testing.T) {
		reset()
		// node2 is 8+1 blocks ahead of node1 (0x101 + 8+1 = 0x10a)
		overrideBlock("node2", "latest", "0x10a")
		update()

		// since we ignored node1, the consensus should be at 0x10a
		require.Equal(t, "0x10a", bg.Consensus.GetLatestBlockNumber().String())
		require.Equal(t, "0xe1", bg.Consensus.GetSafeBlockNumber().String())
		require.Equal(t, "0xc1", bg.Consensus.GetFinalizedBlockNumber().String())

		consensusGroup := bg.Consensus.GetConsensusGroup()
		require.NotContains(t, consensusGroup, nodes["node1"].backend)
		require.False(t, bg.Consensus.IsBanned(nodes["node1"].backend))
		require.Equal(t, 1, len(consensusGroup))
	})

	t.Run("prevent using a backend lagging behind - one before limit", func(t *testing.T) {
		reset()
		// node2 is 8 blocks ahead of node1 (0x101 + 8 = 0x109)
		overrideBlock("node2", "latest", "0x109")
		update()

		// both nodes are in consensus with the lowest block
		require.Equal(t, "0x101", bg.Consensus.GetLatestBlockNumber().String())
		require.Equal(t, "0xe1", bg.Consensus.GetSafeBlockNumber().String())
		require.Equal(t, "0xc1", bg.Consensus.GetFinalizedBlockNumber().String())
		require.Equal(t, 2, len(bg.Consensus.GetConsensusGroup()))
	})
}

// func buildResponse(result interface{}) string {
// 	res, err := json.Marshal(proxyd.RPCRes{
// 		Result: result,
// 	})
// 	if err != nil {
// 		panic(err)
// 	}
// 	return string(res)
// }
