package proxyd

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

const testLockPeriod = 30 * time.Second

func newTestRedisTracker(t *testing.T, addr string) *RedisConsensusTracker {
	t.Helper()
	client := redis.NewClient(&redis.Options{
		Addr:        addr,
		DialTimeout: 100 * time.Millisecond,
		MaxRetries:  -1,
	})
	t.Cleanup(func() { _ = client.Close() })
	return NewRedisConsensusTracker(context.Background(), client,
		&BackendGroup{Name: "test"}, "test", WithLockPeriod(testLockPeriod),
	).(*RedisConsensusTracker)
}

func testState(n uint64) ConsensusTrackerState {
	return ConsensusTrackerState{
		Latest:    hexutil.Uint64(n),
		Safe:      hexutil.Uint64(n - 10),
		Finalized: hexutil.Uint64(n - 20),
	}
}

// heartbeat runs one heartbeat as the named host, which postPayload publishes
// as the leader name.
func heartbeat(t *testing.T, ct *RedisConsensusTracker, host string) {
	t.Setenv("HOSTNAME", host)
	ct.stateHeartbeat()
}

// TestRedisConsensusTracker_StepsDownWhenLockTaken reproduces a leader whose
// lock expired and was taken by another node: extending the lock fails, and
// releasing it fails with "lock already taken". The old leader must step down
// and follow the new leader rather than serve its own frozen state.
func TestRedisConsensusTracker_StepsDownWhenLockTaken(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	a := newTestRedisTracker(t, mr.Addr())
	b := newTestRedisTracker(t, mr.Addr())

	a.SetState(testState(100))
	heartbeat(t, a, "node-a")
	require.True(t, a.leader)
	require.Equal(t, hexutil.Uint64(100), a.GetState().Latest)

	// a's lock lapses without a noticing (e.g. it could not extend in time)
	// and b takes over.
	mr.FastForward(testLockPeriod + time.Second)
	b.SetState(testState(200))
	heartbeat(t, b, "node-b")
	require.True(t, b.leader)

	// a still believes it is leader: Extend and Unlock both fail.
	heartbeat(t, a, "node-a")
	require.False(t, a.leader, "node must step down when it cannot extend the lock")

	// Next heartbeat a follows b's state.
	heartbeat(t, a, "node-a")
	require.False(t, a.leader)
	require.Equal(t, "node-b", a.leaderName)
	require.Equal(t, hexutil.Uint64(200), a.GetState().Latest)

	// b keeps advancing and a keeps following.
	b.SetState(testState(250))
	heartbeat(t, b, "node-b")
	heartbeat(t, a, "node-a")
	require.Equal(t, hexutil.Uint64(250), a.GetState().Latest)
}

// TestRedisConsensusTracker_RecoversFromRedisOutage reproduces the
// 2026-10-22 incident: the leader loses its connection to Redis (so neither
// reading nor releasing the lock works), the lock expires, another node takes
// over, and connectivity returns.
func TestRedisConsensusTracker_RecoversFromRedisOutage(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	a := newTestRedisTracker(t, mr.Addr())
	b := newTestRedisTracker(t, mr.Addr())

	a.SetState(testState(100))
	heartbeat(t, a, "node-a")
	require.True(t, a.leader)

	// Redis becomes unreachable: read_lock fails and the release fails too.
	mr.Close()
	heartbeat(t, a, "node-a")
	require.False(t, a.leader, "node must step down when it cannot read the lock")

	require.NoError(t, mr.Restart())
	mr.FastForward(testLockPeriod + time.Second)
	b.SetState(testState(200))
	heartbeat(t, b, "node-b")
	require.True(t, b.leader)

	a.SetState(testState(190))
	heartbeat(t, a, "node-a")
	require.False(t, a.leader)
	require.Equal(t, "node-b", a.leaderName)
	require.Equal(t, hexutil.Uint64(200), a.GetState().Latest)
}
