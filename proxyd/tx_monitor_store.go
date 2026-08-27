package proxyd

import (
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

// txmonEntry is one observed eth_sendRawTransaction awaiting inclusion.
type txmonEntry struct {
	Hash         common.Hash
	BackendGroup string
	Source       string
	IngestedAt   time.Time
	SubblockAt   time.Time // zero until seen in the subblocks stream
}

// PendingStore tracks pending transactions. The interface is batch-shaped so
// a future Redis implementation can pipeline one multi-key op per block.
type PendingStore interface {
	Add(e txmonEntry) bool
	MatchAndRemove(hashes []common.Hash) []txmonEntry
	MarkSubblock(h common.Hash, at time.Time) (txmonEntry, bool)
	ExpireBefore(cutoff time.Time) []txmonEntry
	Len() int
}

type MemoryPendingStore struct {
	mu  sync.Mutex
	max int
	txs map[common.Hash]txmonEntry
}

func NewMemoryPendingStore(maxPending int) *MemoryPendingStore {
	return &MemoryPendingStore{max: maxPending, txs: make(map[common.Hash]txmonEntry)}
}

// Add records a pending tx. Returns false when the store is at capacity.
// Re-adding a hash already present keeps whichever entry has the earlier
// IngestedAt: that is the user-perceived submission time, and because
// IngestedAt is stamped at request ingress, a later duplicate submission may
// be observed (enqueued) before an earlier one. Any already-set SubblockAt on
// the retained entry is preserved.
func (s *MemoryPendingStore) Add(e txmonEntry) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.txs[e.Hash]; ok {
		if e.IngestedAt.Before(existing.IngestedAt) {
			e.SubblockAt = existing.SubblockAt
			s.txs[e.Hash] = e
		}
		return true
	}
	if len(s.txs) >= s.max {
		return false
	}
	s.txs[e.Hash] = e
	return true
}

func (s *MemoryPendingStore) MatchAndRemove(hashes []common.Hash) []txmonEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []txmonEntry
	for _, h := range hashes {
		if e, ok := s.txs[h]; ok {
			out = append(out, e)
			delete(s.txs, h)
		}
	}
	return out
}

// MarkSubblock stamps the first subblock sighting. Returns the updated entry
// and true only on the first mark of a known hash.
func (s *MemoryPendingStore) MarkSubblock(h common.Hash, at time.Time) (txmonEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.txs[h]
	if !ok || !e.SubblockAt.IsZero() {
		return txmonEntry{}, false
	}
	e.SubblockAt = at
	s.txs[h] = e
	return e, true
}

func (s *MemoryPendingStore) ExpireBefore(cutoff time.Time) []txmonEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []txmonEntry
	for h, e := range s.txs {
		if e.IngestedAt.Before(cutoff) {
			out = append(out, e)
			delete(s.txs, h)
		}
	}
	return out
}

func (s *MemoryPendingStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.txs)
}
