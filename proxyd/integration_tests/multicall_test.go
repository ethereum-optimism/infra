package integration_tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	// "sync"
	"testing"
	"time"

	"github.com/ethereum-optimism/optimism/proxyd"
	ms "github.com/ethereum-optimism/optimism/proxyd/tools/mockserver/handler"
	"github.com/stretchr/testify/require"
)

const nonceErrorResponse = `{"jsonrpc": "2.0","error": {"code": -32000, "message": "nonce too low"},"id": 1}`
const txAccepted = `{"jsonrpc": "2.0","result": "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef","id": 1}`

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
	node2.SetHandler(SingleResponseHandler(200, txAccepted))

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

	nodes["node1"].mockBackend.SetHandler(SingleResponseHandler(200, txAccepted))
	nodes["node2"].mockBackend.SetHandler(http.HandlerFunc(handlers[0].Handler))

	return nodes, bg, client, shutdown, svr, handlers
}

func setServerBackend(s *proxyd.Server, nodes map[string]nodeContext) *proxyd.Server {
	bg := s.BackendGroups
	bg["node"].Backends = []*proxyd.Backend{
		nodes["node1"].backend,
		nodes["node2"].backend,
	}
	s.BackendGroups = bg
	return s
}

func TestMulticall(t *testing.T) {

	// convenient methods to manipulate state and mock responses

	nodeBackendRequestCount := func(nodes map[string]nodeContext, node string) int {
		return len(nodes[node].mockBackend.requests)
	}

	t.Run("Multicall will request all backends", func(t *testing.T) {
		nodes, _, _, shutdown, svr, _ := setupMulticall(t)
		defer nodes["node1"].mockBackend.Close()
		defer nodes["node2"].mockBackend.Close()
		defer shutdown()

		// reset()

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

		require.Equal(t, 1, nodeBackendRequestCount(nodes, "node1"))
		require.Equal(t, 1, nodeBackendRequestCount(nodes, "node2"))
	})

	t.Run("When all of the backends return non 200, multicall should return 503", func(t *testing.T) {
		nodes, _, _, shutdown, svr, _ := setupMulticall(t)
		defer nodes["node1"].mockBackend.Close()
		defer nodes["node2"].mockBackend.Close()
		defer shutdown()

		nodes["node1"].mockBackend.SetHandler(SingleResponseHandler(429, dummyRes))
		nodes["node2"].mockBackend.SetHandler(SingleResponseHandler(429, dummyRes))

		localSvr := setServerBackend(svr, nodes)

		body := makeSendRawTransaction(txHex1)
		req, _ := http.NewRequest("POST", "https://1.1.1.1:8080", bytes.NewReader(body))
		req.Header.Set("X-Forwarded-For", "203.0.113.1")
		rr := httptest.NewRecorder()

		localSvr.HandleRPC(rr, req)

		resp := rr.Result()
		defer resp.Body.Close()

		require.NotNil(t, resp.Body)
		require.Equal(t, 503, resp.StatusCode)
		rpcRes := &proxyd.RPCRes{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(rpcRes))
		require.True(t, rpcRes.IsError())
		require.Equal(t, proxyd.ErrNoBackends.Code, rpcRes.Error.Code)
		require.Equal(t, proxyd.ErrNoBackends.Message, rpcRes.Error.Message)

		require.Equal(t, 1, nodeBackendRequestCount(nodes, "node1"))
		require.Equal(t, 1, nodeBackendRequestCount(nodes, "node2"))
	})

	t.Run("It should return the first 200 response", func(t *testing.T) {
		nodes, _, _, shutdown, svr, _ := setupMulticall(t)
		defer nodes["node1"].mockBackend.Close()
		defer nodes["node2"].mockBackend.Close()
		defer shutdown()

		nodes["node1"].mockBackend.SetHandler(SingleResponseHandlerWithSleep(200, txAccepted, 3*time.Second))
		nodes["node2"].mockBackend.SetHandler(SingleResponseHandler(200, txAccepted))

		localSvr := setServerBackend(svr, nodes)

		body := makeSendRawTransaction(txHex1)
		req, _ := http.NewRequest("POST", "https://1.1.1.1:8080", bytes.NewReader(body))
		req.Header.Set("X-Forwarded-For", "203.0.113.1")
		rr := httptest.NewRecorder()

		localSvr.HandleRPC(rr, req)

		resp := rr.Result()
		defer resp.Body.Close()

		require.NotNil(t, resp.Body)
		require.Equal(t, 200, resp.StatusCode)
		rpcRes := &proxyd.RPCRes{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(rpcRes))
		require.False(t, rpcRes.IsError())
		require.Equal(t, "2.0", rpcRes.JSONRPC)

		require.Equal(t, resp.Header["X-Served-By"], []string{"node/node2"})
		require.False(t, rpcRes.IsError())

		require.Equal(t, 1, nodeBackendRequestCount(nodes, "node1"))
		require.Equal(t, 1, nodeBackendRequestCount(nodes, "node2"))
	})

	t.Run("Ensure application level error is returned to caller", func(t *testing.T) {
		nodes, _, _, shutdown, svr, _ := setupMulticall(t)
		defer nodes["node1"].mockBackend.Close()
		defer nodes["node2"].mockBackend.Close()
		defer shutdown()

		nodes["node1"].mockBackend.SetHandler(SingleResponseHandlerWithSleep(200, nonceErrorResponse, 3*time.Second))
		nodes["node2"].mockBackend.SetHandler(SingleResponseHandler(200, nonceErrorResponse))

		localSvr := setServerBackend(svr, nodes)

		body := makeSendRawTransaction(txHex1)
		req, _ := http.NewRequest("POST", "https://1.1.1.1:8080", bytes.NewReader(body))
		req.Header.Set("X-Forwarded-For", "203.0.113.1")
		rr := httptest.NewRecorder()

		localSvr.HandleRPC(rr, req)

		resp := rr.Result()
		defer resp.Body.Close()

		require.NotNil(t, resp.Body)
		require.Equal(t, 200, resp.StatusCode)
		rpcRes := &proxyd.RPCRes{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(rpcRes))
		require.True(t, rpcRes.IsError())
		require.Equal(t, "2.0", rpcRes.JSONRPC)

		require.Equal(t, resp.Header["X-Served-By"], []string{"node/node2"})
		require.True(t, rpcRes.IsError())

		require.Equal(t, 1, nodeBackendRequestCount(nodes, "node1"))
		require.Equal(t, 1, nodeBackendRequestCount(nodes, "node2"))

	})

	t.Run("When one of the backends times out", func(t *testing.T) {
		nodes, _, _, shutdown, svr, _ := setupMulticall(t)
		defer nodes["node1"].mockBackend.Close()
		defer nodes["node2"].mockBackend.Close()
		defer shutdown()

		shutdownChan := make(chan struct{})
		nodes["node1"].mockBackend.SetHandler(SingleResponseHandler(200, dummyRes))
		nodes["node2"].mockBackend.SetHandler(SingleResponseHandlerWithSleepShutdown(200, dummyRes, shutdownChan, 6*time.Second))

		localSvr := setServerBackend(svr, nodes)

		body := makeSendRawTransaction(txHex1)
		req, _ := http.NewRequest("POST", "https://1.1.1.1:8080", bytes.NewReader(body))
		req.Header.Set("X-Forwarded-For", "203.0.113.1")
		rr := httptest.NewRecorder()

		localSvr.HandleRPC(rr, req)
		resp := rr.Result()
		shutdownChan <- struct{}{}
		defer resp.Body.Close()

		require.NotNil(t, resp.Body)
		servedBy := "node/node1"
		require.Equal(t, 200, resp.StatusCode, "expected 200 response from node1")

		require.Equal(t, resp.Header["X-Served-By"], []string{servedBy}, "Error incorrect node served the request")
		rpcRes := &proxyd.RPCRes{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(rpcRes))
		require.False(t, rpcRes.IsError())

		require.Equal(t, 1, nodeBackendRequestCount(nodes, "node1"))
		require.Equal(t, 1, nodeBackendRequestCount(nodes, "node2"))
	})

	t.Run("allBackends times out", func(t *testing.T) {

		nodes, _, _, shutdown, svr, _ := setupMulticall(t)
		defer nodes["node1"].mockBackend.Close()
		defer nodes["node2"].mockBackend.Close()
		defer shutdown()

		shutdownChan1 := make(chan struct{})
		shutdownChan2 := make(chan struct{})
		nodes["node1"].mockBackend.SetHandler(SingleResponseHandlerWithSleepShutdown(200, dummyRes, shutdownChan1, 6*time.Second))
		nodes["node2"].mockBackend.SetHandler(SingleResponseHandlerWithSleepShutdown(200, dummyRes, shutdownChan2, 6*time.Second))

		localSvr := setServerBackend(svr, nodes)

		body := makeSendRawTransaction(txHex1)
		req, _ := http.NewRequest("POST", "https://1.1.1.1:8080", bytes.NewReader(body))
		req.Header.Set("X-Forwarded-For", "203.0.113.1")
		rr := httptest.NewRecorder()

		go func() {
			shutdownChan1 <- struct{}{}
			shutdownChan2 <- struct{}{}
		}()

		fmt.Println("sending request")
		localSvr.HandleRPC(rr, req)

		resp := rr.Result()
		defer resp.Body.Close()

		require.NotNil(t, resp.Body)
		require.Equal(t, 503, resp.StatusCode, "expected no response")
		rpcRes := &proxyd.RPCRes{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(rpcRes))
		require.True(t, rpcRes.IsError())
		require.Equal(t, rpcRes.Error.Code, proxyd.ErrNoBackends.Code)

		require.Equal(t, 1, nodeBackendRequestCount(nodes, "node1"))
		require.Equal(t, 1, nodeBackendRequestCount(nodes, "node2"))
	})

}

// func SingleResponseHandlerWithSleepWg(code int, response string, responseTime time.Duration, wg *sync.WaitGroup) http.HandlerFunc {
// 	return func(w http.ResponseWriter, r *http.Request) {
// 		defer wg.Done()
// 		time.Sleep(responseTime)
// 		w.WriteHeader(code)
// 		_, _ = w.Write([]byte(response))
// 	}
// }

func SingleResponseHandlerWithSleep(code int, response string, duration time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fmt.Println("sleeping")
		time.Sleep(duration)
		fmt.Println("Shutting down Single Response Handler")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(response))
	}
}

func SingleResponseHandlerWithSleepShutdown(code int, response string, shutdownServer chan struct{}, duration time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fmt.Println("sleeping")
		time.Sleep(duration)
		<-shutdownServer
		fmt.Println("Shutting down Single Response Handler")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(response))
	}
}

// t.Run("Modifying the backend list, we should expect only one request", func(t *testing.T) {
// 	for i := 1; i < 3; i++ {
// 		reset()
//
// 		body := makeSendRawTransaction(txHex1)
// 		req, _ := http.NewRequest("POST", "https://1.1.1.1:8080", bytes.NewReader(body))
// 		req.Header.Set("X-Forwarded-For", "203.0.113.1")
// 		rr := httptest.NewRecorder()
//
// 		// bg1 := svr.BackendGroups
// 		// bg1["node"].Backends = []*proxyd.Backend{
// 		// 	nodes[fmt.Sprintf("node%d", i+1)].backend,
// 		// }
// 		localSvr := setServerBackend(
// 			map[string]nodeContext{
// 				"node": nodes[fmt.Sprintf("node%d", i+1)],
// 			},
// 		)
//
// 		// if nodeName == node {
// 		// 	nodes[nodeName].mockBackend.SetHandler(SingleResponseHandler(200, txAccepted))
// 		// } else {
// 		// 	nodes[nodeName].mockBackend.SetHandler(http.HandlerFunc(handlers[0].Handler))
// 		// }
//
// 		localSvr.HandleRPC(rr, req)
//
// 		resp := rr.Result()
// 		defer resp.Body.Close()
// 		require.NotNil(t, resp.Body)
// 		require.Equal(t, 200, resp.StatusCode)
// 		servedBy := fmt.Sprintf("node/node%d", i+1)
// 		require.Equal(t, resp.Header["X-Served-By"], []string{servedBy})
// 		rpcRes := &proxyd.RPCRes{}
// 		require.NoError(t, json.NewDecoder(resp.Body).Decode(rpcRes))
// 		require.False(t, rpcRes.IsError())
// 		if i == 0 {
// 			require.Equal(t, 1, nodeBackendRequestCount("node1"))
// 			require.Equal(t, 0, nodeBackendRequestCount("node2"))
// 		} else {
// 			require.Equal(t, 0, nodeBackendRequestCount("node1"))
// 			require.Equal(t, 1, nodeBackendRequestCount("node2"))
//
// 		}
// 	}
// })
