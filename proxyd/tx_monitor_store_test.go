package proxyd

import (
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func txmonHash(b byte) common.Hash { return common.Hash{b} }

func TestMemoryPendingStoreAddAndCap(t *testing.T) {
	s := NewMemoryPendingStore(2)
	require.True(t, s.Add(txmonEntry{Hash: txmonHash(1)}))
	require.True(t, s.Add(txmonEntry{Hash: txmonHash(2)}))
	require.False(t, s.Add(txmonEntry{Hash: txmonHash(3)}), "add beyond cap must be rejected")
	require.Equal(t, 2, s.Len())
	// Re-adding an existing hash (client retry through same replica) must not
	// double-count: it is a no-op success.
	require.True(t, s.Add(txmonEntry{Hash: txmonHash(1)}))
	require.Equal(t, 2, s.Len())
}

func TestMemoryPendingStoreMatchAndRemove(t *testing.T) {
	s := NewMemoryPendingStore(10)
	t0 := time.Unix(100, 0)
	s.Add(txmonEntry{Hash: txmonHash(1), IngestedAt: t0, BackendGroup: "main"})
	s.Add(txmonEntry{Hash: txmonHash(2), IngestedAt: t0})

	got := s.MatchAndRemove([]common.Hash{txmonHash(1), txmonHash(9)})
	require.Len(t, got, 1)
	require.Equal(t, txmonHash(1), got[0].Hash)
	require.Equal(t, "main", got[0].BackendGroup)
	require.Equal(t, 1, s.Len())
	// Second match returns nothing — entry was removed.
	require.Empty(t, s.MatchAndRemove([]common.Hash{txmonHash(1)}))
}

func TestMemoryPendingStoreMarkSubblock(t *testing.T) {
	s := NewMemoryPendingStore(10)
	t0 := time.Unix(100, 0)
	t1 := time.Unix(101, 0)
	s.Add(txmonEntry{Hash: txmonHash(1), IngestedAt: t0})

	e, ok := s.MarkSubblock(txmonHash(1), t1)
	require.True(t, ok)
	require.Equal(t, t1, e.SubblockAt)
	require.Equal(t, t0, e.IngestedAt)

	// Idempotent: second mark reports false (already preconfirmed).
	_, ok = s.MarkSubblock(txmonHash(1), t1.Add(time.Second))
	require.False(t, ok)
	// Unknown hash: false.
	_, ok = s.MarkSubblock(txmonHash(9), t1)
	require.False(t, ok)

	// Entry stays until block inclusion; MatchAndRemove sees SubblockAt.
	got := s.MatchAndRemove([]common.Hash{txmonHash(1)})
	require.Len(t, got, 1)
	require.Equal(t, t1, got[0].SubblockAt)
}

func TestMemoryPendingStoreExpireBefore(t *testing.T) {
	s := NewMemoryPendingStore(10)
	s.Add(txmonEntry{Hash: txmonHash(1), IngestedAt: time.Unix(100, 0)})
	s.Add(txmonEntry{Hash: txmonHash(2), IngestedAt: time.Unix(200, 0)})

	expired := s.ExpireBefore(time.Unix(150, 0))
	require.Len(t, expired, 1)
	require.Equal(t, txmonHash(1), expired[0].Hash)
	require.Equal(t, 1, s.Len())
}
