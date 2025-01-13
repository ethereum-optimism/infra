package proxyd

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/log"
)

func NewNonconsensusPoller(bg *BackendGroup, opts ...NonconsensusOpt) *NonconsensusPoller {
	ctx, cancel := context.WithCancel(context.Background())
	np := &NonconsensusPoller{
		interval:       DefaultPollerInterval,
		ctx:            ctx,
		cancel:         cancel,
		latestBlock:    math.MaxUint64,
		safeBlock:      math.MaxUint64,
		finalizedBlock: math.MaxUint64,
	}
	for _, opt := range opts {
		opt(np)
	}
	for _, backend := range bg.Backends {
		np.wg.Add(1)
		go np.poll(backend)
	}
	return np
}

type NonconsensusOpt func(p *NonconsensusPoller)

func WithPollingInterval(interval time.Duration) NonconsensusOpt {
	return func(p *NonconsensusPoller) {
		p.interval = interval
	}
}

type NonconsensusPoller struct {
	interval       time.Duration
	ctx            context.Context
	cancel         context.CancelFunc
	lock           sync.RWMutex
	wg             sync.WaitGroup
	latestBlock    uint64
	safeBlock      uint64
	finalizedBlock uint64
}

func (p *NonconsensusPoller) Shutdown() {
	p.cancel()
	p.wg.Wait()
}

func (p *NonconsensusPoller) GetLatestBlockNumber() (uint64, bool) {
	return p.getMaxBlockNumber(&p.latestBlock)
}

func (p *NonconsensusPoller) GetSafeBlockNumber() (uint64, bool) {
	return p.getMaxBlockNumber(&p.safeBlock)
}

func (p *NonconsensusPoller) GetFinalizedBlockNumber() (uint64, bool) {
	return p.getMaxBlockNumber(&p.finalizedBlock)
}

func (p *NonconsensusPoller) getMaxBlockNumber(ptr *uint64) (uint64, bool) {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return *ptr, *ptr != math.MaxUint64
}

func (p *NonconsensusPoller) poll(be *Backend) {
	timer := time.NewTimer(p.interval)
	for {
		select {
		case <-p.ctx.Done():
			p.wg.Done()
			return
		case <-timer.C:
			p.update(be, "latest", &p.latestBlock)
			p.update(be, "safe", &p.safeBlock)
			p.update(be, "finalized", &p.finalizedBlock)
			timer.Reset(p.interval)
		}
	}
}

func (p *NonconsensusPoller) update(be *Backend, label string, ptr *uint64) {
	value, err := p.fetchBlock(p.ctx, be, label)
	if err != nil {
		log.Error("failed to fetch block", "backend", be.Name, "label", label, "err", err)
		return
	}

	p.lock.Lock()
	defer p.lock.Unlock()

	if *ptr < value || *ptr == math.MaxUint64 {
		*ptr = value
	}
}

func (p *NonconsensusPoller) fetchBlock(ctx context.Context, be *Backend, block string) (uint64, error) {
	var rpcRes RPCRes
	err := be.ForwardRPC(ctx, &rpcRes, "68", "eth_getBlockByNumber", block, false)
	if err != nil {
		return 0, err
	}

	jsonMap, ok := rpcRes.Result.(map[string]interface{})
	if !ok {
		return 0, fmt.Errorf("unexpected response to eth_getBlockByNumber on backend %s", be.Name)
	}
	return hexutil.MustDecodeUint64(jsonMap["number"].(string)), nil
}
