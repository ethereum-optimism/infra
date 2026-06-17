package proxyd

import (
	"fmt"
	"time"

	"github.com/ethereum-optimism/optimism/op-core/interop/messages"

	"github.com/ethereum/go-ethereum/common"
)

func accessObjectToKey(accessObject messages.Access) string {
	return fmt.Sprintf("%s/%d/%d/%d/%s", accessObject.ChainID, accessObject.BlockNumber, accessObject.LogIndex, accessObject.Timestamp, accessObject.Checksum)
}

// validateAndDeduplicateInteropAccessList
// - validates all the interop access list entries by trying to successfully parse them on a per "Access" basis
// - discard any successfully parsed yet duplicate "Access" objects along the way.
// This is because op-interop-filter does the same for the incoming inbox entries and validates them against its DB on a per "Access" basis.
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

const (
	// Clock-skew tolerance only; the executing message's later block is the real lower bound.
	interopExecutingDescriptorClockToleranceSeconds uint64 = 30

	// Expiry-window margin; matches op-reth's CHECK_ACCESS_LIST_TIMEOUT_SECS.
	interopExecutingDescriptorTimeoutSeconds uint64 = 7200
)

func getInteropExecutingDescriptorTimestamp() uint64 {
	return uint64(time.Now().Unix()) + interopExecutingDescriptorClockToleranceSeconds
}
