package proxyd

// CL (op-node / consensus layer) consensus support.
// All op-node-specific types, RPC methods, fetchers, and option functions live here.
// consensus_poller.go contains only shared infrastructure and the EL-specific paths.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/log"
)

// clSyncStatus holds the parsed result of an optimism_syncStatus RPC call.
type clSyncStatus struct {
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
// An odd number of backends is required (enforced at initialization) so that
// a majority is always unambiguous.
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

// WithCLOutputRootBanThreshold sets the number of consecutive per-cycle
// optimism_outputAtBlock timeouts before a CL backend is banned.
func WithCLOutputRootBanThreshold(threshold uint) ConsensusOpt {
	return func(cp *ConsensusPoller) {
		cp.clOutputRootBanThreshold = threshold
	}
}

// updateCLBackend fetches the op-node sync status for a single backend and
// determines whether it is considered in sync. It encapsulates the CL branch of
// UpdateBackend, keeping all op-node logic out of the shared poller file.
//
// On success it returns the parsed sync status, the raw JSON body, the inSync
// determination, and nil. On error it logs and returns a non-nil error; the
// caller should skip state updates.
func (cp *ConsensusPoller) updateCLBackend(ctx context.Context, be *Backend) (*clSyncStatus, json.RawMessage, bool, error) {
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
	RecordCLBackendCurrentL1(be, syncStatus.CurrentL1Number)

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
		RecordCLBanInteropSafeGtLocalSafe(be)
		cp.Ban(be)
		return false
	}
	return true
}

// GetConsensusSyncStatusBody returns the cached optimism_syncStatus response body
// from the current pin backend. Returns nil if no poll cycle has completed yet.
// When Redis HA is configured, this reads from the shared Redis cache so all
// pods serve the same response regardless of which pod the LB routes to.
func (cp *ConsensusPoller) GetConsensusSyncStatusBody() json.RawMessage {
	body, _ := cp.tracker.GetCLSyncBody()
	return body
}

// selectConsensusSyncStatusBody selects the consensus-group backend with the lowest
// current_l1.number (subject to a monotonicity floor) and caches its full
// optimism_syncStatus response body via the tracker. This ensures the served response
// is internally consistent — all fields come from one backend snapshot, not a mix of
// backends. When Redis HA is configured, the tracker propagates the body to Redis so
// all pods serve the same response.
func (cp *ConsensusPoller) selectConsensusSyncStatusBody(consensusGroup []*Backend) {
	type pinCandidate struct {
		be   *Backend
		l1   uint64
		body json.RawMessage
	}
	var pin *pinCandidate
	lowestL1 := uint64(math.MaxUint64)

	_, floor := cp.tracker.GetCLSyncBody()

	for _, be := range consensusGroup {
		bs := cp.backendState[be]
		bs.backendStateMux.Lock()
		l1Num := bs.currentL1Number
		body := bs.syncStatusRaw
		bs.backendStateMux.Unlock()

		if len(body) > 0 && l1Num >= floor && l1Num < lowestL1 {
			lowestL1 = l1Num
			pin = &pinCandidate{be: be, l1: l1Num, body: body}
		}
	}

	if pin == nil {
		RecordCLNoPinCandidate(cp.backendGroup)
		log.Warn("CL pin selection: no valid candidate in consensus group",
			"floor", floor,
			"group_size", len(consensusGroup),
		)
		return
	}

	cp.tracker.SetCLSyncBody(pin.body, pin.l1)

	RecordCLGroupPinL1(cp.backendGroup, pin.be, pin.l1)
	log.Info("CL pin backend selected",
		"backend", pin.be.Name,
		"current_l1_number", pin.l1,
		"floor", floor,
	)
}

// fetchCLSyncStatus calls optimism_syncStatus and parses the full sync status
// for a CL (op-node) backend. It returns both the parsed status and the raw JSON
// result body so the caller can cache it for serving.
func (cp *ConsensusPoller) fetchCLSyncStatus(ctx context.Context, be *Backend) (*clSyncStatus, json.RawMessage, error) {
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

	return &clSyncStatus{
		LatestBlockNumber:    hexutil.Uint64(latestBlockNumber),
		LatestBlockHash:      latestBlockHash,
		SafeBlockNumber:      hexutil.Uint64(safeBlockNumber),
		LocalSafeBlockNumber: hexutil.Uint64(localSafeBlockNumber),
		FinalizedBlockNumber: hexutil.Uint64(finalizedBlockNumber),
		CurrentL1Number:      currentL1Number,
		HeadL1Number:         headL1Number,
		HeadL1Timestamp:      headL1Timestamp,
	}, rawBody, nil
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
// With an odd number of backends (enforced at init), the majority is always unambiguous:
//   - 2v1: the minority backend is banned.
//   - All backends error (outputAtBlock unsupported): graceful degradation,
//     candidates returned unchanged.
//
// Returns the filtered candidates map and recomputed lowestSafeBlock / lowestLocalSafeBlock.
//
// clOutputRootTimeouts mutations are protected by backendStateMux, which synchronizes
// with concurrent Ban/Unban and UpdateBackend callers.
func (cp *ConsensusPoller) verifyCLOutputRoots(
	ctx context.Context,
	candidates map[*Backend]*backendState,
	safeBlock hexutil.Uint64,
) (map[*Backend]*backendState, hexutil.Uint64, hexutil.Uint64) {
	type result struct {
		be         *Backend
		outputRoot string
		err        error
		timedOut   bool
	}

	results := make([]result, 0, len(candidates))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for be := range candidates {
		wg.Add(1)
		go func() {
			defer wg.Done()
			root, err := cp.fetchCLOutputRoot(ctx, be, safeBlock)
			timedOut := errors.Is(err, context.DeadlineExceeded)
			mu.Lock()
			results = append(results, result{be: be, outputRoot: root, err: err, timedOut: timedOut})
			mu.Unlock()
		}()
	}
	wg.Wait()

	// Process timeouts first. Consecutive timeouts accumulate toward a ban threshold;
	// a single timeout is tolerated to avoid banning on transient slowness.
	// This runs unconditionally so the counter advances even when no majority is established.
	// Access cp.backendState directly (under lock) so the counter persists across cycles;
	// candidates holds copies from GetBackendState and does not carry clOutputRootTimeouts.
	for _, r := range results {
		if !r.timedOut {
			continue
		}
		bs := cp.backendState[r.be]
		bs.backendStateMux.Lock()
		bs.clOutputRootTimeouts++
		timeouts := bs.clOutputRootTimeouts
		bs.backendStateMux.Unlock()
		if timeouts >= cp.clOutputRootBanThreshold {
			log.Error("banning CL backend: repeated optimism_outputAtBlock timeouts",
				"backend", r.be.Name,
				"consecutive_timeouts", timeouts,
				"threshold", cp.clOutputRootBanThreshold,
				"safe_block", safeBlock,
			)
			RecordCLBanOutputRootTimeout(r.be)
			cp.Ban(r.be)
			delete(candidates, r.be)
		} else {
			log.Warn("CL output root fetch timed out, tolerating until threshold",
				"backend", r.be.Name,
				"consecutive_timeouts", timeouts,
				"threshold", cp.clOutputRootBanThreshold,
				"safe_block", safeBlock,
			)
		}
	}

	// Count successful responses per output root and reset the timeout counter for any
	// backend that responded successfully. The counter tracks consecutive fetch failures;
	// any successful response proves the backend is responsive regardless of majority.
	counts := make(map[string]int)
	for _, r := range results {
		if r.err == nil {
			counts[r.outputRoot]++
			bs := cp.backendState[r.be]
			bs.backendStateMux.Lock()
			bs.clOutputRootTimeouts = 0
			bs.backendStateMux.Unlock()
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

	// candidates holds *backendState values that are copies produced by GetBackendState,
	// not live pointers into cp.backendState. Reading them here without holding
	// backendStateMux is safe — they are point-in-time snapshots. Any banned backend
	// was already removed from candidates by delete() above, so stale entries are not
	// a concern.
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
		//   - every backend returned a unique root (all-disagree, no quorum).
		// Don't ban anyone — we can't determine which backend is correct.
		if len(counts) > 1 {
			backendNames := make([]string, 0, len(results))
			for _, r := range results {
				if r.err == nil {
					backendNames = append(backendNames, r.be.Name)
					RecordCLOutputRootDisagreement(r.be)
				}
			}
			log.Error("CL output root disagreement detected but no majority — cannot determine correct root",
				"safe_block", safeBlock,
				"distinct_roots", len(counts),
				"backends", backendNames,
			)
		}
		lowestSafe, lowestLocalSafe := lowestFromCandidates()
		return candidates, lowestSafe, lowestLocalSafe
	}

	log.Info("CL output root verification",
		"agreed_root", agreedRoot,
		"safe_block", safeBlock,
		"agreeing_backends", maxCount,
		"total_responding", len(counts),
	)

	for _, r := range results {
		if r.err != nil {
			if !r.timedOut {
				log.Warn("error fetching CL output root, skipping verification for backend",
					"backend", r.be.Name,
					"safe_block", safeBlock,
					"err", r.err,
				)
			}
			// timedOut already handled above
			continue
		}
		if r.outputRoot != agreedRoot {
			log.Error("banning CL backend: output root disagrees with consensus majority",
				"backend", r.be.Name,
				"backend_root", r.outputRoot,
				"agreed_root", agreedRoot,
				"safe_block", safeBlock,
			)
			RecordCLBanOutputRootMismatch(r.be)
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
