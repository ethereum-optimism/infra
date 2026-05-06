package proxyd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// newDummyServer returns a no-op HTTP server for tests that don't make HTTP calls.
func newDummyServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(srv.Close)
	return srv
}

// setCLBackendState sets the CL-related fields on a backend's state within the poller.
func setCLBackendState(cp *ConsensusPoller, be *Backend, l1 uint64, body json.RawMessage) {
	cp.backendState[be].backendStateMux.Lock()
	cp.backendState[be].currentL1Number = l1
	cp.backendState[be].syncStatusRaw = body
	cp.backendState[be].backendStateMux.Unlock()
}

// --- InMemoryConsensusTracker tests ---

func TestInMemoryTracker_CLSyncBodyRoundTrip(t *testing.T) {
	tracker := NewInMemoryConsensusTracker()

	body, l1 := tracker.GetCLSyncBody()
	require.Nil(t, body)
	require.Equal(t, uint64(0), l1)

	expected := json.RawMessage(`{"safe_l2":{"number":"0x1"}}`)
	tracker.SetCLSyncBody(expected, 42)

	body, l1 = tracker.GetCLSyncBody()
	require.JSONEq(t, string(expected), string(body))
	require.Equal(t, uint64(42), l1)
}

func TestInMemoryTracker_CLSyncBodyOverwrite(t *testing.T) {
	tracker := NewInMemoryConsensusTracker()

	tracker.SetCLSyncBody(json.RawMessage(`{"v":1}`), 10)
	tracker.SetCLSyncBody(json.RawMessage(`{"v":2}`), 20)

	body, l1 := tracker.GetCLSyncBody()
	require.JSONEq(t, `{"v":2}`, string(body))
	require.Equal(t, uint64(20), l1)
}

func TestInMemoryTracker_CLSyncBodyConcurrentAccess(t *testing.T) {
	tracker := NewInMemoryConsensusTracker()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		l1 := uint64(i + 1)
		go func() {
			defer wg.Done()
			tracker.SetCLSyncBody(json.RawMessage(`{"v":1}`), l1)
		}()
		go func() {
			defer wg.Done()
			body, l1Num := tracker.GetCLSyncBody()
			// Body and L1 should always be consistent with each other:
			// either both zero (initial) or both set.
			if body != nil {
				require.NotEqual(t, uint64(0), l1Num, "body set but l1 is 0")
			}
		}()
	}
	wg.Wait()
}

// --- selectConsensusSyncStatusBody tests ---

func TestSelectConsensusSyncStatusBody_MonotonicityFloor(t *testing.T) {
	srv := newDummyServer(t)
	be := newTestBackend(t, srv, 2*time.Second)
	cp := newTestPoller([]*Backend{be}, WithCLConsensusMode())

	setCLBackendState(cp, be, 100, json.RawMessage(`{"l1":100}`))
	cp.selectConsensusSyncStatusBody([]*Backend{be})

	body, floor := cp.tracker.GetCLSyncBody()
	require.NotNil(t, body)
	require.Equal(t, uint64(100), floor)

	// Higher L1 with single backend: accepted (200 >= floor 100).
	setCLBackendState(cp, be, 200, json.RawMessage(`{"l1":200}`))
	cp.selectConsensusSyncStatusBody([]*Backend{be})

	_, floor = cp.tracker.GetCLSyncBody()
	require.Equal(t, uint64(200), floor)

	// L1 below floor: rejected.
	setCLBackendState(cp, be, 50, json.RawMessage(`{"l1":50}`))
	cp.selectConsensusSyncStatusBody([]*Backend{be})

	_, floor = cp.tracker.GetCLSyncBody()
	require.Equal(t, uint64(200), floor, "L1=50 < floor=200, should not update")
}

func TestSelectConsensusSyncStatusBody_PicksLowestL1(t *testing.T) {
	srv := newDummyServer(t)
	be1 := newTestBackend(t, srv, 2*time.Second)
	be2 := newTestBackend(t, srv, 2*time.Second)
	cp := newTestPoller([]*Backend{be1, be2}, WithCLConsensusMode())

	setCLBackendState(cp, be1, 150, json.RawMessage(`{"backend":"be1"}`))
	setCLBackendState(cp, be2, 100, json.RawMessage(`{"backend":"be2"}`))

	cp.selectConsensusSyncStatusBody([]*Backend{be1, be2})

	body, floor := cp.tracker.GetCLSyncBody()
	require.Equal(t, uint64(100), floor)
	require.JSONEq(t, `{"backend":"be2"}`, string(body))
}

func TestSelectConsensusSyncStatusBody_EmptyGroup(t *testing.T) {
	srv := newDummyServer(t)
	be := newTestBackend(t, srv, 2*time.Second)
	cp := newTestPoller([]*Backend{be}, WithCLConsensusMode())

	// No candidates -> body stays nil.
	cp.selectConsensusSyncStatusBody([]*Backend{})

	body := cp.GetConsensusSyncStatusBody()
	require.Nil(t, body, "empty consensus group should not set a body")
}

func TestSelectConsensusSyncStatusBody_EmptyBodySkipped(t *testing.T) {
	srv := newDummyServer(t)
	be := newTestBackend(t, srv, 2*time.Second)
	cp := newTestPoller([]*Backend{be}, WithCLConsensusMode())

	// Backend has L1 number but empty sync body -> should be skipped.
	setCLBackendState(cp, be, 100, nil)
	cp.selectConsensusSyncStatusBody([]*Backend{be})

	body := cp.GetConsensusSyncStatusBody()
	require.Nil(t, body, "backend with nil syncStatusRaw should not be selected")
}

func TestSelectConsensusSyncStatusBody_FloorPreservedAcrossCycles(t *testing.T) {
	srv := newDummyServer(t)
	be1 := newTestBackend(t, srv, 2*time.Second)
	be2 := newTestBackend(t, srv, 2*time.Second)
	cp := newTestPoller([]*Backend{be1, be2}, WithCLConsensusMode())

	// Cycle 1: both at L1=100.
	setCLBackendState(cp, be1, 100, json.RawMessage(`{"cycle":1}`))
	setCLBackendState(cp, be2, 100, json.RawMessage(`{"cycle":1,"be2":true}`))
	cp.selectConsensusSyncStatusBody([]*Backend{be1, be2})
	_, floor := cp.tracker.GetCLSyncBody()
	require.Equal(t, uint64(100), floor)

	// Cycle 2: be1 drops out (empty body), be2 at L1=99 (below floor).
	setCLBackendState(cp, be1, 100, nil)
	setCLBackendState(cp, be2, 99, json.RawMessage(`{"cycle":2}`))
	cp.selectConsensusSyncStatusBody([]*Backend{be1, be2})

	// Floor should remain 100 — no valid candidate.
	body, floor := cp.tracker.GetCLSyncBody()
	require.Equal(t, uint64(100), floor, "floor should not decrease")
	require.JSONEq(t, `{"cycle":1}`, string(body), "body should be from cycle 1")
}

// --- GetConsensusSyncStatusBody serving tests ---

func TestGetConsensusSyncStatusBody_NilBeforeFirstCycle(t *testing.T) {
	srv := newDummyServer(t)
	be := newTestBackend(t, srv, 2*time.Second)
	cp := newTestPoller([]*Backend{be}, WithCLConsensusMode())

	body := cp.GetConsensusSyncStatusBody()
	require.Nil(t, body)
}

func TestGetConsensusSyncStatusBody_ReturnsBodyAfterSelection(t *testing.T) {
	srv := newDummyServer(t)
	be := newTestBackend(t, srv, 2*time.Second)
	cp := newTestPoller([]*Backend{be}, WithCLConsensusMode())

	expected := json.RawMessage(`{"safe_l2":{"number":"0x1"}}`)
	setCLBackendState(cp, be, 100, expected)
	cp.selectConsensusSyncStatusBody([]*Backend{be})

	body := cp.GetConsensusSyncStatusBody()
	require.NotNil(t, body)
	require.JSONEq(t, string(expected), string(body))
}

func TestGetConsensusSyncStatusBody_BodySurvivesEmptyCycle(t *testing.T) {
	srv := newDummyServer(t)
	be := newTestBackend(t, srv, 2*time.Second)
	cp := newTestPoller([]*Backend{be}, WithCLConsensusMode())

	// Cycle 1: set a valid body.
	setCLBackendState(cp, be, 100, json.RawMessage(`{"v":"good"}`))
	cp.selectConsensusSyncStatusBody([]*Backend{be})

	// Cycle 2: no valid candidates.
	cp.selectConsensusSyncStatusBody([]*Backend{})

	// Should still return the body from cycle 1.
	body := cp.GetConsensusSyncStatusBody()
	require.NotNil(t, body)
	require.JSONEq(t, `{"v":"good"}`, string(body))
}

// --- EL mode (non-CL) tests ---

func TestNonCLMode_CLSyncBodyUnused(t *testing.T) {
	srv := newDummyServer(t)
	be := newTestBackend(t, srv, 2*time.Second)
	// No WithCLConsensusMode() — EL mode.
	cp := newTestPoller([]*Backend{be})

	body, l1 := cp.tracker.GetCLSyncBody()
	require.Nil(t, body)
	require.Equal(t, uint64(0), l1)
}
