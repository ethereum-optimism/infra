package proxyd

import (
	"context"
	"fmt"
	"time"

	"github.com/ethereum-optimism/optimism/op-core/interop/messages"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
)

func accessObjectToKey(accessObject messages.Access) string {
	return fmt.Sprintf("%s/%d/%d/%d/%s", accessObject.ChainID, accessObject.BlockNumber, accessObject.LogIndex, accessObject.Timestamp, accessObject.Checksum)
}

// validateAndDeduplicateInteropAccessList
// - validates all the interop access list entries by trying to successfully parse them on a per "Access" basis
// - discard any successfully parsed yet duplicate "Access" objects along the way.
// This is because the interop filter does the same for the incoming inbox entries and validates them against its DB on a per "Access" basis.
// So it makes sense to recognise and discard duplicate "Access" objects early.
func validateAndDeduplicateInteropAccessList(entriesToParse []common.Hash) ([]common.Hash, error) {
	if len(entriesToParse) == 0 {
		return nil, nil
	}

	var deduplicatedAccessObjects []messages.Access

	alreadySeenAccessObjectsSet := map[string]bool{}

	for len(entriesToParse) > 0 {
		remaining, parsedAccessObject, err := messages.ParseAccess(entriesToParse)
		if err != nil {
			return nil, err
		}

		key := accessObjectToKey(parsedAccessObject)
		if _, alreadySeen := alreadySeenAccessObjectsSet[key]; !alreadySeen {
			deduplicatedAccessObjects = append(deduplicatedAccessObjects, parsedAccessObject)

			alreadySeenAccessObjectsSet[key] = true
		}

		entriesToParse = remaining
	}

	deduplicatedAccessListEntries := messages.EncodeAccessList(deduplicatedAccessObjects)

	return deduplicatedAccessListEntries, nil
}

func getInteropExecutingDescriptorTimestamp() uint64 {
	// intentionally kept to be slightly in the future (but within the expiryAt of the associated message) to proceed through the access-list time-checks
	return uint64(time.Now().Unix() + 1000)
}

func (s *Server) rateLimitInteropSender(ctx context.Context, tx *types.Transaction) error {
	if s.interopSenderLim == nil {
		log.Warn("interop sender rate limiter is not enabled, skipping", "req_id", GetReqID(ctx))
		return nil
	}
	return s.genericRateLimitSender(ctx, tx, s.interopSenderLim)
}
