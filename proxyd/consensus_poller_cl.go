package proxyd

// CL (op-node / consensus layer) consensus support.
// All op-node-specific types, RPC methods, fetchers, and option functions live here.
// consensus_poller.go contains only shared infrastructure and the EL-specific paths.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/log"
)

// CLSyncStatus holds the parsed result of an optimism_syncStatus RPC call.
type CLSyncStatus struct {
	LatestBlockNumber    hexutil.Uint64
	LatestBlockHash      string
	SafeBlockNumber      hexutil.Uint64
	LocalSafeBlockNumber hexutil.Uint64
	FinalizedBlockNumber hexutil.Uint64
	CurrentL1Number      uint64
	HeadL1Number         uint64
	HeadL1Timestamp      uint64 // Unix seconds; used to detect L1 connection staleness
}

// WithCLConsensusMode enables CL (op-node / consensus layer) consensus mode.
//
// In this mode the poller queries optimism_syncStatus and optimism_outputAtBlock
// instead of eth_getBlockByNumber, and serves optimism_syncStatus responses from
// a cached pin backend (the consensus group member with the lowest current_l1
// derivation position) rather than rewriting individual fields.
//
// Output root verification: each poll cycle the poller calls
// optimism_outputAtBlock at the consensus safe block on every candidate backend
// and bans any backend whose output root disagrees with the majority. This
// catches derivation divergences between heterogeneous CL clients (e.g. op-node
// vs kona-node) before incorrect data reaches consumers.
//
// Deployment recommendation: use an odd number of backends (e.g. 3, 5) so that
// a majority is always unambiguous. With an even number of backends, a perfect
// split in output roots cannot be resolved automatically — disagreement is
// detected and logged but no backend is evicted until the tie is broken.
func WithCLConsensusMode() ConsensusOpt {
	return func(cp *ConsensusPoller) {
		cp.consensusLayer = true
	}
}

// WithCLSyncThreshold sets the maximum tolerated L1 block lag before a CL backend
// is considered out of sync.
func WithCLSyncThreshold(threshold uint64) ConsensusOpt {
	return func(cp *ConsensusPoller) {
		cp.clSyncThreshold = threshold
	}
}

// WithCLHeadL1MaxAge sets the maximum age of the L1 head timestamp before a CL
// backend is considered stale.
func WithCLHeadL1MaxAge(maxAge time.Duration) ConsensusOpt {
	return func(cp *ConsensusPoller) {
		cp.clHeadL1MaxAge = maxAge
	}
}

// WithCLSafeLeapWarnThreshold sets the number of blocks by which a backend's safe_l2
// may exceed the peer minimum before a warning is logged and a metric recorded.
// This detects the op-node premature finalization bug (https://github.com/ethereum-optimism/optimism/issues/17631)
// where a node finishing EL sync incorrectly reports safe == finalized == unsafe == EL sync tip.
// The minimum-safe consensus logic already protects the served value; this adds observability.
func WithCLSafeLeapWarnThreshold(threshold uint64) ConsensusOpt {
	return func(cp *ConsensusPoller) {
		cp.clSafeLeapWarnThreshold = threshold
	}
}

// logCLConfigWarnings logs startup warnings for CL-mode configuration issues.
// Called once from NewConsensusPoller after all options have been applied.
func (cp *ConsensusPoller) logCLConfigWarnings() {
	n := len(cp.backendGroup.Backends)
	if n%2 == 0 {
		log.Warn("CL consensus: backend group has an even number of backends — output root verification requires a majority (≥2 agreeing backends) to evict a diverging backend; with an even-sized group a tie cannot be resolved automatically. Add one backend to ensure unambiguous majority.",
			"backend_count", n,
		)
	}
}

// updateCLBackend fetches the op-node sync status for a single backend and
// determines whether it is considered in sync. It encapsulates the CL branch of
// UpdateBackend, keeping all op-node logic out of the shared poller file.
//
// On success it returns the parsed sync status, the raw JSON body, the inSync
// determination, and nil. On error it logs and returns a non-nil error; the
// caller should skip state updates.
func (cp *ConsensusPoller) updateCLBackend(ctx context.Context, be *Backend) (*CLSyncStatus, json.RawMessage, bool, error) {
	syncStatus, rawBody, err := cp.fetchCLSyncStatus(ctx, be)
	if err != nil {
		log.Warn("error updating CL backend - backend will not be updated", "name", be.Name, "err", err)
		return nil, nil, false, err
	}

	lag := uint64(0)
	if syncStatus.HeadL1Number > syncStatus.CurrentL1Number {
		lag = syncStatus.HeadL1Number - syncStatus.CurrentL1Number
	}
	l1BlockLagOK := lag <= cp.clSyncThreshold
	if !l1BlockLagOK {
		log.Warn("CL backend L1 block lag too high",
			"backend", be.Name,
			"lag", lag,
			"threshold", cp.clSyncThreshold,
			"current_l1", syncStatus.CurrentL1Number,
			"head_l1", syncStatus.HeadL1Number,
		)
	}
	RecordCLBackendL1Lag(be, lag)

	l1TimestampOK := true
	if syncStatus.HeadL1Timestamp == 0 {
		// timestamp=0 means the node hasn't synced to L1 yet (e.g. initializing after restart)
		l1TimestampOK = false
		log.Warn("CL backend L1 head timestamp is zero — node is initializing",
			"backend", be.Name,
		)
	} else if cp.clHeadL1MaxAge > 0 {
		l1Age := time.Since(time.Unix(int64(syncStatus.HeadL1Timestamp), 0))
		l1TimestampOK = l1Age <= cp.clHeadL1MaxAge
		if !l1TimestampOK {
			log.Warn("CL backend L1 head is stale",
				"backend", be.Name,
				"head_l1_age", l1Age,
				"max_age", cp.clHeadL1MaxAge,
			)
		}
	}
	RecordCLBackendL1Stale(be, !l1TimestampOK)

	inSync := l1BlockLagOK && l1TimestampOK
	RecordConsensusBackendInSync(be, inSync)

	if syncStatus.LatestBlockNumber == 0 {
		log.Warn("error backend responded a 200 with blockheight 0 for latest block", "name", be.Name)
		be.intermittentErrorsSlidingWindow.Incr()
		return nil, nil, false, fmt.Errorf("latest block is 0 for backend %s", be.Name)
	}
	if syncStatus.SafeBlockNumber == 0 {
		log.Warn("error backend responded a 200 with blockheight 0 for safe block", "name", be.Name)
		be.intermittentErrorsSlidingWindow.Incr()
		return nil, nil, false, fmt.Errorf("safe block is 0 for backend %s", be.Name)
	}
	if syncStatus.FinalizedBlockNumber == 0 {
		log.Warn("error backend responded a 200 with blockheight 0 for finalized block", "name", be.Name)
		be.intermittentErrorsSlidingWindow.Incr()
		return nil, nil, false, fmt.Errorf("finalized block is 0 for backend %s", be.Name)
	}
	if syncStatus.LocalSafeBlockNumber == 0 {
		log.Warn("error backend responded with blockheight 0 for local_safe block", "name", be.Name)
		be.intermittentErrorsSlidingWindow.Incr()
		return nil, nil, false, fmt.Errorf("local_safe block is 0 for backend %s", be.Name)
	}

	return syncStatus, rawBody, inSync, nil
}

// validateCLBackendUpdate performs CL-specific post-fetch validation before a backend's
// state is written. Returns false if the backend should be excluded and the caller should
// return early.
func (cp *ConsensusPoller) validateCLBackendUpdate(be *Backend, safeBlockNumber, localSafeBlockNumber hexutil.Uint64) bool {
	// On interop chains, cross-safe (safe_l2) always lags behind or equals local-safe.
	// A backend reporting safe > local_safe is in an invalid state and must be excluded.
	if safeBlockNumber > 0 && safeBlockNumber > localSafeBlockNumber {
		log.Warn("banning CL backend: safe > local_safe (invalid interop state)",
			"backend", be.Name,
			"safe", safeBlockNumber,
			"local_safe", localSafeBlockNumber,
		)
		RecordCLBan(be, "interop_safe_gt_local_safe")
		cp.Ban(be)
		return false
	}
	return true
}


// warnCLSafeLeap records a metric and logs a warning for any candidate whose safe_l2
// is more than clSafeLeapWarnThreshold blocks ahead of the peer minimum.
// This detects the op-node premature finalization bug (https://github.com/ethereum-optimism/optimism/issues/17631)
// where a node finishing EL sync incorrectly reports safe == finalized == unsafe == EL sync tip.
// The minimum-safe consensus logic already protects the served value; this is observability only.
func (cp *ConsensusPoller) warnCLSafeLeap(candidates map[*Backend]*backendState, lowestSafeBlock hexutil.Uint64) {
	if cp.clSafeLeapWarnThreshold == 0 || lowestSafeBlock == 0 {
		return
	}
	for be, bs := range candidates {
		leap := uint64(bs.safeBlockNumber) - uint64(lowestSafeBlock)
		RecordCLBackendSafeLeap(be, leap)
		if leap > cp.clSafeLeapWarnThreshold {
			RecordCLBackendSafeLeapWarning(be)
			log.Warn("CL backend safe head far ahead of peer minimum — possible premature finalization",
				"backend", be.Name,
				"safe", bs.safeBlockNumber,
				"peer_min_safe", lowestSafeBlock,
				"leap", leap,
				"threshold", cp.clSafeLeapWarnThreshold,
			)
		}
	}
}

// GetConsensusSyncStatusBody returns the cached optimism_syncStatus response body
// from the current pin backend. Returns nil if no poll cycle has completed yet.
func (cp *ConsensusPoller) GetConsensusSyncStatusBody() json.RawMessage {
	cp.syncStatusBodyMu.RLock()
	defer cp.syncStatusBodyMu.RUnlock()
	return cp.consensusSyncBody
}

// selectConsensusSyncStatusBody selects the consensus-group backend with the lowest
// current_l1.number (subject to a monotonicity floor) and caches its full
// optimism_syncStatus response body. This ensures the served response is internally
// consistent — all fields come from one backend snapshot, not a mix of backends.
func (cp *ConsensusPoller) selectConsensusSyncStatusBody(consensusGroup []*Backend) {
	var pinBackend *Backend
	var lowestL1 uint64 = math.MaxUint64

	cp.syncStatusBodyMu.RLock()
	floor := cp.lastServedCLL1Num
	cp.syncStatusBodyMu.RUnlock()

	for _, be := range consensusGroup {
		bs := cp.backendState[be]
		bs.backendStateMux.Lock()
		l1Num := bs.currentL1Number
		hasBody := len(bs.syncStatusRaw) > 0
		bs.backendStateMux.Unlock()

		if hasBody && l1Num >= floor && l1Num < lowestL1 {
			lowestL1 = l1Num
			pinBackend = be
		}
	}

	if pinBackend == nil {
		return // no valid pin candidate; keep existing body
	}

	bs := cp.backendState[pinBackend]
	bs.backendStateMux.Lock()
	body := bs.syncStatusRaw
	bs.backendStateMux.Unlock()

	cp.syncStatusBodyMu.Lock()
	cp.consensusSyncBody = body
	cp.lastServedCLL1Num = lowestL1
	cp.syncStatusBodyMu.Unlock()
}

// fetchCLSyncStatus calls optimism_syncStatus and parses the full sync status
// for a CL (op-node) backend. It returns both the parsed status and the raw JSON
// result body so the caller can cache it for serving.
func (cp *ConsensusPoller) fetchCLSyncStatus(ctx context.Context, be *Backend) (*CLSyncStatus, json.RawMessage, error) {
	var rpcRes RPCRes
	log.Trace("executing fetchCLSyncStatus for backend",
		"backend", be.Name,
	)
	if err := be.ForwardRPC(ctx, &rpcRes, "67", "optimism_syncStatus"); err != nil {
		return nil, nil, err
	}

	syncStatusResult, ok := rpcRes.Result.(map[string]interface{})
	log.Trace("syncStatus response for backend",
		"backend", be.Name,
		"syncStatus", syncStatusResult,
	)
	if !ok {
		return nil, nil, fmt.Errorf("unexpected response to optimism_syncStatus on backend %s", be.Name)
	}

	rawBody, err := json.Marshal(syncStatusResult)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal syncStatus for backend %s: %w", be.Name, err)
	}

	latestBlockNumber, latestBlockHash, err := parseCLSyncStatusBlock(syncStatusResult, "unsafe_l2")
	if err != nil {
		return nil, nil, err
	}

	safeBlockNumber, _, err := parseCLSyncStatusBlock(syncStatusResult, "safe_l2")
	if err != nil {
		return nil, nil, err
	}

	localSafeBlockNumber, _, err := parseCLSyncStatusBlock(syncStatusResult, "local_safe_l2")
	if err != nil {
		return nil, nil, err
	}

	finalizedBlockNumber, _, err := parseCLSyncStatusBlock(syncStatusResult, "finalized_l2")
	if err != nil {
		return nil, nil, err
	}

	currentL1Number, _, err := parseCLSyncStatusBlock(syncStatusResult, "current_l1")
	if err != nil {
		return nil, nil, err
	}

	headL1Number, _, err := parseCLSyncStatusBlock(syncStatusResult, "head_l1")
	if err != nil {
		return nil, nil, err
	}

	headL1Timestamp, err := parseCLBlockTimestamp(syncStatusResult, "head_l1")
	if err != nil {
		return nil, nil, err
	}

	return &CLSyncStatus{
		LatestBlockNumber:    hexutil.Uint64(latestBlockNumber),
		LatestBlockHash:      latestBlockHash,
		SafeBlockNumber:      hexutil.Uint64(safeBlockNumber),
		LocalSafeBlockNumber: hexutil.Uint64(localSafeBlockNumber),
		FinalizedBlockNumber: hexutil.Uint64(finalizedBlockNumber),
		CurrentL1Number:      currentL1Number,
		HeadL1Number:         headL1Number,
		HeadL1Timestamp:      headL1Timestamp,
	}, json.RawMessage(rawBody), nil
}

// verifyCLOutputRoots calls optimism_outputAtBlock(safeBlock) on every candidate
// and bans any backend whose outputRoot disagrees with the majority.
//
// The outputRoot is a cryptographic commitment to the full L2 state at a block:
//
//	keccak256(version || stateRoot || withdrawalStorageRoot || l2BlockHash)
//
// If two CL clients (e.g. op-node and kona-node) derive different state from
// the same L1 data, their output roots will differ at the same safe block number.
// This check catches such derivation divergences before incorrect data is served.
//
// Majority semantics: a backend is banned only when the agreed root has ≥2 votes.
// This means:
//   - 2 backends disagree (1v1 tie): no ban — cannot determine which is correct.
//     A warning is logged; operators should investigate.
//   - 3+ backends, one disagrees (e.g. 2v1): the minority backend is banned.
//   - All backends error (outputAtBlock unsupported): graceful degradation,
//     candidates returned unchanged.
//
// Deployment note: use an odd number of backends so ties cannot occur.
// Returns the filtered candidates map and recomputed lowestSafeBlock / lowestLocalSafeBlock.
func (cp *ConsensusPoller) verifyCLOutputRoots(
	ctx context.Context,
	candidates map[*Backend]*backendState,
	safeBlock hexutil.Uint64,
) (map[*Backend]*backendState, hexutil.Uint64, hexutil.Uint64) {
	type result struct {
		be         *Backend
		outputRoot string
		err        error
	}

	results := make([]result, 0, len(candidates))
	for be := range candidates {
		root, err := cp.fetchCLOutputRoot(ctx, be, safeBlock)
		results = append(results, result{be: be, outputRoot: root, err: err})
	}

	// Count successful responses per output root.
	counts := make(map[string]int)
	for _, r := range results {
		if r.err == nil {
			counts[r.outputRoot]++
		}
	}

	// Find the most common root.
	var agreedRoot string
	var maxCount int
	for root, count := range counts {
		if count > maxCount {
			maxCount = count
			agreedRoot = root
		}
	}

	lowestFromCandidates := func() (hexutil.Uint64, hexutil.Uint64) {
		var lowestSafe, lowestLocalSafe hexutil.Uint64
		for _, bs := range candidates {
			if lowestSafe == 0 || bs.safeBlockNumber < lowestSafe {
				lowestSafe = bs.safeBlockNumber
			}
			if lowestLocalSafe == 0 || bs.localSafeBlockNumber < lowestLocalSafe {
				lowestLocalSafe = bs.localSafeBlockNumber
			}
		}
		return lowestSafe, lowestLocalSafe
	}

	if maxCount < 2 {
		// Cannot establish a clear majority:
		//   - all backends errored (maxCount == 0), or
		//   - every root has exactly 1 vote (2-backend tie or all-unique).
		// Don't ban anyone — we can't determine which backend is correct.
		// Log a warning when there is actual disagreement (distinct roots > 1).
		if len(counts) > 1 {
			log.Warn("CL output root disagreement detected but no majority — cannot determine correct root (≥3 agreeing backends required to ban)",
				"safe_block", safeBlock,
				"distinct_roots", len(counts),
			)
		}
		lowestSafe, lowestLocalSafe := lowestFromCandidates()
		return candidates, lowestSafe, lowestLocalSafe
	}

	for _, r := range results {
		if r.err != nil {
			log.Warn("error fetching CL output root, skipping verification for backend",
				"backend", r.be.Name,
				"safe_block", safeBlock,
				"err", r.err,
			)
			continue
		}
		if r.outputRoot != agreedRoot {
			log.Warn("banning CL backend: output root disagrees with consensus",
				"backend", r.be.Name,
				"backend_root", r.outputRoot,
				"agreed_root", agreedRoot,
				"safe_block", safeBlock,
			)
			RecordCLBan(r.be, "output_root_mismatch")
			cp.Ban(r.be)
			delete(candidates, r.be)
		}
	}

	lowestSafe, lowestLocalSafe := lowestFromCandidates()
	return candidates, lowestSafe, lowestLocalSafe
}

// fetchCLOutputRoot calls optimism_outputAtBlock and returns the outputRoot hash.
func (cp *ConsensusPoller) fetchCLOutputRoot(ctx context.Context, be *Backend, block hexutil.Uint64) (string, error) {
	var rpcRes RPCRes
	if err := be.ForwardRPC(ctx, &rpcRes, "67", "optimism_outputAtBlock", block.String()); err != nil {
		return "", err
	}
	jsonMap, ok := rpcRes.Result.(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("unexpected response to optimism_outputAtBlock on backend %s", be.Name)
	}
	outputRoot, ok := jsonMap["outputRoot"].(string)
	if !ok {
		return "", fmt.Errorf("missing or invalid outputRoot in optimism_outputAtBlock response on backend %s", be.Name)
	}
	return outputRoot, nil
}

// fetchCLPeerCount calls opp2p_peerStats and returns the connected peer count.
func (cp *ConsensusPoller) fetchCLPeerCount(ctx context.Context, be *Backend) (count uint64, err error) {
	var rpcRes RPCRes
	// https://docs.optimism.io/operators/node-operators/json-rpc#opp2p_peerstats
	log.Trace("executing fetchCLPeerCount",
		"backend", be.Name,
	)
	err = be.ForwardRPC(ctx, &rpcRes, "67", "opp2p_peerStats")
	if err != nil {
		return 0, err
	}

	jsonMap, ok := rpcRes.Result.(map[string]interface{})
	if !ok {
		return 0, fmt.Errorf("unexpected response to opp2p_peerStats on backend %s", be.Name)
	}
	connectedFloat, ok := jsonMap["connected"].(float64)
	if !ok {
		return 0, fmt.Errorf("missing or invalid 'connected' field in opp2p_peerStats response from backend %s", be.Name)
	}
	count = uint64(connectedFloat)

	log.Trace("fetchCLPeerCount result",
		"backend", be.Name,
		"result", jsonMap,
		"count", count,
	)

	return count, nil
}

// parseCLBlockTimestamp extracts the Unix timestamp from a block ref field in an
// optimism_syncStatus response.
func parseCLBlockTimestamp(jsonMap map[string]interface{}, key string) (uint64, error) {
	blockMap, ok := jsonMap[key].(map[string]interface{})
	if !ok {
		return 0, fmt.Errorf("unexpected type for %s in optimism_syncStatus", key)
	}
	tsVal, ok := blockMap["timestamp"].(float64)
	if !ok {
		return 0, fmt.Errorf("missing or invalid timestamp in %s", key)
	}
	return uint64(tsVal), nil
}

// parseCLSyncStatusBlock parses the block number and hash from a named field
// (e.g. "unsafe_l2", "safe_l2") within an optimism_syncStatus response map.
func parseCLSyncStatusBlock(jsonMap map[string]interface{}, safety string) (blockNumber uint64, blockHash string, err error) {
	safetyMap, ok := jsonMap[safety].(map[string]interface{})
	if !ok {
		return 0, "", fmt.Errorf("unexpected unmarshall to optimism_syncStatus on consensus layer backend safety %s", safety)
	}
	log.Trace("safetyMap",
		"safetyMap", safetyMap,
	)

	numberVal, nOk := safetyMap["number"].(float64)
	hashVal, hOk := safetyMap["hash"].(string)
	if !nOk || !hOk {
		return 0, "", fmt.Errorf("missing or invalid 'number' or 'hash' field in %s block", safety)
	}
	blockNumber = uint64(numberVal)
	blockHash = hashVal

	return blockNumber, blockHash, nil
}
