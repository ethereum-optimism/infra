package proxyd

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

func TestBuildValidationPayload(t *testing.T) {
	tx := createSignedTestTransaction(t)
	txHash := tx.Hash().Hex()

	from, err := getSender(tx)
	require.NoError(t, err)

	txsWithSenders, err := buildTxsWithSenders(context.Background(), []*types.Transaction{tx})
	require.NoError(t, err)

	payload, err := buildValidationPayload(txsWithSenders)
	require.NoError(t, err)

	var result map[string]map[string]interface{}
	err = json.Unmarshal(payload, &result)
	require.NoError(t, err)

	// Verify the tx hash key exists
	txData, ok := result[txHash]
	require.True(t, ok)

	// Verify "from" field is at the same level as other tx fields
	require.Equal(t, from.Hex(), txData["from"])

	// Verify tx fields are at the same level (flattened, not nested under "tx")
	require.NotNil(t, txData["nonce"])
	require.NotNil(t, txData["gas"])
	require.NotNil(t, txData["value"])
	require.NotNil(t, txData["hash"])
	require.NotNil(t, txData["chainId"])
	require.NotNil(t, txData["type"])
	// Signature fields allow deriving sender
	require.NotNil(t, txData["v"])
	require.NotNil(t, txData["r"])
	require.NotNil(t, txData["s"])
}

func TestBuildTxsWithSenders(t *testing.T) {
	tx1 := createSignedTestTransaction(t)
	tx2 := createSignedTestTransaction(t)

	txsWithSenders, err := buildTxsWithSenders(context.Background(), []*types.Transaction{tx1, tx2})
	require.NoError(t, err)
	require.Len(t, txsWithSenders, 2)

	// Verify both tx hashes are keys
	_, ok := txsWithSenders[tx1.Hash().Hex()]
	require.True(t, ok)
	_, ok = txsWithSenders[tx2.Hash().Hex()]
	require.True(t, ok)

	// Verify from addresses and tx fields are populated (flattened structure)
	for _, txData := range txsWithSenders {
		require.NotEmpty(t, txData["from"])
		require.NotNil(t, txData["nonce"])
		require.NotNil(t, txData["hash"])
	}
}

func TestGetSender(t *testing.T) {
	tx := createSignedTestTransaction(t)
	from, err := getSender(tx)
	require.NoError(t, err)
	require.NotEqual(t, common.Address{}, from)
}

func TestTxValidationMethodSet(t *testing.T) {
	methods := NewTxValidationMethodSet([]string{"eth_sendRawTransaction", "eth_sendBundle"})

	require.True(t, methods.Contains("eth_sendRawTransaction"))
	require.True(t, methods.Contains("eth_sendBundle"))
	require.False(t, methods.Contains("eth_getBalance"))
	require.False(t, methods.Contains(""))
}

func TestDefaultTxValidationMethods(t *testing.T) {
	methods := defaultTxValidationMethods()
	require.True(t, methods.Contains("eth_sendRawTransaction"))
	require.True(t, methods.Contains("eth_sendRawTransactionConditional"))
	require.True(t, methods.Contains("eth_sendBundle"))
}

func createSignedTestTransaction(t *testing.T) *types.Transaction {
	privateKey, err := crypto.GenerateKey()
	require.NoError(t, err)

	to := common.HexToAddress("0x0987654321098765432109876543210987654321")
	chainID := big.NewInt(1) // Use mainnet chain ID
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     1,
		GasFeeCap: big.NewInt(1000000000),
		GasTipCap: big.NewInt(1000000000),
		Gas:       21000,
		To:        &to,
		Value:     big.NewInt(1000000000000000000),
		Data:      []byte{},
	})

	signer := types.LatestSignerForChainID(chainID)
	signedTx, err := types.SignTx(tx, signer, privateKey)
	require.NoError(t, err)
	return signedTx
}

func TestValidateTransactions_SingleTx(t *testing.T) {
	tx := createSignedTestTransaction(t)

	validationCalled := false
	mockValidation := func(ctx context.Context, endpoint string, payload []byte) (map[string]bool, error) {
		validationCalled = true
		return map[string]bool{}, nil // empty map = no unauthorized txs
	}

	err := validateTransactions(context.Background(), []*types.Transaction{tx}, "http://test", mockValidation, true)
	require.NoError(t, err)
	require.True(t, validationCalled)
}

func TestValidateTransactions_MultipleTxs(t *testing.T) {
	txs := make([]*types.Transaction, 3)
	for i := 0; i < 3; i++ {
		txs[i] = createSignedTestTransaction(t)
	}

	callCount := 0
	mockValidation := func(ctx context.Context, endpoint string, payload []byte) (map[string]bool, error) {
		callCount++
		// Verify the payload contains all 3 txs (flattened structure)
		var requestMap map[string]map[string]interface{}
		err := json.Unmarshal(payload, &requestMap)
		require.NoError(t, err)
		require.Len(t, requestMap, 3)
		// Verify each tx has "from" at the same level as other fields
		for _, txData := range requestMap {
			require.NotEmpty(t, txData["from"])
			require.NotNil(t, txData["nonce"])
		}
		return map[string]bool{}, nil
	}

	err := validateTransactions(context.Background(), txs, "http://test", mockValidation, true)
	require.NoError(t, err)
	require.Equal(t, 1, callCount) // Should be a single batch call now
}

func TestValidateTransactions_RejectsUnauthorized(t *testing.T) {
	txs := make([]*types.Transaction, 3)
	for i := 0; i < 3; i++ {
		txs[i] = createSignedTestTransaction(t)
	}

	// Mark the second tx as unauthorized
	unauthorizedTxHash := txs[1].Hash().Hex()

	mockValidation := func(ctx context.Context, endpoint string, payload []byte) (map[string]bool, error) {
		return map[string]bool{
			unauthorizedTxHash: true,
		}, nil
	}

	err := validateTransactions(context.Background(), txs, "http://test", mockValidation, true)
	require.Error(t, err)
	require.Equal(t, ErrTransactionRejected, err)
}

func TestValidateTransactions_AllowsAuthorized(t *testing.T) {
	txs := make([]*types.Transaction, 3)
	for i := 0; i < 3; i++ {
		txs[i] = createSignedTestTransaction(t)
	}

	mockValidation := func(ctx context.Context, endpoint string, payload []byte) (map[string]bool, error) {
		// All txs are authorized (false = not unauthorized)
		result := make(map[string]bool)
		for _, tx := range txs {
			result[tx.Hash().Hex()] = false
		}
		return result, nil
	}

	err := validateTransactions(context.Background(), txs, "http://test", mockValidation, true)
	require.NoError(t, err)
}

func TestValidateTransactions_TooManyTxs(t *testing.T) {
	txs := make([]*types.Transaction, maxBundleTransactions+1)
	for i := 0; i <= maxBundleTransactions; i++ {
		txs[i] = createSignedTestTransaction(t)
	}

	mockValidation := func(ctx context.Context, endpoint string, payload []byte) (map[string]bool, error) {
		return map[string]bool{}, nil
	}

	err := validateTransactions(context.Background(), txs, "http://test", mockValidation, true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "maximum allowed")
}

func TestValidateTransactions_ServiceError_AllowsThrough(t *testing.T) {
	tx := createSignedTestTransaction(t)

	mockValidation := func(ctx context.Context, endpoint string, payload []byte) (map[string]bool, error) {
		return nil, errors.New("service unavailable")
	}

	// With failOpen=true, service errors should allow transaction through
	err := validateTransactions(context.Background(), []*types.Transaction{tx}, "http://test", mockValidation, true)
	require.NoError(t, err)
}

func TestValidateTransactions_ServiceError_FailClosed(t *testing.T) {
	tx := createSignedTestTransaction(t)

	mockValidation := func(ctx context.Context, endpoint string, payload []byte) (map[string]bool, error) {
		return nil, errors.New("service unavailable")
	}

	// With failOpen=false, service errors should reject transaction
	err := validateTransactions(context.Background(), []*types.Transaction{tx}, "http://test", mockValidation, false)
	require.Error(t, err)
	require.Equal(t, ErrInternal, err)
}

func TestTxValidationClient_HTTPServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"unauthorized": {}}`))
	}))
	defer server.Close()

	client := NewTxValidationClient(5)
	unauthorized, err := client.Validate(context.Background(), server.URL, []byte(`{}`))
	require.NoError(t, err)
	require.Empty(t, unauthorized)
}

func TestTxValidationClient_UnauthorizedResponse(t *testing.T) {
	txHash := "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"unauthorized": {"` + txHash + `": true}}`))
	}))
	defer server.Close()

	client := NewTxValidationClient(5)
	unauthorized, err := client.Validate(context.Background(), server.URL, []byte(`{}`))
	require.NoError(t, err)
	require.True(t, unauthorized[txHash])
}

func TestTxValidationClient_ErrorResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"unauthorized": {}, "errorCode": "ERR001", "errorMessage": "internal error"}`))
	}))
	defer server.Close()

	client := NewTxValidationClient(5)
	_, err := client.Validate(context.Background(), server.URL, []byte(`{}`))
	require.Error(t, err)
	require.Equal(t, ErrInternal, err)
}

func TestDecodeSignedTx(t *testing.T) {
	tx := createSignedTestTransaction(t)
	txBytes, err := tx.MarshalBinary()
	require.NoError(t, err)

	// Must have 0x prefix for hexutil.Bytes.UnmarshalText
	txHex := "0x" + common.Bytes2Hex(txBytes)

	decoded, err := decodeSignedTx(context.Background(), txHex)
	require.NoError(t, err)
	require.Equal(t, tx.Hash(), decoded.Hash())
}

func TestDecodeSignedTx_InvalidHex(t *testing.T) {
	_, err := decodeSignedTx(context.Background(), "not-valid-hex")
	require.Error(t, err)
}

func TestDecodeSignedTx_Missing0xPrefix(t *testing.T) {
	tx := createSignedTestTransaction(t)
	txBytes, err := tx.MarshalBinary()
	require.NoError(t, err)

	// Without 0x prefix, hexutil.Bytes.UnmarshalText returns error
	txHex := common.Bytes2Hex(txBytes)
	_, err = decodeSignedTx(context.Background(), txHex)
	require.Error(t, err)
}

func TestTxValidationClient_CanceledContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"unauthorized": {}}`))
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	client := NewTxValidationClient(5)
	_, err := client.Validate(ctx, server.URL, []byte(`{}`))
	require.Error(t, err)
	require.True(t, errors.Is(err, context.Canceled))
}

func TestValidateTransactions_CanceledContext(t *testing.T) {
	tx := createSignedTestTransaction(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	mockValidation := func(ctx context.Context, endpoint string, payload []byte) (map[string]bool, error) {
		return nil, ctx.Err()
	}

	// Due to fail-open behavior, validation service errors
	// result in allowing the transaction through, not returning an error
	err := validateTransactions(ctx, []*types.Transaction{tx}, "http://test", mockValidation, true)
	require.NoError(t, err) // Transaction is allowed through
}

func TestTransactionsFromBundleReq(t *testing.T) {
	tx1 := createSignedTestTransaction(t)
	tx2 := createSignedTestTransaction(t)

	tx1Bytes, _ := tx1.MarshalBinary()
	tx2Bytes, _ := tx2.MarshalBinary()

	bundleParams := []interface{}{
		map[string]interface{}{
			"txs": []string{
				"0x" + common.Bytes2Hex(tx1Bytes),
				"0x" + common.Bytes2Hex(tx2Bytes),
			},
		},
	}
	paramsJSON, _ := json.Marshal(bundleParams)

	req := &RPCReq{
		Method: "eth_sendBundle",
		Params: paramsJSON,
	}

	txs, err := transactionsFromBundleReq(context.Background(), req)
	require.NoError(t, err)
	require.Len(t, txs, 2)
}

func TestTransactionsFromBundleReq_EmptyBundle(t *testing.T) {
	bundleParams := []interface{}{
		map[string]interface{}{
			"txs": []string{},
		},
	}
	paramsJSON, _ := json.Marshal(bundleParams)

	req := &RPCReq{
		Method: "eth_sendBundle",
		Params: paramsJSON,
	}

	_, err := transactionsFromBundleReq(context.Background(), req)
	require.Error(t, err)
}
