package integration_tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/ethereum-optimism/optimism/proxyd"
	ms "github.com/ethereum-optimism/optimism/proxyd/tools/mockserver/handler"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"testing"
	// "time"
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
	require.Nil(t, bg.Consensus, "Expeceted consensus not to be initialized")
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

	setServerBackend := func(nodes map[string]nodeContext) {
		bg := svr.BackendGroups
		bg["node"].Backends = []*proxyd.Backend{
			nodes["node1"].backend,
			nodes["node2"].backend,
		}
		svr.BackendGroups = bg
	}
	// convenient methods to manipulate state and mock responses
	reset := func() {
		for _, node := range nodes {
			node.handler.ResetOverrides()
			node.mockBackend.Reset()
			require.Zero(t, len(node.mockBackend.requests))
		}

		// NOTE: Handlers to Original Values, Default Node 1 will respond
		nodes["node1"].mockBackend.SetHandler(SingleResponseHandler(200, dummyRes))
		nodes["node2"].mockBackend.SetHandler(http.HandlerFunc(handlers[0].Handler))

		setServerBackend(nodes)
	}

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
			if i == 0 {
				require.Equal(t, 1, nodeBackendRequestCount("node1"))
				require.Equal(t, 0, nodeBackendRequestCount("node2"))
			} else {
				require.Equal(t, 0, nodeBackendRequestCount("node1"))
				require.Equal(t, 1, nodeBackendRequestCount("node2"))

			}
		}
	})

	t.Run("When all of the backends return non 200", func(t *testing.T) {
		reset()
		nodes["node1"].mockBackend.SetHandler(SingleResponseHandler(429, dummyRes))
		nodes["node2"].mockBackend.SetHandler(SingleResponseHandler(429, dummyRes))

		setServerBackend(nodes)

		body := makeSendRawTransaction(txHex1)
		req, _ := http.NewRequest("POST", "https://1.1.1.1:8080", bytes.NewReader(body))
		req.Header.Set("X-Forwarded-For", "203.0.113.1")
		rr := httptest.NewRecorder()

		svr.HandleRPC(rr, req)

		resp := rr.Result()
		defer resp.Body.Close()

		require.NotNil(t, resp.Body)
		require.Equal(t, 503, resp.StatusCode)
		rpcRes := &proxyd.RPCRes{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(rpcRes))
		require.True(t, rpcRes.IsError())
		require.Equal(t, proxyd.ErrNoBackends.Code, rpcRes.Error.Code)
		require.Equal(t, proxyd.ErrNoBackends.Message, rpcRes.Error.Message)

		require.Equal(t, 1, nodeBackendRequestCount("node1"))
		require.Equal(t, 1, nodeBackendRequestCount("node2"))
	})

	// t.Run("When one of the backends times out", func(t *testing.T) {
	//
	// 	for i := 1; i < 3; i++ {
	// 		reset()
	// 		fmt.Println("===============backend timeout iteration test ", i, " ================")
	// 		shutdownChan := make(chan struct{})
	// 		if i == 1 {
	// 			nodes["node1"].mockBackend.SetHandler(SingleResponseHandler(200, dummyRes))
	// 			nodes["node2"].mockBackend.SetHandler(SingleResponseHandlerWithSleepShutdown(200, dummyRes, shutdownChan))
	// 		} else if i == 2 {
	// 			nodes["node1"].mockBackend.SetHandler(SingleResponseHandlerWithSleepShutdown(200, dummyRes, shutdownChan))
	// 			nodes["node2"].mockBackend.SetHandler(SingleResponseHandler(200, dummyRes))
	// 		}
	//
	// 		setServerBackend(nodes)
	//
	// 		body := makeSendRawTransaction(txHex1)
	// 		req, _ := http.NewRequest("POST", "https://1.1.1.1:8080", bytes.NewReader(body))
	// 		req.Header.Set("X-Forwarded-For", "203.0.113.1")
	// 		rr := httptest.NewRecorder()
	//
	// 		svr.HandleRPC(rr, req)
	// 		resp := rr.Result()
	// 		shutdownChan <- struct{}{}
	// 		defer resp.Body.Close()
	//
	// 		require.NotNil(t, resp.Body)
	// 		servedBy := fmt.Sprintf("node/node%d", i)
	// 		require.Equal(t, 200, resp.StatusCode, fmt.Sprintf("expected 200 response from node%d", i))
	//
	// 		require.Equal(t, resp.Header["X-Served-By"], []string{servedBy}, "Error incorrect node served the request")
	// 		rpcRes := &proxyd.RPCRes{}
	// 		require.NoError(t, json.NewDecoder(resp.Body).Decode(rpcRes))
	// 		require.False(t, rpcRes.IsError())
	//
	// 		require.Equal(t, 1, nodeBackendRequestCount("node1"))
	// 		require.Equal(t, 1, nodeBackendRequestCount("node2"))
	// 	}
	// })

	// t.Run("allBackends times out", func(t *testing.T) {
	// 	reset()
	// 	shutdownChan2 := make(chan struct{})
	// 	nodes["node1"].mockBackend.SetHandler(SingleResponseHandlerWithSleepShutdown(200, dummyRes, shutdownChan2))
	// 	nodes["node2"].mockBackend.SetHandler(SingleResponseHandlerWithSleepShutdown(200, dummyRes, shutdownChan2))
	// 	// nodes["node2"].mockBackend.SetHandler(SingleResponseHandler(200, dummyRes))
	//
	// 	setServerBackend(nodes)
	//
	// 	body := makeSendRawTransaction(txHex1)
	// 	req, _ := http.NewRequest("POST", "https://1.1.1.1:8080", bytes.NewReader(body))
	// 	req.Header.Set("X-Forwarded-For", "203.0.113.1")
	// 	rr := httptest.NewRecorder()
	//
	// 	go func() {
	// 		shutdownChan2 <- struct{}{}
	// 	}()
	//
	// 	fmt.Println("sending request")
	// 	svr.HandleRPC(rr, req)
	//
	// 	resp := rr.Result()
	// 	defer resp.Body.Close()
	//
	// 	require.NotNil(t, resp.Body)
	// 	require.Equal(t, 200, resp.StatusCode, "expected no response")
	// 	rpcRes := &proxyd.RPCRes{}
	// 	require.NoError(t, json.NewDecoder(resp.Body).Decode(rpcRes))
	// 	require.True(t, rpcRes.IsError())
	// 	require.Equal(t, rpcRes.Error.Code, proxyd.JSONRPCErrorInternal)
	//
	// 	require.Equal(t, 1, nodeBackendRequestCount("node1"))
	// 	require.Equal(t, 1, nodeBackendRequestCount("node2"))
	// })
	//
	//
	// // Make the handlers take various time, and ensure shortest time is always returned first
	// t.Run("Ensure we return the first success back to the caller", func(t *testing.T) {})
	//
	// t.Run("Ensure application level error is returned to caller", func(t *testing.T) {})

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
