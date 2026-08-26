package proxyd

import (
	"context"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/ethereum/go-ethereum/common"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
)

func TestTxMonitorConfigDecode(t *testing.T) {
	cfgToml := `
[tx_monitor]
enabled = true
block_poll_url = "http://localhost:8545"
block_poll_backend_group = "replicas"
subblocks_ws_url = "ws://localhost:1112/ws"
inclusion_timeout = "45s"
max_pending = 5000
poll_interval = "500ms"
`
	var cfg Config
	_, err := toml.Decode(cfgToml, &cfg)
	require.NoError(t, err)
	require.True(t, cfg.TxMonitor.Enabled)
	require.Equal(t, "http://localhost:8545", cfg.TxMonitor.BlockPollURL)
	require.Equal(t, "replicas", cfg.TxMonitor.BlockPollBackendGroup)
	require.Equal(t, "ws://localhost:1112/ws", cfg.TxMonitor.SubblocksWSURL)
	require.Equal(t, TOMLDuration(45*time.Second), cfg.TxMonitor.InclusionTimeout)
	require.Equal(t, 5000, cfg.TxMonitor.MaxPending)
	require.Equal(t, TOMLDuration(500*time.Millisecond), cfg.TxMonitor.PollInterval)
}

// newTestTxMonitor builds a monitor with a fake clock and no goroutines.
func newTestTxMonitor(t *testing.T) (*TxMonitor, *time.Time) {
	t.Helper()
	now := time.Unix(1000, 0)
	m := &TxMonitor{
		cfg:    TxMonitorConfig{MaxPending: 100, InclusionTimeout: TOMLDuration(60 * time.Second)},
		events: make(chan txmonEvent, 4),
		store:  NewMemoryPendingStore(100),
		now:    func() time.Time { return now },
	}
	return m, &now
}

func TestTxMonitorBlockInclusion(t *testing.T) {
	m, now := newTestTxMonitor(t)
	h := common.Hash{0xab}
	m.store.Add(txmonEntry{Hash: h, IngestedAt: *now, BackendGroup: "main"})

	*now = now.Add(2 * time.Second)
	m.handleBlock([]common.Hash{h}, *now)

	require.Equal(t, 0, m.store.Len(), "included tx must be evicted")
	// Latency observed exactly once for source=block.
	c := testutil.CollectAndCount(txmonInclusionLatency)
	require.GreaterOrEqual(t, c, 1)
}

func TestTxMonitorSubblockThenBlock(t *testing.T) {
	m, now := newTestTxMonitor(t)
	h := common.Hash{0xcd}
	m.store.Add(txmonEntry{Hash: h, IngestedAt: *now, BackendGroup: "main"})

	*now = now.Add(250 * time.Millisecond)
	m.handleSubblockHashes([]common.Hash{h}, *now)
	require.Equal(t, 1, m.store.Len(), "preconfirmed tx stays pending until block inclusion")
	// Second sighting in a later subblock is a no-op (idempotent).
	m.handleSubblockHashes([]common.Hash{h}, now.Add(250*time.Millisecond))

	*now = now.Add(2 * time.Second)
	m.handleBlock([]common.Hash{h}, *now)
	require.Equal(t, 0, m.store.Len())
}

func TestTxMonitorReap(t *testing.T) {
	m, now := newTestTxMonitor(t)
	h := common.Hash{0xef}
	m.store.Add(txmonEntry{Hash: h, IngestedAt: *now, BackendGroup: "main"})
	before := testutil.ToFloat64(txmonTimeoutsTotal.WithLabelValues("main"))

	*now = now.Add(61 * time.Second)
	m.reapOnce()

	require.Equal(t, 0, m.store.Len())
	require.Equal(t, before+1, testutil.ToFloat64(txmonTimeoutsTotal.WithLabelValues("main")))
}

func TestTxMonitorObserveNeverBlocks(t *testing.T) {
	m, _ := newTestTxMonitor(t) // events buffer of 4, no ingest loop running
	before := testutil.ToFloat64(txmonDroppedEventsTotal.WithLabelValues("channel_full"))
	for i := range 10 {
		m.Observe(common.Hash{byte(i)}, "main", RPCRequestSourceHTTP) // must return immediately
	}
	require.Equal(t, before+6, testutil.ToFloat64(txmonDroppedEventsTotal.WithLabelValues("channel_full")))
}

type fakeFetcher struct {
	latestNum    uint64
	latestHashes []common.Hash
	byNum        map[uint64][]common.Hash
}

func (f *fakeFetcher) LatestBlock(ctx context.Context) (uint64, []common.Hash, error) {
	return f.latestNum, f.latestHashes, nil
}
func (f *fakeFetcher) BlockByNumber(ctx context.Context, num uint64) ([]common.Hash, bool, error) {
	h, ok := f.byNum[num]
	return h, ok, nil
}

func TestTxMonitorPollWalksMissedHeights(t *testing.T) {
	m, now := newTestTxMonitor(t)
	m.ctx, m.cancel = context.WithCancel(context.Background())
	defer m.cancel()
	hGap := common.Hash{0x11}
	hTip := common.Hash{0x22}
	m.store.Add(txmonEntry{Hash: hGap, IngestedAt: *now})
	m.store.Add(txmonEntry{Hash: hTip, IngestedAt: *now})

	f := &fakeFetcher{latestNum: 10, byNum: map[uint64][]common.Hash{}}
	m.fetcher = f
	m.pollOnce() // establishes lastBlock=10
	require.Equal(t, uint64(10), m.lastBlock)

	// Height 11 lands hGap while we weren't looking; tip jumps to 12 with hTip.
	f.byNum[11] = []common.Hash{hGap}
	f.latestNum = 12
	f.latestHashes = []common.Hash{hTip}
	m.pollOnce()

	require.Equal(t, uint64(12), m.lastBlock)
	require.Equal(t, 0, m.store.Len(), "both gap-block and tip-block txs must be matched")
}
