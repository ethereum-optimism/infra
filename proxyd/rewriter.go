package proxyd

import (
	"encoding/json"
	"errors"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
)

type RewriteContext struct {
	latest         hexutil.Uint64
	safe           hexutil.Uint64
	finalized      hexutil.Uint64
	maxBlockRange  uint64
	consensusMode  bool
	latestHash     string
	safeHash       string
	localSafe      hexutil.Uint64
	localSafeHash  string
	finalizedHash  string
	consensusLayer bool
}

type RewriteResult uint8

const (
	// RewriteNone means request should be forwarded as-is
	RewriteNone RewriteResult = iota

	// RewriteOverrideError means there was an error attempting to rewrite
	RewriteOverrideError

	// RewriteOverrideRequest means the modified request should be forwarded to the backend
	RewriteOverrideRequest

	// RewriteOverrideResponse means to skip calling the backend and serve the overridden response
	RewriteOverrideResponse
)

var (
	ErrRewriteBlockOutOfRange = errors.New("block is out of range")
	ErrRewriteRangeTooLarge   = errors.New("block range is too large")
)

// RewriteTags modifies the request and the response based on block tags
func RewriteTags(rctx RewriteContext, req *RPCReq, res *RPCRes) (RewriteResult, error) {
	rw, err := RewriteResponse(rctx, req, res)
	if rw == RewriteOverrideResponse {
		return rw, err
	}
	return RewriteRequest(rctx, req, res)
}

// RewriteResponse synthesizes responses from consensus state before the backend is called.
// This is EL-only: CL mode always needs the real backend response (for passthrough fields
// like current_l1 / head_l1) and rewrites specific fields post-fetch instead.
func RewriteResponse(rctx RewriteContext, req *RPCReq, res *RPCRes) (RewriteResult, error) {
	if rctx.consensusLayer {
		return RewriteNone, nil
	}
	switch req.Method {
	case "eth_blockNumber":
		res.Result = rctx.latest
		return RewriteOverrideResponse, nil
	}
	return RewriteNone, nil
}

// RewriteConsensusBackendResponse rewrites an actual backend response to enforce
// consensus values. It operates on the real backend response rather than synthesizing
// one pre-flight because optimism_syncStatus contains passthrough fields (current_l1,
// head_l1, timestamps) that the consensus poller does not track — those must come from
// the backend. Only the four L2 block fields are overwritten with consensus values.
//
// All four consensus hashes must be present before any field is written. A partial
// rewrite — unsafe_l2 consensus but raw safe_l2 — is more dangerous than a no-op:
// the client would see finality fields from a divergent or lagging backend.
// If any hash is missing the poller hasn't completed its first cycle; the response is
// passed through unchanged.
//
// On a malformed backend response (field missing or wrong type), res.Error is set to
// ErrBackendBadResponse and res.Result is cleared, so no raw data reaches the client.
func RewriteConsensusBackendResponse(rctx RewriteContext, req *RPCReq, res *RPCRes) {
	if !rctx.consensusLayer || rctx.latestHash == "" || rctx.safeHash == "" ||
		rctx.localSafeHash == "" || rctx.finalizedHash == "" ||
		res == nil || res.Error != nil || res.Result == nil {
		return
	}
	if req.Method != "optimism_syncStatus" {
		return
	}
	result, ok := res.Result.(map[string]interface{})
	if !ok {
		res.Result = nil
		res.Error = ErrBackendBadResponse
		return
	}
	fields := []struct {
		key    string
		number hexutil.Uint64
		hash   string
	}{
		{"unsafe_l2", rctx.latest, rctx.latestHash},
		{"safe_l2", rctx.safe, rctx.safeHash},
		{"local_safe_l2", rctx.localSafe, rctx.localSafeHash},
		{"finalized_l2", rctx.finalized, rctx.finalizedHash},
	}
	for _, f := range fields {
		block, ok := result[f.key].(map[string]interface{})
		if !ok {
			res.Result = nil
			res.Error = ErrBackendBadResponse
			return
		}
		block["number"] = float64(f.number)
		block["hash"] = f.hash
	}
}

// RewriteRequest modifies the request object to comply with the rewrite context
// before the method has been called at the backend
// it returns false if nothing was changed
func RewriteRequest(rctx RewriteContext, req *RPCReq, res *RPCRes) (RewriteResult, error) {
	switch req.Method {
	case "eth_getLogs",
		"eth_newFilter":
		return rewriteRange(rctx, req, res, 0)
	case "debug_getRawReceipts", "consensus_getReceipts":
		return rewriteParam(rctx, req, res, 0, true, false)
	case "eth_getBalance",
		"eth_getCode",
		"eth_getTransactionCount",
		"eth_call":
		return rewriteParam(rctx, req, res, 1, false, true)
	case "eth_getStorageAt",
		"eth_getProof":
		return rewriteParam(rctx, req, res, 2, false, true)
	case "eth_getBlockTransactionCountByNumber",
		"eth_getUncleCountByBlockNumber",
		"eth_getBlockByNumber",
		"eth_getTransactionByBlockNumberAndIndex",
		"eth_getUncleByBlockNumberAndIndex":
		return rewriteParam(rctx, req, res, 0, false, false)
	}
	return RewriteNone, nil
}

func rewriteParam(rctx RewriteContext, req *RPCReq, res *RPCRes, pos int, required bool, blockNrOrHash bool) (RewriteResult, error) {
	var p []interface{}
	err := json.Unmarshal(req.Params, &p)
	if err != nil {
		return RewriteOverrideError, err
	}

	// we assume latest if the param is missing,
	// and we don't rewrite if there is not enough params
	if len(p) == pos && !required {
		p = append(p, "latest")
	} else if len(p) <= pos {
		return RewriteNone, nil
	}

	// support for https://eips.ethereum.org/EIPS/eip-1898
	var val interface{}
	var rw bool
	if blockNrOrHash {
		bnh, err := remarshalBlockNumberOrHash(p[pos])
		if err != nil {
			// fallback to string
			s, ok := p[pos].(string)
			if ok {
				val, rw, err = rewriteTag(rctx, s)
				if err != nil {
					return RewriteOverrideError, err
				}
			} else {
				return RewriteOverrideError, errors.New("expected BlockNumberOrHash or string")
			}
		} else {
			val, rw, err = rewriteTagBlockNumberOrHash(rctx, bnh)
			if err != nil {
				return RewriteOverrideError, err
			}
		}
	} else {
		s, ok := p[pos].(string)
		if !ok {
			return RewriteOverrideError, errors.New("expected string")
		}

		val, rw, err = rewriteTag(rctx, s)
		if err != nil {
			return RewriteOverrideError, err
		}
	}

	if rw {
		p[pos] = val
		paramRaw, err := json.Marshal(p)
		if err != nil {
			return RewriteOverrideError, err
		}
		req.Params = paramRaw
		return RewriteOverrideRequest, nil
	}
	return RewriteNone, nil
}

func rewriteRange(rctx RewriteContext, req *RPCReq, res *RPCRes, pos int) (RewriteResult, error) {
	var p []map[string]interface{}
	err := json.Unmarshal(req.Params, &p)
	if err != nil {
		return RewriteOverrideError, err
	}

	_, hasFrom := p[pos]["fromBlock"]
	_, hasTo := p[pos]["toBlock"]

	defaultsSet := false

	if rctx.consensusMode {
		if hasFrom && !hasTo {
			p[pos]["toBlock"] = "latest"
			hasTo = true
			defaultsSet = true
		} else if hasTo && !hasFrom {
			p[pos]["fromBlock"] = "latest"
			hasFrom = true
			defaultsSet = true
		}
	} else {
		if rctx.maxBlockRange > 0 && !hasTo {
			return RewriteOverrideError, errors.New("toBlock must be specified when max_block_range is configured")
		}
		if !hasFrom {
			p[pos]["fromBlock"] = "earliest"
			hasFrom = true
			defaultsSet = true
		}
	}

	modifiedFrom, err := rewriteTagMap(rctx, p[pos], "fromBlock")
	if err != nil {
		return RewriteOverrideError, err
	}

	modifiedTo, err := rewriteTagMap(rctx, p[pos], "toBlock")
	if err != nil {
		return RewriteOverrideError, err
	}

	if rctx.maxBlockRange > 0 && (hasFrom || hasTo) {
		from, err := blockNumber(p[pos], "fromBlock", uint64(rctx.latest))
		if err != nil {
			return RewriteOverrideError, err
		}
		to, err := blockNumber(p[pos], "toBlock", uint64(rctx.latest))
		if err != nil {
			return RewriteOverrideError, err
		}
		if to-from > rctx.maxBlockRange {
			return RewriteOverrideError, ErrRewriteRangeTooLarge
		}
	}

	if modifiedFrom || modifiedTo || defaultsSet {
		paramsRaw, err := json.Marshal(p)
		req.Params = paramsRaw
		if err != nil {
			return RewriteOverrideError, err
		}
		return RewriteOverrideRequest, nil
	}

	return RewriteNone, nil
}

func blockNumber(m map[string]interface{}, key string, latest uint64) (uint64, error) {
	current, ok := m[key].(string)
	if !ok {
		return 0, errors.New("expected string")
	}
	// the latest/safe/finalized tags are already replaced by rewriteTag
	if current == "earliest" {
		return 0, nil
	}
	if current == "pending" {
		return latest + 1, nil
	}
	return hexutil.DecodeUint64(current)
}

func rewriteTagMap(rctx RewriteContext, m map[string]interface{}, key string) (bool, error) {
	if m[key] == nil || m[key] == "" {
		return false, nil
	}

	current, ok := m[key].(string)
	if !ok {
		return false, errors.New("expected string")
	}

	val, rw, err := rewriteTag(rctx, current)
	if err != nil {
		return false, err
	}
	if rw {
		m[key] = val
		return true, nil
	}

	return false, nil
}

func remarshalBlockNumberOrHash(current interface{}) (*rpc.BlockNumberOrHash, error) {
	jv, err := json.Marshal(current)
	if err != nil {
		return nil, err
	}

	var bnh rpc.BlockNumberOrHash
	err = bnh.UnmarshalJSON(jv)
	if err != nil {
		return nil, err
	}

	return &bnh, nil
}

func rewriteTag(rctx RewriteContext, current string) (string, bool, error) {
	bnh, err := remarshalBlockNumberOrHash(current)
	if err != nil {
		return "", false, err
	}

	// this is a hash, not a block
	if bnh.BlockNumber == nil {
		return current, false, nil
	}

	switch *bnh.BlockNumber {
	case rpc.EarliestBlockNumber:
		return current, false, nil
	case rpc.PendingBlockNumber, rpc.FinalizedBlockNumber, rpc.SafeBlockNumber, rpc.LatestBlockNumber:
		if !rctx.consensusMode {
			return "", false, errors.New("block tags (latest/pending/safe/finalized) are not allowed when max_block_range is configured")
		}
		switch *bnh.BlockNumber {
		case rpc.PendingBlockNumber:
			return current, false, nil
		case rpc.FinalizedBlockNumber:
			return rctx.finalized.String(), true, nil
		case rpc.SafeBlockNumber:
			return rctx.safe.String(), true, nil
		case rpc.LatestBlockNumber:
			return rctx.latest.String(), true, nil
		}
	default:
		if rctx.latest > 0 && bnh.BlockNumber.Int64() > int64(rctx.latest) {
			return "", false, ErrRewriteBlockOutOfRange
		}
	}

	return current, false, nil
}

func rewriteTagBlockNumberOrHash(rctx RewriteContext, current *rpc.BlockNumberOrHash) (*rpc.BlockNumberOrHash, bool, error) {
	// this is a hash, not a block number
	if current.BlockNumber == nil {
		return current, false, nil
	}

	switch *current.BlockNumber {
	case rpc.PendingBlockNumber,
		rpc.EarliestBlockNumber:
		return current, false, nil
	case rpc.FinalizedBlockNumber:
		bn := rpc.BlockNumberOrHashWithNumber(rpc.BlockNumber(rctx.finalized))
		return &bn, true, nil
	case rpc.SafeBlockNumber:
		bn := rpc.BlockNumberOrHashWithNumber(rpc.BlockNumber(rctx.safe))
		return &bn, true, nil
	case rpc.LatestBlockNumber:
		bn := rpc.BlockNumberOrHashWithNumber(rpc.BlockNumber(rctx.latest))
		return &bn, true, nil
	default:
		if current.BlockNumber.Int64() > int64(rctx.latest) {
			return nil, false, ErrRewriteBlockOutOfRange
		}
	}

	return current, false, nil
}
