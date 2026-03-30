package proxyd

// CL (op-node / consensus layer) consensus support.
// All op-node-specific types, RPC methods, fetchers, and option functions live here.
// consensus_poller.go contains only shared infrastructure and the EL-specific paths.

import (
	"context"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/log"
)

// CLSyncStatus holds the parsed result of an optimism_syncStatus RPC call.
type CLSyncStatus struct {
	LatestBlockNumber    hexutil.Uint64
	LatestBlockHash      string
	SafeBlockNumber      hexutil.Uint64
	SafeBlockHash        string
	LocalSafeBlockNumber hexutil.Uint64
	LocalSafeBlockHash   string
	FinalizedBlockNumber hexutil.Uint64
	FinalizedBlockHash   string
	CurrentL1Number      uint64
	HeadL1Number         uint64
	HeadL1Timestamp      uint64 // Unix seconds; used to detect L1 connection staleness
}

// WithCLConsensusMode enables CL (op-node) consensus mode.
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

// updateCLBackend fetches the op-node sync status for a single backend and
// determines whether it is considered in sync. It encapsulates the CL branch of
// UpdateBackend, keeping all op-node logic out of the shared poller file.
//
// On success it returns the parsed sync status, the inSync determination, and nil.
// On error it logs and returns a non-nil error; the caller should skip state updates.
func (cp *ConsensusPoller) updateCLBackend(ctx context.Context, be *Backend) (*CLSyncStatus, bool, error) {
	syncStatus, err := cp.fetchCLSyncStatus(ctx, be)
	if err != nil {
		log.Warn("error updating CL backend - backend will not be updated", "name", be.Name, "err", err)
		return nil, false, err
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

	return syncStatus, inSync, nil
}

// validateCLBackendUpdate performs CL-specific post-fetch validation before a backend's
// state is written. Returns false if the backend should be excluded and the caller should
// return early.
func (cp *ConsensusPoller) validateCLBackendUpdate(be *Backend, safeBlockNumber, localSafeBlockNumber hexutil.Uint64) bool {
	if localSafeBlockNumber == 0 {
		log.Warn("error backend responded with blockheight 0 for local_safe block", "name", be.Name)
		be.intermittentErrorsSlidingWindow.Incr()
		return false
	}
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

// computeCLGroupMinimums extracts CL-specific per-candidate minimums that are not
// tracked in the shared candidate loop: the finalized block hash (needed for CL
// hash-verified consensus) and the lowest local_safe_l2 block and hash.
func (cp *ConsensusPoller) computeCLGroupMinimums(
	candidates map[*Backend]*backendState,
	lowestFinalizedBlock hexutil.Uint64,
) (finalizedBlockHash string, lowestLocalSafeBlock hexutil.Uint64, lowestLocalSafeBlockHash string) {
	for _, bs := range candidates {
		if bs.finalizedBlockNumber == lowestFinalizedBlock && finalizedBlockHash == "" {
			finalizedBlockHash = bs.finalizedBlockHash
		}
		if lowestLocalSafeBlock == 0 || bs.localSafeBlockNumber < lowestLocalSafeBlock {
			lowestLocalSafeBlock = bs.localSafeBlockNumber
			lowestLocalSafeBlockHash = bs.localSafeBlockHash
		}
	}
	return
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

// updateCLGroupConsensus runs a hash-verified walk-back for safe_l2 in CL mode.
// EL mode uses raw minimums; CL enforces L1-derived safety guarantees for safe.
// local_safe_l2 and finalized_l2 use their minimum block+hash directly (no walk-back needed:
// local_safe is L1-derived and finalized is immutable — hash divergence is a backend bug, not a fork).
//
// It returns the agreed (safeBlock, safeHash) after walk-back.
func (cp *ConsensusPoller) updateCLGroupConsensus(
	ctx context.Context,
	candidates map[*Backend]*backendState,
	lowestSafeBlock hexutil.Uint64, lowestSafeBlockHash string,
) (hexutil.Uint64, string) {
	if lowestSafeBlock > 0 {
		var safeBroken bool
		lowestSafeBlock, lowestSafeBlockHash, safeBroken = cp.findConsensusBlock(
			ctx, candidates, cp.GetSafeBlockNumber(),
			lowestSafeBlock, lowestSafeBlockHash, cp.safeBlockFetcher, "safe")
		if safeBroken {
			log.Warn("safe consensus broken",
				"currentConsensusSafeBlock", cp.GetSafeBlockNumber(),
				"proposedSafeBlock", lowestSafeBlock,
				"proposedSafeBlockHash", lowestSafeBlockHash)
			RecordCLGroupConsensusWalkback(cp.backendGroup, "safe")
		}
	}
	return lowestSafeBlock, lowestSafeBlockHash
}

// clBlockFetcher is a blockHashFetcher for CL unsafe blocks.
// Uses the cached latest hash when the backend is at exactly the proposed block
// to avoid an extra RPC call; otherwise calls optimism_outputAtBlock.
func (cp *ConsensusPoller) clBlockFetcher(ctx context.Context, be *Backend, bs *backendState, block hexutil.Uint64) (hexutil.Uint64, string, error) {
	if bs.latestBlockNumber == block {
		return bs.latestBlockNumber, bs.latestBlockHash, nil
	}
	return cp.fetchCLBlock(ctx, be, block.String())
}

// safeBlockFetcher is a blockHashFetcher for CL safe blocks.
// Uses the cached safe hash when the backend is at exactly the proposed block,
// otherwise calls optimism_outputAtBlock. All candidates have safeBlockNumber >=
// lowestSafeBlock >= proposedBlock at every walk-back step, so no abstain is needed.
func (cp *ConsensusPoller) safeBlockFetcher(ctx context.Context, be *Backend, bs *backendState, block hexutil.Uint64) (hexutil.Uint64, string, error) {
	if bs.safeBlockNumber == block {
		return bs.safeBlockNumber, bs.safeBlockHash, nil
	}
	return cp.fetchCLBlock(ctx, be, block.String())
}

// fetchCLBlock calls optimism_outputAtBlock and returns the block number and hash
// from the blockRef in the response.
func (cp *ConsensusPoller) fetchCLBlock(ctx context.Context, be *Backend, block string) (blockNumber hexutil.Uint64, blockHash string, err error) {
	var rpcRes RPCRes
	log.Trace("executing fetchCLBlock for backend",
		"backend", be.Name,
		"block", block,
	)
	err = be.ForwardRPC(ctx, &rpcRes, "67", "optimism_outputAtBlock", block)
	if err != nil {
		return 0, "", err
	}

	jsonMap, ok := rpcRes.Result.(map[string]interface{})
	if !ok {
		return 0, "", fmt.Errorf("unexpected response to optimism_outputAtBlock on backend %s", be.Name)
	}
	blockRef, ok := jsonMap["blockRef"].(map[string]interface{})
	if !ok {
		return 0, "", fmt.Errorf("missing blockRef in optimism_outputAtBlock response on backend %s", be.Name)
	}
	numberVal, nOk := blockRef["number"].(float64)
	hashVal, hOk := blockRef["hash"].(string)
	if !nOk || !hOk {
		return 0, "", fmt.Errorf("missing or invalid number/hash in blockRef on backend %s", be.Name)
	}
	return hexutil.Uint64(uint64(numberVal)), hashVal, nil
}

// fetchCLSyncStatus calls optimism_syncStatus and parses the full sync status
// for a CL (op-node) backend.
func (cp *ConsensusPoller) fetchCLSyncStatus(ctx context.Context, be *Backend) (clSyncStatus *CLSyncStatus, err error) {
	var rpcRes RPCRes
	log.Trace("executing fetchCLSyncStatus for backend",
		"backend", be.Name,
	)
	err = be.ForwardRPC(ctx, &rpcRes, "67", "optimism_syncStatus")
	if err != nil {
		return nil, err
	}

	syncStatusResult, ok := rpcRes.Result.(map[string]interface{})
	log.Trace("syncStatus response for backend",
		"backend", be.Name,
		"syncStatus", syncStatusResult,
	)
	if !ok {
		return nil, fmt.Errorf("unexpected response to optimism_syncStatus on backend %s", be.Name)
	}

	latestBlockNumber, latestBlockHash, err := parseCLSyncStatusBlock(syncStatusResult, "unsafe_l2")
	if err != nil {
		return nil, err
	}

	safeBlockNumber, safeBlockHash, err := parseCLSyncStatusBlock(syncStatusResult, "safe_l2")
	if err != nil {
		return nil, err
	}

	localSafeBlockNumber, localSafeBlockHash, err := parseCLSyncStatusBlock(syncStatusResult, "local_safe_l2")
	if err != nil {
		return nil, err
	}

	finalizedBlockNumber, finalizedBlockHash, err := parseCLSyncStatusBlock(syncStatusResult, "finalized_l2")
	if err != nil {
		return nil, err
	}

	currentL1Number, _, err := parseCLSyncStatusBlock(syncStatusResult, "current_l1")
	if err != nil {
		return nil, err
	}

	headL1Number, _, err := parseCLSyncStatusBlock(syncStatusResult, "head_l1")
	if err != nil {
		return nil, err
	}

	headL1Timestamp, err := parseCLBlockTimestamp(syncStatusResult, "head_l1")
	if err != nil {
		return nil, err
	}

	return &CLSyncStatus{
		LatestBlockNumber:    hexutil.Uint64(latestBlockNumber),
		LatestBlockHash:      latestBlockHash,
		SafeBlockNumber:      hexutil.Uint64(safeBlockNumber),
		SafeBlockHash:        safeBlockHash,
		LocalSafeBlockNumber: hexutil.Uint64(localSafeBlockNumber),
		LocalSafeBlockHash:   localSafeBlockHash,
		FinalizedBlockNumber: hexutil.Uint64(finalizedBlockNumber),
		FinalizedBlockHash:   finalizedBlockHash,
		CurrentL1Number:      currentL1Number,
		HeadL1Number:         headL1Number,
		HeadL1Timestamp:      headL1Timestamp,
	}, nil
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
