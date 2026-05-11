package proxyd

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum-optimism/optimism/op-service/eth"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rpc"
)

// CrossUnsafeLatestPoller tracks the latest block accepted by op-interop-filter's
// cross-unsafe validation.
type CrossUnsafeLatestPoller struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	urls     []string
	chainID  eth.ChainID
	interval time.Duration

	latest atomic.Uint64
	hash   atomic.Value
	ok     atomic.Bool
}

func NewCrossUnsafeLatestPoller(urls []string, chainID uint64, interval time.Duration) *CrossUnsafeLatestPoller {
	ctx, cancel := context.WithCancel(context.Background())
	return &CrossUnsafeLatestPoller{
		ctx:      ctx,
		cancel:   cancel,
		urls:     urls,
		chainID:  eth.ChainIDFromUInt64(chainID),
		interval: interval,
	}
}

func (p *CrossUnsafeLatestPoller) Start() {
	if p.interval <= 0 {
		p.interval = DefaultPollerInterval
	}

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.poll(p.ctx)

		timer := time.NewTimer(p.interval)
		defer timer.Stop()
		for {
			select {
			case <-timer.C:
				p.poll(p.ctx)
				timer.Reset(p.interval)
			case <-p.ctx.Done():
				return
			}
		}
	}()
}

func (p *CrossUnsafeLatestPoller) Shutdown() {
	p.cancel()
	p.wg.Wait()
}

func (p *CrossUnsafeLatestPoller) LatestBlock() (eth.BlockID, bool) {
	if !p.ok.Load() {
		return eth.BlockID{}, false
	}
	hash, _ := p.hash.Load().(common.Hash)
	return eth.BlockID{
		Hash:   hash,
		Number: p.latest.Load(),
	}, true
}

func (p *CrossUnsafeLatestPoller) SetLatestBlock(block eth.BlockID) {
	p.latest.Store(block.Number)
	p.hash.Store(block.Hash)
	p.ok.Store(true)
}

func (p *CrossUnsafeLatestPoller) poll(ctx context.Context) {
	var min eth.BlockID
	found := false
	for _, url := range p.urls {
		block, err := p.fetchLatestCrossUnsafeBlock(ctx, url)
		if err != nil {
			log.Warn("failed to fetch latest cross-unsafe block", "url", url, "chain_id", p.chainID, "err", err)
			continue
		}
		if !found || block.Number < min.Number {
			min = block
			found = true
		}
	}
	if !found {
		return
	}

	p.SetLatestBlock(min)
	log.Debug("updated latest cross-unsafe block", "chain_id", p.chainID, "block", min)
}

func (p *CrossUnsafeLatestPoller) fetchLatestCrossUnsafeBlock(ctx context.Context, url string) (eth.BlockID, error) {
	cl, err := rpc.DialContext(ctx, url)
	if err != nil {
		return eth.BlockID{}, fmt.Errorf("dial interop filter: %w", err)
	}
	defer cl.Close()

	var block eth.BlockID
	if err := cl.CallContext(ctx, &block, "supervisor_getLatestCrossUnsafeBlock", p.chainID); err != nil {
		return eth.BlockID{}, err
	}
	return block, nil
}
