package proxyd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/stretchr/testify/require"
)

// outputRootHandler returns an HTTP handler that serves a valid optimism_outputAtBlock response.
func outputRootHandler(root string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(`67`),
			"result": map[string]interface{}{
				"outputRoot": root,
				"blockRef":   map[string]interface{}{"hash": "hash_0xe1", "number": float64(225)},
			},
		})
	}
}

// hangingHandler returns an HTTP handler that does not respond until the client closes
// the connection or 500ms elapse (whichever comes first), so srv.Close() completes promptly.
func hangingHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// newTestBackend creates a minimal Backend pointing at srv with the given HTTP timeout.
func newTestBackend(t *testing.T, srv *httptest.Server, timeout time.Duration) *Backend {
	t.Helper()
	be := NewBackend(srv.URL, srv.URL, "", nil, WithTimeout(timeout))
	be.healthyProbe.Store(true)
	return be
}

// newTestPoller creates a ConsensusPoller with the given backends.
func newTestPoller(backends []*Backend, opts ...ConsensusOpt) *ConsensusPoller {
	bg := &BackendGroup{Backends: backends}
	return NewConsensusPoller(bg, opts...)
}

// candidatesFor returns a candidates map containing only the given backend.
func candidatesFor(cp *ConsensusPoller, be *Backend) map[*Backend]*backendState {
	bs := cp.backendState[be]
	bs.safeBlockNumber = hexutil.Uint64(225)
	bs.localSafeBlockNumber = hexutil.Uint64(225)
	return map[*Backend]*backendState{be: bs}
}

func TestVerifyCLOutputRoots_SingleTimeoutDoesNotBan(t *testing.T) {
	srv := httptest.NewServer(hangingHandler())
	defer srv.Close()

	be := newTestBackend(t, srv, 100*time.Millisecond)
	cp := newTestPoller([]*Backend{be}, WithCLConsensusMode())

	cp.verifyCLOutputRoots(context.Background(), candidatesFor(cp, be), hexutil.Uint64(225))

	require.Equal(t, uint(1), cp.backendState[be].clOutputRootTimeouts, "counter should increment on first timeout")
	require.False(t, cp.IsBanned(be), "single timeout should not trigger a ban")
}

func TestVerifyCLOutputRoots_ConsecutiveTimeoutsBanAtThreshold(t *testing.T) {
	srv := httptest.NewServer(hangingHandler())
	defer srv.Close()

	be := newTestBackend(t, srv, 100*time.Millisecond)
	cp := newTestPoller([]*Backend{be}, WithCLConsensusMode())
	threshold := cp.clOutputRootBanThreshold // default 3

	for i := uint(0); i < threshold-1; i++ {
		cp.verifyCLOutputRoots(context.Background(), candidatesFor(cp, be), hexutil.Uint64(225))
		require.False(t, cp.IsBanned(be), "should not be banned before threshold (cycle %d)", i+1)
		require.Equal(t, i+1, cp.backendState[be].clOutputRootTimeouts)
	}

	cp.verifyCLOutputRoots(context.Background(), candidatesFor(cp, be), hexutil.Uint64(225))
	require.True(t, cp.IsBanned(be), "should be banned after %d consecutive timeouts", threshold)
}

func TestVerifyCLOutputRoots_TimeoutCounterResetsOnSuccess(t *testing.T) {
	srv := httptest.NewServer(outputRootHandler("root_A"))
	defer srv.Close()

	be := newTestBackend(t, srv, 2*time.Second)
	cp := newTestPoller([]*Backend{be}, WithCLConsensusMode())

	// Simulate prior timeouts by setting the counter directly.
	cp.backendState[be].clOutputRootTimeouts = 2

	cp.verifyCLOutputRoots(context.Background(), candidatesFor(cp, be), hexutil.Uint64(225))

	require.Equal(t, uint(0), cp.backendState[be].clOutputRootTimeouts, "counter should reset on successful fetch")
	require.False(t, cp.IsBanned(be))
}

func TestVerifyCLOutputRoots_ConfigurableBanThreshold(t *testing.T) {
	srv := httptest.NewServer(hangingHandler())
	defer srv.Close()

	be := newTestBackend(t, srv, 100*time.Millisecond)
	// threshold=1: first timeout should immediately ban
	cp := newTestPoller([]*Backend{be}, WithCLConsensusMode(), WithCLOutputRootBanThreshold(1))

	cp.verifyCLOutputRoots(context.Background(), candidatesFor(cp, be), hexutil.Uint64(225))
	require.True(t, cp.IsBanned(be), "threshold=1: first timeout should immediately ban")
}
