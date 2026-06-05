package proxyd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
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

// newTestPoller creates a ConsensusPoller with the given backends, registering each as a
// primary so it is eligible as a consensus candidate (Primaries() requires an explicit
// non-fallback entry in FallbackBackends).
//
// It defaults to a noop async handler so NewConsensusPoller does not spawn background
// poller goroutines: these tests drive verifyCLOutputRoots / UpdateBackendGroupConsensus
// synchronously and mutate backendState directly, so a live poller would race them.
// Callers may override by passing their own WithAsyncHandler (last one wins).
func newTestPoller(backends []*Backend, opts ...ConsensusOpt) *ConsensusPoller {
	fallbacks := make(map[string]bool, len(backends))
	for _, be := range backends {
		fallbacks[be.Name] = false
	}
	bg := &BackendGroup{Backends: backends, FallbackBackends: fallbacks}
	opts = append([]ConsensusOpt{WithAsyncHandler(NewNoopAsyncHandler())}, opts...)
	return NewConsensusPoller(bg, opts...)
}

// candidatesFor returns a candidates map containing only the given backend.
func candidatesFor(cp *ConsensusPoller, be *Backend) map[*Backend]*backendState {
	bs := cp.backendState[be]
	bs.safeBlockNumber = hexutil.Uint64(225)
	bs.localSafeBlockNumber = hexutil.Uint64(225)
	return map[*Backend]*backendState{be: bs}
}

// candidatesForMulti returns a candidates map containing all the given backends, each with a
// safe/local-safe block of 225 so verifyCLOutputRoots fetches the output root at that block.
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

// --- halt-on-tie: verifyCLOutputRoots decision tests ---

func TestVerifyCLOutputRoots_TieHalts(t *testing.T) {
	// Two backends disagree 1v1 — no majority. The split is unresolvable, so verify halts
	// and bans no one.
	srvA := httptest.NewServer(outputRootHandler("root_A"))
	defer srvA.Close()
	srvB := httptest.NewServer(outputRootHandler("root_B"))
	defer srvB.Close()

	beA := newTestBackend(t, srvA, 2*time.Second)
	beB := newTestBackend(t, srvB, 2*time.Second)
	cp := newTestPoller([]*Backend{beA, beB}, WithCLConsensusMode())

	candidates := candidatesForMulti(cp, beA, beB)
	resultCandidates, _, _, halt := cp.verifyCLOutputRoots(context.Background(), candidates, hexutil.Uint64(225))

	require.True(t, halt, "1v1 disagreement has no majority — must halt")
	require.False(t, cp.IsBanned(beA), "halt must not ban any backend")
	require.False(t, cp.IsBanned(beB), "halt must not ban any backend")
	require.Len(t, resultCandidates, 2, "no backend should be removed on halt")
}

func TestVerifyCLOutputRoots_ThreeWaySplitHalts(t *testing.T) {
	// Three backends, three distinct roots — no majority. Must halt, ban no one.
	srvA := httptest.NewServer(outputRootHandler("root_A"))
	defer srvA.Close()
	srvB := httptest.NewServer(outputRootHandler("root_B"))
	defer srvB.Close()
	srvC := httptest.NewServer(outputRootHandler("root_C"))
	defer srvC.Close()

	beA := newTestBackend(t, srvA, 2*time.Second)
	beB := newTestBackend(t, srvB, 2*time.Second)
	beC := newTestBackend(t, srvC, 2*time.Second)
	cp := newTestPoller([]*Backend{beA, beB, beC}, WithCLConsensusMode())

	_, _, _, halt := cp.verifyCLOutputRoots(context.Background(), candidatesForMulti(cp, beA, beB, beC), hexutil.Uint64(225))

	require.True(t, halt, "3-way split has no majority — must halt")
	require.False(t, cp.IsBanned(beA))
	require.False(t, cp.IsBanned(beB))
	require.False(t, cp.IsBanned(beC))
}

func TestVerifyCLOutputRoots_TopTieHalts(t *testing.T) {
	// Five backends split 2-2-1: the top two roots tie at 2 votes each (2*2 == 4, not > 5
	// responders, and certainly not a strict majority). No clear winner — must halt.
	roots := []string{"root_A", "root_A", "root_B", "root_B", "root_C"}
	backends := make([]*Backend, 0, len(roots))
	for _, root := range roots {
		srv := httptest.NewServer(outputRootHandler(root))
		defer srv.Close()
		backends = append(backends, newTestBackend(t, srv, 2*time.Second))
	}
	cp := newTestPoller(backends, WithCLConsensusMode())

	_, _, _, halt := cp.verifyCLOutputRoots(context.Background(), candidatesForMulti(cp, backends...), hexutil.Uint64(225))

	require.True(t, halt, "2-2-1 has a tie at the top — must halt")
	for _, be := range backends {
		require.False(t, cp.IsBanned(be), "halt must not ban any backend")
	}
}

func TestVerifyCLOutputRoots_PluralityNoMajorityHalts(t *testing.T) {
	// Five backends split 2-1-1-1: the top root leads with 2 votes (a plurality) but holds
	// no strict majority of the 5 responders (2*2 == 4, not > 5). Without a strict majority
	// the lead cannot be trusted as canonical, so verify must halt and ban no one.
	roots := []string{"root_A", "root_A", "root_B", "root_C", "root_D"}
	backends := make([]*Backend, 0, len(roots))
	for _, root := range roots {
		srv := httptest.NewServer(outputRootHandler(root))
		defer srv.Close()
		backends = append(backends, newTestBackend(t, srv, 2*time.Second))
	}
	cp := newTestPoller(backends, WithCLConsensusMode())

	_, _, _, halt := cp.verifyCLOutputRoots(context.Background(), candidatesForMulti(cp, backends...), hexutil.Uint64(225))

	require.True(t, halt, "2-1-1-1 plurality without a strict majority — must halt")
	for _, be := range backends {
		require.False(t, cp.IsBanned(be), "halt must not ban any backend")
	}
}

func TestVerifyCLOutputRoots_MajorityResolvesNoHalt(t *testing.T) {
	// 2v1 majority: a clear winner exists, so verify resolves (bans the minority) rather
	// than halting. This is the "disagreement but majority -> don't halt" case.
	srvA := httptest.NewServer(outputRootHandler("root_majority"))
	defer srvA.Close()
	srvB := httptest.NewServer(outputRootHandler("root_majority"))
	defer srvB.Close()
	srvC := httptest.NewServer(outputRootHandler("root_minority"))
	defer srvC.Close()

	beA := newTestBackend(t, srvA, 2*time.Second)
	beB := newTestBackend(t, srvB, 2*time.Second)
	beC := newTestBackend(t, srvC, 2*time.Second)
	cp := newTestPoller([]*Backend{beA, beB, beC}, WithCLConsensusMode())

	resultCandidates, _, _, halt := cp.verifyCLOutputRoots(context.Background(), candidatesForMulti(cp, beA, beB, beC), hexutil.Uint64(225))

	require.False(t, halt, "a strict majority resolves the disagreement — must not halt")
	require.True(t, cp.IsBanned(beC), "the minority backend must be banned")
	require.False(t, cp.IsBanned(beA), "majority backends must not be banned")
	require.False(t, cp.IsBanned(beB), "majority backends must not be banned")
	require.Len(t, resultCandidates, 2, "only the banned minority is removed")
}

func TestVerifyCLOutputRoots_MajorityOverMultipleMinoritiesResolves(t *testing.T) {
	// Five backends split 3-1-1: the top root holds 3 of 5 votes — a strict majority
	// (3*2 == 6 > 5). The disagreement resolves rather than halting, and every disagreeing
	// backend is banned, not just one. This is the companion to the 2-1-1-1 plurality case,
	// which halts because it falls one vote short of a majority.
	srvMaj1 := httptest.NewServer(outputRootHandler("root_majority"))
	defer srvMaj1.Close()
	srvMaj2 := httptest.NewServer(outputRootHandler("root_majority"))
	defer srvMaj2.Close()
	srvMaj3 := httptest.NewServer(outputRootHandler("root_majority"))
	defer srvMaj3.Close()
	srvMin1 := httptest.NewServer(outputRootHandler("root_minority_1"))
	defer srvMin1.Close()
	srvMin2 := httptest.NewServer(outputRootHandler("root_minority_2"))
	defer srvMin2.Close()

	beMaj1 := newTestBackend(t, srvMaj1, 2*time.Second)
	beMaj2 := newTestBackend(t, srvMaj2, 2*time.Second)
	beMaj3 := newTestBackend(t, srvMaj3, 2*time.Second)
	beMin1 := newTestBackend(t, srvMin1, 2*time.Second)
	beMin2 := newTestBackend(t, srvMin2, 2*time.Second)
	cp := newTestPoller([]*Backend{beMaj1, beMaj2, beMaj3, beMin1, beMin2}, WithCLConsensusMode())

	resultCandidates, _, _, halt := cp.verifyCLOutputRoots(context.Background(), candidatesForMulti(cp, beMaj1, beMaj2, beMaj3, beMin1, beMin2), hexutil.Uint64(225))

	require.False(t, halt, "3-of-5 is a strict majority — must resolve, not halt")
	require.False(t, cp.IsBanned(beMaj1), "majority backends must not be banned")
	require.False(t, cp.IsBanned(beMaj2), "majority backends must not be banned")
	require.False(t, cp.IsBanned(beMaj3), "majority backends must not be banned")
	require.True(t, cp.IsBanned(beMin1), "every minority backend must be banned")
	require.True(t, cp.IsBanned(beMin2), "every minority backend must be banned")
	require.Len(t, resultCandidates, 3, "both minorities are removed, majority remains")
}

func TestVerifyCLOutputRoots_UnanimousNoHalt(t *testing.T) {
	// All agree — no disagreement, no halt, no bans.
	srvA := httptest.NewServer(outputRootHandler("root_same"))
	defer srvA.Close()
	srvB := httptest.NewServer(outputRootHandler("root_same"))
	defer srvB.Close()
	srvC := httptest.NewServer(outputRootHandler("root_same"))
	defer srvC.Close()

	beA := newTestBackend(t, srvA, 2*time.Second)
	beB := newTestBackend(t, srvB, 2*time.Second)
	beC := newTestBackend(t, srvC, 2*time.Second)
	cp := newTestPoller([]*Backend{beA, beB, beC}, WithCLConsensusMode())

	resultCandidates, _, _, halt := cp.verifyCLOutputRoots(context.Background(), candidatesForMulti(cp, beA, beB, beC), hexutil.Uint64(225))

	require.False(t, halt, "unanimous agreement must not halt")
	require.Len(t, resultCandidates, 3, "no backend removed when all agree")
	require.False(t, cp.IsBanned(beA))
	require.False(t, cp.IsBanned(beB))
	require.False(t, cp.IsBanned(beC))
}

func TestVerifyCLOutputRoots_AllErroredNoHalt(t *testing.T) {
	// No backend returns a usable root (all time out). Verification could not run — this is
	// not a disagreement, so it must NOT halt (halting is reserved for genuine splits).
	srvA := httptest.NewServer(hangingHandler())
	defer srvA.Close()
	srvB := httptest.NewServer(hangingHandler())
	defer srvB.Close()

	beA := newTestBackend(t, srvA, 100*time.Millisecond)
	beB := newTestBackend(t, srvB, 100*time.Millisecond)
	cp := newTestPoller([]*Backend{beA, beB}, WithCLConsensusMode())

	_, _, _, halt := cp.verifyCLOutputRoots(context.Background(), candidatesForMulti(cp, beA, beB), hexutil.Uint64(225))

	require.False(t, halt, "all-errored is not a disagreement — must not halt")
}

func TestVerifyCLOutputRoots_TimeoutsExcludedFromResponderCount(t *testing.T) {
	// Five backends, but two time out: only three respond, splitting 2-1. Errored backends
	// are excluded from the responder count, so the majority is measured over the 3 responders
	// (2*2 == 4 > 3) — a strict majority that resolves rather than halts. The lone minority
	// responder is banned; the timed-out backends are tolerated (one timeout is below the ban
	// threshold) and stay candidates, so four candidates survive.
	srvMaj1 := httptest.NewServer(outputRootHandler("root_majority"))
	defer srvMaj1.Close()
	srvMaj2 := httptest.NewServer(outputRootHandler("root_majority"))
	defer srvMaj2.Close()
	srvMin := httptest.NewServer(outputRootHandler("root_minority"))
	defer srvMin.Close()
	srvTimeout1 := httptest.NewServer(hangingHandler())
	defer srvTimeout1.Close()
	srvTimeout2 := httptest.NewServer(hangingHandler())
	defer srvTimeout2.Close()

	beMaj1 := newTestBackend(t, srvMaj1, 2*time.Second)
	beMaj2 := newTestBackend(t, srvMaj2, 2*time.Second)
	beMin := newTestBackend(t, srvMin, 2*time.Second)
	beTimeout1 := newTestBackend(t, srvTimeout1, 100*time.Millisecond)
	beTimeout2 := newTestBackend(t, srvTimeout2, 100*time.Millisecond)
	cp := newTestPoller([]*Backend{beMaj1, beMaj2, beMin, beTimeout1, beTimeout2}, WithCLConsensusMode())

	resultCandidates, _, _, halt := cp.verifyCLOutputRoots(context.Background(), candidatesForMulti(cp, beMaj1, beMaj2, beMin, beTimeout1, beTimeout2), hexutil.Uint64(225))

	require.False(t, halt, "majority over the 3 responders resolves — timed-out backends don't count toward the split")
	require.True(t, cp.IsBanned(beMin), "the minority responder must be banned")
	require.False(t, cp.IsBanned(beMaj1), "majority responders must not be banned")
	require.False(t, cp.IsBanned(beMaj2), "majority responders must not be banned")
	require.False(t, cp.IsBanned(beTimeout1), "a single timeout is tolerated — must not ban")
	require.False(t, cp.IsBanned(beTimeout2), "a single timeout is tolerated — must not ban")
	require.Len(t, resultCandidates, 4, "minority removed; majority and tolerated-timeout backends remain")
}

// --- halt-on-tie: caller freeze + auto-recover ---

// switchableRootHandler returns an HTTP handler whose served output root can be changed at
// runtime via the returned setter, so a single backend can flip between disagreement and
// agreement across consensus cycles.
func switchableRootHandler(initial string) (http.HandlerFunc, func(string)) {
	var root atomic.Pointer[string]
	root.Store(&initial)
	h := func(w http.ResponseWriter, r *http.Request) {
		outputRootHandler(*root.Load())(w, r)
	}
	return h, func(s string) {
		v := s
		root.Store(&v)
	}
}

// seedCLBackendState populates the live backend state so the backend passes FilterCandidates
// in CL mode (healthy, in sync, enough peers, recently updated, not lagging).
func seedCLBackendState(cp *ConsensusPoller, be *Backend, latest, safe, localSafe, finalized hexutil.Uint64) {
	bs := cp.backendState[be]
	bs.latestBlockNumber = latest
	bs.latestBlockHash = "0xhash"
	bs.safeBlockNumber = safe
	bs.localSafeBlockNumber = localSafe
	bs.finalizedBlockNumber = finalized
	bs.peerCount = 5
	bs.inSync = true
	bs.lastUpdate = time.Now()
}

func TestUpdateBackendGroupConsensus_HaltFreezesAndRecovers(t *testing.T) {
	// Backend A flips its root; backend B is fixed at root_B. While they disagree (1v1, no
	// majority), consensus halts: the served block height stays frozen at the last-agreed
	// value (200) instead of advancing to the disputed block (225), and the served group is
	// emptied so general RPCs fail closed. Backends are not persistently banned. Once A
	// agrees again, consensus auto-recovers: the group repopulates and the block advances.
	handlerA, setRootA := switchableRootHandler("root_A")
	srvA := httptest.NewServer(handlerA)
	defer srvA.Close()
	srvB := httptest.NewServer(outputRootHandler("root_B"))
	defer srvB.Close()

	beA := newTestBackend(t, srvA, 2*time.Second)
	beB := newTestBackend(t, srvB, 2*time.Second)
	cp := newTestPoller([]*Backend{beA, beB}, WithCLConsensusMode())

	// Seed the last-agreed consensus state.
	cp.tracker.SetState(ConsensusTrackerState{Latest: 210, Safe: 200, Finalized: 190, LocalSafe: 200})
	// Both backends now report a higher safe block (225), but their output roots disagree.
	seedCLBackendState(cp, beA, 230, 225, 225, 190)
	seedCLBackendState(cp, beB, 230, 225, 225, 190)

	// Disagreement cycle: must halt - freeze the served height and empty the group.
	cp.UpdateBackendGroupConsensus(context.Background())
	require.EqualValues(t, 200, cp.GetSafeBlockNumber(), "safe block must stay frozen at the last-agreed value")
	require.EqualValues(t, 210, cp.GetLatestBlockNumber(), "latest block must stay frozen during a halt")
	require.Empty(t, cp.GetConsensusGroup(), "served group must be emptied so general RPCs fail closed")
	require.False(t, cp.IsBanned(beA), "halt must not persistently ban a backend")
	require.False(t, cp.IsBanned(beB), "halt must not persistently ban a backend")

	// Backends re-agree: consensus must auto-recover and advance.
	setRootA("root_B")
	cp.UpdateBackendGroupConsensus(context.Background())
	require.EqualValues(t, 225, cp.GetSafeBlockNumber(), "safe block must advance once a majority returns")
	require.EqualValues(t, 230, cp.GetLatestBlockNumber(), "latest block must advance after recovery")
	require.Len(t, cp.GetConsensusGroup(), 2, "both backends must repopulate the group after recovery")
}
