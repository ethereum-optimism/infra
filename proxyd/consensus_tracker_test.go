package proxyd

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// TestRedisConsensusTracker_SyncBodyPropagation verifies that SyncBody set by the
// leader is serialized into Redis and deserialized by a follower instance, so all
// proxyd replicas serve an identical optimism_syncStatus response body.
func TestRedisConsensusTracker_SyncBodyPropagation(t *testing.T) {
	redisServer, err := miniredis.Run()
	require.NoError(t, err)
	defer redisServer.Close()

	redisClient := redis.NewClient(&redis.Options{
		Addr: fmt.Sprintf("127.0.0.1:%s", redisServer.Port()),
	})

	ctx := context.Background()
	bg := &BackendGroup{Name: "test"}

	syncBody := json.RawMessage(`{"jsonrpc":"2.0","result":{"current_l1":{"number":"0x64"}},"id":1}`)

	// --- Leader ---
	leader := NewRedisConsensusTracker(ctx, redisClient, bg, "test",
		WithHeartbeatInterval(50*time.Millisecond),
	).(*RedisConsensusTracker)

	// Seed a valid consensus state with SyncBody on the leader's local tracker.
	leader.local.SetState(ConsensusTrackerState{
		Latest:    hexutil.Uint64(0x10),
		Safe:      hexutil.Uint64(0x10),
		Finalized: hexutil.Uint64(0x10),
		SyncBody:  syncBody,
	})

	// First heartbeat: no lock exists in Redis → leader acquires it and posts state.
	leader.stateHeartbeat()

	require.True(t, leader.leader, "expected leader to have acquired the lock")

	// Leader's GetState returns ct.local, so it immediately reflects the posted body.
	require.Equal(t, syncBody, leader.GetState().SyncBody, "leader GetState should return local state with SyncBody")

	// --- Follower ---
	follower := NewRedisConsensusTracker(ctx, redisClient, bg, "test",
		WithHeartbeatInterval(50*time.Millisecond),
	).(*RedisConsensusTracker)

	// Follower heartbeat: lock exists → follower reads state from Redis.
	follower.stateHeartbeat()

	require.False(t, follower.leader, "follower should not hold the lock")
	require.Equal(t, syncBody, follower.GetState().SyncBody, "follower should have received SyncBody from Redis")
}

// TestRedisConsensusTracker_SyncBodyUpdated verifies that when the leader posts
// an updated SyncBody, the follower picks up the new body on its next heartbeat.
func TestRedisConsensusTracker_SyncBodyUpdated(t *testing.T) {
	redisServer, err := miniredis.Run()
	require.NoError(t, err)
	defer redisServer.Close()

	redisClient := redis.NewClient(&redis.Options{
		Addr: fmt.Sprintf("127.0.0.1:%s", redisServer.Port()),
	})

	ctx := context.Background()
	bg := &BackendGroup{Name: "test"}

	body1 := json.RawMessage(`{"jsonrpc":"2.0","result":{"current_l1":{"number":"0x64"}},"id":1}`)
	body2 := json.RawMessage(`{"jsonrpc":"2.0","result":{"current_l1":{"number":"0x65"}},"id":1}`)

	leader := NewRedisConsensusTracker(ctx, redisClient, bg, "test",
		WithHeartbeatInterval(50*time.Millisecond),
	).(*RedisConsensusTracker)

	leader.local.SetState(ConsensusTrackerState{
		Latest:    hexutil.Uint64(0x10),
		Safe:      hexutil.Uint64(0x10),
		Finalized: hexutil.Uint64(0x10),
		SyncBody:  body1,
	})
	leader.stateHeartbeat()
	require.True(t, leader.leader)

	// Leader advances to body2.
	leader.local.SetState(ConsensusTrackerState{
		Latest:    hexutil.Uint64(0x11),
		Safe:      hexutil.Uint64(0x11),
		Finalized: hexutil.Uint64(0x11),
		SyncBody:  body2,
	})
	// Second heartbeat: leader extends the lock and re-posts the updated state.
	leader.stateHeartbeat()
	require.Equal(t, body2, leader.GetState().SyncBody)

	follower := NewRedisConsensusTracker(ctx, redisClient, bg, "test",
		WithHeartbeatInterval(50*time.Millisecond),
	).(*RedisConsensusTracker)

	follower.stateHeartbeat()
	require.False(t, follower.leader)
	require.Equal(t, body2, follower.GetState().SyncBody, "follower should have received updated SyncBody")
}
