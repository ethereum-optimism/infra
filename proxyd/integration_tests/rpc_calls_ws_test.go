package integration_tests

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum-optimism/infra/proxyd"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// wsTestConfig returns a config where eth_chainId is both ws-whitelisted and
// mapped (so it takes the handleWSRPC path) while eth_subscribe is ws-only (so it
// takes the relay path). Mirrors the setup in ws_test.go:153.
func wsTestConfig(httpBackendURL, wsBackendURL string) *proxyd.Config {
	config := ReadConfig("rpc_calls")
	config.Server.RPCPort = 0
	config.Server.WSPort = 8546
	config.WSBackendGroup = "main"
	config.WSMethodWhitelist = []string{"eth_subscribe", "eth_accounts", "eth_chainId"}
	config.Backends["good"].RPCURL = httpBackendURL
	config.Backends["good"].WSURL = wsBackendURL
	return config
}

func TestRPCCallsWSRelayedSubscribeAndNotifications(t *testing.T) {
	InitLogger()

	// The WS backend answers the subscribe, then pushes two notifications.
	wsBackend := NewMockWSBackend(nil, func(conn *websocket.Conn, msgType int, data []byte) {
		require.NoError(t, conn.WriteMessage(websocket.TextMessage,
			[]byte(`{"jsonrpc":"2.0","id":1,"result":"0xcafe"}`)))
		for i := 0; i < 2; i++ {
			require.NoError(t, conn.WriteMessage(websocket.TextMessage,
				[]byte(`{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":"0xcafe","result":{}}}`)))
		}
	}, nil)
	defer wsBackend.Close()

	httpBackend := NewMockBackend(SingleResponseHandler(200, buildResponse("0x1")))
	defer httpBackend.Close()

	_, shutdown, err := proxyd.Start(wsTestConfig(httpBackend.URL(), wsBackend.URL()))
	require.NoError(t, err)
	defer shutdown()

	received := make(chan struct{}, 8)
	client, err := NewProxydWSClient("ws://127.0.0.1:8546", func(msgType int, data []byte) {
		received <- struct{}{}
	}, nil)
	require.NoError(t, err)
	defer client.HardClose()

	relayedLabels := map[string]string{
		"method_name": "eth_subscribe",
		"status_code": "relayed",
		"transport":   "ws",
	}
	beforeCalls := sumRPCCalls(t, relayedLabels)
	beforeNotifs := sumCounter(t, "proxyd_rpc_notifications_total", map[string]string{})

	require.NoError(t, client.WriteMessage(websocket.TextMessage,
		[]byte(`{"id":1,"method":"eth_subscribe","params":["newHeads"]}`)))

	// Wait for the response plus both notifications to reach the client.
	for i := 0; i < 3; i++ {
		select {
		case <-received:
		case <-time.After(10 * time.Second):
			t.Fatalf("timed out waiting for WS message %d", i)
		}
	}

	// One frame in = exactly one call, marked relayed because its response cannot
	// be correlated back to the request.
	require.Equal(t, beforeCalls+1, sumRPCCalls(t, relayedLabels))
	// Both notifications counted as notifications, not as calls.
	require.Equal(t, beforeNotifs+2, sumCounter(t, "proxyd_rpc_notifications_total", map[string]string{}))
	// A notification must never be counted as a call.
	require.Zero(t, sumRPCCalls(t, map[string]string{"method_name": "eth_subscription"}))
}

func TestRPCCallsWSMappedMethodRecordsRealStatusAndBackend(t *testing.T) {
	InitLogger()

	// eth_chainId is ws-whitelisted AND mapped, so it goes through handleWSRPC and
	// out to the HTTP backend — the WS backend must see nothing.
	var wsBackendMessages atomic.Int64
	wsBackend := NewMockWSBackend(nil, func(conn *websocket.Conn, msgType int, data []byte) {
		wsBackendMessages.Add(1)
	}, nil)
	defer wsBackend.Close()

	httpBackend := NewMockBackend(SingleResponseHandler(200, buildResponse("0x1")))
	defer httpBackend.Close()

	_, shutdown, err := proxyd.Start(wsTestConfig(httpBackend.URL(), wsBackend.URL()))
	require.NoError(t, err)
	defer shutdown()

	received := make(chan struct{}, 4)
	client, err := NewProxydWSClient("ws://127.0.0.1:8546", func(msgType int, data []byte) {
		received <- struct{}{}
	}, nil)
	require.NoError(t, err)
	defer client.HardClose()

	// Real derived status and the serving backend's name — not "relayed".
	labels := map[string]string{
		"backend_name": "good",
		"method_name":  "eth_chainId",
		"status_code":  "200",
		"transport":    "ws",
	}
	before := sumRPCCalls(t, labels)

	require.NoError(t, client.WriteMessage(websocket.TextMessage,
		[]byte(`{"jsonrpc":"2.0","method":"eth_chainId","id":1}`)))

	select {
	case <-received:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for eth_chainId response")
	}

	require.Equal(t, before+1, sumRPCCalls(t, labels))
	require.Equal(t, int64(0), wsBackendMessages.Load())
	// It must not also be counted as relayed — that would mean both clientPump and
	// handleWSRPC recorded the same call.
	require.Zero(t, sumRPCCalls(t, map[string]string{
		"method_name": "eth_chainId",
		"status_code": "relayed",
	}))
}
