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

func TestResolveCLOutputRootTiebreak(t *testing.T) {
	beRank1 := &Backend{Name: "rank1", clRank: 1}
	beRank2 := &Backend{Name: "rank2", clRank: 2}
	beRank3 := &Backend{Name: "rank3", clRank: 3}
	beUnranked := &Backend{Name: "unranked", clRank: 0}

	tests := []struct {
		name       string
		results    []clOutputRootResult
		wantWinner *Backend
		wantRoot   string
		wantFound  bool
	}{
		{
			name: "lowest rank wins",
			results: []clOutputRootResult{
				{be: beRank2, outputRoot: "root_B"},
				{be: beRank1, outputRoot: "root_A"},
				{be: beRank3, outputRoot: "root_C"},
			},
			wantWinner: beRank1,
			wantRoot:   "root_A",
			wantFound:  true,
		},
		{
			name: "no ranked backends returns found=false",
			results: []clOutputRootResult{
				{be: beUnranked, outputRoot: "root_A"},
				{be: &Backend{Name: "unranked2", clRank: 0}, outputRoot: "root_B"},
			},
			wantWinner: nil,
			wantRoot:   "",
			wantFound:  false,
		},
		{
			name: "single ranked among unranked wins",
			results: []clOutputRootResult{
				{be: beUnranked, outputRoot: "root_A"},
				{be: beRank2, outputRoot: "root_B"},
				{be: &Backend{Name: "unranked2", clRank: 0}, outputRoot: "root_C"},
			},
			wantWinner: beRank2,
			wantRoot:   "root_B",
			wantFound:  true,
		},
		{
			name: "errored backends are skipped",
			results: []clOutputRootResult{
				{be: beRank1, outputRoot: "", err: context.DeadlineExceeded},
				{be: beRank2, outputRoot: "root_B"},
			},
			wantWinner: beRank2,
			wantRoot:   "root_B",
			wantFound:  true,
		},
		{
			name:       "empty results returns found=false",
			results:    []clOutputRootResult{},
			wantWinner: nil,
			wantRoot:   "",
			wantFound:  false,
		},
		{
			name: "all errored returns found=false",
			results: []clOutputRootResult{
				{be: beRank1, outputRoot: "", err: context.DeadlineExceeded},
				{be: beRank2, outputRoot: "", err: context.DeadlineExceeded},
			},
			wantWinner: nil,
			wantRoot:   "",
			wantFound:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			winner, root, found := resolveCLOutputRootTiebreak(tt.results)
			require.Equal(t, tt.wantFound, found, "found mismatch")
			require.Equal(t, tt.wantRoot, root, "root mismatch")
			if tt.wantWinner == nil {
				require.Nil(t, winner, "expected nil winner")
			} else {
				require.Equal(t, tt.wantWinner, winner, "winner mismatch")
			}
		})
	}
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
