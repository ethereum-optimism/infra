package op_txproxy

import (
	"context"
	"math/big"
	"net/http/httptest"
	"testing"

	"github.com/ethereum-optimism/optimism/op-service/metrics"
	"github.com/ethereum-optimism/optimism/op-service/predeploys"
	"github.com/ethereum-optimism/optimism/op-service/testlog"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/rpc"

	"github.com/stretchr/testify/require"
)

type testBackend struct{}

func (b *testBackend) SendRawTransactionConditional(ctx context.Context, txBytes hexutil.Bytes, cond types.TransactionConditional) (common.Hash, error) {
	return common.Hash{}, nil
}

func setupSvc(t *testing.T) *ConditionalTxService {
	// setup no-op backend
	srv := rpc.NewServer()
	t.Cleanup(func() { srv.Stop() })
	require.NoError(t, srv.RegisterName("eth", new(testBackend)))

	httpSrv := httptest.NewServer(srv)
	t.Cleanup(func() { httpSrv.Close() })

	log := testlog.Logger(t, log.LevelInfo)
	cfg := &CLIConfig{
		SendRawTransactionConditionalEnabled:   true,
		SendRawTransactionConditionalBackend:   httpSrv.URL,
		SendRawTransactionConditionalRateLimit: 10_000,
	}

	svc, err := NewConditionalTxService(context.Background(), log, metrics.With(metrics.NewRegistry()), cfg)
	require.NoError(t, err)

	return svc
}

func TestSendRawTransactionConditionalDisabled(t *testing.T) {
	svc := setupSvc(t)
	svc.cfg.SendRawTransactionConditionalEnabled = false
	hash, err := svc.SendRawTransactionConditional(context.Background(), nil, types.TransactionConditional{})
	require.Zero(t, hash)
	require.Equal(t, endpointDisabledErr, err)
}

func TestSendRawTransactionConditionalMissingAuth(t *testing.T) {
	svc := setupSvc(t)

	tx := types.NewTransaction(0, predeploys.EntryPoint_v060Addr, big.NewInt(0), 0, big.NewInt(0), nil)
	txBytes, err := rlp.EncodeToBytes(tx)
	require.NoError(t, err)

	// See Issue: https://github.com/ethereum-optimism/infra/issues/68.
	// We'll be re-enforcing authentcation when fixed
	hash, err := svc.SendRawTransactionConditional(context.Background(), txBytes, types.TransactionConditional{})
	require.Equal(t, hash, tx.Hash())
	require.Nil(t, err)
}

func TestSendRawTransactionConditionalInvalidTxTarget(t *testing.T) {
	svc := setupSvc(t)

	txBytes, err := rlp.EncodeToBytes(types.NewTransaction(0, common.Address{19: 1}, big.NewInt(0), 0, big.NewInt(0), nil))
	require.NoError(t, err)

	// setup auth
	ctx := context.WithValue(context.Background(), authContextKey{}, &AuthContext{Caller: common.HexToAddress("0xa")})
	hash, err := svc.SendRawTransactionConditional(ctx, txBytes, types.TransactionConditional{})
	require.Zero(t, hash)
	require.Equal(t, entrypointSupportErr, err)
}

func TestSendRawTransactionConditionals(t *testing.T) {
	costExcessiveCond := types.TransactionConditional{KnownAccounts: make(types.KnownAccounts)}
	for i := 0; i < (params.TransactionConditionalMaxCost + 1); i++ {
		iBig := big.NewInt(int64(i))
		root := common.BigToHash(iBig)
		costExcessiveCond.KnownAccounts[common.BigToAddress(iBig)] = types.KnownAccount{StorageRoot: &root}
	}

	uint64Ptr := func(num uint64) *uint64 { return &num }
	tests := []struct {
		name     string
		cond     types.TransactionConditional
		mustFail bool
	}{

		{
			name:     "passes",
			cond:     types.TransactionConditional{BlockNumberMin: big.NewInt(1), BlockNumberMax: big.NewInt(2), TimestampMin: uint64Ptr(1), TimestampMax: uint64Ptr(2)},
			mustFail: false,
		},
		{
			name:     "validation. block min greater than max",
			cond:     types.TransactionConditional{BlockNumberMin: big.NewInt(2), BlockNumberMax: big.NewInt(1)},
			mustFail: true,
		},
		{
			name:     "validation. timestamp min greater than max",
			cond:     types.TransactionConditional{TimestampMin: uint64Ptr(2), TimestampMax: uint64Ptr(1)},
			mustFail: true,
		},
		{
			name:     "excessive cost",
			cond:     costExcessiveCond,
			mustFail: true,
		},
	}

	svc := setupSvc(t)
	txBytes, err := rlp.EncodeToBytes(types.NewTransaction(0, predeploys.EntryPoint_v060Addr, big.NewInt(0), 0, big.NewInt(0), nil))
	require.NoError(t, err)

	for _, test := range tests {
		ctx := context.Background()
		if !test.mustFail {
			ctx = context.WithValue(ctx, authContextKey{}, &AuthContext{Caller: common.HexToAddress("0xa")})
		}

		_, err := svc.SendRawTransactionConditional(ctx, txBytes, test.cond)
		if test.mustFail && err == nil {
			t.Errorf("Test %s should fail", test.name)
		}
		if !test.mustFail && err != nil {
			t.Errorf("Test %s should pass but got err: %v", test.name, err)
		}
	}
}
