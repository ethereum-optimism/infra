package proxyd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/log"
	"github.com/go-redsync/redsync/v4"
	"github.com/go-redsync/redsync/v4/redis/goredis/v9"
	"github.com/redis/go-redis/v9"
)

// ConsensusTracker abstracts how we store and retrieve the current consensus
// allowing it to be stored locally in-memory or in a shared Redis cluster
type ConsensusTracker interface {
	GetState() ConsensusTrackerState
	SetState(state ConsensusTrackerState)
	// GetCLSyncBody returns the most recent CL sync status response body and
	// the L1 block number it was derived from. Used as the monotonicity floor
	// in selectConsensusSyncStatusBody.
	GetCLSyncBody() (body json.RawMessage, lastServedL1Num uint64)
	// SetCLSyncBody stores the CL sync status response body and L1 number.
	// On RedisConsensusTracker, this also updates the remote copy immediately
	// so GetCLSyncBody always returns fresh data on the leader.
	SetCLSyncBody(body json.RawMessage, l1Num uint64)
}

// ConsensusTrackerState holds the full consensus state in one snapshot.
// Adding a new field only requires changing this struct and update().
type ConsensusTrackerState struct {
	Latest    hexutil.Uint64 `json:"latest"`
	Safe      hexutil.Uint64 `json:"safe"`
	Finalized hexutil.Uint64 `json:"finalized"`
	LocalSafe hexutil.Uint64 `json:"local_safe"`
}

func (ct *InMemoryConsensusTracker) update(o *ConsensusTrackerState) {
	ct.mutex.Lock()
	defer ct.mutex.Unlock()

	ct.state.Latest = o.Latest
	ct.state.Safe = o.Safe
	ct.state.Finalized = o.Finalized
	ct.state.LocalSafe = o.LocalSafe
}

// InMemoryConsensusTracker store and retrieve in memory, async-safe
type InMemoryConsensusTracker struct {
	mutex sync.Mutex
	state *ConsensusTrackerState

	clSyncBody  json.RawMessage
	clSyncL1Num uint64
	clSyncMu    sync.RWMutex
}

func NewInMemoryConsensusTracker() ConsensusTracker {
	return &InMemoryConsensusTracker{
		mutex: sync.Mutex{},
		state: &ConsensusTrackerState{},
	}
}

func (ct *InMemoryConsensusTracker) Valid() bool {
	s := ct.GetState()
	return s.Latest > 0 && s.Safe > 0 && s.Finalized > 0
}

func (ct *InMemoryConsensusTracker) Behind(other *InMemoryConsensusTracker) bool {
	local := ct.GetState()
	remote := other.GetState()
	// LocalSafe is only non-zero in CL mode; in EL mode both sides are always 0
	// so this condition never fires for EL deployments.
	localSafeBehind := local.LocalSafe > 0 && local.LocalSafe < remote.LocalSafe
	return local.Latest < remote.Latest ||
		local.Safe < remote.Safe ||
		local.Finalized < remote.Finalized ||
		localSafeBehind
}

func (ct *InMemoryConsensusTracker) GetState() ConsensusTrackerState {
	ct.mutex.Lock()
	defer ct.mutex.Unlock()
	return *ct.state
}

func (ct *InMemoryConsensusTracker) SetState(state ConsensusTrackerState) {
	ct.update(&state)
}

func (ct *InMemoryConsensusTracker) GetCLSyncBody() (json.RawMessage, uint64) {
	ct.clSyncMu.RLock()
	defer ct.clSyncMu.RUnlock()
	return ct.clSyncBody, ct.clSyncL1Num
}

func (ct *InMemoryConsensusTracker) SetCLSyncBody(body json.RawMessage, l1Num uint64) {
	ct.clSyncMu.Lock()
	defer ct.clSyncMu.Unlock()
	ct.clSyncBody = body
	ct.clSyncL1Num = l1Num
}

// RedisConsensusTracker store and retrieve in a shared Redis cluster, with leader election
type RedisConsensusTracker struct {
	ctx          context.Context
	client       redis.UniversalClient
	namespace    string
	backendGroup *BackendGroup

	redlock           *redsync.Mutex
	lockPeriod        time.Duration
	heartbeatInterval time.Duration

	leader     bool
	leaderName string

	// holds the state collected by local pollers
	local *InMemoryConsensusTracker

	// holds a copy of the remote shared state
	// when leader, updates the remote with the local state
	remote *InMemoryConsensusTracker

	// CL sync body: local copy (written by SetCLSyncBody on the leader)
	// and remote copy (read by GetCLSyncBody, updated from Redis on followers).
	clLocalSyncBody  json.RawMessage
	clLocalL1Num     uint64
	clRemoteSyncBody json.RawMessage
	clRemoteL1Num    uint64
	clSyncMu         sync.RWMutex
}

type RedisConsensusTrackerOpt func(cp *RedisConsensusTracker)

func WithLockPeriod(lockPeriod time.Duration) RedisConsensusTrackerOpt {
	return func(ct *RedisConsensusTracker) {
		ct.lockPeriod = lockPeriod
	}
}

func WithHeartbeatInterval(heartbeatInterval time.Duration) RedisConsensusTrackerOpt {
	return func(ct *RedisConsensusTracker) {
		ct.heartbeatInterval = heartbeatInterval
	}
}
func NewRedisConsensusTracker(ctx context.Context,
	redisClient redis.UniversalClient,
	bg *BackendGroup,
	namespace string,
	opts ...RedisConsensusTrackerOpt) ConsensusTracker {

	tracker := &RedisConsensusTracker{
		ctx:          ctx,
		client:       redisClient,
		backendGroup: bg,
		namespace:    namespace,

		lockPeriod:        30 * time.Second,
		heartbeatInterval: 2 * time.Second,
		local:             NewInMemoryConsensusTracker().(*InMemoryConsensusTracker),
		remote:            NewInMemoryConsensusTracker().(*InMemoryConsensusTracker),
	}

	for _, opt := range opts {
		opt(tracker)
	}

	return tracker
}

func (ct *RedisConsensusTracker) Init() {
	go func() {
		for {
			timer := time.NewTimer(ct.heartbeatInterval)
			ct.stateHeartbeat()

			select {
			case <-timer.C:
				continue
			case <-ct.ctx.Done():
				timer.Stop()
				return
			}
		}
	}()
}

func (ct *RedisConsensusTracker) stateHeartbeat() {
	pool := goredis.NewPool(ct.client)
	rs := redsync.New(pool)
	key := ct.key("mutex")

	val, err := ct.client.Get(ct.ctx, key).Result()
	if err != nil && err != redis.Nil {
		log.Error("failed to read the lock", "err", err)
		RecordGroupConsensusError(ct.backendGroup, "read_lock", err)
		if ct.leader {
			ok, err := ct.redlock.Unlock()
			if err != nil || !ok {
				log.Error("failed to release the lock after error", "err", err)
				RecordGroupConsensusError(ct.backendGroup, "leader_release_lock", err)
				return
			}
			ct.leader = false
		}
		return
	}
	if val != "" {
		// Capture the lock token before it can be shadowed by inner declarations.
		lockToken := val

		if ct.leader {
			log.Debug("extending lock")
			ok, err := ct.redlock.Extend()
			if err != nil || !ok {
				log.Error("failed to extend lock", "err", err, "mutex", ct.redlock.Name(), "val", ct.redlock.Value())
				RecordGroupConsensusError(ct.backendGroup, "leader_extend_lock", err)
				ok, err := ct.redlock.Unlock()
				if err != nil || !ok {
					log.Error("failed to release the lock after error", "err", err)
					RecordGroupConsensusError(ct.backendGroup, "leader_release_lock", err)
					return
				}
				ct.leader = false
				return
			}
			ct.postPayload(lockToken)
		} else {
			// retrieve current leader
			leaderName, err := ct.client.Get(ct.ctx, ct.key(fmt.Sprintf("leader:%s", lockToken))).Result()
			if err != nil && err != redis.Nil {
				log.Error("failed to read the remote leader", "err", err)
				RecordGroupConsensusError(ct.backendGroup, "read_leader", err)
				return
			}
			ct.leaderName = leaderName
			log.Debug("following", "val", lockToken, "leader", leaderName)
			// retrieve payload
			stateVal, err := ct.client.Get(ct.ctx, ct.key(fmt.Sprintf("state:%s", lockToken))).Result()
			if err != nil && err != redis.Nil {
				log.Error("failed to read the remote state", "err", err)
				RecordGroupConsensusError(ct.backendGroup, "read_state", err)
				return
			}
			if stateVal == "" {
				log.Error("remote state is missing (recent leader election maybe?)")
				RecordGroupConsensusError(ct.backendGroup, "read_state_missing", err)
				return
			}
			state := &ConsensusTrackerState{}
			err = json.Unmarshal([]byte(stateVal), state)
			if err != nil {
				log.Error("failed to unmarshal the remote state", "err", err)
				RecordGroupConsensusError(ct.backendGroup, "read_unmarshal_state", err)
				return
			}

			ct.remote.update(state)
			log.Debug("updated state from remote", "state", stateVal, "leader", leaderName)

			remoteState := ct.remote.GetState()
			RecordGroupConsensusHALatestBlock(ct.backendGroup, leaderName, remoteState.Latest)
			RecordGroupConsensusHASafeBlock(ct.backendGroup, leaderName, remoteState.Safe)
			RecordGroupConsensusHAFinalizedBlock(ct.backendGroup, leaderName, remoteState.Finalized)

			// Read CL sync body from Redis (best-effort: continue serving last-known body on error)
			clKey := ct.key(fmt.Sprintf("cl_sync_body:%s", lockToken))
			clVal, err := ct.client.Get(ct.ctx, clKey).Result()
			if err != nil && err != redis.Nil {
				log.Error("failed to read CL sync body from Redis", "err", err)
				RecordGroupConsensusError(ct.backendGroup, "follower_read_cl_sync_body", err)
				// Best-effort: continue serving last-known body
			} else if clVal != "" {
				var payload clSyncBodyPayload
				if err := json.Unmarshal([]byte(clVal), &payload); err != nil {
					log.Error("failed to unmarshal CL sync body", "err", err)
				} else {
					ct.clSyncMu.Lock()
					ct.clRemoteSyncBody = payload.Body
					ct.clRemoteL1Num = payload.L1Num
					ct.clSyncMu.Unlock()
				}
			}
		}
	} else {
		if !ct.local.Valid() {
			log.Warn("local state is not valid or behind remote, skipping")
			return
		}
		if ct.remote.Valid() && ct.local.Behind(ct.remote) {
			log.Warn("local state is behind remote, skipping")
			return
		}

		log.Info("lock not found, creating a new one")

		mutex := rs.NewMutex(key,
			redsync.WithExpiry(ct.lockPeriod),
			redsync.WithFailFast(true),
			redsync.WithTries(1))

		// nosemgrep: missing-unlock-before-return
		// this lock is hold indefinitely, and it is extended until the leader dies
		if err := mutex.Lock(); err != nil {
			log.Debug("failed to obtain lock", "err", err)
			ct.leader = false
			return
		}

		log.Info("lock acquired", "mutex", mutex.Name(), "val", mutex.Value())
		ct.redlock = mutex
		ct.leader = true
		ct.postPayload(mutex.Value())
	}
}

func (ct *RedisConsensusTracker) key(tag string) string {
	return fmt.Sprintf("consensus:%s:%s", ct.namespace, tag)
}

func (ct *RedisConsensusTracker) GetState() ConsensusTrackerState {
	return ct.remote.GetState()
}

func (ct *RedisConsensusTracker) SetState(state ConsensusTrackerState) {
	ct.local.SetState(state)
}

// clSyncBodyPayload is the Redis wire format for the CL sync status body.
type clSyncBodyPayload struct {
	Body  json.RawMessage `json:"body"`
	L1Num uint64          `json:"l1_num"`
}

func (ct *RedisConsensusTracker) GetCLSyncBody() (json.RawMessage, uint64) {
	ct.clSyncMu.RLock()
	defer ct.clSyncMu.RUnlock()
	return ct.clRemoteSyncBody, ct.clRemoteL1Num
}

func (ct *RedisConsensusTracker) SetCLSyncBody(body json.RawMessage, l1Num uint64) {
	ct.clSyncMu.Lock()
	defer ct.clSyncMu.Unlock()
	ct.clLocalSyncBody = body
	ct.clLocalL1Num = l1Num
	// Update remote copy immediately so GetCLSyncBody returns fresh data
	// on the leader without waiting for the next postPayload Redis write.
	// Followers overwrite these from Redis in stateHeartbeat.
	ct.clRemoteSyncBody = body
	ct.clRemoteL1Num = l1Num
}

func (ct *RedisConsensusTracker) postPayload(mutexVal string) {
	state := ct.local.GetState()
	jsonState, err := json.Marshal(state)
	if err != nil {
		log.Error("failed to marshal local", "err", err)
		RecordGroupConsensusError(ct.backendGroup, "leader_marshal_local_state", err)
		ct.leader = false
		return
	}
	err = ct.client.Set(ct.ctx, ct.key(fmt.Sprintf("state:%s", mutexVal)), jsonState, ct.lockPeriod).Err()
	if err != nil {
		log.Error("failed to post the state", "err", err)
		RecordGroupConsensusError(ct.backendGroup, "leader_post_state", err)
		ct.leader = false
		return
	}

	leader, _ := os.LookupEnv("HOSTNAME")
	err = ct.client.Set(ct.ctx, ct.key(fmt.Sprintf("leader:%s", mutexVal)), leader, ct.lockPeriod).Err()
	if err != nil {
		log.Error("failed to post the leader", "err", err)
		RecordGroupConsensusError(ct.backendGroup, "leader_post_leader", err)
		ct.leader = false
		return
	}

	log.Debug("posted state", "state", string(jsonState), "leader", leader)

	ct.leaderName = leader
	ct.remote.update(&state)

	remoteState := ct.remote.GetState()
	RecordGroupConsensusHALatestBlock(ct.backendGroup, leader, remoteState.Latest)
	RecordGroupConsensusHASafeBlock(ct.backendGroup, leader, remoteState.Safe)
	RecordGroupConsensusHAFinalizedBlock(ct.backendGroup, leader, remoteState.Finalized)

	// Propagate CL sync body to Redis for follower consumption.
	// This is not the source of truth for the leader (SetCLSyncBody handles that).
	// Failures here are degraded state, not leadership failures.
	ct.clSyncMu.RLock()
	localBody := ct.clLocalSyncBody
	localL1 := ct.clLocalL1Num
	ct.clSyncMu.RUnlock()

	if len(localBody) > 0 {
		payload := clSyncBodyPayload{Body: localBody, L1Num: localL1}
		jsonPayload, err := json.Marshal(payload)
		if err != nil {
			log.Error("failed to marshal CL sync body payload", "err", err)
			RecordGroupConsensusError(ct.backendGroup, "leader_marshal_cl_sync_body", err)
			// CL body propagation failure is degraded state, not leadership failure
		} else {
			err = ct.client.Set(ct.ctx, ct.key(fmt.Sprintf("cl_sync_body:%s", mutexVal)), jsonPayload, ct.lockPeriod).Err()
			if err != nil {
				log.Error("failed to post CL sync body to Redis", "err", err)
				RecordGroupConsensusError(ct.backendGroup, "leader_post_cl_sync_body", err)
				// CL body propagation failure is degraded state, not leadership failure
			}
		}
	}
}
