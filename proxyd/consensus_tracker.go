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

	GetCLSyncBody() (body json.RawMessage, lastServedL1Num uint64)
	SetCLSyncBody(body json.RawMessage, l1Num uint64)
}

// ConsensusTrackerState holds the full consensus state in one snapshot.
// Adding a new field only requires changing this struct and update().
type ConsensusTrackerState struct {
	Latest    hexutil.Uint64 `json:"latest"`
	Safe      hexutil.Uint64 `json:"safe"`
	Finalized hexutil.Uint64 `json:"finalized"`
	LocalSafe hexutil.Uint64 `json:"local_safe"`

	CLSyncBody json.RawMessage `json:"cl_sync_body,omitempty"`
	CLL1Num    uint64          `json:"cl_l1_num,omitempty"`
}

func (ct *InMemoryConsensusTracker) update(o *ConsensusTrackerState) {
	ct.mutex.Lock()
	defer ct.mutex.Unlock()

	ct.state.Latest = o.Latest
	ct.state.Safe = o.Safe
	ct.state.Finalized = o.Finalized
	ct.state.LocalSafe = o.LocalSafe
	ct.updatedAt = time.Now()

	// Only overwrite CL fields when the source has them set.
	// SetState (EL path) does not populate CL fields; SetCLSyncBody does.
	if len(o.CLSyncBody) > 0 {
		ct.state.CLSyncBody = o.CLSyncBody
		ct.state.CLL1Num = o.CLL1Num
	}
}

// InMemoryConsensusTracker store and retrieve in memory, async-safe
type InMemoryConsensusTracker struct {
	mutex     sync.Mutex
	state     *ConsensusTrackerState
	updatedAt time.Time
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

// UpdatedAt returns when the state was last written by update.
func (ct *InMemoryConsensusTracker) UpdatedAt() time.Time {
	ct.mutex.Lock()
	defer ct.mutex.Unlock()
	return ct.updatedAt
}

func (ct *InMemoryConsensusTracker) SetState(state ConsensusTrackerState) {
	ct.update(&state)
}

func (ct *InMemoryConsensusTracker) GetCLSyncBody() (json.RawMessage, uint64) {
	ct.mutex.Lock()
	defer ct.mutex.Unlock()
	return ct.state.CLSyncBody, ct.state.CLL1Num
}

func (ct *InMemoryConsensusTracker) SetCLSyncBody(body json.RawMessage, l1Num uint64) {
	ct.mutex.Lock()
	defer ct.mutex.Unlock()
	ct.state.CLSyncBody = body
	ct.state.CLL1Num = l1Num
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

	// whether the last heartbeat found view() serving local state; heartbeat goroutine only
	servingLocal bool

	// serve local state instead of a remote state older than lockPeriod
	serveLocalWhenStale bool

	// holds the state collected by local pollers
	local *InMemoryConsensusTracker

	// holds a copy of the remote shared state
	// when leader, updates the remote with the local state
	remote *InMemoryConsensusTracker
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

func WithServeLocalWhenStale(serveLocalWhenStale bool) RedisConsensusTrackerOpt {
	return func(ct *RedisConsensusTracker) {
		ct.serveLocalWhenStale = serveLocalWhenStale
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
	defer ct.recordView()

	pool := goredis.NewPool(ct.client)
	rs := redsync.New(pool)
	key := ct.key("mutex")

	val, err := ct.client.Get(ct.ctx, key).Result()
	if err != nil && err != redis.Nil {
		log.Error("failed to read the lock", "err", err)
		RecordGroupConsensusError(ct.backendGroup, "read_lock", err)
		if ct.leader {
			ct.stepDown()
		}
		return
	}
	if val != "" {
		if ct.leader {
			log.Debug("extending lock")
			ok, err := ct.redlock.Extend()
			if err != nil || !ok {
				log.Error("failed to extend lock", "err", err, "mutex", ct.redlock.Name(), "val", ct.redlock.Value())
				RecordGroupConsensusError(ct.backendGroup, "leader_extend_lock", err)
				ct.stepDown()
				return
			}
			ct.postPayload(val)
		} else {
			// retrieve current leader
			leaderName, err := ct.client.Get(ct.ctx, ct.key(fmt.Sprintf("leader:%s", val))).Result()
			if err != nil && err != redis.Nil {
				log.Error("failed to read the remote leader", "err", err)
				RecordGroupConsensusError(ct.backendGroup, "read_leader", err)
				return
			}
			ct.leaderName = leaderName
			log.Debug("following", "val", val, "leader", leaderName)
			// retrieve payload
			val, err := ct.client.Get(ct.ctx, ct.key(fmt.Sprintf("state:%s", val))).Result()
			if err != nil && err != redis.Nil {
				log.Error("failed to read the remote state", "err", err)
				RecordGroupConsensusError(ct.backendGroup, "read_state", err)
				return
			}
			if val == "" {
				log.Error("remote state is missing (recent leader election maybe?)")
				RecordGroupConsensusError(ct.backendGroup, "read_state_missing", err)
				return
			}
			state := &ConsensusTrackerState{}
			err = json.Unmarshal([]byte(val), state)
			if err != nil {
				log.Error("failed to unmarshal the remote state", "err", err)
				RecordGroupConsensusError(ct.backendGroup, "read_unmarshal_state", err)
				return
			}

			ct.remote.update(state)
			log.Debug("updated state from remote", "state", val, "leader", leaderName)

			remoteState := ct.remote.GetState()
			RecordGroupConsensusHALatestBlock(ct.backendGroup, leaderName, remoteState.Latest)
			RecordGroupConsensusHASafeBlock(ct.backendGroup, leaderName, remoteState.Safe)
			RecordGroupConsensusHAFinalizedBlock(ct.backendGroup, leaderName, remoteState.Finalized)
			if remoteState.CLL1Num > 0 {
				RecordGroupConsensusHACLPinL1(ct.backendGroup, leaderName, remoteState.CLL1Num)
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

// stepDown gives up leadership after the lock could not be confirmed. The lock
// is released on a best-effort basis: the release fails when another node has
// already taken the lock or Redis is unreachable, and in both cases this node
// is no longer the leader (an unreleased lock expires after lockPeriod).
// Leadership must be dropped regardless, otherwise this node stops following
// the remote state and keeps serving a frozen view.
func (ct *RedisConsensusTracker) stepDown() {
	ok, err := ct.redlock.Unlock()
	if err != nil || !ok {
		log.Warn("failed to release the lock while stepping down", "err", err)
		RecordGroupConsensusError(ct.backendGroup, "leader_release_lock", err)
	}
	ct.leader = false
}

func (ct *RedisConsensusTracker) key(tag string) string {
	return fmt.Sprintf("consensus:%s:%s", ct.namespace, tag)
}

// view returns the tracker whose state is served. Normally that is the shared
// remote state, refreshed every heartbeat either by posting it as leader or by
// reading it from Redis as follower. If it has not been refreshed for longer
// than lockPeriod, no live leader is backing it (e.g. this node cannot reach
// Redis); with serveLocalWhenStale, serve the locally polled state instead of
// a frozen view.
func (ct *RedisConsensusTracker) view() *InMemoryConsensusTracker {
	if ct.serveLocalWhenStale && time.Since(ct.remote.UpdatedAt()) > ct.lockPeriod && ct.local.Valid() {
		return ct.local
	}
	return ct.remote
}

// recordView reports which state view() serves, logging when it switches.
func (ct *RedisConsensusTracker) recordView() {
	servingLocal := ct.view() == ct.local
	if servingLocal != ct.servingLocal {
		if servingLocal {
			log.Warn("shared consensus state is stale, serving local state",
				"remote_updated_at", ct.remote.UpdatedAt(), "lock_period", ct.lockPeriod)
		} else {
			log.Info("serving shared consensus state again", "leader", ct.leaderName)
		}
		ct.servingLocal = servingLocal
	}
	RecordGroupConsensusHAServingLocal(ct.backendGroup, servingLocal)
}

func (ct *RedisConsensusTracker) GetState() ConsensusTrackerState {
	return ct.view().GetState()
}

func (ct *RedisConsensusTracker) SetState(state ConsensusTrackerState) {
	ct.local.SetState(state)
}

func (ct *RedisConsensusTracker) GetCLSyncBody() (json.RawMessage, uint64) {
	return ct.view().GetCLSyncBody()
}

func (ct *RedisConsensusTracker) SetCLSyncBody(body json.RawMessage, l1Num uint64) {
	ct.local.SetCLSyncBody(body, l1Num)
	// updates to remote should only happen via a leader-only section of code like in postPayload function
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
	if remoteState.CLL1Num > 0 {
		RecordGroupConsensusHACLPinL1(ct.backendGroup, leader, remoteState.CLL1Num)
	}
}
