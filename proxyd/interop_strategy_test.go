package proxyd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	interopErrors "github.com/ethereum-optimism/optimism/op-core/interop"
	"github.com/ethereum-optimism/optimism/op-core/interop/messages"
	"github.com/ethereum-optimism/optimism/op-service/eth"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

const (
	validInteropFilterResponse = `{"jsonrpc":"2.0","id":1,"result":null}`
	// failsafeRPCCode is the dedicated failsafe code emitted by op-interop-filter.
	failsafeRPCCode = -320602
	// futureDataRPCCode and uninitializedRPCCode are the soft out-of-sync codes.
	futureDataRPCCode    = -321401
	uninitializedRPCCode = -320400
)

// interopFilterErrorResponse builds a JSON-RPC error body whose message matches an
// interop error so ParseInteropError maps it to the corresponding code.
func interopFilterErrorResponse(rpcCode int, message string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"error":{"code":%d,"message":%q}}`, rpcCode, message)
}

// newInteropFilterServer starts an httptest server that responds to every
// interop_checkAccessList call with the given HTTP code and body after an
// optional delay. It returns the server URL.
func newInteropFilterServer(t *testing.T, httpCode int, body string, delay time.Duration) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(httpCode)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// newSignalingInteropFilterServer is like newInteropFilterServer but closes done once
// it has written its response, letting another endpoint sequence after it.
func newSignalingInteropFilterServer(t *testing.T, httpCode int, body string, done chan<- struct{}) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(httpCode)
		_, _ = w.Write([]byte(body))
		close(done)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// newGatedInteropFilterServer responds only after gate is closed, then waits an
// additional settle period. The settle gives an endpoint that signalled the
// gate time to have its verdict fully parsed and buffered by the strategy before
// this endpoint's verdict arrives, making ordering deterministic.
func newGatedInteropFilterServer(t *testing.T, httpCode int, body string, gate <-chan struct{}, settle time.Duration) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-gate:
		case <-r.Context().Done():
			return
		}
		if settle > 0 {
			select {
			case <-time.After(settle):
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(httpCode)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func newAgreementStrategy(urls []string, minResponses int) *agreementStrategyImpl {
	return NewAgreementStrategy(urls, minResponses,
		WithChainID(420120003),
		WithValidateAndDeduplicateInteropAccessList(false),
	)
}

var testAccessList = []common.Hash{common.HexToHash("0x01")}

func TestAgreement_AllValid_Accepts(t *testing.T) {
	urls := []string{
		newInteropFilterServer(t, 200, validInteropFilterResponse, 0),
		newInteropFilterServer(t, 200, validInteropFilterResponse, 0),
	}
	s := newAgreementStrategy(urls, 2)
	require.NoError(t, s.ValidateAccessList(context.Background(), testAccessList))
}

func TestAgreement_AllInvalid_RejectsWithRealError(t *testing.T) {
	body := interopFilterErrorResponse(-32000, interopErrors.ErrConflict.Error())
	urls := []string{
		newInteropFilterServer(t, 409, body, 0),
		newInteropFilterServer(t, 409, body, 0),
	}
	s := newAgreementStrategy(urls, 2)
	err := s.ValidateAccessList(context.Background(), testAccessList)
	require.Error(t, err)
	rpcErr, ok := err.(*RPCErr)
	require.True(t, ok, "expected *RPCErr, got %T", err)
	require.Equal(t, -320600, rpcErr.Code, "should surface the real conflicting-data verdict")
}

func TestAgreement_Disagreement_RejectsAndLogs(t *testing.T) {
	invalidBody := interopFilterErrorResponse(-32000, interopErrors.ErrConflict.Error())
	urls := []string{
		newInteropFilterServer(t, 200, validInteropFilterResponse, 0),
		newInteropFilterServer(t, 409, invalidBody, 0),
	}
	s := newAgreementStrategy(urls, 2)
	err := s.ValidateAccessList(context.Background(), testAccessList)
	require.Error(t, err, "a mixed verdict must reject")
	rpcErr, ok := err.(*RPCErr)
	require.True(t, ok, "expected *RPCErr, got %T", err)
	require.Equal(t, -320600, rpcErr.Code)
}

func TestAgreement_FailsafeShortCircuitsSlowBackend(t *testing.T) {
	// Failsafe is a hard reject that short-circuits: a fast failsafe verdict ends
	// the check immediately without awaiting a slow endpoint.
	failsafeBody := interopFilterErrorResponse(failsafeRPCCode, interopErrors.ErrFailsafeEnabled.Error())
	urls := []string{
		newInteropFilterServer(t, 503, failsafeBody, 0),
		newInteropFilterServer(t, 200, validInteropFilterResponse, 1500*time.Millisecond),
	}
	s := newAgreementStrategy(urls, 2)

	start := time.Now()
	err := s.ValidateAccessList(context.Background(), testAccessList)
	require.Error(t, err, "failsafe must reject")
	require.Less(t, time.Since(start), 1*time.Second, "failsafe short-circuits without awaiting the slow endpoint")
}

func TestAgreement_IgnoresSlowBackend(t *testing.T) {
	// A slow non-failsafe endpoint must not delay an accept once the quorum of
	// fast valid verdicts is in.
	urls := []string{
		newInteropFilterServer(t, 200, validInteropFilterResponse, 0),
		newInteropFilterServer(t, 200, validInteropFilterResponse, 0),
		newInteropFilterServer(t, 200, validInteropFilterResponse, 1500*time.Millisecond),
	}
	s := newAgreementStrategy(urls, 2)

	start := time.Now()
	require.NoError(t, s.ValidateAccessList(context.Background(), testAccessList))
	require.Less(t, time.Since(start), 1*time.Second, "must not await the slow endpoint once the quorum is met")
}

func TestAgreement_5xxNotCounted_FailClosed(t *testing.T) {
	urls := []string{
		newInteropFilterServer(t, 200, validInteropFilterResponse, 0),
		newInteropFilterServer(t, 500, `{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"boom"}}`, 0),
	}
	s := newAgreementStrategy(urls, 2)
	err := s.ValidateAccessList(context.Background(), testAccessList)
	require.Error(t, err, "one valid + one 5xx is below quorum, must fail closed")
	require.Contains(t, err.Error(), "quorum not reached")
}

func TestAgreement_TimeoutNotCounted_FailClosed(t *testing.T) {
	urls := []string{
		newInteropFilterServer(t, 200, validInteropFilterResponse, 0),
		newInteropFilterServer(t, 200, validInteropFilterResponse, 1500*time.Millisecond),
	}
	s := newAgreementStrategy(urls, 2)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	err := s.ValidateAccessList(ctx, testAccessList)
	require.Error(t, err, "a timed-out endpoint is a non-response, below quorum must fail closed")
	require.Contains(t, err.Error(), "quorum not reached")
}

func TestAgreement_FailsafeHardRejects(t *testing.T) {
	failsafeBody := interopFilterErrorResponse(failsafeRPCCode, interopErrors.ErrFailsafeEnabled.Error())
	urls := []string{
		newInteropFilterServer(t, 503, failsafeBody, 0),
		newInteropFilterServer(t, 503, failsafeBody, 0),
	}
	s := newAgreementStrategy(urls, 1)
	err := s.ValidateAccessList(context.Background(), testAccessList)
	require.Error(t, err, "failsafe is a hard rejection")
	require.NotContains(t, err.Error(), "quorum not reached", "failsafe short-circuits before the quorum check")
}

func TestAgreement_FailsafeOnAnyEndpoint_HardRejects(t *testing.T) {
	// One received failsafe must reject the message even though the other two are
	// healthy and meet the quorum. The failsafe responds immediately and the two
	// valid endpoints are gated to respond only after the failsafe has landed
	// (plus a settle), so the failsafe verdict is observed before the accepts and
	// the test is deterministic.
	failsafeBody := interopFilterErrorResponse(failsafeRPCCode, interopErrors.ErrFailsafeEnabled.Error())
	failsafeDone := make(chan struct{})
	const settle = 100 * time.Millisecond
	urls := []string{
		newSignalingInteropFilterServer(t, 503, failsafeBody, failsafeDone),
		newGatedInteropFilterServer(t, 200, validInteropFilterResponse, failsafeDone, settle),
		newGatedInteropFilterServer(t, 200, validInteropFilterResponse, failsafeDone, settle),
	}
	s := newAgreementStrategy(urls, 2)
	err := s.ValidateAccessList(context.Background(), testAccessList)
	require.Error(t, err, "a received failsafe must reject even when the quorum of accepts is met")
	require.NotContains(t, err.Error(), "quorum not reached")
}

func TestFailsafeError_Detection(t *testing.T) {
	require.True(t, isFailsafeError(&RPCErr{Code: failsafeRPCCode, HTTPErrorCode: 503}),
		"the dedicated failsafe code is failsafe")
	require.False(t, isFailsafeError(&RPCErr{Code: -32602, HTTPErrorCode: 503}),
		"the legacy generic params code is no longer treated as failsafe")
	require.False(t, isFailsafeError(&RPCErr{Code: -320600, HTTPErrorCode: 409}),
		"a definitive rejection is not failsafe")
	require.False(t, isFailsafeError(fmt.Errorf("plain error")),
		"a non-RPCErr error is not failsafe")
}

func TestAgreement_CancelledRequestNotCounted(t *testing.T) {
	// A context.Canceled / HTTP 499 result is the strategy's own cancellation (or
	// an upstream disconnect) and must never count as a definitive rejection,
	// even though 499 lives in the 4xx band.
	require.False(t, isDefinitiveInteropRejection(ErrContextCanceled),
		"ErrContextCanceled (499) must not count as a definitive rejection")

	wrapped := fmt.Errorf("dial failed: %w", context.Canceled)
	require.False(t, isDefinitiveInteropRejection(wrapped),
		"a context.Canceled error must not count")

	// An interop filter verdict counts.
	require.True(t, isDefinitiveInteropRejection(&RPCErr{Code: -320600, HTTPErrorCode: 409}))
	// A generic invalid-params rejection (-32602) is a real INVALID and counts.
	require.True(t, isDefinitiveInteropRejection(&RPCErr{Code: -32602, HTTPErrorCode: 400}))
	// A non-RPCErr error does not count.
	require.False(t, isDefinitiveInteropRejection(fmt.Errorf("plain error")))
}

func TestAgreement_SingleUrl_MinOne(t *testing.T) {
	t.Run("valid accepts", func(t *testing.T) {
		urls := []string{newInteropFilterServer(t, 200, validInteropFilterResponse, 0)}
		s := newAgreementStrategy(urls, 1)
		require.NoError(t, s.ValidateAccessList(context.Background(), testAccessList))
	})

	t.Run("invalid rejects with real error", func(t *testing.T) {
		body := interopFilterErrorResponse(-32000, interopErrors.ErrConflict.Error())
		urls := []string{newInteropFilterServer(t, 409, body, 0)}
		s := newAgreementStrategy(urls, 1)
		err := s.ValidateAccessList(context.Background(), testAccessList)
		require.Error(t, err)
		rpcErr, ok := err.(*RPCErr)
		require.True(t, ok, "expected *RPCErr, got %T", err)
		require.Equal(t, -320600, rpcErr.Code)
	})
}

func TestAgreement_GenericParseRejection_CountsAsInvalid(t *testing.T) {
	// The filter rejects a malformed/fabricated access list with the generic
	// -32602 ("failed to parse access entry"). That is a real definitive INVALID:
	// all endpoints rejecting must produce a clean reject (reject_agreed), NOT
	// quorum-not-reached.
	parseRejectBody := interopFilterErrorResponse(-32602, "failed to parse access entry")
	urls := []string{
		newInteropFilterServer(t, 400, parseRejectBody, 0),
		newInteropFilterServer(t, 400, parseRejectBody, 0),
	}
	s := newAgreementStrategy(urls, 2)
	err := s.ValidateAccessList(context.Background(), testAccessList)
	require.Error(t, err, "a -32602 parse rejection from all endpoints must reject")
	require.NotContains(t, err.Error(), "quorum not reached",
		"a -32602 rejection is a definitive invalid, not a non-response")
	rpcErr, ok := err.(*RPCErr)
	require.True(t, ok, "expected *RPCErr, got %T", err)
	require.Equal(t, -32602, rpcErr.Code, "the real filter rejection code must be surfaced")
}

func TestAgreement_OutOfSyncEndpointIsSoft(t *testing.T) {
	// FutureData / Uninitialized mean "this node does not have the data yet". A
	// single out-of-sync node must be ignored (non-response) as long as the
	// other nodes meet the quorum, so the message is accepted.
	cases := []struct {
		name string
		code int
		msg  string
		http int
	}{
		{"future data", futureDataRPCCode, interopErrors.ErrFuture.Error(), 422},
		{"uninitialized", uninitializedRPCCode, interopErrors.ErrUninitialized.Error(), 400},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			softBody := interopFilterErrorResponse(c.code, c.msg)
			urls := []string{
				newInteropFilterServer(t, c.http, softBody, 0),
				newInteropFilterServer(t, 200, validInteropFilterResponse, 0),
				newInteropFilterServer(t, 200, validInteropFilterResponse, 0),
			}
			s := newAgreementStrategy(urls, 2)
			require.NoError(t, s.ValidateAccessList(context.Background(), testAccessList),
				"an out-of-sync node must be ignored when quorum is met by others")
		})
	}
}

func TestAgreement_AllOutOfSyncFailClosed(t *testing.T) {
	// If every node is out-of-sync, no definitive verdicts are collected and the
	// strategy fails closed with quorum-not-reached.
	softBody := interopFilterErrorResponse(futureDataRPCCode, interopErrors.ErrFuture.Error())
	urls := []string{
		newInteropFilterServer(t, 422, softBody, 0),
		newInteropFilterServer(t, 422, softBody, 0),
	}
	s := newAgreementStrategy(urls, 2)
	err := s.ValidateAccessList(context.Background(), testAccessList)
	require.Error(t, err, "all endpoints out-of-sync must fail closed")
	require.Contains(t, err.Error(), "quorum not reached")
}

func TestSoftInteropFailure_Detection(t *testing.T) {
	require.True(t, isSoftInteropFailure(&RPCErr{Code: futureDataRPCCode, HTTPErrorCode: 422}))
	require.True(t, isSoftInteropFailure(&RPCErr{Code: uninitializedRPCCode, HTTPErrorCode: 400}))
	require.False(t, isSoftInteropFailure(&RPCErr{Code: -320600, HTTPErrorCode: 409}),
		"a real interop filter verdict is not soft")
	require.False(t, isSoftInteropFailure(&RPCErr{Code: -32602, HTTPErrorCode: 400}),
		"a generic parse rejection is not soft")
	require.False(t, isSoftInteropFailure(fmt.Errorf("plain error")))
}

func TestDefinitiveInteropRejection_Classification(t *testing.T) {
	// Any filter rejection counts as a definitive INVALID...
	require.True(t, isDefinitiveInteropRejection(&RPCErr{Code: -320600, HTTPErrorCode: 409}),
		"an interop filter verdict counts")
	require.True(t, isDefinitiveInteropRejection(&RPCErr{Code: -321501, HTTPErrorCode: 422}),
		"an interop filter verdict counts")
	require.True(t, isDefinitiveInteropRejection(&RPCErr{Code: -32602, HTTPErrorCode: 400}),
		"a generic invalid-params parse rejection counts")

	// ...except failsafe and non-verdict failures.
	require.False(t, isDefinitiveInteropRejection(&RPCErr{Code: failsafeRPCCode, HTTPErrorCode: 503}),
		"failsafe is handled separately and never a definitive verdict")
	require.False(t, isDefinitiveInteropRejection(&RPCErr{Code: -32603, HTTPErrorCode: 500}),
		"JSON-RPC internal error is not a verdict")
	require.False(t, isDefinitiveInteropRejection(&RPCErr{Code: -32000, HTTPErrorCode: 500}),
		"proxyd internal fallback is not a verdict")
	require.False(t, isDefinitiveInteropRejection(&RPCErr{Code: -32602, HTTPErrorCode: 502}),
		"a 5xx transport failure is not a verdict regardless of code")
	require.False(t, isDefinitiveInteropRejection(&RPCErr{Code: futureDataRPCCode, HTTPErrorCode: 422}),
		"a soft out-of-sync FutureData failure is not a verdict")
	require.False(t, isDefinitiveInteropRejection(&RPCErr{Code: uninitializedRPCCode, HTTPErrorCode: 400}),
		"a soft out-of-sync Uninitialized failure is not a verdict")
}

// Descriptor must carry a near-future timestamp (clock skew only) and op-reth's expiry timeout.
func TestExecutingDescriptor_NearFutureTimestampAndExpiryTimeout(t *testing.T) {
	var got messages.ExecutingDescriptor
	captured := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Params []json.RawMessage `json:"params"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Len(t, req.Params, 3)
		require.NoError(t, json.Unmarshal(req.Params[2], &got))
		captured <- struct{}{}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(validInteropFilterResponse))
	}))
	t.Cleanup(srv.Close)

	before := uint64(time.Now().Unix())
	_, _, err := performCheckAccessListOp(context.Background(), testAccessList, srv.URL, eth.ChainIDFromUInt64(420120003))
	require.NoError(t, err)
	<-captured
	after := uint64(time.Now().Unix())

	require.Equal(t, interopExecutingDescriptorTimeoutSeconds, got.Timeout, "timeout should match op-reth's expiry margin")
	require.GreaterOrEqual(t, got.Timestamp, before+interopExecutingDescriptorClockToleranceSeconds)
	require.LessOrEqual(t, got.Timestamp, after+interopExecutingDescriptorClockToleranceSeconds,
		"timestamp must be near-future (clock skew only), not a large forward window")
}

func newMulticallStrategy(urls []string) *multicallStrategyImpl {
	return NewMulticallStrategy(urls,
		WithChainID(420120003),
		WithValidateAndDeduplicateInteropAccessList(false),
	)
}

// multicallValidateGoroutineRunning reports whether any goroutine is still
// parked inside multicallStrategyImpl.ValidateAccessList. After the call
// returns, the broadcast sender goroutines exit on their own; anything left
// behind is a leak.
func multicallValidateGoroutineRunning() bool {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	return strings.Contains(string(buf[:n]), "multicallStrategyImpl).ValidateAccessList")
}

// TestMulticall_DoesNotLeakGoroutine guards against the dual-receiver pattern:
// a drainer goroutine that out-lives the call by blocking forever on the
// result channel (and can steal verdicts from the main collector). The result
// channel is buffered to the number of backends, so no sender ever blocks even
// when we return early on the first success, and no drainer is needed.
func TestMulticall_DoesNotLeakGoroutine(t *testing.T) {
	urls := []string{
		newInteropFilterServer(t, 200, validInteropFilterResponse, 0),
		newInteropFilterServer(t, 200, validInteropFilterResponse, 0),
		newInteropFilterServer(t, 200, validInteropFilterResponse, 0),
	}
	s := newMulticallStrategy(urls)
	require.NoError(t, s.ValidateAccessList(context.Background(), testAccessList))

	require.Eventually(t, func() bool {
		return !multicallValidateGoroutineRunning()
	}, 2*time.Second, 20*time.Millisecond,
		"multicall strategy leaked a goroutine blocked on its result channel")
}
