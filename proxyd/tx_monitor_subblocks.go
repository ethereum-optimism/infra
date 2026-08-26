package proxyd

import (
	"encoding/json"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
)

// subblockPayload is the subset of FlashblocksPayloadV1 the monitor needs.
// diff.transactions carries raw RLP-encoded transactions (0x-hex), not hashes;
// metadata is builder-specific and deliberately ignored.
type subblockPayload struct {
	Index uint64       `json:"index"`
	Diff  subblockDiff `json:"diff"`
}

type subblockDiff struct {
	Transactions []hexutil.Bytes `json:"transactions"`
}

// parseSubblockTxHashes extracts tx hashes from one subblocks-stream frame.
// Individual undecodable transactions are skipped (a foreign tx type must not
// blind the monitor to the rest of the subblock); malformed JSON is an error.
func parseSubblockTxHashes(msg []byte) ([]common.Hash, error) {
	var p subblockPayload
	if err := json.Unmarshal(msg, &p); err != nil {
		return nil, err
	}
	hashes := make([]common.Hash, 0, len(p.Diff.Transactions))
	for _, raw := range p.Diff.Transactions {
		tx := new(types.Transaction)
		if err := tx.UnmarshalBinary(raw); err != nil {
			log.Debug("tx_monitor: skipping undecodable subblock tx", "err", err)
			continue
		}
		hashes = append(hashes, tx.Hash())
	}
	return hashes, nil
}
