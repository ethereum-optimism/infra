package proxyd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/semaphore"
)

// TestExecuteMulticallUnserviceableMetric is an end-to-end check that the
// proxyd_unserviceable_requests_total counter is incremented exactly once when
// all backends fail, and not at all when at least one backend serves the
// request.
func TestExecuteMulticallUnserviceableMetric(t *testing.T) {
	newServer := func(status int, body string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}))
	}

	req := []*RPCReq{{ID: []byte("1"), Method: "eth_sendRawTransaction", JSONRPC: "2.0"}}

	t.Run("all backends fail increments exactly once", func(t *testing.T) {
		s1 := newServer(http.StatusServiceUnavailable, `{"error":"down"}`)
		s2 := newServer(http.StatusServiceUnavailable, `{"error":"down"}`)
		defer s1.Close()
		defer s2.Close()

		bg := &BackendGroup{
			Name: "group",
			Backends: []*Backend{
				NewBackend("b1", s1.URL, "", semaphore.NewWeighted(100)),
				NewBackend("b2", s2.URL, "", semaphore.NewWeighted(100)),
			},
		}

		before := getUnserviceableRequestCount(RPCRequestSourceHTTP)
		resp := bg.ExecuteMulticall(context.Background(), req)
		require.Error(t, resp.error)
		require.True(t, errors.Is(resp.error, ErrNoBackends))
		after := getUnserviceableRequestCount(RPCRequestSourceHTTP)

		require.Equal(t, float64(1), after-before,
			"a fully-unserviceable multicall must increment the counter exactly once")
	})

	t.Run("a successful backend does not increment", func(t *testing.T) {
		s1 := newServer(http.StatusServiceUnavailable, `{"error":"down"}`)
		s2 := newServer(http.StatusOK, `{"jsonrpc":"2.0","result":"0x123","id":1}`)
		defer s1.Close()
		defer s2.Close()

		bg := &BackendGroup{
			Name: "group",
			Backends: []*Backend{
				NewBackend("b1", s1.URL, "", semaphore.NewWeighted(100)),
				NewBackend("b2", s2.URL, "", semaphore.NewWeighted(100)),
			},
		}

		before := getUnserviceableRequestCount(RPCRequestSourceHTTP)
		resp := bg.ExecuteMulticall(context.Background(), req)
		require.NoError(t, resp.error)
		after := getUnserviceableRequestCount(RPCRequestSourceHTTP)

		require.Equal(t, float64(0), after-before,
			"a served multicall must not increment the unserviceable counter")
	})
}

// TestForwardUnserviceableMetric covers the normal (non-multicall) routing
// path: ForwardRequestToBackendGroup is invoked once with the full backend
// list, so the unserviceable counter must increment exactly once when every
// backend fails, and never when any backend serves the request.
func TestForwardUnserviceableMetric(t *testing.T) {
	newServer := func(status int, body string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}))
	}

	req := []*RPCReq{{ID: []byte("1"), Method: "eth_blockNumber", JSONRPC: "2.0"}}

	t.Run("all backends fail increments exactly once", func(t *testing.T) {
		s1 := newServer(http.StatusServiceUnavailable, `{"error":"down"}`)
		s2 := newServer(http.StatusServiceUnavailable, `{"error":"down"}`)
		defer s1.Close()
		defer s2.Close()

		bg := &BackendGroup{
			Name: "group",
			Backends: []*Backend{
				NewBackend("b1", s1.URL, "", semaphore.NewWeighted(100)),
				NewBackend("b2", s2.URL, "", semaphore.NewWeighted(100)),
			},
		}
		require.NotEqual(t, MulticallRoutingStrategy, bg.GetRoutingStrategy())

		before := getUnserviceableRequestCount(RPCRequestSourceHTTP)
		_, _, err := bg.Forward(context.Background(), req, false)
		require.Error(t, err)
		require.True(t, errors.Is(err, ErrNoBackends))
		after := getUnserviceableRequestCount(RPCRequestSourceHTTP)

		require.Equal(t, float64(1), after-before,
			"a fully-unserviceable forward must increment the counter exactly once regardless of backend count")
	})

	t.Run("a successful backend does not increment", func(t *testing.T) {
		s1 := newServer(http.StatusServiceUnavailable, `{"error":"down"}`)
		s2 := newServer(http.StatusOK, `{"jsonrpc":"2.0","result":"0x123","id":1}`)
		defer s1.Close()
		defer s2.Close()

		bg := &BackendGroup{
			Name: "group",
			Backends: []*Backend{
				NewBackend("b1", s1.URL, "", semaphore.NewWeighted(100)),
				NewBackend("b2", s2.URL, "", semaphore.NewWeighted(100)),
			},
		}

		before := getUnserviceableRequestCount(RPCRequestSourceHTTP)
		_, _, err := bg.Forward(context.Background(), req, false)
		require.NoError(t, err)
		after := getUnserviceableRequestCount(RPCRequestSourceHTTP)

		require.Equal(t, float64(0), after-before,
			"a served forward must not increment the unserviceable counter")
	})
}

func getUnserviceableRequestCount(requestSource string) float64 {
	metricFamilies, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		return 0
	}

	var total float64
	for _, mf := range metricFamilies {
		if mf.GetName() != "proxyd_unserviceable_requests_total" {
			continue
		}
		for _, metric := range mf.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == "request_source" && label.GetValue() == requestSource {
					total += metric.GetCounter().GetValue()
				}
			}
		}
	}
	return total
}
