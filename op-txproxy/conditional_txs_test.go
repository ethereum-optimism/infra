package op_txproxy

import (
	"context"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ethereum-optimism/optimism/op-core/predeploys"
	"github.com/ethereum-optimism/optimism/op-service/jsonrpc"
	"github.com/ethereum-optimism/optimism/op-service/metrics"
	"github.com/ethereum-optimism/optimism/op-service/testlog"

	"github.com/ethereum-optimism/optimism/op-service/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/rpc"

	"github.com/stretchr/testify/require"
)

type testBackend struct{}

func (b *testBackend) SendRawTransactionConditional(ctx context.Context, txBytes hexutil.Bytes, cond TransactionConditional) (common.Hash, error) {
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
	hash, err := svc.SendRawTransactionConditional(context.Background(), nil, TransactionConditional{})
	require.Zero(t, hash)
	require.Equal(t, endpointDisabledErr, err)
}

func TestSendRawTransactionConditionalInvalidTxTarget(t *testing.T) {
	svc := setupSvc(t)

	txBytes, err := rlp.EncodeToBytes(types.NewTransaction(0, common.Address{19: 1}, big.NewInt(0), 0, big.NewInt(0), nil))
	require.NoError(t, err)

	// setup auth
	hash, err := svc.SendRawTransactionConditional(context.Background(), txBytes, TransactionConditional{})
	require.Zero(t, hash)
	require.Equal(t, entrypointSupportErr, err)
}

func TestSendRawTransactionConditionals(t *testing.T) {
	costExcessiveCond := TransactionConditional{KnownAccounts: make(KnownAccounts)}
	for i := 0; i < (maxConditionalCost + 1); i++ {
		iBig := big.NewInt(int64(i))
		root := common.BigToHash(iBig)
		costExcessiveCond.KnownAccounts[common.BigToAddress(iBig)] = KnownAccount{StorageRoot: &root}
	}

	uint64Ptr := func(num uint64) *uint64 { return &num }
	tests := []struct {
		name     string
		cond     TransactionConditional
		mustFail bool
	}{

		{
			name:     "passes",
			cond:     TransactionConditional{BlockNumberMin: big.NewInt(1), BlockNumberMax: big.NewInt(2), TimestampMin: uint64Ptr(1), TimestampMax: uint64Ptr(2)},
			mustFail: false,
		},
		{
			name:     "validation. block min greater than max",
			cond:     TransactionConditional{BlockNumberMin: big.NewInt(2), BlockNumberMax: big.NewInt(1)},
			mustFail: true,
		},
		{
			name:     "validation. timestamp min greater than max",
			cond:     TransactionConditional{TimestampMin: uint64Ptr(2), TimestampMax: uint64Ptr(1)},
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
		_, err := svc.SendRawTransactionConditional(context.Background(), txBytes, test.cond)
		if test.mustFail && err == nil {
			t.Errorf("Test %s should fail", test.name)
		}
		if !test.mustFail && err != nil {
			t.Errorf("Test %s should pass but got err: %v", test.name, err)
		}
	}
}

// The JSON-RPC errors are part of this service's wire format; the codes are op-geth's.
func TestConditionalErrors(t *testing.T) {
	tests := []struct {
		err     *jsonrpc.Error
		code    int
		message string
	}{
		{rateLimitErr, -32005, "rate limited"},
		{endpointDisabledErr, -32003, "endpoint disabled"},
		{entrypointSupportErr, -32003, "only 4337 Entrypoint contract support"},
		{failedValidationErr, -32003, "failed conditional validation"},
		{maxCostExceededErr, -32003, "max cost exceeded"},
	}
	for _, test := range tests {
		require.Equal(t, test.code, test.err.ErrorCode(), test.message)
		require.Equal(t, test.message, test.err.Error())
	}
}

// A served error reaches the client with its code and message.
func TestSendRawTransactionConditionalErrorOverRPC(t *testing.T) {
	svc := setupSvc(t)
	svc.cfg.SendRawTransactionConditionalEnabled = false

	srv := rpc.NewServer()
	t.Cleanup(srv.Stop)
	require.NoError(t, srv.RegisterName("eth", svc))
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	req := `{"jsonrpc":"2.0","id":1,"method":"eth_sendRawTransactionConditional","params":["0x",{}]}`
	resp, err := http.Post(httpSrv.URL, "application/json", strings.NewReader(req))
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.JSONEq(t, `{"jsonrpc":"2.0","id":1,"error":{"code":-32003,"message":"endpoint disabled"}}`, string(body))
}
