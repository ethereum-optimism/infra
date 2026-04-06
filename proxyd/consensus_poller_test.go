package proxyd

import (
	"context"
	"testing"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/stretchr/testify/require"
)

func TestFindConsensusBlock_exhaustsAtGenesis(t *testing.T) {
	ctx := context.Background()
	cp := &ConsensusPoller{}

	be1 := &Backend{Name: "node-a"}
	be2 := &Backend{Name: "node-b"}
	candidates := map[*Backend]*backendState{
		be1: {},
		be2: {},
	}

	fetch := func(ctx context.Context, be *Backend, _ *backendState, block hexutil.Uint64) (hexutil.Uint64, string, error) {
		if be == be1 {
			return block, "hash-a", nil
		}
		return block, "hash-b", nil
	}

	num, hash, broken := cp.findConsensusBlock(ctx, candidates, 0, 2, "hash-a", fetch, "test")
	require.Equal(t, hexutil.Uint64(0), num)
	require.Equal(t, "", hash)
	require.True(t, broken)
}

func TestFindConsensusBlock_agreesAtBlockZero(t *testing.T) {
	ctx := context.Background()
	cp := &ConsensusPoller{}

	be1 := &Backend{Name: "node-a"}
	be2 := &Backend{Name: "node-b"}
	candidates := map[*Backend]*backendState{
		be1: {},
		be2: {},
	}

	fetch := func(ctx context.Context, be *Backend, _ *backendState, block hexutil.Uint64) (hexutil.Uint64, string, error) {
		switch uint64(block) {
		case 1:
			if be == be1 {
				return block, "hash-a-at-1", nil
			}
			return block, "hash-b-at-1", nil
		case 0:
			return block, "genesis-shared", nil
		default:
			return block, "unused", nil
		}
	}

	num, hash, broken := cp.findConsensusBlock(ctx, candidates, 0, 1, "hash-a-at-1", fetch, "test")
	require.Equal(t, hexutil.Uint64(0), num)
	require.Equal(t, "genesis-shared", hash)
	require.False(t, broken)
}
