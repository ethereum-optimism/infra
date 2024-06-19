package integration_tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"sync"
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
	node3 := NewMockBackend(nil)

	dir, err := os.Getwd()
	require.NoError(t, err)

	responses := path.Join(dir, "testdata/multicall_responses.yml")
	emptyResponses := path.Join(dir, "testdata/empty_responses.yml")

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
	h3 := ms.MockedHandler{
		Overrides:    []*ms.MethodTemplate{},
		Autoload:     true,
		AutoloadFile: emptyResponses,
	}

	require.NoError(t, os.Setenv("NODE1_URL", node1.URL()))
	require.NoError(t, os.Setenv("NODE2_URL", node2.URL()))
	require.NoError(t, os.Setenv("NODE3_URL", node3.URL()))

	node1.SetHandler(http.HandlerFunc(h1.Handler))
	node2.SetHandler(SingleResponseHandler(200, txAccepted))
	node3.SetHandler(SingleResponseHandler(429, dummyRes))

	// setup proxyd
	config := ReadConfig("multicall")
	fmt.Printf("[SetupMulticall] Using Timeout of %d \n", config.Server.TimeoutSeconds)
	svr, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)

	// expose the proxyd client
	client := NewProxydClient("http://127.0.0.1:8545")

	// expose the backend group
	bg := svr.BackendGroups["node"]
	require.NotNil(t, bg)
	require.Nil(t, bg.Consensus, "Expeceted consensus not to be initialized")
	require.Equal(t, 3, len(bg.Backends))
	require.Equal(t, bg.GetRoutingStrategy(), proxyd.MulticallRoutingStrategy)

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
		"node3": {
			mockBackend: node3,
			backend:     bg.Backends[2],
			handler:     &h3,
		},
	}

	handlers := []*ms.MockedHandler{&h1, &h2, &h3}

	nodes["node1"].mockBackend.SetHandler(SingleResponseHandler(200, txAccepted))
	nodes["node2"].mockBackend.SetHandler(http.HandlerFunc(handlers[1].Handler))
	//Node 3 has no handler empty handler never respondes should always context timeout
	nodes["node3"].mockBackend.SetHandler(http.HandlerFunc(handlers[2].Handler))

	require.Equal(t, 0, nodeBackendRequestCount(nodes, "node1"))
	require.Equal(t, 0, nodeBackendRequestCount(nodes, "node2"))
	require.Equal(t, 0, nodeBackendRequestCount(nodes, "node3"))

	return nodes, bg, client, shutdown, svr, handlers
}

func setServerBackend(s *proxyd.Server, nm map[string]nodeContext) *proxyd.Server {
	bg := s.BackendGroups
	bg["node"].Backends = []*proxyd.Backend{
		nm["node1"].backend,
		nm["node2"].backend,
		nm["node3"].backend,
	}
	s.BackendGroups = bg
	return s
}

func nodeBackendRequestCount(nodes map[string]nodeContext, node string) int {
	return len(nodes[node].mockBackend.requests)
}

func TestMulticall(t *testing.T) {

	t.Run("Multicall will request all backends", func(t *testing.T) {
		nodes, _, _, shutdown, svr, _ := setupMulticall(t)
		defer nodes["node1"].mockBackend.Close()
		defer nodes["node2"].mockBackend.Close()
		defer nodes["node3"].mockBackend.Close()
		defer shutdown()

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
		require.Equal(t, 1, nodeBackendRequestCount(nodes, "node3"))
	})

	t.Run("When all of the backends return non 200, multicall should return 503", func(t *testing.T) {
		nodes, _, _, shutdown, svr, _ := setupMulticall(t)
		defer nodes["node1"].mockBackend.Close()
		defer nodes["node2"].mockBackend.Close()
		defer nodes["node3"].mockBackend.Close()
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
		require.Equal(t, 1, nodeBackendRequestCount(nodes, "node3"))
	})

	t.Run("It should return the first 200 response", func(t *testing.T) {
		nodes, _, _, shutdown, svr, _ := setupMulticall(t)
		defer nodes["node1"].mockBackend.Close()
		defer nodes["node2"].mockBackend.Close()
		defer nodes["node3"].mockBackend.Close()
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
		require.Equal(t, 1, nodeBackendRequestCount(nodes, "node3"))
	})

	t.Run("Ensure application level error is returned to caller if its first", func(t *testing.T) {
		nodes, _, _, shutdown, svr, _ := setupMulticall(t)
		defer nodes["node1"].mockBackend.Close()
		defer nodes["node2"].mockBackend.Close()
		defer nodes["node3"].mockBackend.Close()
		defer shutdown()

		nodes["node1"].mockBackend.SetHandler(SingleResponseHandlerWithSleep(200, txAccepted, 2*time.Second))
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
		require.Equal(t, 1, nodeBackendRequestCount(nodes, "node3"))
	})

	t.Run("It should ignore network errors and return a 200 from a slower request", func(t *testing.T) {
		nodes, _, _, shutdown, svr, _ := setupMulticall(t)
		defer nodes["node1"].mockBackend.Close()
		defer nodes["node2"].mockBackend.Close()
		defer nodes["node3"].mockBackend.Close()
		defer shutdown()

		// We should ignore node2 first response cause 429, and return node 1 because 200
		nodes["node1"].mockBackend.SetHandler(SingleResponseHandlerWithSleep(200, txAccepted, 3*time.Second))
		nodes["node2"].mockBackend.SetHandler(SingleResponseHandler(429, txAccepted))

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

		require.Equal(t, resp.Header["X-Served-By"], []string{"node/node1"})
		require.Equal(t, 1, nodeBackendRequestCount(nodes, "node1"))
		require.Equal(t, 1, nodeBackendRequestCount(nodes, "node2"))
		require.Equal(t, 1, nodeBackendRequestCount(nodes, "node3"))
	})

	t.Run("When one of the backends times out", func(t *testing.T) {
		nodes, _, _, shutdown, svr, _ := setupMulticall(t)
		defer nodes["node1"].mockBackend.Close()
		defer nodes["node2"].mockBackend.Close()
		defer nodes["node3"].mockBackend.Close()
		defer shutdown()

		shutdownChan := make(chan struct{})
		nodes["node1"].mockBackend.SetHandler(SingleResponseHandler(200, dummyRes))
		nodes["node2"].mockBackend.SetHandler(SingleResponseHandlerWithSleepShutdown(200, dummyRes, shutdownChan, 7*time.Second))

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
		require.Equal(t, 1, nodeBackendRequestCount(nodes, "node3"))
	})

	t.Run("allBackends times out", func(t *testing.T) {

		nodes, _, _, shutdown, svr, _ := setupMulticall(t)
		defer nodes["node1"].mockBackend.Close()
		defer nodes["node2"].mockBackend.Close()
		defer nodes["node3"].mockBackend.Close()
		defer shutdown()

		shutdownChan1 := make(chan struct{})
		shutdownChan2 := make(chan struct{})
		nodes["node1"].mockBackend.SetHandler(SingleResponseHandlerWithSleepShutdown(200, dummyRes, shutdownChan1, 7*time.Second))
		nodes["node2"].mockBackend.SetHandler(SingleResponseHandlerWithSleepShutdown(200, dummyRes, shutdownChan2, 7*time.Second))

		localSvr := setServerBackend(svr, nodes)

		body := makeSendRawTransaction(txHex1)
		req, _ := http.NewRequest("POST", "https://1.1.1.1:8080", bytes.NewReader(body))
		req.Header.Set("X-Forwarded-For", "203.0.113.1")
		rr := httptest.NewRecorder()

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			shutdownChan1 <- struct{}{}
			shutdownChan2 <- struct{}{}
			wg.Done()
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

		// Wait for test response to complete before checking query count
		wg.Wait()
		require.Equal(t, 1, nodeBackendRequestCount(nodes, "node1"))
		require.Equal(t, 1, nodeBackendRequestCount(nodes, "node2"))
		require.Equal(t, 1, nodeBackendRequestCount(nodes, "node3"))
	})

	t.Run("Test with many multi-calls in without resetting", func(t *testing.T) {
		nodes, _, _, shutdown, svr, _ := setupMulticall(t)
		defer nodes["node1"].mockBackend.Close()
		defer nodes["node2"].mockBackend.Close()
		defer nodes["node3"].mockBackend.Close()
		defer shutdown()

		for i := 1; i < 4; i++ {
			shutdownChan1 := make(chan struct{})
			shutdownChan2 := make(chan struct{})
			shutdownChan3 := make(chan struct{})

			switch {
			case i == 1:
				nodes["node1"].mockBackend.SetHandler(SingleResponseHandlerWithSleepShutdown(200, txAccepted, shutdownChan1, 1*time.Second))
				nodes["node2"].mockBackend.SetHandler(SingleResponseHandlerWithSleepShutdown(429, dummyRes, shutdownChan2, 1*time.Second))
				nodes["node3"].mockBackend.SetHandler(SingleResponseHandlerWithSleepShutdown(503, dummyRes, shutdownChan3, 1*time.Second))
			case i == 2:
				nodes["node1"].mockBackend.SetHandler(SingleResponseHandlerWithSleepShutdown(404, dummyRes, shutdownChan1, 1*time.Second))
				nodes["node2"].mockBackend.SetHandler(SingleResponseHandlerWithSleepShutdown(200, nonceErrorResponse, shutdownChan2, 1*time.Second))
				nodes["node3"].mockBackend.SetHandler(SingleResponseHandlerWithSleepShutdown(405, dummyRes, shutdownChan3, 1*time.Second))
			case i == 3:
				// Return the quickest response
				nodes["node1"].mockBackend.SetHandler(SingleResponseHandlerWithSleepShutdown(404, dummyRes, shutdownChan1, 1*time.Second))
				nodes["node2"].mockBackend.SetHandler(SingleResponseHandlerWithSleepShutdown(500, dummyRes, shutdownChan2, 1*time.Second))
				nodes["node3"].mockBackend.SetHandler(SingleResponseHandlerWithSleepShutdown(200, nonceErrorResponse, shutdownChan3, 1*time.Second))
			}

			localSvr := setServerBackend(svr, nodes)

			body := makeSendRawTransaction(txHex1)
			req, _ := http.NewRequest("POST", "https://1.1.1.1:8080", bytes.NewReader(body))
			req.Header.Set("X-Forwarded-For", "203.0.113.1")
			rr := httptest.NewRecorder()

			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				shutdownChan1 <- struct{}{}
				shutdownChan2 <- struct{}{}
				shutdownChan3 <- struct{}{}
				wg.Done()
			}()

			localSvr.HandleRPC(rr, req)

			resp := rr.Result()
			defer resp.Body.Close()

			require.NotNil(t, resp.Body)
			rpcRes := &proxyd.RPCRes{}
			require.NoError(t, json.NewDecoder(resp.Body).Decode(rpcRes))

			switch {
			case i == 1:
				servedBy := "node/node1"
				require.NotNil(t, rpcRes.Result)
				require.Equal(t, 200, resp.StatusCode, "expected 200 response from node1")
				require.Equal(t, resp.Header["X-Served-By"], []string{servedBy}, "Error incorrect node served the request")
				require.False(t, rpcRes.IsError())
			case i == 2:
				servedBy := "node/node2"
				require.Nil(t, rpcRes.Result)
				require.Equal(t, 200, resp.StatusCode, "expected 200 response from node2")
				require.Equal(t, resp.Header["X-Served-By"], []string{servedBy}, "Error incorrect node served the request")
				require.True(t, rpcRes.IsError())
			case i == 3:
				servedBy := "node/node3"
				require.Nil(t, rpcRes.Result)
				require.Equal(t, 200, resp.StatusCode, "expected 200 response from node3")
				require.Equal(t, resp.Header["X-Served-By"], []string{servedBy}, "Error incorrect node served the request")
				require.True(t, rpcRes.IsError())
			}
			// Wait for test response to complete before checking query count
			wg.Wait()
			require.Equal(t, i, nodeBackendRequestCount(nodes, "node1"))
			require.Equal(t, i, nodeBackendRequestCount(nodes, "node2"))
			require.Equal(t, i, nodeBackendRequestCount(nodes, "node3"))
		}
	})

}

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
