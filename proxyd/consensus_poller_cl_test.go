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

// newTestBackendWithRank creates a minimal Backend with a CL rank.
func newTestBackendWithRank(t *testing.T, name string, srv *httptest.Server, timeout time.Duration, rank int) *Backend {
	t.Helper()
	be := NewBackend(name, srv.URL, "", nil, WithTimeout(timeout), WithCLRank(rank))
	be.healthyProbe.Store(true)
	return be
}

// candidatesForMulti returns a candidates map for multiple backends.
func candidatesForMulti(cp *ConsensusPoller, backends ...*Backend) map[*Backend]*backendState {
	m := make(map[*Backend]*backendState, len(backends))
	for _, be := range backends {
		bs := cp.backendState[be]
		bs.safeBlockNumber = hexutil.Uint64(225)
		bs.localSafeBlockNumber = hexutil.Uint64(225)
		m[be] = bs
	}
	return m
}

func TestVerifyCLOutputRoots_RankedTiebreaking_LowestRankWins(t *testing.T) {
	// Two backends return different output roots — no majority (each has 1 vote).
	// Backend with lower rank should win and the other should be banned.
	srvA := httptest.NewServer(outputRootHandler("root_A"))
	defer srvA.Close()
	srvB := httptest.NewServer(outputRootHandler("root_B"))
	defer srvB.Close()

	beA := newTestBackendWithRank(t, "nodeA", srvA, 2*time.Second, 1) // rank 1 = highest priority
	beB := newTestBackendWithRank(t, "nodeB", srvB, 2*time.Second, 2) // rank 2

	cp := newTestPoller([]*Backend{beA, beB}, WithCLConsensusMode(), WithAsyncHandler(NewNoopAsyncHandler()))

	candidates := candidatesForMulti(cp, beA, beB)
	resultCandidates, _, _ := cp.verifyCLOutputRoots(context.Background(), candidates, hexutil.Uint64(225))

	// beA (rank 1) should survive, beB (rank 2) should be banned.
	_, beAInResult := resultCandidates[beA]
	_, beBInResult := resultCandidates[beB]
	require.True(t, beAInResult, "beA (rank 1) should remain in candidates")
	require.False(t, beBInResult, "beB (rank 2) should be removed from candidates")
	require.True(t, cp.IsBanned(beB), "beB should be banned")
	require.False(t, cp.IsBanned(beA), "beA should not be banned")
}

func TestVerifyCLOutputRoots_RankedTiebreaking_HigherRankBanned(t *testing.T) {
	// Same as above but with reversed ranks — the one with lower rank wins.
	srvA := httptest.NewServer(outputRootHandler("root_A"))
	defer srvA.Close()
	srvB := httptest.NewServer(outputRootHandler("root_B"))
	defer srvB.Close()

	beA := newTestBackendWithRank(t, "nodeA", srvA, 2*time.Second, 5) // rank 5
	beB := newTestBackendWithRank(t, "nodeB", srvB, 2*time.Second, 2) // rank 2 = higher priority

	cp := newTestPoller([]*Backend{beA, beB}, WithCLConsensusMode(), WithAsyncHandler(NewNoopAsyncHandler()))

	candidates := candidatesForMulti(cp, beA, beB)
	resultCandidates, _, _ := cp.verifyCLOutputRoots(context.Background(), candidates, hexutil.Uint64(225))

	_, beAInResult := resultCandidates[beA]
	_, beBInResult := resultCandidates[beB]
	require.False(t, beAInResult, "beA (rank 5) should be removed")
	require.True(t, beBInResult, "beB (rank 2) should remain")
	require.True(t, cp.IsBanned(beA), "beA should be banned")
	require.False(t, cp.IsBanned(beB), "beB should not be banned")
}

func TestVerifyCLOutputRoots_NoRanks_NoBans(t *testing.T) {
	// Two backends disagree but neither has a rank — no tiebreaking, no bans.
	srvA := httptest.NewServer(outputRootHandler("root_A"))
	defer srvA.Close()
	srvB := httptest.NewServer(outputRootHandler("root_B"))
	defer srvB.Close()

	beA := newTestBackendWithRank(t, "nodeA", srvA, 2*time.Second, 0) // unranked
	beB := newTestBackendWithRank(t, "nodeB", srvB, 2*time.Second, 0) // unranked

	cp := newTestPoller([]*Backend{beA, beB}, WithCLConsensusMode(), WithAsyncHandler(NewNoopAsyncHandler()))

	candidates := candidatesForMulti(cp, beA, beB)
	resultCandidates, _, _ := cp.verifyCLOutputRoots(context.Background(), candidates, hexutil.Uint64(225))

	require.Len(t, resultCandidates, 2, "both should remain — no tiebreaking possible")
	require.False(t, cp.IsBanned(beA))
	require.False(t, cp.IsBanned(beB))
}

func TestVerifyCLOutputRoots_RankedTiebreaking_SameRootNoAction(t *testing.T) {
	// Two backends agree on the same root — no tiebreaking needed even though
	// they have different ranks.
	srvA := httptest.NewServer(outputRootHandler("root_same"))
	defer srvA.Close()
	srvB := httptest.NewServer(outputRootHandler("root_same"))
	defer srvB.Close()

	beA := newTestBackendWithRank(t, "nodeA", srvA, 2*time.Second, 1)
	beB := newTestBackendWithRank(t, "nodeB", srvB, 2*time.Second, 2)

	cp := newTestPoller([]*Backend{beA, beB}, WithCLConsensusMode(), WithAsyncHandler(NewNoopAsyncHandler()))

	candidates := candidatesForMulti(cp, beA, beB)
	resultCandidates, _, _ := cp.verifyCLOutputRoots(context.Background(), candidates, hexutil.Uint64(225))

	// Both agree, maxCount=2 so the majority path is taken, not tiebreaking.
	require.Len(t, resultCandidates, 2)
	require.False(t, cp.IsBanned(beA))
	require.False(t, cp.IsBanned(beB))
}
