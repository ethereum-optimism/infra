package proxyd

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/semaphore"
)

func TestStripXFF(t *testing.T) {
	tests := []struct {
		in, out string
	}{
		{"1.2.3, 4.5.6, 7.8.9", "1.2.3"},
		{"1.2.3,4.5.6", "1.2.3"},
		{" 1.2.3 , 4.5.6 ", "1.2.3"},
	}

	for _, test := range tests {
		actual := stripXFF(test.in)
		assert.Equal(t, test.out, actual)
	}
}

func TestLimitedHTTPClientDoLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	t.Run("unlimited requests", func(t *testing.T) {
		client := &LimitedHTTPClient{
			Client:      http.Client{},
			sem:         nil,
			backendName: "test-unlimited",
		}

		req, err := http.NewRequest("GET", server.URL, nil)
		require.NoError(t, err)

		resp, err := client.DoLimited(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()
	})

	t.Run("limited requests", func(t *testing.T) {
		sem := semaphore.NewWeighted(1)
		client := &LimitedHTTPClient{
			Client:      http.Client{},
			sem:         sem,
			backendName: "test-limited",
		}

		req, err := http.NewRequest("GET", server.URL, nil)
		require.NoError(t, err)

		resp, err := client.DoLimited(req)
		if resp != nil {
			defer resp.Body.Close()
		}
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)

		// Exhaust semaphore
		require.True(t, sem.TryAcquire(1))

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		req, err = http.NewRequestWithContext(ctx, "GET", server.URL, nil)
		require.NoError(t, err)

		resp, err = client.DoLimited(req)
		if resp != nil {
			defer resp.Body.Close()
		}
		require.Error(t, err)
		require.Contains(t, err.Error(), "too many requests")
		require.Nil(t, resp)
	})
}

func TestClientDisconnectionFlow499(t *testing.T) {
	initialCount := getHttpResponseCodeCount("499")

	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			// Context cancelled - return immediately to simulate backend detecting cancellation
			return
		case <-time.After(100 * time.Millisecond):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":"0x1","id":1}`))
		}
	}))
	defer backendServer.Close()

	backend := NewBackend("test-backend", backendServer.URL, "", semaphore.NewWeighted(1))
	require.Equal(t, []int{400, 413}, backend.allowedStatusCodes) // default allowed status codes
	backendGroup := &BackendGroup{
		Name:     "test-group",
		Backends: []*Backend{backend},
	}

	rpcMethodMappings := map[string]string{
		"eth_blockNumber": "test-group",
	}

	proxydServer, err := NewServer(
		map[string]*BackendGroup{"test-group": backendGroup},
		backendGroup,
		NewStringSetFromStrings([]string{"eth_blockNumber"}),
		rpcMethodMappings,
		1024*1024,               // maxBodySize
		map[string]string{},     // authenticatedPaths
		false,                   // publicAccess
		5*time.Second,           // timeout - longer than our test
		10,                      // maxUpstreamBatchSize
		false,                   // enableServedByHeader
		&NoopRPCCache{},         // cache
		RateLimitConfig{},       // rateLimitConfig
		SenderRateLimitConfig{}, // senderRateLimitConfig
		SenderRateLimitConfig{}, // interopSenderRateLimitConfig
		false,                   // enableRequestLog
		0,                       // maxRequestBodyLogLen
		100,                     // maxBatchSize
		func(dur time.Duration, max int, prefix string) FrontendRateLimiter {
			return NoopFrontendRateLimiter
		}, // limiterFactory
		InteropValidationConfig{},              // interopValidatingConfig
		NewFirstSupervisorStrategy([]string{}), // interopStrategy
		false,                                  // enableTxHashLogging
		nil,                                    // limExemptKeys
		TxValidationMiddlewareConfig{},         // txValidationConfig
		10*time.Second,                         // gracefulShutdownDuration
	)
	require.NoError(t, err)

	reqBody := `{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}`

	httpReq := httptest.NewRequest("POST", "/", strings.NewReader(reqBody))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Forwarded-For", "127.0.0.1")

	ctx, cancel := context.WithCancel(httpReq.Context())
	httpReq = httpReq.WithContext(ctx)

	rr := httptest.NewRecorder()

	// Start the request in a goroutine and cancel it immediately to simulate client disconnection
	done := make(chan bool)
	go func() {
		defer func() { done <- true }()
		proxydServer.HandleRPC(rr, httpReq)
	}()

	cancel()
	<-done

	t.Logf("Response status code: %d", rr.Code)

	finalCount := getHttpResponseCodeCount("499")

	assert.Greater(t, finalCount, initialCount, "httpResponseCodesTotal should be incremented for 499 status code")
}

func TestAllowedStatusCodes(t *testing.T) {
	unhealthyBackendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","error":{"code":-32000,"message":"backend has a foo problem"},"id":1}`))
	}))
	defer unhealthyBackendServer.Close()

	healthyBackendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":"0x1","id":1}`))
	}))
	defer healthyBackendServer.Close()

	unhealthyBackend := NewBackend("test-backend-healthy", unhealthyBackendServer.URL, "", semaphore.NewWeighted(1))
	healthyBackend := NewBackend("test-backend-unhealthy", healthyBackendServer.URL, "", semaphore.NewWeighted(1))

	require.Equal(t, []int{400, 413}, healthyBackend.allowedStatusCodes) // default allowed status codes

	healthyBackend.allowedStatusCodes = []int{} // no need to explicitly allow 200
	res, err := healthyBackend.doForward(context.Background(), []*RPCReq{{ID: []byte("1"), Method: "eth_blockNumber"}}, false)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Nil(t, res[0].Error) // denoting a successful RPC response

	res, err = unhealthyBackend.doForward(context.Background(), []*RPCReq{{ID: []byte("1"), Method: "eth_blockNumber"}}, false)
	require.Error(t, err)
	require.Nil(t, res)
	require.ErrorContains(t, err, "response code 503")

	unhealthyBackend.allowedStatusCodes = []int{503}
	res, err = unhealthyBackend.doForward(context.Background(), []*RPCReq{{ID: []byte("1"), Method: "eth_blockNumber"}}, false)
	require.NoError(t, err)
	require.NotNil(t, res)
	// denotes the RPC response holding the error
	require.NotNil(t, res[0].Error)
	require.Equal(t, -32000, res[0].Error.Code)
	require.Equal(t, "backend has a foo problem", res[0].Error.Message)
	require.Equal(t, http.StatusServiceUnavailable, res[0].Error.HTTPErrorCode)
}

func TestIngressForwarding(t *testing.T) {
	backendRequests := make(chan []byte, 10)
	ingressRequests := make(chan []byte, 10)

	// Mock backend server
	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		backendRequests <- body
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":"0x1234","id":1}`))
	}))
	defer backendServer.Close()

	// Mock ingress server
	ingressServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ingressRequests <- body
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":"ok","id":1}`))
	}))
	defer ingressServer.Close()

	// Create backend with ingress RPC configured
	backend := NewBackend(
		"test-backend",
		backendServer.URL,
		"",
		semaphore.NewWeighted(10),
		WithIngressRPC(ingressServer.URL),
	)

	// Create a test RPC request
	rpcReq := &RPCReq{
		JSONRPC: "2.0",
		Method:  "eth_blockNumber",
		Params:  []byte(`[]`),
		ID:      []byte(`1`),
	}

	// Forward the request
	ctx := context.Background()
	res, err := backend.Forward(ctx, []*RPCReq{rpcReq}, false)

	// Verify the backend request was successful
	require.NoError(t, err)
	require.Len(t, res, 1)
	require.False(t, res[0].IsError())

	// Verify both backend and ingress received the request
	select {
	case backendBody := <-backendRequests:
		require.Contains(t, string(backendBody), "eth_blockNumber")
		require.Contains(t, string(backendBody), `"id":1`)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Backend did not receive request")
	}

	// Give a bit more time for the async ingress request to complete
	select {
	case ingressBody := <-ingressRequests:
		require.Contains(t, string(ingressBody), "eth_blockNumber")
		require.Contains(t, string(ingressBody), `"id":1`)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Ingress did not receive request")
	}
}

func getHttpResponseCodeCount(statusCode string) float64 {
	metricFamilies, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		return 0
	}

	for _, mf := range metricFamilies {
		if mf.GetName() == "proxyd_http_response_codes_total" {
			for _, metric := range mf.GetMetric() {
				for _, label := range metric.GetLabel() {
					if label.GetName() == "status_code" && label.GetValue() == statusCode {
						return metric.GetCounter().GetValue()
					}
				}
			}
		}
	}
	return 0
}
