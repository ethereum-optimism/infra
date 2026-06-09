package proxyd

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/txpool"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/types/interoptypes"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	"github.com/stretchr/testify/require"
)

// fakeRateLimiter records the keys it is asked to take and can be configured to
// reject after a number of successful takes.
type fakeRateLimiter struct {
	keys      []string
	allowFunc func(key string) (bool, error)
}

func (f *fakeRateLimiter) Take(ctx context.Context, key string) (bool, error) {
	f.keys = append(f.keys, key)
	if f.allowFunc != nil {
		return f.allowFunc(key)
	}
	return true, nil
}

type fakeInteropStrategy struct {
	calls int
	err   error
}

func (f *fakeInteropStrategy) ValidateAccessList(ctx context.Context, _ []common.Hash) error {
	f.calls++
	return f.err
}

// interopTx builds a signed tx carrying an interop access list so
// checkInteropAndReturnAccessList treats it as an interop submission.
func interopTx(t *testing.T, chainID *big.Int, nonce uint64) *types.Transaction {
	t.Helper()
	key, err := crypto.GenerateKey()
	require.NoError(t, err)
	to := common.HexToAddress("0x8f3Ddd0FBf3e78CA1D6cd17379eD88E261249B53")
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     nonce,
		GasFeeCap: big.NewInt(1000000000),
		GasTipCap: big.NewInt(1000000000),
		Gas:       21000,
		To:        &to,
		Value:     big.NewInt(0),
		AccessList: types.AccessList{
			{
				Address:     params.InteropCrossL2InboxAddress,
				StorageKeys: []common.Hash{common.HexToHash("0x01")},
			},
		},
	})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(chainID), key)
	require.NoError(t, err)
	return signed
}

func TestChainIDModule(t *testing.T) {
	m := &chainIDModule{allowedChainIds: []*big.Int{big.NewInt(10)}}

	t.Run("accept", func(t *testing.T) {
		sub := &TxSubmission{Txs: []*types.Transaction{signedTxWithChainID(t, big.NewInt(10), 0)}}
		require.NoError(t, m.Apply(context.Background(), sub))
	})

	t.Run("reject wrong chain", func(t *testing.T) {
		sub := &TxSubmission{Txs: []*types.Transaction{
			signedTxWithChainID(t, big.NewInt(10), 0),
			signedTxWithChainID(t, big.NewInt(99), 1),
		}}
		require.ErrorIs(t, m.Apply(context.Background(), sub), txpool.ErrInvalidSender)
	})
}

func TestSenderRateLimitModule(t *testing.T) {
	t.Run("bypass skips limiter", func(t *testing.T) {
		lim := &fakeRateLimiter{}
		m := &senderRateLimitModule{lim: lim}
		sub := &TxSubmission{Txs: []*types.Transaction{signedTxWithChainID(t, big.NewInt(1), 0)}, BypassRateLimit: true}
		require.NoError(t, m.Apply(context.Background(), sub))
		require.Empty(t, lim.keys)
	})

	t.Run("accept consumes per tx", func(t *testing.T) {
		lim := &fakeRateLimiter{}
		m := &senderRateLimitModule{lim: lim}
		sub := &TxSubmission{Txs: []*types.Transaction{
			signedTxWithChainID(t, big.NewInt(1), 0),
			signedTxWithChainID(t, big.NewInt(1), 1),
		}}
		require.NoError(t, m.Apply(context.Background(), sub))
		require.Len(t, lim.keys, 2)
	})

	t.Run("over limit rejects", func(t *testing.T) {
		lim := &fakeRateLimiter{allowFunc: func(string) (bool, error) { return false, nil }}
		m := &senderRateLimitModule{lim: lim}
		sub := &TxSubmission{Txs: []*types.Transaction{signedTxWithChainID(t, big.NewInt(1), 0)}}
		require.ErrorIs(t, m.Apply(context.Background(), sub), ErrOverSenderRateLimit)
	})
}

// TestSenderRateLimitModule_FailsClosedOnLimiterError documents the fail-closed
// contract: a backing-limiter error rejects the submission (ErrInternal), it
// does not silently allow the tx through.
func TestSenderRateLimitModule_FailsClosedOnLimiterError(t *testing.T) {
	lim := &fakeRateLimiter{allowFunc: func(string) (bool, error) { return false, errors.New("limiter unavailable") }}
	m := &senderRateLimitModule{lim: lim}
	sub := &TxSubmission{Txs: []*types.Transaction{signedTxWithChainID(t, big.NewInt(1), 0)}}
	require.ErrorIs(t, m.Apply(context.Background(), sub), ErrInternal)
}

func TestInteropModule_SkipsNonInteropTxs(t *testing.T) {
	strategy := &fakeInteropStrategy{}
	m := &interopModule{strategy: strategy, validatingCfg: InteropValidationConfig{}}

	plainTx := signedTxWithChainID(t, big.NewInt(1), 0)
	interop := interopTx(t, big.NewInt(1), 1)
	sub := &TxSubmission{Txs: []*types.Transaction{plainTx, interop}}

	require.NoError(t, m.Apply(context.Background(), sub))
	require.Equal(t, 1, strategy.calls, "only the interop tx should reach the strategy")

	// Sanity: the plain tx really carries no interop access list.
	require.Empty(t, interoptypes.TxToInteropAccessList(plainTx))
	require.NotEmpty(t, interoptypes.TxToInteropAccessList(interop))
}

func TestInteropModule_RejectsOnStrategyError(t *testing.T) {
	strategy := &fakeInteropStrategy{err: ErrInternal}
	m := &interopModule{strategy: strategy, validatingCfg: InteropValidationConfig{}}
	sub := &TxSubmission{Txs: []*types.Transaction{interopTx(t, big.NewInt(1), 0)}}
	require.ErrorIs(t, m.Apply(context.Background(), sub), ErrInternal)
}

func TestInteropModule_SenderRateLimit(t *testing.T) {
	lim := &fakeRateLimiter{allowFunc: func(string) (bool, error) { return false, nil }}
	m := &interopModule{strategy: &fakeInteropStrategy{}, interopSenderLim: lim, validatingCfg: InteropValidationConfig{}}
	sub := &TxSubmission{Txs: []*types.Transaction{interopTx(t, big.NewInt(1), 0)}}
	require.ErrorIs(t, m.Apply(context.Background(), sub), ErrOverSenderRateLimit)
}

func TestTxMiddlewareModule_RespectsMethodConfig(t *testing.T) {
	called := false
	fn := func(ctx context.Context, endpoint string, payload []byte) (map[string]bool, error) {
		called = true
		return map[string]bool{}, nil
	}
	methods := NewTxValidationMethodSet([]string{"eth_sendRawTransaction"})
	m := &txMiddlewareModule{endpoint: "http://test", fn: fn, failOpen: true, methods: methods}

	t.Run("method not configured is skipped", func(t *testing.T) {
		called = false
		sub := &TxSubmission{Method: "eth_sendBundle", Txs: []*types.Transaction{signedTxWithChainID(t, big.NewInt(1), 0)}}
		require.NoError(t, m.Apply(context.Background(), sub))
		require.False(t, called)
	})

	t.Run("configured method runs validation", func(t *testing.T) {
		called = false
		sub := &TxSubmission{Method: "eth_sendRawTransaction", Txs: []*types.Transaction{signedTxWithChainID(t, big.NewInt(1), 0)}}
		require.NoError(t, m.Apply(context.Background(), sub))
		require.True(t, called)
	})
}
