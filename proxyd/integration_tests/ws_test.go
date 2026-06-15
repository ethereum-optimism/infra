package integration_tests

import (
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum-optimism/infra/proxyd"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

type backendHandler struct {
	msgCB   atomic.Value
	closeCB atomic.Value
}

func (b *backendHandler) MsgCB(conn *websocket.Conn, msgType int, data []byte) {
	cb := b.msgCB.Load()
	if cb == nil {
		return
	}
	cb.(MockWSBackendOnMessage)(conn, msgType, data)
}

func (b *backendHandler) SetMsgCB(cb MockWSBackendOnMessage) {
	b.msgCB.Store(cb)
}

func (b *backendHandler) CloseCB(conn *websocket.Conn, err error) {
	cb := b.closeCB.Load()
	if cb == nil {
		return
	}
	cb.(MockWSBackendOnClose)(conn, err)
}

func (b *backendHandler) SetCloseCB(cb MockWSBackendOnClose) {
	b.closeCB.Store(cb)
}

type clientHandler struct {
	msgCB atomic.Value
}

func (c *clientHandler) MsgCB(msgType int, data []byte) {
	cb := c.msgCB.Load().(ProxydWSClientOnMessage)
	if cb == nil {
		return
	}
	cb(msgType, data)
}

func (c *clientHandler) SetMsgCB(cb ProxydWSClientOnMessage) {
	c.msgCB.Store(cb)
}

func TestWS(t *testing.T) {
	backendHdlr := new(backendHandler)
	clientHdlr := new(clientHandler)

	backend := NewMockWSBackend(nil, func(conn *websocket.Conn, msgType int, data []byte) {
		backendHdlr.MsgCB(conn, msgType, data)
	}, func(conn *websocket.Conn, err error) {
		backendHdlr.CloseCB(conn, err)
	})
	defer backend.Close()

	require.NoError(t, os.Setenv("GOOD_BACKEND_RPC_URL", backend.URL()))

	config := ReadConfig("ws")
	_, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)
	client, err := NewProxydWSClient("ws://127.0.0.1:8546", func(msgType int, data []byte) {
		clientHdlr.MsgCB(msgType, data)
	}, nil)
	defer client.HardClose()
	require.NoError(t, err)
	defer shutdown()

	tests := []struct {
		name       string
		backendRes string
		expRes     string
		clientReq  string
	}{
		{
			"ok response",
			"{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":\"0xcd0c3e8af590364c09d0fa6a1210faf5\"}",
			"{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":\"0xcd0c3e8af590364c09d0fa6a1210faf5\"}",
			"{\"id\": 1, \"method\": \"eth_subscribe\", \"params\": [\"newHeads\"]}",
		},
		{
			"garbage backend response",
			"gibblegabble",
			"{\"jsonrpc\":\"2.0\",\"error\":{\"code\":-32013,\"message\":\"backend returned an invalid response\"},\"id\":null}",
			"{\"id\": 1, \"method\": \"eth_subscribe\", \"params\": [\"newHeads\"]}",
		},
		{
			"blacklisted RPC",
			"}",
			"{\"jsonrpc\":\"2.0\",\"error\":{\"code\":-32601,\"message\":\"rpc method is not whitelisted\"},\"id\":1}",
			"{\"id\": 1, \"method\": \"eth_whatever\", \"params\": []}",
		},
		{
			"garbage client request",
			"{}",
			"{\"jsonrpc\":\"2.0\",\"error\":{\"code\":-32700,\"message\":\"parse error\"},\"id\":null}",
			"barf",
		},
		{
			"invalid client request",
			"{}",
			"{\"jsonrpc\":\"2.0\",\"error\":{\"code\":-32700,\"message\":\"parse error\"},\"id\":null}",
			"{\"jsonrpc\": \"2.0\", \"method\": true}",
		},
		{
			"eth_accounts",
			"{}",
			"{\"jsonrpc\":\"2.0\",\"result\":[],\"id\":1}",
			"{\"jsonrpc\": \"2.0\", \"method\": \"eth_accounts\", \"id\": 1}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			timeout := time.NewTicker(10 * time.Second)
			doneCh := make(chan struct{}, 1)
			backendHdlr.SetMsgCB(func(conn *websocket.Conn, msgType int, data []byte) {
				require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(tt.backendRes)))
			})
			clientHdlr.SetMsgCB(func(msgType int, data []byte) {
				require.Equal(t, tt.expRes, string(data))
				doneCh <- struct{}{}
			})
			require.NoError(t, client.WriteMessage(
				websocket.TextMessage,
				[]byte(tt.clientReq),
			))
			select {
			case <-timeout.C:
				t.Fatalf("timed out")
			case <-doneCh:
				return
			}
		})
	}
}

func TestWSMappedTransactionUsesHTTPPipeline(t *testing.T) {
	var wsBackendMessages atomic.Int64
	wsBackend := NewMockWSBackend(nil, func(conn *websocket.Conn, msgType int, data []byte) {
		wsBackendMessages.Add(1)
		require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"jsonrpc":"2.0","id":1,"result":"ws-backend"}`)))
	}, nil)
	defer wsBackend.Close()

	httpBackend := NewMockBackend(SingleResponseHandler(200, dummyRes))
	defer httpBackend.Close()

	config := ReadConfig("sender_rate_limit")
	config.Server.RPCPort = 0
	config.Server.WSPort = 8546
	config.WSBackendGroup = "main"
	config.WSMethodWhitelist = []string{"eth_sendRawTransaction"}
	config.Backends["good"].RPCURL = httpBackend.URL()
	config.Backends["good"].WSURL = wsBackend.URL()

	_, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)
	defer shutdown()

	conn, _, err := websocket.DefaultDialer.Dial("ws://127.0.0.1:8546", nil) // nolint:bodyclose
	require.NoError(t, err)
	defer conn.Close()

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, makeSendRawTransaction(txHex1)))
	_, res, err := conn.ReadMessage()
	require.NoError(t, err)
	RequireEqualJSON(t, []byte(dummyRes), res)
	require.Len(t, httpBackend.Requests(), 1)
	require.Equal(t, int64(0), wsBackendMessages.Load())

	require.NoError(t, conn.WriteMessage(websocket.TextMessage, makeSendRawTransaction(txHex1)))
	_, res, err = conn.ReadMessage()
	require.NoError(t, err)
	RequireEqualJSON(t, []byte(limRes), res)
	require.Len(t, httpBackend.Requests(), 1)
	require.Equal(t, int64(0), wsBackendMessages.Load())
}

func TestWSWhitelistedUnmappedTransactionRejected(t *testing.T) {
	var wsBackendMessages atomic.Int64
	wsBackend := NewMockWSBackend(nil, func(conn *websocket.Conn, msgType int, data []byte) {
		wsBackendMessages.Add(1)
		require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"jsonrpc":"2.0","id":1,"result":"ws-backend"}`)))
	}, nil)
	defer wsBackend.Close()

	httpBackend := NewMockBackend(SingleResponseHandler(200, dummyRes))
	defer httpBackend.Close()

	config := ReadConfig("sender_rate_limit")
	config.Server.RPCPort = 0
	config.Server.WSPort = 8546
	config.WSBackendGroup = "main"
	config.WSMethodWhitelist = []string{"eth_sendRawTransaction"}
	config.Backends["good"].RPCURL = httpBackend.URL()
	config.Backends["good"].WSURL = wsBackend.URL()
	delete(config.RPCMethodMappings, "eth_sendRawTransaction")

	_, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)
	defer shutdown()

	conn, _, err := websocket.DefaultDialer.Dial("ws://127.0.0.1:8546", nil) // nolint:bodyclose
	require.NoError(t, err)
	defer conn.Close()

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, makeSendRawTransaction(txHex1)))
	_, res, err := conn.ReadMessage()
	require.NoError(t, err)
	RequireEqualJSON(t, []byte(`{"jsonrpc":"2.0","error":{"code":-32601,"message":"rpc method is not whitelisted"},"id":1}`), res)
	require.Empty(t, httpBackend.Requests())
	require.Equal(t, int64(0), wsBackendMessages.Load())
}

func TestWSWhitelistedUnmappedSubscriptionUsesWSBackend(t *testing.T) {
	var wsBackendMessages atomic.Int64
	wsBackend := NewMockWSBackend(nil, func(conn *websocket.Conn, msgType int, data []byte) {
		wsBackendMessages.Add(1)
		require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"jsonrpc":"2.0","id":1,"result":"subscription-ok"}`)))
	}, nil)
	defer wsBackend.Close()

	config := ReadConfig("ws")
	config.Server.RPCPort = 0
	config.Backends["good"].RPCURL = wsBackend.URL()
	config.Backends["good"].WSURL = wsBackend.URL()
	config.WSMethodWhitelist = []string{"eth_subscribe"}

	_, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)
	defer shutdown()

	conn, _, err := websocket.DefaultDialer.Dial("ws://127.0.0.1:8546", nil) // nolint:bodyclose
	require.NoError(t, err)
	defer conn.Close()

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"jsonrpc":"2.0","id":1,"method":"eth_subscribe","params":["newHeads"]}`)))
	_, res, err := conn.ReadMessage()
	require.NoError(t, err)
	RequireEqualJSON(t, []byte(`{"jsonrpc":"2.0","id":1,"result":"subscription-ok"}`), res)
	require.Equal(t, int64(1), wsBackendMessages.Load())
}

func TestWSBackendMaxConnsFallsBackAndReleases(t *testing.T) {
	rpcBackend := NewMockBackend(SingleResponseHandler(200, dummyRes))
	defer rpcBackend.Close()

	var firstConnections atomic.Int64
	var firstClosed sync.Once
	firstClosedCh := make(chan struct{})
	firstWSBackend := NewMockWSBackend(func(conn *websocket.Conn) {
		firstConnections.Add(1)
	}, func(conn *websocket.Conn, msgType int, data []byte) {
		require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"jsonrpc":"2.0","id":1,"result":"first"}`)))
	}, func(conn *websocket.Conn, err error) {
		firstClosed.Do(func() {
			close(firstClosedCh)
		})
	})
	defer firstWSBackend.Close()

	var secondConnections atomic.Int64
	secondWSBackend := NewMockWSBackend(func(conn *websocket.Conn) {
		secondConnections.Add(1)
	}, func(conn *websocket.Conn, msgType int, data []byte) {
		require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"jsonrpc":"2.0","id":1,"result":"second"}`)))
	}, nil)
	defer secondWSBackend.Close()

	config := ReadConfig("ws")
	config.Server.RPCPort = 0
	config.Backends["good"].RPCURL = rpcBackend.URL()
	config.Backends["good"].WSURL = firstWSBackend.URL()
	config.Backends["good"].MaxWSConns = 1
	config.Backends["next"] = &proxyd.BackendConfig{
		RPCURL: rpcBackend.URL(),
		WSURL:  secondWSBackend.URL(),
	}
	config.BackendGroups["main"].Backends = []string{"good", "next"}
	config.WSMethodWhitelist = []string{"eth_subscribe"}

	_, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)
	defer shutdown()

	dial := func() (*websocket.Conn, error) {
		conn, _, err := websocket.DefaultDialer.Dial("ws://127.0.0.1:8546", nil) // nolint:bodyclose
		if err != nil {
			return nil, err
		}
		require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
		return conn, nil
	}
	sendSubscribe := func(conn *websocket.Conn) ([]byte, error) {
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"jsonrpc":"2.0","id":1,"method":"eth_subscribe","params":["newHeads"]}`)); err != nil {
			return nil, err
		}
		_, res, err := conn.ReadMessage()
		return res, err
	}

	firstConn, err := dial()
	require.NoError(t, err)
	defer firstConn.Close()
	res, err := sendSubscribe(firstConn)
	require.NoError(t, err)
	RequireEqualJSON(t, []byte(`{"jsonrpc":"2.0","id":1,"result":"first"}`), res)

	secondConn, err := dial()
	require.NoError(t, err)
	defer secondConn.Close()
	res, err = sendSubscribe(secondConn)
	require.NoError(t, err)
	RequireEqualJSON(t, []byte(`{"jsonrpc":"2.0","id":1,"result":"second"}`), res)

	require.NoError(t, firstConn.Close())
	require.Eventually(t, func() bool {
		select {
		case <-firstClosedCh:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)

	var thirdConn *websocket.Conn
	require.Eventually(t, func() bool {
		conn, err := dial()
		if err != nil {
			return false
		}
		res, err := sendSubscribe(conn)
		if err == nil && string(res) == `{"jsonrpc":"2.0","id":1,"result":"first"}` {
			thirdConn = conn
			return true
		}
		conn.Close()
		return false
	}, time.Second, 10*time.Millisecond)
	defer thirdConn.Close()

	require.Equal(t, int64(2), firstConnections.Load())
	require.Equal(t, int64(1), secondConnections.Load())
}

func TestWSMappedRequestLimitReturnsTooManyRequests(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() {
		close(release)
	})
	httpBackend := NewMockBackend(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedOnce.Do(func() {
			close(started)
		})
		<-release
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x123"}`))
	}))
	defer httpBackend.Close()

	wsBackend := NewMockWSBackend(nil, nil, nil)
	defer wsBackend.Close()

	config := ReadConfig("ws")
	config.Server.RPCPort = 0
	config.Server.MaxConcurrentWSRPCs = 1
	config.Backends["good"].RPCURL = httpBackend.URL()
	config.Backends["good"].WSURL = wsBackend.URL()
	config.WSMethodWhitelist = []string{"eth_chainId"}

	_, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)
	defer shutdown()

	conn, _, err := websocket.DefaultDialer.Dial("ws://127.0.0.1:8546", nil) // nolint:bodyclose
	require.NoError(t, err)
	defer conn.Close()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))

	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[]}`)))
	require.Eventually(t, func() bool {
		select {
		case <-started:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)

	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"jsonrpc":"2.0","id":2,"method":"eth_chainId","params":[]}`)))
	_, res, err := conn.ReadMessage()
	require.NoError(t, err)
	RequireEqualJSON(t, []byte(`{"jsonrpc":"2.0","error":{"code":-32024,"message":"too many requests"},"id":2}`), res)

	releaseOnce.Do(func() {
		close(release)
	})
	_, res, err = conn.ReadMessage()
	require.NoError(t, err)
	RequireEqualJSON(t, []byte(`{"jsonrpc":"2.0","id":1,"result":"0x123"}`), res)
}

func TestWSMappedRequestDoesNotBlockSubscriptionTraffic(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() {
		close(release)
	})
	httpBackend := NewMockBackend(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedOnce.Do(func() {
			close(started)
		})
		<-release
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x123"}`))
	}))
	defer httpBackend.Close()

	var wsBackendMessages atomic.Int64
	wsBackend := NewMockWSBackend(nil, func(conn *websocket.Conn, msgType int, data []byte) {
		wsBackendMessages.Add(1)
		require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"jsonrpc":"2.0","id":2,"result":"subscription-ok"}`)))
	}, nil)
	defer wsBackend.Close()

	config := ReadConfig("ws")
	config.Server.RPCPort = 0
	config.Backends["good"].RPCURL = httpBackend.URL()
	config.Backends["good"].WSURL = wsBackend.URL()
	config.WSMethodWhitelist = []string{"eth_chainId", "eth_subscribe"}

	_, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)
	defer shutdown()

	conn, _, err := websocket.DefaultDialer.Dial("ws://127.0.0.1:8546", nil) // nolint:bodyclose
	require.NoError(t, err)
	defer conn.Close()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))

	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[]}`)))
	require.Eventually(t, func() bool {
		select {
		case <-started:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)

	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"jsonrpc":"2.0","id":2,"method":"eth_subscribe","params":["newHeads"]}`)))
	_, res, err := conn.ReadMessage()
	require.NoError(t, err)
	RequireEqualJSON(t, []byte(`{"jsonrpc":"2.0","id":2,"result":"subscription-ok"}`), res)
	require.Equal(t, int64(1), wsBackendMessages.Load())

	releaseOnce.Do(func() {
		close(release)
	})
	_, res, err = conn.ReadMessage()
	require.NoError(t, err)
	RequireEqualJSON(t, []byte(`{"jsonrpc":"2.0","id":1,"result":"0x123"}`), res)
}

func TestWSClientClosure(t *testing.T) {
	backendHdlr := new(backendHandler)
	clientHdlr := new(clientHandler)

	backend := NewMockWSBackend(nil, func(conn *websocket.Conn, msgType int, data []byte) {
		backendHdlr.MsgCB(conn, msgType, data)
	}, func(conn *websocket.Conn, err error) {
		backendHdlr.CloseCB(conn, err)
	})
	defer backend.Close()

	require.NoError(t, os.Setenv("GOOD_BACKEND_RPC_URL", backend.URL()))

	config := ReadConfig("ws")
	_, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)
	defer shutdown()

	for _, closeType := range []string{"soft", "hard"} {
		t.Run(closeType, func(t *testing.T) {
			client, err := NewProxydWSClient("ws://127.0.0.1:8546", func(msgType int, data []byte) {
				clientHdlr.MsgCB(msgType, data)
			}, nil)
			require.NoError(t, err)

			timeout := time.NewTicker(30 * time.Second)
			doneCh := make(chan struct{}, 1)
			backendHdlr.SetCloseCB(func(conn *websocket.Conn, err error) {
				doneCh <- struct{}{}
			})

			if closeType == "soft" {
				require.NoError(t, client.SoftClose())
			} else {
				client.HardClose()
			}

			select {
			case <-timeout.C:
				t.Fatalf("timed out")
			case <-doneCh:
				return
			}
		})
	}
}

func TestWSClientExceedReadLimit(t *testing.T) {
	backendHdlr := new(backendHandler)
	clientHdlr := new(clientHandler)

	backend := NewMockWSBackend(nil, func(conn *websocket.Conn, msgType int, data []byte) {
		backendHdlr.MsgCB(conn, msgType, data)
	}, func(conn *websocket.Conn, err error) {
		backendHdlr.CloseCB(conn, err)
	})
	defer backend.Close()

	require.NoError(t, os.Setenv("GOOD_BACKEND_RPC_URL", backend.URL()))

	config := ReadConfig("ws")
	_, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)
	defer shutdown()

	client, err := NewProxydWSClient("ws://127.0.0.1:8546", func(msgType int, data []byte) {
		clientHdlr.MsgCB(msgType, data)
	}, nil)
	require.NoError(t, err)

	var closed atomic.Bool
	originalHandler := client.conn.CloseHandler()
	client.conn.SetCloseHandler(func(code int, text string) error {
		closed.Store(true)
		return originalHandler(code, text)
	})

	backendHdlr.SetMsgCB(func(conn *websocket.Conn, msgType int, data []byte) {
		t.Fatalf("backend should not get the large message")
	})

	payload := strings.Repeat("barf", 1024*1024)
	clientReq := "{\"id\": 1, \"method\": \"eth_subscribe\", \"params\": [\"" + payload + "\"]}"
	writeErr := client.WriteMessage(
		websocket.TextMessage,
		[]byte(clientReq),
	)
	require.Eventually(t, closed.Load, time.Second, 10*time.Millisecond,
		"connection should be closed after exceeding the read limit; write error: %v", writeErr)

}
