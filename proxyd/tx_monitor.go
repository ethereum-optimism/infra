package proxyd

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"
	"github.com/gorilla/websocket"
)

const (
	txmonEventBuffer = 1024
	// txmonMaxBlockWalk caps how many missed heights one poll will backfill,
	// bounding catch-up load after an outage. Anything older times out.
	txmonMaxBlockWalk = 32
	txmonReapInterval = 5 * time.Second
	txmonFetchTimeout = 10 * time.Second
	txmonMaxBackoff   = 30 * time.Second
	// txmonMaxFrameBytes caps websocket frame size on the subblocks stream;
	// gorilla defaults to unlimited, and the stream is untrusted input.
	txmonMaxFrameBytes = 10 << 20
)

type txmonEvent struct {
	hash         common.Hash
	backendGroup string
	source       string
	at           time.Time
}

// TxMonitor passively measures user tx inclusion latency. It is fed by
// Observe (non-blocking) from the request hot path and runs its own
// block-poll, subblocks-stream, and reaper goroutines. Never critical:
// every failure mode degrades to missing metrics, not degraded ingestion.
type TxMonitor struct {
	cfg       TxMonitorConfig
	events    chan txmonEvent
	store     PendingStore
	fetcher   blockFetcher
	now       func() time.Time
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	lastBlock uint64
}

func NewTxMonitor(cfg TxMonitorConfig, backendGroups map[string]*BackendGroup, rpcMethodMappings map[string]string) (*TxMonitor, error) {
	// Zero means default; negative values would panic time.NewTicker or make
	// the reaper cutoff nonsensical, so fail fast at startup.
	if cfg.PollInterval < 0 {
		return nil, fmt.Errorf("tx_monitor: poll_interval must not be negative, got %s", time.Duration(cfg.PollInterval))
	}
	if cfg.InclusionTimeout < 0 {
		return nil, fmt.Errorf("tx_monitor: inclusion_timeout must not be negative, got %s", time.Duration(cfg.InclusionTimeout))
	}
	if cfg.MaxPending < 0 {
		return nil, fmt.Errorf("tx_monitor: max_pending must not be negative, got %d", cfg.MaxPending)
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = TOMLDuration(time.Second)
	}
	if cfg.InclusionTimeout == 0 {
		cfg.InclusionTimeout = TOMLDuration(60 * time.Second)
	}
	if cfg.MaxPending == 0 {
		cfg.MaxPending = 10000
	}

	var fetcher blockFetcher
	if cfg.BlockPollURL != "" {
		f, err := newRPCBlockFetcher(cfg.BlockPollURL)
		if err != nil {
			return nil, err
		}
		fetcher = f
	} else {
		groupName := cfg.BlockPollBackendGroup
		if groupName == "" {
			groupName = rpcMethodMappings["eth_getBlockByNumber"]
		}
		if groupName == "" {
			groupName = rpcMethodMappings["eth_sendRawTransaction"]
		}
		bg, ok := backendGroups[groupName]
		if !ok {
			return nil, fmt.Errorf("tx_monitor: cannot resolve block poll backend group %q; set block_poll_backend_group or block_poll_url", groupName)
		}
		fetcher = newBackendGroupBlockFetcher(bg)
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &TxMonitor{
		cfg:     cfg,
		events:  make(chan txmonEvent, txmonEventBuffer),
		store:   NewMemoryPendingStore(cfg.MaxPending),
		fetcher: fetcher,
		now:     time.Now,
		ctx:     ctx,
		cancel:  cancel,
	}, nil
}

func (m *TxMonitor) Start() {
	m.wg.Add(3)
	go m.runIngest()
	go m.runBlockWatch()
	go m.runReaper()
	if m.cfg.SubblocksWSURL != "" {
		m.wg.Add(1)
		go m.runSubblocks()
	}
	log.Info("tx_monitor started",
		"subblocks", m.cfg.SubblocksWSURL != "",
		"poll_interval", time.Duration(m.cfg.PollInterval),
		"inclusion_timeout", time.Duration(m.cfg.InclusionTimeout),
	)
}

func (m *TxMonitor) Shutdown() {
	m.cancel()
	m.wg.Wait()
	if c, ok := m.fetcher.(io.Closer); ok {
		c.Close()
	}
}

// Observe records a successfully forwarded tx. Non-blocking by construction:
// a full channel drops the observation and counts it. This is the ONLY method
// called from the request hot path.
func (m *TxMonitor) Observe(hash common.Hash, backendGroup string, source string) {
	select {
	case m.events <- txmonEvent{hash: hash, backendGroup: backendGroup, source: source, at: m.now()}:
	default:
		txmonDroppedChannelFull.Inc()
	}
}

func (m *TxMonitor) runIngest() {
	defer m.wg.Done()
	for {
		select {
		case <-m.ctx.Done():
			return
		case ev := <-m.events:
			ok := m.store.Add(txmonEntry{
				Hash:         ev.hash,
				BackendGroup: ev.backendGroup,
				Source:       ev.source,
				IngestedAt:   ev.at,
			})
			if !ok {
				txmonDroppedMapFull.Inc()
			}
		}
	}
}

func (m *TxMonitor) runBlockWatch() {
	defer m.wg.Done()
	ticker := time.NewTicker(time.Duration(m.cfg.PollInterval))
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.pollOnce()
		}
	}
}

func (m *TxMonitor) pollOnce() {
	ctx, cancel := context.WithTimeout(m.ctx, txmonFetchTimeout)
	defer cancel()

	num, hashes, err := m.fetcher.LatestBlock(ctx)
	if err != nil {
		log.Debug("tx_monitor: block poll failed", "err", err)
		return
	}
	if m.lastBlock != 0 && num <= m.lastBlock {
		return // no new block (or a reorg to a lower height — v1 ignores reorgs)
	}
	if m.lastBlock != 0 {
		start := m.lastBlock + 1
		if num-start > txmonMaxBlockWalk {
			start = num - txmonMaxBlockWalk
		}
		// Advance the cursor only over successfully processed heights: a failed
		// or not-yet-found gap height leaves lastBlock behind it so the
		// remaining heights (and the tip) are retried next tick.
		for h := start; h < num; h++ {
			gapHashes, found, err := m.fetcher.BlockByNumber(ctx, h)
			if err != nil || !found {
				log.Debug("tx_monitor: gap block fetch failed, retrying next tick", "height", h, "found", found, "err", err)
				m.lastBlock = h - 1
				return
			}
			m.handleBlock(gapHashes, m.now())
		}
	}
	m.handleBlock(hashes, m.now())
	m.lastBlock = num
}

func (m *TxMonitor) handleBlock(hashes []common.Hash, at time.Time) {
	for _, e := range m.store.MatchAndRemove(hashes) {
		txmonInclusionLatency.WithLabelValues("block", e.BackendGroup).Observe(at.Sub(e.IngestedAt).Seconds())
		if !e.SubblockAt.IsZero() {
			txmonSubblockToBlock.Observe(at.Sub(e.SubblockAt).Seconds())
		}
	}
}

func (m *TxMonitor) handleSubblockHashes(hashes []common.Hash, at time.Time) {
	for _, h := range hashes {
		if e, ok := m.store.MarkSubblock(h, at); ok {
			txmonInclusionLatency.WithLabelValues("subblock", e.BackendGroup).Observe(at.Sub(e.IngestedAt).Seconds())
		}
	}
}

func (m *TxMonitor) runSubblocks() {
	defer m.wg.Done()
	backoff := time.Second
	for m.ctx.Err() == nil {
		connectedAt := m.now()
		err := m.streamSubblocks()
		if m.ctx.Err() != nil {
			return
		}
		txmonStreamDisconnectsTotal.Inc()
		if m.now().Sub(connectedAt) > time.Minute {
			backoff = time.Second // healthy connection: reset backoff
		}
		log.Warn("tx_monitor: subblocks stream disconnected", "err", err, "retry_in", backoff)
		select {
		case <-m.ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < txmonMaxBackoff {
			backoff *= 2
		}
	}
}

func (m *TxMonitor) streamSubblocks() error {
	conn, resp, err := websocket.DefaultDialer.DialContext(m.ctx, m.cfg.SubblocksWSURL, nil)
	// On a successful upgrade gorilla replaces the body with a no-op reader,
	// so closing is always safe; on handshake failure it's the error body.
	if resp != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetReadLimit(txmonMaxFrameBytes)

	// Unblock the blocking ReadMessage on shutdown without leaking a
	// goroutine per reconnect.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-m.ctx.Done():
			conn.Close()
		case <-done:
		}
	}()

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		hashes, err := parseSubblockTxHashes(msg)
		if err != nil {
			log.Debug("tx_monitor: malformed subblock frame", "err", err)
			continue
		}
		m.handleSubblockHashes(hashes, m.now())
	}
}

func (m *TxMonitor) runReaper() {
	defer m.wg.Done()
	ticker := time.NewTicker(txmonReapInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.reapOnce()
		}
	}
}

func (m *TxMonitor) reapOnce() {
	cutoff := m.now().Add(-time.Duration(m.cfg.InclusionTimeout))
	for _, e := range m.store.ExpireBefore(cutoff) {
		txmonTimeoutsTotal.WithLabelValues(e.BackendGroup).Inc()
	}
	txmonPending.Set(float64(m.store.Len()))
}
