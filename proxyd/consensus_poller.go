package proxyd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/log"
)

const (
	DefaultPollerInterval = 1 * time.Second
)

var (
	errZeroLatestBlock    = errors.New("backend responded with blockheight 0 for latest block")
	errZeroSafeBlock      = errors.New("backend responded with blockheight 0 for safe block")
	errZeroFinalizedBlock = errors.New("backend responded with blockheight 0 for finalized block")
)

type OnConsensusBroken func()

// ConsensusPoller checks the consensus state for each member of a BackendGroup
// resolves the highest common block for multiple nodes, and reconciles the consensus
// in case of block hash divergence to minimize re-orgs
type ConsensusPoller struct {
	ctx        context.Context
	cancelFunc context.CancelFunc
	listeners  []OnConsensusBroken

	backendGroup      *BackendGroup
	backendState      map[*Backend]*backendState
	consensusGroupMux sync.Mutex
	consensusGroup    []*Backend

	tracker      ConsensusTracker
	asyncHandler ConsensusAsyncHandler

	minPeerCount       uint64
	banPeriod          time.Duration
	maxUpdateThreshold time.Duration
	maxBlockLag        uint64
	maxBlockRange      uint64
	interval           time.Duration

	// CL (op-node) consensus fields — only populated when consensusLayer is true.
	// All logic that reads these fields lives in consensus_poller_cl.go.
	consensusLayer  bool
	clSyncThreshold uint64
	clHeadL1MaxAge  time.Duration

	// Pin-backend cache for optimism_syncStatus (CL mode only).
	// selectConsensusSyncStatusBody selects the pin backend after each consensus
	// cycle and stores its full response body here for serving.
	syncStatusBodyMu  sync.RWMutex
	consensusSyncBody json.RawMessage // served response body for optimism_syncStatus
	lastServedCLL1Num uint64          // monotonicity floor for pin selection
}

type backendState struct {
	backendStateMux sync.Mutex

	latestBlockNumber    hexutil.Uint64
	latestBlockHash      string
	safeBlockNumber      hexutil.Uint64
	localSafeBlockNumber hexutil.Uint64
	finalizedBlockNumber hexutil.Uint64

	// CL mode only: used for pin-backend selection.
	currentL1Number uint64
	syncStatusRaw   json.RawMessage

	peerCount uint64
	inSync    bool

	lastUpdate time.Time

	bannedUntil time.Time
}

func (bs *backendState) IsBanned() bool {
	return time.Now().Before(bs.bannedUntil)
}

func (bs *backendState) GetLatestBlock() (hexutil.Uint64, string) {
	bs.backendStateMux.Lock()
	defer bs.backendStateMux.Unlock()
	return bs.latestBlockNumber, bs.latestBlockHash
}

func (bs *backendState) GetSafeBlockNumber() hexutil.Uint64 {
	bs.backendStateMux.Lock()
	defer bs.backendStateMux.Unlock()
	return bs.safeBlockNumber
}

func (bs *backendState) GetFinalizedBlockNumber() hexutil.Uint64 {
	bs.backendStateMux.Lock()
	defer bs.backendStateMux.Unlock()
	return bs.finalizedBlockNumber
}

// GetConsensusGroup returns the backend members that are agreeing in a consensus
func (cp *ConsensusPoller) GetConsensusGroup() []*Backend {
	defer cp.consensusGroupMux.Unlock()
	cp.consensusGroupMux.Lock()

	g := make([]*Backend, len(cp.consensusGroup))
	copy(g, cp.consensusGroup)

	return g
}

// GetLatestBlockNumber returns the `latest` agreed block number in a consensus
func (cp *ConsensusPoller) GetLatestBlockNumber() hexutil.Uint64 {
	return cp.tracker.GetState().Latest
}

// GetSafeBlockNumber returns the `safe` agreed block number in a consensus
func (cp *ConsensusPoller) GetSafeBlockNumber() hexutil.Uint64 {
	return cp.tracker.GetState().Safe
}

// GetFinalizedBlockNumber returns the `finalized` agreed block number in a consensus
func (cp *ConsensusPoller) GetFinalizedBlockNumber() hexutil.Uint64 {
	return cp.tracker.GetState().Finalized
}

// GetLocalSafeBlockNumber returns the `local_safe` agreed block number in a consensus (CL mode only)
func (cp *ConsensusPoller) GetLocalSafeBlockNumber() hexutil.Uint64 {
	return cp.tracker.GetState().LocalSafe
}

// IsConsensusLayer returns true if this poller is operating in CL (op-node) mode
func (cp *ConsensusPoller) IsConsensusLayer() bool {
	return cp.consensusLayer
}

func (cp *ConsensusPoller) Shutdown() {
	cp.asyncHandler.Shutdown()
}

// ConsensusAsyncHandler controls the asynchronous polling mechanism, interval and shutdown
type ConsensusAsyncHandler interface {
	Init()
	Shutdown()
}

// NoopAsyncHandler allows fine control updating the consensus
type NoopAsyncHandler struct{}

func NewNoopAsyncHandler() ConsensusAsyncHandler {
	log.Warn("using NewNoopAsyncHandler")
	return &NoopAsyncHandler{}
}
func (ah *NoopAsyncHandler) Init()     {}
func (ah *NoopAsyncHandler) Shutdown() {}

// PollerAsyncHandler asynchronously updates each individual backend and the group consensus
type PollerAsyncHandler struct {
	ctx context.Context
	cp  *ConsensusPoller
}

func NewPollerAsyncHandler(ctx context.Context, cp *ConsensusPoller) ConsensusAsyncHandler {
	return &PollerAsyncHandler{
		ctx: ctx,
		cp:  cp,
	}
}
func (ah *PollerAsyncHandler) Init() {
	// create the individual backend pollers.
	log.Info("total number of primary candidates", "primaries", len(ah.cp.backendGroup.Primaries()))
	log.Info("total number of fallback candidates", "fallbacks", len(ah.cp.backendGroup.Fallbacks()))

	for _, be := range ah.cp.backendGroup.Primaries() {
		go func(be *Backend) {
			for {
				timer := time.NewTimer(ah.cp.interval)
				ah.cp.UpdateBackend(ah.ctx, be)
				select {
				case <-timer.C:
				case <-ah.ctx.Done():
					timer.Stop()
					return
				}
			}
		}(be)
	}

	for _, be := range ah.cp.backendGroup.Fallbacks() {
		go func(be *Backend) {
			for {
				timer := time.NewTimer(ah.cp.interval)

				healthyCandidates := ah.cp.FilterCandidates(ah.cp.backendGroup.Primaries())

				log.Info("number of healthy primary candidates", "healthy_candidates", len(healthyCandidates))
				if len(healthyCandidates) == 0 {
					log.Debug("zero healthy candidates, querying fallback backend",
						"backend_name", be.Name)
					ah.cp.UpdateBackend(ah.ctx, be)
				}

				select {
				case <-timer.C:
				case <-ah.ctx.Done():
					timer.Stop()
					return
				}
			}
		}(be)
	}

	// create the group consensus poller
	go func() {
		for {
			timer := time.NewTimer(ah.cp.interval)
			log.Info("updating backend group consensus")
			ah.cp.UpdateBackendGroupConsensus(ah.ctx)

			select {
			case <-timer.C:
			case <-ah.ctx.Done():
				timer.Stop()
				return
			}
		}
	}()
}
func (ah *PollerAsyncHandler) Shutdown() {
	ah.cp.cancelFunc()
}

type ConsensusOpt func(cp *ConsensusPoller)

func WithTracker(tracker ConsensusTracker) ConsensusOpt {
	return func(cp *ConsensusPoller) {
		cp.tracker = tracker
	}
}

func WithAsyncHandler(asyncHandler ConsensusAsyncHandler) ConsensusOpt {
	return func(cp *ConsensusPoller) {
		cp.asyncHandler = asyncHandler
	}
}

func WithListener(listener OnConsensusBroken) ConsensusOpt {
	return func(cp *ConsensusPoller) {
		cp.AddListener(listener)
	}
}

func (cp *ConsensusPoller) AddListener(listener OnConsensusBroken) {
	cp.listeners = append(cp.listeners, listener)
}

func (cp *ConsensusPoller) ClearListeners() {
	cp.listeners = []OnConsensusBroken{}
}

func WithBanPeriod(banPeriod time.Duration) ConsensusOpt {
	return func(cp *ConsensusPoller) {
		cp.banPeriod = banPeriod
	}
}

func WithMaxUpdateThreshold(maxUpdateThreshold time.Duration) ConsensusOpt {
	return func(cp *ConsensusPoller) {
		cp.maxUpdateThreshold = maxUpdateThreshold
	}
}

func WithMaxBlockLag(maxBlockLag uint64) ConsensusOpt {
	return func(cp *ConsensusPoller) {
		cp.maxBlockLag = maxBlockLag
	}
}

func WithMaxBlockRange(maxBlockRange uint64) ConsensusOpt {
	return func(cp *ConsensusPoller) {
		cp.maxBlockRange = maxBlockRange
	}
}

func WithMinPeerCount(minPeerCount uint64) ConsensusOpt {
	return func(cp *ConsensusPoller) {
		cp.minPeerCount = minPeerCount
	}
}

func WithPollerInterval(interval time.Duration) ConsensusOpt {
	return func(cp *ConsensusPoller) {
		cp.interval = interval
	}
}

func NewConsensusPoller(bg *BackendGroup, opts ...ConsensusOpt) *ConsensusPoller {
	ctx, cancelFunc := context.WithCancel(context.Background())

	state := make(map[*Backend]*backendState, len(bg.Backends))

	cp := &ConsensusPoller{
		ctx:          ctx,
		cancelFunc:   cancelFunc,
		backendGroup: bg,
		backendState: state,

		banPeriod:               5 * time.Minute,
		maxUpdateThreshold:      30 * time.Second,
		maxBlockLag:             8, // 8*12 seconds = 96 seconds ~ 1.6 minutes
		minPeerCount:            3,
		interval:                DefaultPollerInterval,
		clSyncThreshold: 10,              // 10 L1 blocks ~ 2 minutes
		clHeadL1MaxAge:  5 * time.Minute, // L1 head older than this → node is stalled
	}

	for _, opt := range opts {
		opt(cp)
	}

	if cp.consensusLayer {
		cp.logCLConfigWarnings()
	}

	if cp.tracker == nil {
		cp.tracker = NewInMemoryConsensusTracker()
	}

	if cp.asyncHandler == nil {
		cp.asyncHandler = NewPollerAsyncHandler(ctx, cp)
	}

	cp.Reset()
	cp.asyncHandler.Init()

	return cp
}

// UpdateBackend refreshes the consensus state of a single backend
func (cp *ConsensusPoller) UpdateBackend(ctx context.Context, be *Backend) {
	bs := cp.GetBackendState(be)
	RecordConsensusBackendBanned(be, bs.IsBanned())

	if bs.IsBanned() {
		log.Debug("skipping backend - banned", "backend", be.Name)
		return
	}

	// if backend is not healthy state we'll only resume checking it after ban
	if !be.IsHealthy() && !be.forcedCandidate {
		log.Warn("backend banned - not healthy", "backend", be.Name)
		if cp.consensusLayer {
			RecordCLBanNotHealthy(be)
		}
		cp.Ban(be)
		return
	}

	var peerCount uint64
	var err error
	if !be.skipPeerCountCheck {
		peerCount, err = cp.getPeerCount(ctx, be)
		if err != nil {
			log.Warn("error updating backend peer count", "name", be.Name, "err", err)
			return
		}
		if peerCount == 0 {
			log.Warn("peer count responded with 200 and 0 peers", "name", be.Name)
			be.intermittentErrorsSlidingWindow.Incr()
			return
		}
		RecordConsensusBackendPeerCount(be, peerCount)
	}

	var inSync bool
	var latestBlockNumber, safeBlockNumber, localSafeBlockNumber, finalizedBlockNumber hexutil.Uint64
	var latestBlockHash string
	var currentL1Number uint64
	var syncStatusRaw json.RawMessage
	if cp.consensusLayer {
		syncStatus, rawBody, clInSync, err := cp.updateCLBackend(ctx, be)
		if err != nil {
			return
		}
		inSync = clInSync
		latestBlockNumber, latestBlockHash = syncStatus.LatestBlockNumber, syncStatus.LatestBlockHash
		safeBlockNumber = syncStatus.SafeBlockNumber
		localSafeBlockNumber = syncStatus.LocalSafeBlockNumber
		finalizedBlockNumber = syncStatus.FinalizedBlockNumber
		currentL1Number, syncStatusRaw = syncStatus.CurrentL1Number, rawBody
		if !cp.validateCLBackendUpdate(be, safeBlockNumber, localSafeBlockNumber) {
			return
		}
	} else {
		var err error
		inSync, err = cp.isELInSync(ctx, be)
		RecordConsensusBackendInSync(be, err == nil && inSync)
		if err != nil {
			log.Warn("error updating backend sync state", "name", be.Name, "err", err)
			return
		}
		els, err := cp.fetchELState(ctx, be)
		if err != nil {
			return
		}
		latestBlockNumber, latestBlockHash = els.LatestBlockNumber, els.LatestBlockHash
		safeBlockNumber = els.SafeBlockNumber
		finalizedBlockNumber = els.FinalizedBlockNumber
	}

	RecordConsensusBackendUpdateDelay(be, bs.lastUpdate)

	changed := cp.setBackendState(be, backendStateUpdate{
		peerCount:            peerCount,
		inSync:               inSync,
		latestBlockNumber:    latestBlockNumber,
		latestBlockHash:      latestBlockHash,
		safeBlockNumber:      safeBlockNumber,
		finalizedBlockNumber: finalizedBlockNumber,
	})

	if cp.consensusLayer {
		clbs := cp.backendState[be]
		clbs.backendStateMux.Lock()
		clbs.localSafeBlockNumber = localSafeBlockNumber
		clbs.currentL1Number = currentL1Number
		clbs.syncStatusRaw = syncStatusRaw
		clbs.backendStateMux.Unlock()
	}

	RecordBackendLatestBlock(be, latestBlockNumber)
	RecordBackendSafeBlock(be, safeBlockNumber)
	RecordBackendFinalizedBlock(be, finalizedBlockNumber)
	if cp.consensusLayer {
		RecordCLBackendLocalSafeBlock(be, localSafeBlockNumber)
	}

	if changed {
		log.Debug("backend state updated",
			"name", be.Name,
			"peerCount", peerCount,
			"inSync", inSync,
			"latestBlockNumber", latestBlockNumber,
			"latestBlockHash", latestBlockHash,
			"safeBlockNumber", safeBlockNumber,
			"finalizedBlockNumber", finalizedBlockNumber,
			"lastUpdate", bs.lastUpdate)
	}

	// sanity check for latest, safe and finalized block tags
	expectedBlockTags := cp.checkExpectedBlockTags(
		be.safeBlockDriftThreshold,
		be.finalizedBlockDriftThreshold,
		latestBlockNumber,
		bs.safeBlockNumber, safeBlockNumber,
		bs.finalizedBlockNumber, finalizedBlockNumber)

	RecordBackendUnexpectedBlockTags(be, !expectedBlockTags)

	if !expectedBlockTags && !be.forcedCandidate {
		log.Warn("backend banned - unexpected block tags",
			"backend", be.Name,
			"oldFinalized", bs.finalizedBlockNumber,
			"finalizedBlockNumber", finalizedBlockNumber,
			"oldSafe", bs.safeBlockNumber,
			"safeBlockNumber", safeBlockNumber,
			"latestBlockNumber", latestBlockNumber,
		)
		if cp.consensusLayer {
			RecordCLBanUnexpectedBlockTags(be)
		}
		cp.Ban(be)
	}
}

// checkExpectedBlockTags for unexpected conditions on block tags
// - finalized block number should never decrease by more than finalizedBlockDriftThreshold
// - safe block number should never decrease by more than safeBlockDriftThreshold
// - finalized block should be <= safe block <= latest block
func (cp *ConsensusPoller) checkExpectedBlockTags(
	safeBlockDriftThreshold uint64,
	finalizedBlockDriftThreshold uint64,
	currentLatest hexutil.Uint64,
	oldSafe hexutil.Uint64, currentSafe hexutil.Uint64,
	oldFinalized hexutil.Uint64, currentFinalized hexutil.Uint64) bool {

	minSafeBlockAllowance := oldSafe
	minFinalizedBlockAllowance := oldFinalized
	if minSafeBlockAllowance > hexutil.Uint64(safeBlockDriftThreshold) {
		minSafeBlockAllowance -= hexutil.Uint64(safeBlockDriftThreshold)
	}
	if minFinalizedBlockAllowance > hexutil.Uint64(finalizedBlockDriftThreshold) {
		minFinalizedBlockAllowance -= hexutil.Uint64(finalizedBlockDriftThreshold)
	}

	return currentFinalized >= minFinalizedBlockAllowance &&
		currentSafe >= minSafeBlockAllowance &&
		currentFinalized <= currentSafe &&
		currentSafe <= currentLatest
}

// UpdateBackendGroupConsensus resolves the current group consensus based on the state of the backends
func (cp *ConsensusPoller) UpdateBackendGroupConsensus(ctx context.Context) {
	// get the latest block number from the tracker
	currentConsensusBlockNumber := cp.GetLatestBlockNumber()

	// get the candidates for the consensus group
	candidates := cp.getConsensusCandidates()

	var lowestLatestBlock hexutil.Uint64
	var lowestLatestBlockHash string
	var lowestFinalizedBlock hexutil.Uint64
	var lowestSafeBlock hexutil.Uint64
	var lowestLocalSafeBlock hexutil.Uint64 // only populated in CL mode
	for _, bs := range candidates {
		if lowestLatestBlock == 0 || bs.latestBlockNumber < lowestLatestBlock {
			lowestLatestBlock = bs.latestBlockNumber
			lowestLatestBlockHash = bs.latestBlockHash
		}
		if lowestFinalizedBlock == 0 || bs.finalizedBlockNumber < lowestFinalizedBlock {
			lowestFinalizedBlock = bs.finalizedBlockNumber
		}
		if lowestSafeBlock == 0 || bs.safeBlockNumber < lowestSafeBlock {
			lowestSafeBlock = bs.safeBlockNumber
		}
		if cp.consensusLayer && (lowestLocalSafeBlock == 0 || bs.localSafeBlockNumber < lowestLocalSafeBlock) {
			lowestLocalSafeBlock = bs.localSafeBlockNumber
		}
	}

	if cp.consensusLayer && lowestSafeBlock > 0 {
		candidates, lowestSafeBlock, lowestLocalSafeBlock = cp.verifyCLOutputRoots(ctx, candidates, lowestSafeBlock)
	}

	// find the proposed block among the candidates
	// the proposed block needs have the same hash in the entire consensus group
	proposedBlock := lowestLatestBlock
	proposedBlockHash := lowestLatestBlockHash
	broken := false

	if lowestLatestBlock > currentConsensusBlockNumber {
		log.Debug("validating consensus on block", "lowestLatestBlock", lowestLatestBlock)
	}

	// if there is a block to propose, verify all candidates agree on the same hash,
	// walking back one block at a time until consensus is found.
	// CL mode: no hash walk-back needed — optimism_syncStatus is served from the pin
	// backend cache (a single-backend snapshot), so unsafe block hash consensus is irrelevant.
	// optimism_outputAtBlock also does not work for unsafe blocks.
	if proposedBlock > 0 && !cp.consensusLayer {
		proposedBlock, proposedBlockHash, broken = cp.findConsensusBlock(ctx, candidates, currentConsensusBlockNumber, proposedBlock, proposedBlockHash, cp.elBlockFetcher, "unsafe")
	}

	if broken {
		// propagate event to other interested parts, such as cache invalidator
		for _, l := range cp.listeners {
			l()
		}
		log.Info("consensus broken",
			"currentConsensusBlockNumber", currentConsensusBlockNumber,
			"proposedBlock", proposedBlock,
			"proposedBlockHash", proposedBlockHash)
	}

	// update tracker
	cp.tracker.SetState(ConsensusTrackerState{
		Latest:    proposedBlock,
		Safe:      lowestSafeBlock,
		Finalized: lowestFinalizedBlock,
		LocalSafe: lowestLocalSafeBlock,
	})

	// update consensus group
	group := make([]*Backend, 0, len(candidates))
	consensusBackendsNames := make([]string, 0, len(candidates))
	filteredBackendsNames := make([]string, 0, len(cp.backendGroup.Backends))
	for _, be := range cp.backendGroup.Backends {
		_, exist := candidates[be]
		if exist {
			group = append(group, be)
			consensusBackendsNames = append(consensusBackendsNames, be.Name)
		} else {
			filteredBackendsNames = append(filteredBackendsNames, be.Name)
		}
	}

	cp.consensusGroupMux.Lock()
	cp.consensusGroup = group
	cp.consensusGroupMux.Unlock()

	if cp.consensusLayer {
		cp.selectConsensusSyncStatusBody(group)
	}

	RecordGroupConsensusLatestBlock(cp.backendGroup, proposedBlock)
	RecordGroupConsensusSafeBlock(cp.backendGroup, lowestSafeBlock)
	RecordGroupConsensusFinalizedBlock(cp.backendGroup, lowestFinalizedBlock)
	if cp.consensusLayer {
		RecordCLGroupLocalSafeBlock(cp.backendGroup, lowestLocalSafeBlock)
	}

	RecordGroupConsensusCount(cp.backendGroup, len(group))
	RecordGroupConsensusFilteredCount(cp.backendGroup, len(filteredBackendsNames))
	RecordGroupTotalCount(cp.backendGroup, len(cp.backendGroup.Backends))

	log.Debug("group state",
		"proposedBlock", proposedBlock,
		"consensusBackends", strings.Join(consensusBackendsNames, ", "),
		"filteredBackends", strings.Join(filteredBackendsNames, ", "))
}

// IsBanned checks if a specific backend is banned
func (cp *ConsensusPoller) IsBanned(be *Backend) bool {
	bs := cp.backendState[be]
	defer bs.backendStateMux.Unlock()
	bs.backendStateMux.Lock()
	return bs.IsBanned()
}

// IsBanned checks if a specific backend is banned
func (cp *ConsensusPoller) BannedUntil(be *Backend) time.Time {
	bs := cp.backendState[be]
	defer bs.backendStateMux.Unlock()
	bs.backendStateMux.Lock()
	return bs.bannedUntil
}

// Ban bans a specific backend
func (cp *ConsensusPoller) Ban(be *Backend) {
	if be.forcedCandidate {
		return
	}

	bs := cp.backendState[be]
	defer bs.backendStateMux.Unlock()
	bs.backendStateMux.Lock()
	bs.bannedUntil = time.Now().Add(cp.banPeriod)

	// when we ban a node, we give it the chance to start from any block when it is back
	bs.latestBlockNumber = 0
	bs.safeBlockNumber = 0
	bs.finalizedBlockNumber = 0
}

// Unban removes any bans from the backends
func (cp *ConsensusPoller) Unban(be *Backend) {
	bs := cp.backendState[be]
	defer bs.backendStateMux.Unlock()
	bs.backendStateMux.Lock()
	bs.bannedUntil = time.Now().Add(-10 * time.Hour)
}

// Reset resets all backend states.
func (cp *ConsensusPoller) Reset() {
	for _, be := range cp.backendGroup.Backends {
		cp.backendState[be] = &backendState{}
	}
}

// blockHashFetcher retrieves the block number and hash for a given block from a backend.
type blockHashFetcher func(ctx context.Context, be *Backend, block hexutil.Uint64) (hexutil.Uint64, string, error)

// elBlockFetcher is a blockHashFetcher for EL backends.
func (cp *ConsensusPoller) elBlockFetcher(ctx context.Context, be *Backend, block hexutil.Uint64) (hexutil.Uint64, string, error) {
	return cp.fetchELBlock(ctx, be, block.String())
}

// findConsensusBlock walks back from startBlock until all candidates agree on the same block hash.
// label identifies the safety level ("unsafe", "safe", "finalized") for log context.
// It returns the agreed block number, hash, and whether consensus was broken relative to currentConsensusBlock.
func (cp *ConsensusPoller) findConsensusBlock(
	ctx context.Context,
	candidates map[*Backend]*backendState,
	currentConsensusBlock hexutil.Uint64,
	startBlock hexutil.Uint64,
	startHash string,
	fetch blockHashFetcher,
	label string,
) (proposedBlock hexutil.Uint64, proposedBlockHash string, broken bool) {
	proposedBlock = startBlock
	proposedBlockHash = startHash

	for {
		allAgreed := true
		for be := range candidates {
			actualBlockNumber, actualHash, err := fetch(ctx, be, proposedBlock)
			if err != nil {
				log.Warn("error fetching block for consensus check", "label", label, "name", be.Name, "err", err)
				continue
			}
			if proposedBlockHash == "" {
				proposedBlockHash = actualHash
			}
			if actualBlockNumber != proposedBlock || actualHash != proposedBlockHash {
				if currentConsensusBlock >= actualBlockNumber {
					log.Warn("backend broke consensus",
						"label", label,
						"name", be.Name,
						"actualBlockNumber", actualBlockNumber,
						"actualHash", actualHash,
						"proposedBlock", proposedBlock,
						"proposedBlockHash", proposedBlockHash)
					broken = true
				}
				allAgreed = false
				break
			}
		}
		if allAgreed {
			return proposedBlock, proposedBlockHash, broken
		}
		if proposedBlock == 0 {
			return 0, "", true
		}
		proposedBlock -= 1
		proposedBlockHash = ""
		log.Debug("no consensus, walking back", "label", label, "block", proposedBlock)
	}
}

// fetchELState fetches the block numbers and hashes for the latest, safe, and finalized
// tags from a single EL backend, performing zero-value validation inline.
func (cp *ConsensusPoller) fetchELState(ctx context.Context, be *Backend) (ELBlockState, error) {
	var s ELBlockState
	var err error

	s.LatestBlockNumber, s.LatestBlockHash, err = cp.fetchELBlock(ctx, be, "latest")
	if err != nil {
		log.Warn("error updating backend - latest block will not be updated", "name", be.Name, "err", err)
		return ELBlockState{}, err
	}
	if s.LatestBlockNumber == 0 {
		log.Warn("error backend responded a 200 with blockheight 0 for latest block", "name", be.Name)
		be.intermittentErrorsSlidingWindow.Incr()
		return ELBlockState{}, errZeroLatestBlock
	}

	s.SafeBlockNumber, _, err = cp.fetchELBlock(ctx, be, "safe")
	if err != nil {
		log.Warn("error updating backend - safe block will not be updated", "name", be.Name, "err", err)
		return ELBlockState{}, err
	}
	if s.SafeBlockNumber == 0 {
		log.Warn("error backend responded a 200 with blockheight 0 for safe block", "name", be.Name)
		be.intermittentErrorsSlidingWindow.Incr()
		return ELBlockState{}, errZeroSafeBlock
	}

	s.FinalizedBlockNumber, _, err = cp.fetchELBlock(ctx, be, "finalized")
	if err != nil {
		log.Warn("error updating backend - finalized block will not be updated", "name", be.Name, "err", err)
		return ELBlockState{}, err
	}
	if s.FinalizedBlockNumber == 0 {
		log.Warn("error backend responded a 200 with blockheight 0 for finalized block", "name", be.Name)
		be.intermittentErrorsSlidingWindow.Incr()
		return ELBlockState{}, errZeroFinalizedBlock
	}

	return s, nil
}

// fetchELBlock calls eth_getBlockByNumber and returns the block number and hash.
func (cp *ConsensusPoller) fetchELBlock(ctx context.Context, be *Backend, block string) (blockNumber hexutil.Uint64, blockHash string, err error) {
	var rpcRes RPCRes
	log.Trace("executing fetchELBlock for backend",
		"backend", be.Name,
		"block", block,
	)
	err = be.ForwardRPC(ctx, &rpcRes, "67", "eth_getBlockByNumber", block, false)
	if err != nil {
		return 0, "", err
	}

	jsonMap, ok := rpcRes.Result.(map[string]interface{})
	if !ok {
		return 0, "", fmt.Errorf("unexpected response to eth_getBlockByNumber on backend %s", be.Name)
	}
	numStr, ok := jsonMap["number"].(string)
	if !ok {
		return 0, "", fmt.Errorf("missing or invalid number in eth_getBlockByNumber response on backend %s", be.Name)
	}
	hashStr, ok := jsonMap["hash"].(string)
	if !ok {
		return 0, "", fmt.Errorf("missing or invalid hash in eth_getBlockByNumber response on backend %s", be.Name)
	}
	blockNumber = hexutil.Uint64(hexutil.MustDecodeUint64(numStr))
	blockHash = hashStr

	return
}

// getPeerCount retrieves the current peer count from the backend.
func (cp *ConsensusPoller) getPeerCount(ctx context.Context, be *Backend) (count uint64, err error) {
	if cp.consensusLayer {
		return cp.fetchCLPeerCount(ctx, be)
	}
	return cp.fetchELPeerCount(ctx, be)
}

// fetchELPeerCount calls net_peerCount and returns the peer count.
func (cp *ConsensusPoller) fetchELPeerCount(ctx context.Context, be *Backend) (count uint64, err error) {
	var rpcRes RPCRes
	err = be.ForwardRPC(ctx, &rpcRes, "67", "net_peerCount")
	if err != nil {
		return 0, err
	}

	jsonMap, ok := rpcRes.Result.(string)
	if !ok {
		return 0, fmt.Errorf("unexpected response to net_peerCount on backend %s", be.Name)
	}

	count = hexutil.MustDecodeUint64(jsonMap)

	return count, nil
}

// isELInSync checks if an EL backend is in sync by calling eth_syncing.
func (cp *ConsensusPoller) isELInSync(ctx context.Context, be *Backend) (result bool, err error) {
	var rpcRes RPCRes
	err = be.ForwardRPC(ctx, &rpcRes, "67", "eth_syncing")
	if err != nil {
		return false, err
	}

	var res bool
	switch typed := rpcRes.Result.(type) {
	case bool:
		syncing := typed
		res = !syncing
	case string:
		syncing, err := strconv.ParseBool(typed)
		if err != nil {
			return false, err
		}
		res = !syncing
	default:
		// result is a json when not in sync
		res = false
	}

	return res, nil
}

// GetBackendState creates a copy of backend state so that the caller can use it without locking
func (cp *ConsensusPoller) GetBackendState(be *Backend) *backendState {
	bs := cp.backendState[be]
	defer bs.backendStateMux.Unlock()
	bs.backendStateMux.Lock()

	return &backendState{
		latestBlockNumber:    bs.latestBlockNumber,
		latestBlockHash:      bs.latestBlockHash,
		safeBlockNumber:      bs.safeBlockNumber,
		localSafeBlockNumber: bs.localSafeBlockNumber,
		finalizedBlockNumber: bs.finalizedBlockNumber,
		peerCount:            bs.peerCount,
		inSync:               bs.inSync,
		lastUpdate:           bs.lastUpdate,
		bannedUntil:          bs.bannedUntil,
	}
}

func (cp *ConsensusPoller) GetLastUpdate(be *Backend) time.Time {
	bs := cp.backendState[be]
	defer bs.backendStateMux.Unlock()
	bs.backendStateMux.Lock()
	return bs.lastUpdate
}

// ELBlockState holds the block numbers and hashes fetched from an EL backend in a single polling cycle.
type ELBlockState struct {
	LatestBlockNumber    hexutil.Uint64
	LatestBlockHash      string
	SafeBlockNumber      hexutil.Uint64
	FinalizedBlockNumber hexutil.Uint64
}

// backendStateUpdate is a value object passed to setBackendState to avoid
// a wide positional parameter list of same-typed arguments.
type backendStateUpdate struct {
	peerCount            uint64
	inSync               bool
	latestBlockNumber    hexutil.Uint64
	latestBlockHash      string
	safeBlockNumber      hexutil.Uint64
	finalizedBlockNumber hexutil.Uint64
}

func (cp *ConsensusPoller) setBackendState(be *Backend, upd backendStateUpdate) bool {
	bs := cp.backendState[be]
	bs.backendStateMux.Lock()
	changed := bs.latestBlockHash != upd.latestBlockHash
	bs.peerCount = upd.peerCount
	bs.inSync = upd.inSync
	bs.latestBlockNumber = upd.latestBlockNumber
	bs.latestBlockHash = upd.latestBlockHash
	bs.safeBlockNumber = upd.safeBlockNumber
	bs.finalizedBlockNumber = upd.finalizedBlockNumber
	bs.lastUpdate = time.Now()
	bs.backendStateMux.Unlock()
	return changed
}

// getConsensusCandidates will search for candidates in the primary group,
// if there are none it will search for candidates in he fallback group
func (cp *ConsensusPoller) getConsensusCandidates() map[*Backend]*backendState {

	healthyPrimaries := cp.FilterCandidates(cp.backendGroup.Primaries())

	RecordHealthyCandidates(cp.backendGroup, len(healthyPrimaries))
	if len(healthyPrimaries) > 0 {
		return healthyPrimaries
	}

	return cp.FilterCandidates(cp.backendGroup.Fallbacks())
}

// filterCandidates find out what backends are the candidates to be in the consensus group
// and create a copy of current their state
//
// a candidate is a serving node within the following conditions:
//   - not banned
//   - healthy (network latency and error rate)
//   - with minimum peer count
//   - in sync
//   - updated recently
//   - not lagging latest block
//   - (CL mode) finalized and local_safe blocks are at or above the current group consensus,
//     preventing a restarting backend from dragging consensus backward
func (cp *ConsensusPoller) FilterCandidates(backends []*Backend) map[*Backend]*backendState {

	candidates := make(map[*Backend]*backendState, len(cp.backendGroup.Backends))

	var consensusFinalized, consensusLocalSafe hexutil.Uint64
	if cp.consensusLayer {
		consensusFinalized = cp.GetFinalizedBlockNumber()
		consensusLocalSafe = cp.GetLocalSafeBlockNumber()
	}

	for _, be := range backends {

		bs := cp.GetBackendState(be)
		if be.forcedCandidate {
			candidates[be] = bs
			continue
		}
		if bs.IsBanned() {
			continue
		}
		if !be.IsHealthy() {
			continue
		}
		if !be.skipPeerCountCheck && bs.peerCount < cp.minPeerCount {
			log.Debug("backend peer count too low for inclusion in consensus",
				"backend_name", be.Name,
				"peer_count", bs.peerCount,
				"min_peer_count", cp.minPeerCount,
			)
			continue
		}
		if !be.skipIsSyncingCheck && !bs.inSync {
			continue
		}
		if cp.consensusLayer {
			if consensusFinalized > 0 && bs.finalizedBlockNumber < consensusFinalized {
				log.Warn("backend excluded: finalized block below consensus",
					"backend_name", be.Name,
					"backend_finalized", bs.finalizedBlockNumber,
					"consensus_finalized", consensusFinalized,
				)
				continue
			}
			if consensusLocalSafe > 0 && bs.localSafeBlockNumber < consensusLocalSafe {
				log.Warn("backend excluded: local_safe block below consensus",
					"backend_name", be.Name,
					"backend_local_safe", bs.localSafeBlockNumber,
					"consensus_local_safe", consensusLocalSafe,
				)
				continue
			}
		}
		if bs.lastUpdate.Add(cp.maxUpdateThreshold).Before(time.Now()) {
			continue
		}

		candidates[be] = bs
	}

	// find the highest block, in order to use it defining the highest non-lagging ancestor block
	var highestLatestBlock hexutil.Uint64
	for _, bs := range candidates {
		if bs.latestBlockNumber > highestLatestBlock {
			highestLatestBlock = bs.latestBlockNumber
		}
	}

	// find the highest common ancestor block
	lagging := make([]*Backend, 0, len(candidates))
	for be, bs := range candidates {
		// check if backend is lagging behind the highest block
		if uint64(highestLatestBlock-bs.latestBlockNumber) > cp.maxBlockLag {
			lagging = append(lagging, be)
		}
	}

	// remove lagging backends from the candidates
	for _, be := range lagging {
		delete(candidates, be)
	}

	return candidates
}
