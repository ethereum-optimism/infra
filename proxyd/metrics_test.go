package proxyd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/semaphore"
)

func TestStatusCodeForRPCRes(t *testing.T) {
	tests := []struct {
		name string
		res  *RPCRes
		want string
	}{
		{"success with result", &RPCRes{Result: "0x1"}, "200"},
		{"success with nil result", &RPCRes{}, "200"},
		{"rate limited", &RPCRes{Error: ErrOverRateLimit}, "429"},
		{"client canceled", &RPCRes{Error: ErrContextCanceled}, "499"},
		{"backend offline", &RPCRes{Error: ErrBackendOffline}, "503"},
		{"method not whitelisted", &RPCRes{Error: ErrMethodNotWhitelisted}, "403"},
		{"gateway timeout", &RPCRes{Error: ErrGatewayTimeout}, "504"},
		{"body too large", &RPCRes{Error: ErrRequestBodyTooLarge}, "413"},
		// ErrTooManyBatchRequests carried no HTTPErrorCode, so this rejection reported
		// 200 on both the HTTP and per-call metrics during a 100%-failure condition.
		{"too many batch requests", &RPCRes{Error: ErrTooManyBatchRequests}, "413"},
		{"proxyd internal", &RPCRes{Error: ErrInternal}, "500"},
		{
			"upstream non-200 stamped per element",
			&RPCRes{Error: &RPCErr{Code: -32000, Message: "boom", HTTPErrorCode: 500}},
			"500",
		},
		// The critical cases: backend answered HTTP 200 carrying a JSON-RPC error,
		// so HTTPErrorCode was never stamped. These MUST NOT be 5xx.
		{
			"already known",
			&RPCRes{Error: &RPCErr{Code: -32000, Message: "already known"}},
			"400",
		},
		{
			"nonce too low",
			&RPCRes{Error: &RPCErr{Code: -32000, Message: "nonce too low"}},
			"400",
		},
		{
			"replacement transaction underpriced",
			&RPCRes{Error: &RPCErr{Code: -32000, Message: "replacement transaction underpriced"}},
			"400",
		},
		{
			"upstream method not found",
			&RPCRes{Error: &RPCErr{Code: -32601, Message: "the method does not exist"}},
			"400",
		},
		{"nil response treated as internal error", nil, "500"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, statusCodeForRPCRes(tt.res))
		})
	}
}

func TestBackendNameFromServedBy(t *testing.T) {
	tests := []struct {
		name     string
		servedBy string
		want     string
	}{
		{"group and backend", "replicas/reth-0", "reth-0"},
		{"bare backend name", "reth-0", "reth-0"},
		{"empty falls back to proxyd", "", "proxyd"},
		{"trailing slash falls back to proxyd", "replicas/", "proxyd"},
		{"nested slashes take the last segment", "a/b/c", "c"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, backendNameFromServedBy(tt.servedBy))
		})
	}
}

func TestRecordRPCCallDefaultsEmptyLabels(t *testing.T) {
	before := gatherCounter(t, "proxyd_rpc_calls_total", map[string]string{
		"backend_name": "proxyd",
		"method_name":  "unknown",
		"status_code":  "500",
		"transport":    "http",
	})

	// Empty backend and method must not produce empty label values.
	RecordRPCCall("", "", statusCodeInternal, RPCRequestSourceHTTP)

	after := gatherCounter(t, "proxyd_rpc_calls_total", map[string]string{
		"backend_name": "proxyd",
		"method_name":  "unknown",
		"status_code":  "500",
		"transport":    "http",
	})
	require.Equal(t, before+1, after)
}

func TestRecordRPCCallsFillsNilSlotsWithFallback(t *testing.T) {
	labels := func(status string) map[string]string {
		return map[string]string{
			"backend_name": "reth-0",
			"method_name":  "eth_chainId",
			"status_code":  status,
			"transport":    "http",
		}
	}
	before200 := gatherCounter(t, "proxyd_rpc_calls_total", labels("200"))
	before504 := gatherCounter(t, "proxyd_rpc_calls_total", labels("504"))

	responses := []*RPCRes{{Result: "0x1"}, nil}
	methods := []string{"eth_chainId", "eth_chainId"}
	backends := []string{"reth-0", "reth-0"}

	recordRPCCalls(responses, methods, backends, RPCRequestSourceHTTP, statusCodeGatewayTimeout)

	require.Equal(t, before200+1, gatherCounter(t, "proxyd_rpc_calls_total", labels("200")))
	require.Equal(t, before504+1, gatherCounter(t, "proxyd_rpc_calls_total", labels("504")))
}

// TestRecordRPCCallsAllIgnoresPerElementResponses locks in the IMP-1 fix: on a
// wholesale-failure exit, a slot that was already filled with a real success
// (a cache hit, a locally-answered element, or a minibatch forwarded in a
// prior loop iteration) must NOT be reported as a 200 just because a response
// was computed for it — the client never saw it, only the single error
// envelope. Every element must land on the fallback status.
func TestRecordRPCCallsAllIgnoresPerElementResponses(t *testing.T) {
	labels := func(status string) map[string]string {
		return map[string]string{
			"backend_name": "reth-0",
			"method_name":  "eth_chainId",
			"status_code":  status,
			"transport":    "http",
		}
	}
	before200 := gatherCounter(t, "proxyd_rpc_calls_total", labels("200"))
	before504 := gatherCounter(t, "proxyd_rpc_calls_total", labels("504"))

	// Three elements: two already have a filled, successful response (as they
	// would after earlier minibatches/cache hits succeeded), one was never
	// reached. recordRPCCallsAll must record all three as 504, none as 200.
	methods := []string{"eth_chainId", "eth_chainId", "eth_chainId"}
	backends := []string{"reth-0", "reth-0", "reth-0"}

	recordRPCCallsAll(methods, backends, RPCRequestSourceHTTP, statusCodeGatewayTimeout)

	require.Equal(t, before200, gatherCounter(t, "proxyd_rpc_calls_total", labels("200")))
	require.Equal(t, before504+3, gatherCounter(t, "proxyd_rpc_calls_total", labels("504")))
}

// The deadline short-circuit must record the status the client actually receives,
// which differs by path: HandleRPC maps context.DeadlineExceeded to
// ErrGatewayTimeout only for batches, and writes ErrInternal for single requests.
// Recording 504 for both would fire a timeout alert for what the client saw as an
// internal error — and hide it from a 500-only alert.
func TestDeadlineShortCircuitStatusMatchesHandlerPath(t *testing.T) {
	require.Equal(t, statusCodeGatewayTimeout, deadlineShortCircuitStatus(true))
	require.Equal(t, statusCodeInternal, deadlineShortCircuitStatus(false))
}

// A partially-overridden batch returns a real servedBy for the elements a backend
// answered, so the overridden elements must be distinguishable or they inherit a
// backend that never saw them. OverrideResponses is the single merge point, so
// marking there covers every override site.
func TestOverrideResponsesMarksServedLocally(t *testing.T) {
	forwarded := &RPCRes{ID: json.RawMessage(`2`), Result: "from-backend"}
	overridden := &RPCRes{ID: json.RawMessage(`1`), Result: "from-consensus"}

	res := OverrideResponses([]*RPCRes{forwarded}, []*indexedReqRes{
		{index: 0, res: overridden},
	})

	require.Equal(t, []*RPCRes{overridden, forwarded}, res)
	require.True(t, overridden.servedLocally, "proxyd answered this element itself")
	require.False(t, forwarded.servedLocally, "a backend answered this element")
}

func TestServerMetricMethodNameBoundsCardinality(t *testing.T) {
	s := &Server{rpcMethodMappings: map[string]string{"eth_chainId": "replicas"}}

	require.Equal(t, "eth_chainId", s.metricMethodName("eth_chainId"))
	require.Equal(t, MethodUnknown, s.metricMethodName(""))
	// An arbitrary client-supplied method must never become a label value.
	require.Equal(t, MethodNotAllowed, s.metricMethodName("evil_"+string(make([]byte, 128))))
	require.Equal(t, MethodNotAllowed, s.metricMethodName("not_whitelisted"))
	// eth_accounts is answered locally before the rpcMethodMappings lookup and
	// is deliberately never mapped; it must pass through as itself rather than
	// collapsing to MethodNotAllowed and polluting that bucket with 200s.
	require.Equal(t, "eth_accounts", s.metricMethodName("eth_accounts"))
}

func TestHTTPRequestMethodLabel(t *testing.T) {
	require.Equal(t, MethodBatch, httpRequestMethodLabel(true, []string{"eth_chainId", "eth_blockNumber"}))
	require.Equal(t, "eth_chainId", httpRequestMethodLabel(false, []string{"eth_chainId"}))
	require.Equal(t, MethodUnknown, httpRequestMethodLabel(false, nil))
	require.Equal(t, MethodUnknown, httpRequestMethodLabel(false, []string{""}))
}

func TestHTTPRequestByMethodDuration(t *testing.T) {
	const metric = "proxyd_http_request_by_method_duration_seconds"

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":"0x1","id":1}`))
	}))
	defer backend.Close()

	group := &BackendGroup{
		Name:     "main",
		Backends: []*Backend{NewBackend("reth-0", backend.URL, "", semaphore.NewWeighted(1))},
	}
	srv := newMethodDurationServer(t, map[string]string{"eth_chainId": "main"}, map[string]*BackendGroup{"main": group}, 1024)

	observe := func(body string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		srv.HandleRPC(httptest.NewRecorder(), req)
	}
	count := func(method string) uint64 {
		t.Helper()
		return gatherHistogramSampleCount(t, metric, map[string]string{"method_name": method})
	}

	t.Run("whitelisted method", func(t *testing.T) {
		before := count("eth_chainId")
		observe(`{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1}`)
		require.Equal(t, before+1, count("eth_chainId"))
	})

	t.Run("unlisted method collapses", func(t *testing.T) {
		evil := "evil_not_a_real_method"
		beforeNotAllowed := count(MethodNotAllowed)
		beforeEvil := count(evil)
		observe(`{"jsonrpc":"2.0","method":"` + evil + `","params":[],"id":1}`)
		require.Equal(t, beforeNotAllowed+1, count(MethodNotAllowed))
		require.Equal(t, beforeEvil, count(evil))
	})

	t.Run("client cannot claim the batch sentinel", func(t *testing.T) {
		beforeBatch := count(MethodBatch)
		beforeNotAllowed := count(MethodNotAllowed)
		observe(`{"jsonrpc":"2.0","method":"` + MethodBatch + `","params":[],"id":1}`)
		require.Equal(t, beforeBatch, count(MethodBatch))
		require.Equal(t, beforeNotAllowed+1, count(MethodNotAllowed))
	})

	t.Run("eth_accounts keeps its name", func(t *testing.T) {
		before := count("eth_accounts")
		observe(`{"jsonrpc":"2.0","method":"eth_accounts","params":[],"id":1}`)
		require.Equal(t, before+1, count("eth_accounts"))
	})

	t.Run("healthz probe keeps its name", func(t *testing.T) {
		before := count(proxydHealthzMethod)
		beforeNotAllowed := count(MethodNotAllowed)
		observe(`{"jsonrpc":"2.0","method":"proxyd_healthz","params":[],"id":1}`)
		require.Equal(t, before+1, count(proxydHealthzMethod))
		require.Equal(t, beforeNotAllowed, count(MethodNotAllowed))
	})

	t.Run("batch", func(t *testing.T) {
		before := count(MethodBatch)
		observe(`[{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1},{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":2}]`)
		require.Equal(t, before+1, count(MethodBatch))
	})

	t.Run("unreadable batch stays batch", func(t *testing.T) {
		beforeBatch := count(MethodBatch)
		beforeUnknown := count(MethodUnknown)
		observe(`[`)
		require.Equal(t, beforeBatch+1, count(MethodBatch))
		require.Equal(t, beforeUnknown, count(MethodUnknown))
	})

	t.Run("parse failure is unknown", func(t *testing.T) {
		before := count(MethodUnknown)
		observe(`not-json`)
		require.Equal(t, before+1, count(MethodUnknown))
	})

	t.Run("body too large is unknown", func(t *testing.T) {
		small := newMethodDurationServer(t, nil, nil, 8)
		before := count(MethodUnknown)
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1}`))
		small.HandleRPC(httptest.NewRecorder(), req)
		require.Equal(t, before+1, count(MethodUnknown))
	})

	t.Run("rejected auth is unknown", func(t *testing.T) {
		locked := newMethodDurationServer(t, nil, nil, 1024)
		locked.authenticatedPaths = map[string]string{"secret": "alias"}
		before := count(MethodUnknown)
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1}`))
		locked.HandleRPC(httptest.NewRecorder(), req)
		require.Equal(t, before+1, count(MethodUnknown))
	})
}

func newMethodDurationServer(t *testing.T, mappings map[string]string, groups map[string]*BackendGroup, maxBody int64) *Server {
	t.Helper()
	if mappings == nil {
		mappings = map[string]string{}
	}
	if groups == nil {
		groups = map[string]*BackendGroup{}
	}
	srv, err := NewServer(
		groups,
		&BackendGroup{Name: "ws"},
		NewStringSetFromStrings(nil),
		mappings,
		maxBody,
		map[string]string{},
		false,
		5*time.Second,
		10,
		false,
		&NoopRPCCache{},
		RateLimitConfig{},
		SenderRateLimitConfig{},
		SenderRateLimitConfig{},
		false,
		0,
		100,
		func(time.Duration, int, string) FrontendRateLimiter {
			return NoopFrontendRateLimiter
		},
		InteropValidationConfig{},
		NewFirstFilterStrategy([]string{}),
		false,
		nil,
		TxValidationMiddlewareConfig{},
		10*time.Second,
		defaultMaxConcurrentWSRPCs,
	)
	require.NoError(t, err)
	return srv
}

func TestRecordRPCNotification(t *testing.T) {
	labels := map[string]string{"backend_name": "reth-0"}
	before := gatherCounter(t, "proxyd_rpc_notifications_total", labels)
	RecordRPCNotification("reth-0")
	require.Equal(t, before+1, gatherCounter(t, "proxyd_rpc_notifications_total", labels))
}

// gatherCounter returns the current value of the counter named `name` whose
// labels are a superset of `labels`, or 0 if no such series exists yet.
func gatherCounter(t *testing.T, name string, labels map[string]string) float64 {
	t.Helper()
	m := findMetric(t, name, labels)
	if m == nil || m.GetCounter() == nil {
		return 0
	}
	return m.GetCounter().GetValue()
}

// gatherHistogramSampleCount returns the sample count of the histogram named
// `name` whose labels are a superset of `labels`, or 0 if no such series exists yet.
func gatherHistogramSampleCount(t *testing.T, name string, labels map[string]string) uint64 {
	t.Helper()
	m := findMetric(t, name, labels)
	if m == nil || m.GetHistogram() == nil {
		return 0
	}
	return m.GetHistogram().GetSampleCount()
}

// findMetric returns the first metric named `name` whose labels are a superset
// of `labels`.
func findMetric(t *testing.T, name string, labels map[string]string) *dto.Metric {
	t.Helper()
	mfs, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			if metricLabelsMatch(m, labels) {
				return m
			}
		}
	}
	return nil
}

func metricLabelsMatch(m *dto.Metric, labels map[string]string) bool {
	got := make(map[string]string, len(m.GetLabel()))
	for _, l := range m.GetLabel() {
		got[l.GetName()] = l.GetValue()
	}
	for k, v := range labels {
		if got[k] != v {
			return false
		}
	}
	return true
}

func TestWSProxierMetricMethodNameBoundsCardinality(t *testing.T) {
	w := &WSProxier{methodWhitelist: NewStringSetFromStrings([]string{"eth_subscribe"})}

	require.Equal(t, "eth_subscribe", w.metricMethodName("eth_subscribe"))
	require.Equal(t, MethodUnknown, w.metricMethodName(""))
	require.Equal(t, MethodNotAllowed, w.metricMethodName("not_whitelisted"))
	// A client naming its method with one of the sentinel literals must NOT be
	// able to masquerade as that sentinel — it is just another non-whitelisted
	// method and must collapse to MethodNotAllowed like any other. Callers
	// that need to report a genuine parse failure (no method to speak of) do
	// so by recording MethodUnknown directly, without going through
	// metricMethodName at all — see clientPump's prepareClientMsg-error
	// branch.
	require.Equal(t, MethodNotAllowed, w.metricMethodName(MethodUnknown))
	require.Equal(t, MethodNotAllowed, w.metricMethodName(MethodNotAllowed))
}

func TestWriteBatchRPCResRecordsEnvelopeStatus(t *testing.T) {
	labels := map[string]string{"status_code": "200"}
	before := gatherCounter(t, "proxyd_http_response_codes_total", labels)

	rec := httptest.NewRecorder()
	writeBatchRPCRes(context.Background(), rec, []*RPCRes{
		{JSONRPC: JSONRPCVersion, Result: "0x1", ID: json.RawMessage("1")},
		{JSONRPC: JSONRPCVersion, Result: "0x2", ID: json.RawMessage("2")},
	})

	require.Equal(t, 200, rec.Code)
	// One increment per HTTP response, not per batch element.
	require.Equal(t, before+1, gatherCounter(t, "proxyd_http_response_codes_total", labels))
}
