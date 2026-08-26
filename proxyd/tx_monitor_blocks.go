package proxyd

import (
	"context"
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
)

// blockFetcher abstracts "give me a block's tx hashes" so the monitor can
// poll either an explicit RPC URL or one of proxyd's own backend groups.
type blockFetcher interface {
	LatestBlock(ctx context.Context) (uint64, []common.Hash, error)
	BlockByNumber(ctx context.Context, num uint64) ([]common.Hash, bool, error)
}

// --- explicit URL fetcher (block_poll_url) ---

type rpcBlockFetcher struct {
	c *rpc.Client
}

func newRPCBlockFetcher(url string) (*rpcBlockFetcher, error) {
	c, err := rpc.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("tx_monitor: dialing block_poll_url: %w", err)
	}
	return &rpcBlockFetcher{c: c}, nil
}

// txmonRPCBlock decodes eth_getBlockByNumber(…, false): transactions are hashes.
type txmonRPCBlock struct {
	Number       hexutil.Uint64 `json:"number"`
	Transactions []common.Hash  `json:"transactions"`
}

func (f *rpcBlockFetcher) LatestBlock(ctx context.Context) (uint64, []common.Hash, error) {
	var blk *txmonRPCBlock
	if err := f.c.CallContext(ctx, &blk, "eth_getBlockByNumber", "latest", false); err != nil {
		return 0, nil, err
	}
	if blk == nil {
		return 0, nil, errors.New("tx_monitor: null latest block")
	}
	return uint64(blk.Number), blk.Transactions, nil
}

func (f *rpcBlockFetcher) BlockByNumber(ctx context.Context, num uint64) ([]common.Hash, bool, error) {
	var blk *txmonRPCBlock
	if err := f.c.CallContext(ctx, &blk, "eth_getBlockByNumber", hexutil.EncodeUint64(num), false); err != nil {
		return nil, false, err
	}
	if blk == nil {
		return nil, false, nil
	}
	return blk.Transactions, true, nil
}

// --- backend group fetcher (default: blocks "visible to proxyd") ---

type backendGroupBlockFetcher struct {
	bg *BackendGroup
}

func newBackendGroupBlockFetcher(bg *BackendGroup) *backendGroupBlockFetcher {
	return &backendGroupBlockFetcher{bg: bg}
}

func (f *backendGroupBlockFetcher) LatestBlock(ctx context.Context) (uint64, []common.Hash, error) {
	num, hashes, found, err := f.fetch(ctx, "latest")
	if err != nil {
		return 0, nil, err
	}
	if !found {
		return 0, nil, errors.New("tx_monitor: null latest block")
	}
	return num, hashes, nil
}

func (f *backendGroupBlockFetcher) BlockByNumber(ctx context.Context, num uint64) ([]common.Hash, bool, error) {
	_, hashes, found, err := f.fetch(ctx, hexutil.EncodeUint64(num))
	return hashes, found, err
}

// fetch tries each backend in the group until one answers. First-success is
// enough: this is a monitor, not a consensus mechanism.
func (f *backendGroupBlockFetcher) fetch(ctx context.Context, tag string) (uint64, []common.Hash, bool, error) {
	var lastErr error
	for _, be := range f.bg.Backends {
		var res RPCRes
		// ForwardRPC embeds the id verbatim as JSON, so it must be a valid
		// JSON value — hence the quoted string.
		if err := be.ForwardRPC(ctx, &res, `"txmon"`, "eth_getBlockByNumber", tag, false); err != nil {
			lastErr = err
			continue
		}
		if res.Result == nil {
			return 0, nil, false, nil // block genuinely absent (walked past head)
		}
		num, hashes, err := parseRPCBlockResult(res.Result)
		if err != nil {
			lastErr = err
			continue
		}
		return num, hashes, true, nil
	}
	if lastErr == nil {
		lastErr = errors.New("tx_monitor: no backends in group")
	}
	return 0, nil, false, lastErr
}

// parseRPCBlockResult parses a generic eth_getBlockByNumber(…, false) result
// (as produced by RPCRes.Result: map[string]interface{}).
func parseRPCBlockResult(result interface{}) (uint64, []common.Hash, error) {
	m, ok := result.(map[string]interface{})
	if !ok {
		return 0, nil, fmt.Errorf("tx_monitor: unexpected block result type %T", result)
	}
	numStr, ok := m["number"].(string)
	if !ok {
		return 0, nil, errors.New("tx_monitor: block result missing number")
	}
	num, err := hexutil.DecodeUint64(numStr)
	if err != nil {
		return 0, nil, err
	}
	rawTxs, _ := m["transactions"].([]interface{})
	hashes := make([]common.Hash, 0, len(rawTxs))
	for _, rt := range rawTxs {
		if s, ok := rt.(string); ok {
			hashes = append(hashes, common.HexToHash(s))
		}
	}
	return num, hashes, nil
}
