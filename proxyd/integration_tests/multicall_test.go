package integration_tests

import (
	"bytes"
	// "context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"testing"

	"github.com/ethereum-optimism/optimism/proxyd"
	ms "github.com/ethereum-optimism/optimism/proxyd/tools/mockserver/handler"
	"github.com/stretchr/testify/require"
)

func setupMulticall(t *testing.T) (map[string]nodeContext, *proxyd.BackendGroup, *ProxydHTTPClient, func(), *proxyd.Server, []*ms.MockedHandler) {
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
	require.Nil(t, bg.Consensus, "Expeceted Consensus Not to be Initalized")
	require.Equal(t, 2, len(bg.Backends))                       // should match config
	require.Equal(t, bg.GetRoutingStrategy(), proxyd.Multicall) // should match config

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

	handlers := []*ms.MockedHandler{&h1, &h2}
	return nodes, bg, client, shutdown, svr, handlers
}

func TestMulticall(t *testing.T) {
	nodes, _, _, shutdown, svr, handlers := setupMulticall(t)
	defer nodes["node1"].mockBackend.Close()
	defer nodes["node2"].mockBackend.Close()
	defer shutdown()

	// ctx := context.Background()

	// poll for updated consensus
	// update := func() {
	// 	for _, be := range bg.Backends {
	// 		bg.Consensus.UpdateBackend(ctx, be)
	// 	}
	// 	bg.Consensus.UpdateBackendGroupConsensus(ctx)
	// }

	// convenient methods to manipulate state and mock responses
	reset := func() {
		for _, node := range nodes {
			node.handler.ResetOverrides()
			node.mockBackend.Reset()
			require.Zero(t, len(node.mockBackend.requests))
		}
		// NOTE: May want to make consensus an empty object or getter since it can cause nil pointer
		// bg.Consensus.ClearListeners()
		// bg.Consensus.Reset()

		// Reset Handlers to Original Values, Default Node 1 will respond
		nodes["node1"].mockBackend.SetHandler(SingleResponseHandler(200, dummyRes))
		nodes["node2"].mockBackend.SetHandler(http.HandlerFunc(handlers[0].Handler))
	}

	// override := func(node string, method string, block string, response string) {
	// 	if _, ok := nodes[node]; !ok {
	// 		t.Fatalf("node %s does not exist in the nodes map", node)
	// 	}
	// 	nodes[node].handler.AddOverride(&ms.MethodTemplate{
	// 		Method:   method,
	// 		Block:    block,
	// 		Response: response,
	// 	})
	// }

	// overrideBlock := func(node string, blockRequest string, blockResponse string) {
	// 	override(node,
	// 		"eth_getBlockByNumber",
	// 		blockRequest,
	// 		buildResponse(map[string]string{
	// 			"number": blockResponse,
	// 			"hash":   "hash_" + blockResponse,
	// 		}))
	// }

	// overridePeerCount := func(node string, count int) {
	// 	override(node, "net_peerCount", "", buildResponse(hexutil.Uint64(count).String()))
	// }

	nodeBackendRequestCount := func(node string) int {
		return len(nodes[node].mockBackend.requests)
	}

	setResponsiveBackend := func(node string) {
		for nodeName := range nodes {
			if nodeName == node {
				nodes[nodeName].mockBackend.SetHandler(SingleResponseHandler(200, dummyRes))
			} else {
				nodes[nodeName].mockBackend.SetHandler(http.HandlerFunc(handlers[0].Handler))
			}
		}
	}

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
		defer resp.Body.Close()
		require.NotNil(t, resp.Body)
		require.Equal(t, 200, resp.StatusCode)
		require.Equal(t, resp.Header["X-Served-By"], []string{"node/node1"})
		rpcRes := &proxyd.RPCRes{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(rpcRes))
		require.False(t, rpcRes.IsError())
		// unknown consensus at inik

		require.Equal(t, 1, nodeBackendRequestCount("node1"))
		require.Equal(t, 1, nodeBackendRequestCount("node2"))
	})

	t.Run("Modifying the backend list, we should expect only one request", func(t *testing.T) {
		for i := 0; i < 2; i++ {
			reset()

			body := makeSendRawTransaction(txHex1)
			req, _ := http.NewRequest("POST", "https://1.1.1.1:8080", bytes.NewReader(body))
			req.Header.Set("X-Forwarded-For", "203.0.113.1")
			rr := httptest.NewRecorder()

			bg1 := svr.BackendGroups
			bg1["node"].Backends = []*proxyd.Backend{
				nodes[fmt.Sprintf("node%d", i+1)].backend,
			}
			svr.BackendGroups = bg1

			setResponsiveBackend(fmt.Sprintf("node%d", i+1))

			svr.HandleRPC(rr, req)

			resp := rr.Result()
			defer resp.Body.Close()
			require.NotNil(t, resp.Body)
			require.Equal(t, 200, resp.StatusCode)
			servedBy := fmt.Sprintf("node/node%d", i+1)
			require.Equal(t, resp.Header["X-Served-By"], []string{servedBy})
			rpcRes := &proxyd.RPCRes{}
			require.NoError(t, json.NewDecoder(resp.Body).Decode(rpcRes))
			require.False(t, rpcRes.IsError())
			// unknown consensus at inik

			if i == 0 {
				require.Equal(t, 1, nodeBackendRequestCount("node1"))
				require.Equal(t, 0, nodeBackendRequestCount("node2"))
			} else {
				require.Equal(t, 0, nodeBackendRequestCount("node1"))
				require.Equal(t, 1, nodeBackendRequestCount("node2"))

			}
		}
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
