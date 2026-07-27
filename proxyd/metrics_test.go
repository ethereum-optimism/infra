package proxyd

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
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
	mfs, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			got := make(map[string]string, len(m.GetLabel()))
			for _, l := range m.GetLabel() {
				got[l.GetName()] = l.GetValue()
			}
			match := true
			for k, v := range labels {
				if got[k] != v {
					match = false
					break
				}
			}
			if match {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
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
