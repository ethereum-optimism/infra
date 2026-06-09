package proxyd

import (
	"context"
	"encoding/json"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

// recordingModule records whether Apply was invoked and optionally rejects.
type recordingModule struct {
	name       string
	called     *[]string
	rejectWith error
}

func (m *recordingModule) Name() string { return m.name }

func (m *recordingModule) Apply(ctx context.Context, sub *TxSubmission) error {
	*m.called = append(*m.called, m.name)
	return m.rejectWith
}

func signedTxWithChainID(t *testing.T, chainID *big.Int, nonce uint64) *types.Transaction {
	t.Helper()
	key, err := crypto.GenerateKey()
	require.NoError(t, err)
	to := common.HexToAddress("0x0987654321098765432109876543210987654321")
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     nonce,
		GasFeeCap: big.NewInt(1000000000),
		GasTipCap: big.NewInt(1000000000),
		Gas:       21000,
		To:        &to,
		Value:     big.NewInt(1),
	})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(chainID), key)
	require.NoError(t, err)
	return signed
}

func rawTxReq(t *testing.T, tx *types.Transaction) *RPCReq {
	t.Helper()
	raw, err := tx.MarshalBinary()
	require.NoError(t, err)
	params, err := json.Marshal([]any{hexutil.Encode(raw)})
	require.NoError(t, err)
	return &RPCReq{Method: "eth_sendRawTransaction", Params: params, ID: json.RawMessage("1")}
}

func bundleReq(t *testing.T, txs ...*types.Transaction) *RPCReq {
	t.Helper()
	hexes := make([]string, len(txs))
	for i, tx := range txs {
		raw, err := tx.MarshalBinary()
		require.NoError(t, err)
		hexes[i] = hexutil.Encode(raw)
	}
	bundle, err := json.Marshal([]any{map[string]any{"txs": hexes}})
	require.NoError(t, err)
	return &RPCReq{Method: "eth_sendBundle", Params: bundle, ID: json.RawMessage("1")}
}

func TestTxFilter_Apply_AllAcceptPasses(t *testing.T) {
	var called []string
	f := NewTxFilter(nil,
		&recordingModule{name: "a", called: &called},
		&recordingModule{name: "b", called: &called},
	)
	require.NoError(t, f.Apply(context.Background(), &TxSubmission{}))
	require.Equal(t, []string{"a", "b"}, called)
}

func TestTxFilter_Apply_FirstRejectShortCircuits(t *testing.T) {
	var called []string
	rejectErr := ErrInternal
	f := NewTxFilter(nil,
		&recordingModule{name: "a", called: &called},
		&recordingModule{name: "b", called: &called, rejectWith: rejectErr},
		&recordingModule{name: "c", called: &called},
	)
	err := f.Apply(context.Background(), &TxSubmission{})
	require.ErrorIs(t, err, rejectErr)
	require.Equal(t, []string{"a", "b"}, called, "modules after the rejecter must not run")
}

func TestTxFilter_Build_SingleTx(t *testing.T) {
	srv := &Server{}
	f := NewTxFilter(srv.convertSendReqToSendTx)
	tx := signedTxWithChainID(t, big.NewInt(1), 0)
	sub, err := f.Build(context.Background(), rawTxReq(t, tx), false)
	require.NoError(t, err)
	require.Len(t, sub.Txs, 1)
	require.Equal(t, tx.Hash(), sub.Txs[0].Hash())
}

func TestTxFilter_Build_Bundle(t *testing.T) {
	srv := &Server{}
	f := NewTxFilter(srv.convertSendReqToSendTx)
	tx1 := signedTxWithChainID(t, big.NewInt(1), 0)
	tx2 := signedTxWithChainID(t, big.NewInt(1), 1)
	sub, err := f.Build(context.Background(), bundleReq(t, tx1, tx2), false)
	require.NoError(t, err)
	require.Len(t, sub.Txs, 2)
}

func TestTxFilter_Build_DecodeErrors(t *testing.T) {
	srv := &Server{}
	f := NewTxFilter(srv.convertSendReqToSendTx)

	t.Run("bad json", func(t *testing.T) {
		req := &RPCReq{Method: "eth_sendRawTransaction", Params: json.RawMessage(`not-json`), ID: json.RawMessage("1")}
		_, err := f.Build(context.Background(), req, false)
		require.ErrorIs(t, err, ErrParseErr)
	})

	t.Run("wrong arg count", func(t *testing.T) {
		params, _ := json.Marshal([]any{})
		req := &RPCReq{Method: "eth_sendRawTransaction", Params: params, ID: json.RawMessage("1")}
		_, err := f.Build(context.Background(), req, false)
		var rpcErr *RPCErr
		require.ErrorAs(t, err, &rpcErr)
		require.Equal(t, ErrInvalidParams("missing value for required argument 0").Code, rpcErr.Code)
	})

	t.Run("bad hex", func(t *testing.T) {
		params, _ := json.Marshal([]any{"0xZZ"})
		req := &RPCReq{Method: "eth_sendRawTransaction", Params: params, ID: json.RawMessage("1")}
		_, err := f.Build(context.Background(), req, false)
		var rpcErr *RPCErr
		require.ErrorAs(t, err, &rpcErr)
		require.Equal(t, ErrInvalidParams("").Code, rpcErr.Code)
	})

	t.Run("empty bundle", func(t *testing.T) {
		bundle, _ := json.Marshal([]any{map[string]any{"txs": []string{}}})
		req := &RPCReq{Method: "eth_sendBundle", Params: bundle, ID: json.RawMessage("1")}
		_, err := f.Build(context.Background(), req, false)
		require.EqualError(t, err, ErrInvalidParams("bundle has no txs").Error())
	})
}

func TestTxFilter_Build_BundleSizeCap(t *testing.T) {
	srv := &Server{}
	f := NewTxFilter(srv.convertSendReqToSendTx)
	txs := make([]*types.Transaction, maxBundleTransactions+1)
	for i := range txs {
		txs[i] = signedTxWithChainID(t, big.NewInt(1), uint64(i))
	}
	_, err := f.Build(context.Background(), bundleReq(t, txs...), false)
	var rpcErr *RPCErr
	require.ErrorAs(t, err, &rpcErr)
	require.Equal(t, ErrInvalidParams("").Code, rpcErr.Code)
	require.Contains(t, rpcErr.Message, "maximum allowed")
}

// countingSigner-style memoization check: Sender recovers each index at most once.
func TestTxSubmission_Sender_MemoizedOnce(t *testing.T) {
	tx := signedTxWithChainID(t, big.NewInt(1), 0)
	sub := &TxSubmission{Txs: []*types.Transaction{tx}}

	from1, err := sub.Sender(0)
	require.NoError(t, err)
	require.True(t, sub.recovered[0])

	from2, err := sub.Sender(0)
	require.NoError(t, err)
	require.Equal(t, from1, from2)
}

func TestTxSubmission_Sender_RecoveryError(t *testing.T) {
	// A transaction with a zero/invalid signature fails ecrecover.
	to := common.HexToAddress("0x0987654321098765432109876543210987654321")
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   big.NewInt(1),
		Nonce:     0,
		GasFeeCap: big.NewInt(1),
		GasTipCap: big.NewInt(1),
		Gas:       21000,
		To:        &to,
		Value:     big.NewInt(1),
		V:         big.NewInt(0),
		R:         big.NewInt(0),
		S:         big.NewInt(0),
	})
	sub := &TxSubmission{Txs: []*types.Transaction{tx}}
	_, err := sub.Sender(0)
	require.Error(t, err)
}

func TestTxFilter_ModuleOrder(t *testing.T) {
	moduleNames := func(modules []TxFilterModule) []string {
		names := make([]string, len(modules))
		for i, m := range modules {
			names[i] = m.Name()
		}
		return names
	}

	t.Run("all modules enabled", func(t *testing.T) {
		srv := &Server{
			allowedChainIds:    []*big.Int{big.NewInt(1)},
			senderLim:          &fakeRateLimiter{},
			interopStrategy:    &fakeInteropStrategy{},
			interopSenderLim:   &fakeRateLimiter{},
			enableTxValidation: true,
		}
		require.Equal(t,
			[]string{"chain_id", "sender_rate_limit", "interop", "tx_middleware"},
			moduleNames(srv.txFilterModules()),
		)
	})

	t.Run("rate limiter and middleware disabled", func(t *testing.T) {
		srv := &Server{
			allowedChainIds:    []*big.Int{big.NewInt(1)},
			senderLim:          nil,
			interopStrategy:    &fakeInteropStrategy{},
			enableTxValidation: false,
		}
		require.Equal(t,
			[]string{"chain_id", "interop"},
			moduleNames(srv.txFilterModules()),
		)
	})
}
