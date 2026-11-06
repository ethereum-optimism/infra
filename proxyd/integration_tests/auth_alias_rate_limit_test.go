package integration_tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis"
	"github.com/ethereum-optimism/infra/proxyd"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// Start without fixed ports so tests can create multiple instances sharing Redis.
func startAuthAliasTestProxy(t *testing.T, config *proxyd.Config) string {
	t.Helper()
	srv, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)
	t.Cleanup(shutdown)
	router := mux.NewRouter()
	for _, path := range []string{"/", "/{authorization}"} {
		router.HandleFunc(path, srv.HandleRPC).Methods("POST")
		router.HandleFunc(path, srv.HandleWS).Methods("GET")
	}
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return server.URL
}

func authAliasClient(url, secret, ip string) *ProxydHTTPClient {
	headers := make(http.Header)
	headers.Set("X-Forwarded-For", ip)
	return NewProxydClientWithHeaders(url+"/"+secret, headers)
}

func authAliasTestConfig(t *testing.T, useRedis bool, handler http.Handler) *proxyd.Config {
	t.Helper()
	if handler == nil {
		handler = SingleResponseHandler(200, goodResponse)
	}
	backend := NewMockBackend(handler)
	t.Cleanup(backend.Close)
	config := ReadConfig("frontend_auth_alias")
	config.Backends["good"].RPCURL = backend.URL()
	config.RateLimit.UseRedis = useRedis
	if useRedis {
		redisServer, err := miniredis.Run()
		require.NoError(t, err)
		t.Cleanup(redisServer.Close)
		config.Redis.URL = "redis://" + redisServer.Addr()
	}
	return config
}

func TestFrontendAuthAliasIdentity(t *testing.T) {
	type call struct {
		secret string
		ip     string
		status int
	}
	tests := []struct {
		name     string
		disabled bool
		calls    []call
	}{
		{"different aliases behind same IP", false, []call{
			{"secret_a", "203.0.113.1", 200},
			{"secret_a", "203.0.113.1", 200},
			{"secret_a", "203.0.113.1", 429},
			{"secret_b", "203.0.113.1", 200},
		}},
		{"same alias across IPs", false, []call{
			{"secret_a", "203.0.113.1", 200},
			{"secret_a", "203.0.113.2", 200},
			{"secret_a", "203.0.113.3", 429},
		}},
		{"rotated credentials share alias quota", false, []call{
			{"secret_a", "203.0.113.1", 200},
			{"secret_a_rotated", "203.0.113.2", 200},
			{"secret_a_rotated", "203.0.113.3", 429},
		}},
		{"disabled shares IP quota across aliases", true, []call{
			{"secret_a", "203.0.113.1", 200},
			{"secret_b", "203.0.113.1", 200},
			{"secret_b", "203.0.113.1", 429},
		}},
		{"disabled separates IPs for same alias", true, []call{
			{"secret_a", "203.0.113.1", 200},
			{"secret_a", "203.0.113.1", 200},
			{"secret_a", "203.0.113.2", 200},
		}},
		{"public requests keep IP quotas", false, []call{
			{"", "203.0.113.1", 200},
			{"", "203.0.113.1", 200},
			{"", "203.0.113.1", 429},
			{"", "203.0.113.2", 200},
			{"secret_a", "203.0.113.1", 200},
		}},
		{"alias cannot collide with public IP", false, []call{
			{"secret_ip", "203.0.113.1", 200},
			{"secret_ip", "203.0.113.1", 200},
			{"secret_ip", "203.0.113.1", 429},
			{"", "203.0.113.1", 200},
		}},
		{"client header cannot consume alias quota", false, []call{
			{"", "auth_alias:alpha", 200},
			{"", "auth_alias:alpha", 200},
			{"secret_a", "203.0.113.1", 200},
		}},
	}
	for _, useRedis := range []bool{false, true} {
		for _, method := range []string{"eth_chainId", "eth_foobar"} {
			for _, tt := range tests {
				t.Run(fmt.Sprintf("redis=%t/%s/%s", useRedis, method, tt.name), func(t *testing.T) {
					config := authAliasTestConfig(t, useRedis, nil)
					config.RateLimit.UseAuthAlias = !tt.disabled
					if method == "eth_foobar" {
						config.RateLimit.BaseRate = 0
					}
					url := startAuthAliasTestProxy(t, config)
					for i, c := range tt.calls {
						body, status, err := authAliasClient(url, c.secret, c.ip).SendRPC(method, nil)
						require.NoError(t, err)
						require.Equal(t, c.status, status, "call %d: %s", i, body)
					}
				})
			}
		}
	}
}

func TestFrontendAuthAliasBatchMetrics(t *testing.T) {
	for _, useRedis := range []bool{false, true} {
		t.Run(fmt.Sprintf("redis=%t", useRedis), func(t *testing.T) {
			router := NewBatchRPCResponseRouter()
			for _, id := range []string{"1", "2", "3", "999"} {
				router.SetRoute("eth_chainId", id, "0x1")
			}
			url := startAuthAliasTestProxy(t, authAliasTestConfig(t, useRedis, router))
			alphaLabels := map[string]string{"auth": "alpha", "backend_name": "proxyd", "error_code": "-32016"}
			betaLabels := map[string]string{"auth": "beta", "backend_name": "proxyd", "error_code": "-32016"}
			beforeAlpha := sumCounter(t, "proxyd_rpc_errors_total", alphaLabels)
			beforeBeta := sumCounter(t, "proxyd_rpc_errors_total", betaLabels)

			alpha := authAliasClient(url, "secret_a", "203.0.113.1")
			body, status, err := alpha.SendBatchRPC(
				NewRPCReq("1", "eth_chainId", nil),
				NewRPCReq("2", "eth_chainId", nil),
				NewRPCReq("3", "eth_chainId", nil),
			)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, status)
			var responses []proxyd.RPCRes
			require.NoError(t, json.Unmarshal(body, &responses))
			require.Len(t, responses, 3)
			require.Nil(t, responses[0].Error)
			require.Nil(t, responses[1].Error)
			require.Equal(t, proxyd.ErrOverRateLimit.Code, responses[2].Error.Code)
			require.Equal(t, 1, router.GetNumCalls("eth_chainId", "1"))
			require.Equal(t, 1, router.GetNumCalls("eth_chainId", "2"))
			require.Zero(t, router.GetNumCalls("eth_chainId", "3"))

			body, status, err = alpha.SendRPC("eth_chainId", nil)
			require.NoError(t, err)
			require.Equal(t, http.StatusTooManyRequests, status)
			var singleResponse proxyd.RPCRes
			require.NoError(t, json.Unmarshal(body, &singleResponse))
			require.Equal(t, proxyd.ErrOverRateLimit.Code, singleResponse.Error.Code)
			_, status, err = authAliasClient(url, "secret_b", "203.0.113.1").SendRPC("eth_chainId", nil)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, status)
			require.Equal(t, beforeAlpha+2, sumCounter(t, "proxyd_rpc_errors_total", alphaLabels))
			require.Equal(t, beforeBeta, sumCounter(t, "proxyd_rpc_errors_total", betaLabels))
			require.Equal(t, 1, router.GetNumCalls("eth_chainId", "999"), "only beta reaches the backend")
		})
	}
}

func TestFrontendAuthAliasRedisSharedQuota(t *testing.T) {
	redisServer, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(redisServer.Close)
	config := authAliasTestConfig(t, false, nil)
	config.RateLimit.UseRedis = true
	config.Redis.URL = "redis://" + redisServer.Addr()
	firstURL := startAuthAliasTestProxy(t, config)
	secondURL := startAuthAliasTestProxy(t, config)
	for _, c := range []struct {
		url    string
		secret string
		ip     string
		status int
	}{
		{firstURL, "secret_a", "203.0.113.1", 200},
		{secondURL, "secret_a", "203.0.113.2", 200},
		{secondURL, "secret_a", "203.0.113.3", 429},
		{secondURL, "secret_b", "203.0.113.1", 200},
	} {
		_, status, err := authAliasClient(c.url, c.secret, c.ip).SendRPC("eth_chainId", nil)
		require.NoError(t, err)
		require.Equal(t, c.status, status)
	}
	require.Len(t, redisServer.Keys(), 2)
	for _, key := range redisServer.Keys() {
		require.True(t, strings.HasPrefix(key, "rate_limit:auth_alias:main:"), key)
	}

	// Opting in does not change public Redis keys, including during rolling updates.
	config.RateLimit.UseAuthAlias = false
	defaultURL := startAuthAliasTestProxy(t, config)
	for i, url := range []string{firstURL, defaultURL, secondURL} {
		_, status, err := authAliasClient(url, "", "203.0.113.1").SendRPC("eth_chainId", nil)
		require.NoError(t, err)
		if i < 2 {
			require.Equal(t, http.StatusOK, status)
		} else {
			require.Equal(t, http.StatusTooManyRequests, status)
		}
	}
}

func TestFrontendAuthAliasWSSharedQuota(t *testing.T) {
	for _, useRedis := range []bool{false, true} {
		t.Run(fmt.Sprintf("redis=%t", useRedis), func(t *testing.T) {
			wsBackend := NewMockWSBackend(nil, nil, nil)
			t.Cleanup(wsBackend.Close)
			config := authAliasTestConfig(t, useRedis, nil)
			config.WSBackendGroup = "main"
			config.WSMethodWhitelist = []string{"eth_chainId"}
			config.Backends["good"].WSURL = wsBackend.URL()
			url := startAuthAliasTestProxy(t, config)
			_, status, err := authAliasClient(url, "secret_a", "203.0.113.1").SendRPC("eth_chainId", nil)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, status)

			wsURL := "ws" + strings.TrimPrefix(url, "http")
			headers := make(http.Header)
			headers.Set("X-Forwarded-For", "203.0.113.2")
			alpha, _, err := websocket.DefaultDialer.Dial(wsURL+"/secret_a", headers) // nolint:bodyclose
			require.NoError(t, err)
			t.Cleanup(func() { _ = alpha.Close() })
			beta, _, err := websocket.DefaultDialer.Dial(wsURL+"/secret_b", headers) // nolint:bodyclose
			require.NoError(t, err)
			t.Cleanup(func() { _ = beta.Close() })
			for i, conn := range []*websocket.Conn{alpha, alpha, beta} {
				require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
				require.NoError(t, conn.WriteJSON(NewRPCReq("999", "eth_chainId", nil)))
				var res proxyd.RPCRes
				require.NoError(t, conn.ReadJSON(&res))
				if i == 1 {
					require.Equal(t, proxyd.ErrOverRateLimit.Code, res.Error.Code)
				} else {
					require.Nil(t, res.Error)
				}
			}
		})
	}
}
