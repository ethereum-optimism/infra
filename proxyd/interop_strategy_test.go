package proxyd

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	interopErrors "github.com/ethereum-optimism/optimism/op-core/interop"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

const (
	validSupervisorResponse = `{"jsonrpc":"2.0","id":1,"result":null}`
	// failsafeRPCCode is the dedicated failsafe code emitted by op-interop-filter.
	failsafeRPCCode = -320602
)

// supervisorErrorResponse builds a JSON-RPC error body whose message matches an
// interop error so ParseInteropError maps it to the corresponding code.
func supervisorErrorResponse(rpcCode int, message string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"error":{"code":%d,"message":%q}}`, rpcCode, message)
}

// newSupervisorServer starts an httptest server that responds to every
// interop_checkAccessList call with the given HTTP code and body after an
// optional delay. It returns the server URL.
func newSupervisorServer(t *testing.T, httpCode int, body string, delay time.Duration) string {
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

func newAgreementStrategy(urls []string, minResponses int) *agreementStrategyImpl {
	return NewAgreementStrategy(urls, minResponses,
		WithChainID(420120003),
		WithValidateAndDeduplicateInteropAccessList(false),
	)
}

var testAccessList = []common.Hash{common.HexToHash("0x01")}

func TestAgreement_AllValid_Accepts(t *testing.T) {
	urls := []string{
		newSupervisorServer(t, 200, validSupervisorResponse, 0),
		newSupervisorServer(t, 200, validSupervisorResponse, 0),
	}
	s := newAgreementStrategy(urls, 2)
	require.NoError(t, s.ValidateAccessList(context.Background(), testAccessList))
}

func TestAgreement_AllInvalid_RejectsWithRealError(t *testing.T) {
	body := supervisorErrorResponse(-32000, interopErrors.ErrConflict.Error())
	urls := []string{
		newSupervisorServer(t, 409, body, 0),
		newSupervisorServer(t, 409, body, 0),
	}
	s := newAgreementStrategy(urls, 2)
	err := s.ValidateAccessList(context.Background(), testAccessList)
	require.Error(t, err)
	rpcErr, ok := err.(*RPCErr)
	require.True(t, ok, "expected *RPCErr, got %T", err)
	require.Equal(t, -320600, rpcErr.Code, "should surface the real conflicting-data verdict")
}

func TestAgreement_Disagreement_RejectsAndLogs(t *testing.T) {
	invalidBody := supervisorErrorResponse(-32000, interopErrors.ErrConflict.Error())
	urls := []string{
		newSupervisorServer(t, 200, validSupervisorResponse, 0),
		newSupervisorServer(t, 409, invalidBody, 0),
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
	failsafeBody := supervisorErrorResponse(failsafeRPCCode, interopErrors.ErrFailsafeEnabled.Error())
	urls := []string{
		newSupervisorServer(t, 503, failsafeBody, 0),
		newSupervisorServer(t, 200, validSupervisorResponse, 1500*time.Millisecond),
	}
	s := newAgreementStrategy(urls, 2)

	start := time.Now()
	err := s.ValidateAccessList(context.Background(), testAccessList)
	require.Error(t, err, "failsafe must reject")
	require.Less(t, time.Since(start), 1*time.Second, "failsafe short-circuits without awaiting the slow endpoint")
}

func TestAgreement_AcceptWaitsForAllEndpoints(t *testing.T) {
	// An accept can only be returned once every endpoint has been observed, so a
	// lurking failsafe on a slow endpoint is never missed. This intentionally
	// trades the slow-endpoint latency optimization for failsafe correctness.
	urls := []string{
		newSupervisorServer(t, 200, validSupervisorResponse, 0),
		newSupervisorServer(t, 200, validSupervisorResponse, 0),
		newSupervisorServer(t, 200, validSupervisorResponse, 800*time.Millisecond),
	}
	s := newAgreementStrategy(urls, 2)

	start := time.Now()
	require.NoError(t, s.ValidateAccessList(context.Background(), testAccessList))
	require.GreaterOrEqual(t, time.Since(start), 800*time.Millisecond,
		"accept must await all endpoints to rule out a lurking failsafe")
}

func TestAgreement_5xxNotCounted_FailClosed(t *testing.T) {
	urls := []string{
		newSupervisorServer(t, 200, validSupervisorResponse, 0),
		newSupervisorServer(t, 500, `{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"boom"}}`, 0),
	}
	s := newAgreementStrategy(urls, 2)
	err := s.ValidateAccessList(context.Background(), testAccessList)
	require.Error(t, err, "one valid + one 5xx is below quorum, must fail closed")
	require.Contains(t, err.Error(), "quorum not reached")
}

func TestAgreement_TimeoutNotCounted_FailClosed(t *testing.T) {
	urls := []string{
		newSupervisorServer(t, 200, validSupervisorResponse, 0),
		newSupervisorServer(t, 200, validSupervisorResponse, 1500*time.Millisecond),
	}
	s := newAgreementStrategy(urls, 2)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	err := s.ValidateAccessList(ctx, testAccessList)
	require.Error(t, err, "a timed-out endpoint is a non-response, below quorum must fail closed")
	require.Contains(t, err.Error(), "quorum not reached")
}

func TestAgreement_FailsafeHardRejects(t *testing.T) {
	failsafeBody := supervisorErrorResponse(failsafeRPCCode, interopErrors.ErrFailsafeEnabled.Error())
	urls := []string{
		newSupervisorServer(t, 503, failsafeBody, 0),
		newSupervisorServer(t, 503, failsafeBody, 0),
	}
	s := newAgreementStrategy(urls, 1)
	err := s.ValidateAccessList(context.Background(), testAccessList)
	require.Error(t, err, "failsafe is a hard rejection")
	require.NotContains(t, err.Error(), "quorum not reached", "failsafe short-circuits before the quorum check")
}

func TestAgreement_FailsafeOnAnyEndpoint_HardRejects(t *testing.T) {
	// One endpoint in failsafe must reject the message even though the other two
	// are healthy and would have met the quorum.
	failsafeBody := supervisorErrorResponse(failsafeRPCCode, interopErrors.ErrFailsafeEnabled.Error())
	urls := []string{
		newSupervisorServer(t, 503, failsafeBody, 0),
		newSupervisorServer(t, 200, validSupervisorResponse, 0),
		newSupervisorServer(t, 200, validSupervisorResponse, 0),
	}
	s := newAgreementStrategy(urls, 2)
	err := s.ValidateAccessList(context.Background(), testAccessList)
	require.Error(t, err, "failsafe on any endpoint must reject regardless of quorum")
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

	// A definitive verdict still counts.
	require.True(t, isDefinitiveInteropRejection(&RPCErr{Code: -320600, HTTPErrorCode: 409}))
	// A generic -32602 fallback does not count.
	require.False(t, isDefinitiveInteropRejection(&RPCErr{Code: -32602, HTTPErrorCode: 400}))
	// A non-RPCErr error does not count.
	require.False(t, isDefinitiveInteropRejection(fmt.Errorf("plain error")))
}

func TestAgreement_SingleUrl_MinOne(t *testing.T) {
	t.Run("valid accepts", func(t *testing.T) {
		urls := []string{newSupervisorServer(t, 200, validSupervisorResponse, 0)}
		s := newAgreementStrategy(urls, 1)
		require.NoError(t, s.ValidateAccessList(context.Background(), testAccessList))
	})

	t.Run("invalid rejects with real error", func(t *testing.T) {
		body := supervisorErrorResponse(-32000, interopErrors.ErrConflict.Error())
		urls := []string{newSupervisorServer(t, 409, body, 0)}
		s := newAgreementStrategy(urls, 1)
		err := s.ValidateAccessList(context.Background(), testAccessList)
		require.Error(t, err)
		rpcErr, ok := err.(*RPCErr)
		require.True(t, ok, "expected *RPCErr, got %T", err)
		require.Equal(t, -320600, rpcErr.Code)
	})
}

func TestDefinitiveInteropRejectionSet_ExcludesGenericParams(t *testing.T) {
	// The verdict set must contain the supervisor codes but never the generic
	// params fallback (-32602) nor the failsafe code (-320602).
	require.Contains(t, definitiveInteropRejectionCodes, -320600)
	require.Contains(t, definitiveInteropRejectionCodes, -321501)
	require.NotContains(t, definitiveInteropRejectionCodes, -32602)
	require.NotContains(t, definitiveInteropRejectionCodes, failsafeRPCCode)
	require.NotEmpty(t, definitiveInteropRejectionCodes)
}
