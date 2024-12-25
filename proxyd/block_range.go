package proxyd

import (
	"encoding/json"

	"github.com/ethereum/go-ethereum/common/hexutil"
)

type BlockRange struct {
	FromBlock uint64
	ToBlock   uint64
}

type BlockNumberTracker interface {
	GetLatestBlockNumber() (uint64, bool)
	GetSafeBlockNumber() (uint64, bool)
	GetFinalizedBlockNumber() (uint64, bool)
}

func ExtractBlockRange(req *RPCReq, tracker BlockNumberTracker) *BlockRange {
	switch req.Method {
	case "eth_getLogs", "eth_newFilter":
		var p []map[string]interface{}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil
		}
		fromBlock, hasFrom := p[0]["fromBlock"].(string)
		toBlock, hasTo := p[0]["toBlock"].(string)
		if !hasFrom && !hasTo {
			return nil
		}
		// if either fromBlock or toBlock is defined, default the other to "latest" if unset
		if hasFrom && !hasTo {
			toBlock = "latest"
		} else if hasTo && !hasFrom {
			fromBlock = "latest"
		}
		from, fromOk := stringToBlockNumber(fromBlock, tracker)
		to, toOk := stringToBlockNumber(toBlock, tracker)
		if !fromOk || !toOk {
			return nil
		}
		return &BlockRange{
			FromBlock: from,
			ToBlock:   to,
		}
	default:
		return nil
	}
}

func stringToBlockNumber(tag string, tracker BlockNumberTracker) (uint64, bool) {
	switch tag {
	case "latest":
		return tracker.GetLatestBlockNumber()
	case "safe":
		return tracker.GetSafeBlockNumber()
	case "finalized":
		return tracker.GetFinalizedBlockNumber()
	case "earliest":
		return 0, true
	case "pending":
		latest, ok := tracker.GetLatestBlockNumber()
		return latest + 1, ok
	}
	d, err := hexutil.DecodeUint64(tag)
	return d, err == nil
}
