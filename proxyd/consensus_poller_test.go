package proxyd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/stretchr/testify/require"
)

func TestFindConsensusBlock_exhaustsAtGenesis(t *testing.T) {
	ctx := context.Background()
	cp := &ConsensusPoller{}

	be1 := &Backend{Name: "node-a"}
	be2 := &Backend{Name: "node-b"}
	candidates := map[*Backend]*backendState{
		be1: {},
		be2: {},
	}

	fetch := func(ctx context.Context, be *Backend, block hexutil.Uint64) (hexutil.Uint64, string, error) {
		if be == be1 {
			return block, "hash-a", nil
		}
		return block, "hash-b", nil
	}

	num, hash, broken, _, _ := cp.findConsensusBlock(ctx, candidates, 0, 2, "hash-a", fetch, "test")
	require.Equal(t, hexutil.Uint64(0), num)
	require.Equal(t, "", hash)
	require.True(t, broken)
}

func TestFindConsensusBlock_agreesAtBlockZero(t *testing.T) {
	ctx := context.Background()
	cp := &ConsensusPoller{}

	be1 := &Backend{Name: "node-a"}
	be2 := &Backend{Name: "node-b"}
	candidates := map[*Backend]*backendState{
		be1: {},
		be2: {},
	}

	fetch := func(ctx context.Context, be *Backend, block hexutil.Uint64) (hexutil.Uint64, string, error) {
		switch uint64(block) {
		case 1:
			if be == be1 {
				return block, "hash-a-at-1", nil
			}
			return block, "hash-b-at-1", nil
		case 0:
			return block, "genesis-shared", nil
		default:
			return block, "unused", nil
		}
	}

	num, hash, broken, _, _ := cp.findConsensusBlock(ctx, candidates, 0, 1, "hash-a-at-1", fetch, "test")
	require.Equal(t, hexutil.Uint64(0), num)
	require.Equal(t, "genesis-shared", hash)
	require.False(t, broken)
}

func TestFindConsensusBlock_fetchTimeoutBoundsUnresponsiveBackend(t *testing.T) {
	cp := &ConsensusPoller{fetchTimeout: 50 * time.Millisecond}

	dead := &Backend{Name: "dead"}
	be1 := &Backend{Name: "node-a"}
	be2 := &Backend{Name: "node-b"}
	candidates := map[*Backend]*backendState{dead: {}, be1: {}, be2: {}}

	var sawDeadline atomic.Bool
	fetch := func(ctx context.Context, be *Backend, block hexutil.Uint64) (hexutil.Uint64, string, error) {
		if be == dead {
			_, hasDeadline := ctx.Deadline()
			sawDeadline.Store(hasDeadline)
			<-ctx.Done() // never answers
			return 0, "", ctx.Err()
		}
		return block, "hash-10", nil
	}

	start := time.Now()
	num, hash, broken, failed, verified := cp.findConsensusBlock(context.Background(), candidates, 9, 10, "hash-10", fetch, "test")
	elapsed := time.Since(start)

	require.True(t, sawDeadline.Load(), "fetch to the unresponsive backend must carry a deadline")
	require.Less(t, elapsed, time.Second, "an unresponsive backend must not stall the consensus loop")
	require.Equal(t, hexutil.Uint64(10), num)
	require.Equal(t, "hash-10", hash)
	require.True(t, verified)
	require.Equal(t, map[*Backend]bool{dead: true}, failed, "a timed-out check must not count as agreement")
	require.False(t, broken)
}

func TestFindConsensusBlock_zeroFetchTimeoutPassesContextThrough(t *testing.T) {
	cp := &ConsensusPoller{}
	be := &Backend{Name: "node-a"}

	fetch := func(ctx context.Context, _ *Backend, block hexutil.Uint64) (hexutil.Uint64, string, error) {
		_, hasDeadline := ctx.Deadline()
		require.False(t, hasDeadline)
		return block, "hash", nil
	}

	num, _, broken, _, _ := cp.findConsensusBlock(context.Background(), map[*Backend]*backendState{be: {}}, 0, 3, "hash", fetch, "test")
	require.Equal(t, hexutil.Uint64(3), num)
	require.False(t, broken)
}

// A backend that never answers must cost the loop fetchTimeout, not its 30s client timeout.
func TestFindConsensusBlock_fetchTimeoutWithHangingBackend(t *testing.T) {
	release := make(chan struct{})
	hanging := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer hanging.Close()
	defer close(release)

	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":67,"result":{"number":"0xa","hash":"0xabc"}}`))
	}))
	defer healthy.Close()

	dead := NewBackend("dead", hanging.URL, "", nil, WithTimeout(30*time.Second), WithMaxRetries(0))
	live := NewBackend("live", healthy.URL, "", nil, WithTimeout(30*time.Second), WithMaxRetries(0))

	cp := newTestPoller([]*Backend{dead, live}, WithFetchTimeout(100*time.Millisecond))
	defer cp.Shutdown()

	candidates := map[*Backend]*backendState{dead: {}, live: {}}

	start := time.Now()
	num, hash, _, failed, _ := cp.findConsensusBlock(context.Background(), candidates, 9, 10, "0xabc", cp.elBlockFetcher, "unsafe")
	elapsed := time.Since(start)

	require.Less(t, elapsed, 5*time.Second, "consensus loop waited on the hanging backend's client timeout")
	require.Equal(t, hexutil.Uint64(10), num)
	require.Equal(t, "0xabc", hash)
	require.True(t, failed[dead])
}

func TestNewConsensusPoller_fetchTimeout(t *testing.T) {
	cp := newTestPoller(nil)
	defer cp.Shutdown()
	require.Equal(t, DefaultConsensusFetchTimeout, cp.fetchTimeout)

	cp2 := newTestPoller(nil, WithFetchTimeout(2*time.Second))
	defer cp2.Shutdown()
	require.Equal(t, 2*time.Second, cp2.fetchTimeout)
}

// If no backend answers, nothing confirms the proposal, so the caller must not advance.
func TestFindConsensusBlock_unverifiedWhenEveryCheckFails(t *testing.T) {
	cp := &ConsensusPoller{}
	be1, be2 := &Backend{Name: "node-a"}, &Backend{Name: "node-b"}
	fetch := func(context.Context, *Backend, hexutil.Uint64) (hexutil.Uint64, string, error) {
		return 0, "", context.DeadlineExceeded
	}

	_, _, _, failed, verified := cp.findConsensusBlock(context.Background(), map[*Backend]*backendState{be1: {}, be2: {}}, 9, 10, "hash-10", fetch, "test")
	require.False(t, verified)
	require.Equal(t, map[*Backend]bool{be1: true, be2: true}, failed)
}
