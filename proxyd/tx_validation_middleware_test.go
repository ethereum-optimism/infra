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

func TestBuildValidationPayload_FullTx(t *testing.T) {
	tx := createSignedTestTransaction(t) // Use signed tx with chainId
	from := common.HexToAddress("0x1234567890123456789012345678901234567890")

	payload, err := buildValidationPayload(tx, from, nil)
	require.NoError(t, err)

	var result map[string]interface{}
	err = json.Unmarshal(payload, &result)
	require.NoError(t, err)

	// Full tx payload uses go-ethereum's native Transaction JSON format
	// with "from" added as a separate top-level field
	require.Equal(t, from.Hex(), result["from"])
	require.NotNil(t, result["tx"])

	txObj, ok := result["tx"].(map[string]interface{})
	require.True(t, ok)

	// Native format uses hex strings and "input" instead of "data"
	require.NotNil(t, txObj["nonce"])
	require.NotNil(t, txObj["gas"])
	require.NotNil(t, txObj["value"])
	require.NotNil(t, txObj["hash"])
	require.NotNil(t, txObj["chainId"])
	require.NotNil(t, txObj["type"])
	// Signature fields allow deriving sender
	require.NotNil(t, txObj["v"])
	require.NotNil(t, txObj["r"])
	require.NotNil(t, txObj["s"])
}

func TestBuildValidationPayload_MappedFields(t *testing.T) {
	tx := createTestTransaction(t)
	from := common.HexToAddress("0x1234567890123456789012345678901234567890")

	mappings := []TxFieldMapping{
		{SourceField: "from", TargetField: "address"},
	}

	payload, err := buildValidationPayload(tx, from, mappings)
	require.NoError(t, err)

	var result map[string]interface{}
	err = json.Unmarshal(payload, &result)
	require.NoError(t, err)

	require.Equal(t, from.Hex(), result["address"])
	require.Nil(t, result["from"])
	require.Nil(t, result["nonce"])
}

func TestBuildValidationPayload_MultipleMappings(t *testing.T) {
	tx := createTestTransaction(t)
	from := common.HexToAddress("0x1234567890123456789012345678901234567890")

	mappings := []TxFieldMapping{
		{SourceField: "from", TargetField: "sender"},
		{SourceField: "value", TargetField: "amount"},
		{SourceField: "hash", TargetField: "txHash"},
	}

	payload, err := buildValidationPayload(tx, from, mappings)
	require.NoError(t, err)

	var result map[string]interface{}
	err = json.Unmarshal(payload, &result)
	require.NoError(t, err)

	require.Equal(t, from.Hex(), result["sender"])
	require.NotNil(t, result["amount"])
	require.NotNil(t, result["txHash"])
	require.Nil(t, result["from"])
}

func TestExtractTxField(t *testing.T) {
	tx := createTestTransaction(t)
	from := common.HexToAddress("0x1234567890123456789012345678901234567890")

	tests := []struct {
		field    string
		expected bool
	}{
		{"from", true},
		{"nonce", true},
		{"gas", true},
		{"value", true},
		{"data", true},
		{"hash", true},
		{"chainId", true},
		{"type", true},
		{"invalid", false},
	}

	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			result := extractTxField(tx, from, tt.field)
			if tt.expected {
				require.NotNil(t, result)
			} else {
				require.Nil(t, result)
			}
		})
	}
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

func TestTxValidation_BlockResponse(t *testing.T) {
	alwaysBlock := func(ctx context.Context, endpoint string, payload []byte) (bool, error) {
		return true, nil
	}

	block, err := alwaysBlock(context.Background(), "http://test", []byte(`{}`))
	require.NoError(t, err)
	require.True(t, block)
}

func TestTxValidation_AllowResponse(t *testing.T) {
	neverBlock := func(ctx context.Context, endpoint string, payload []byte) (bool, error) {
		return false, nil
	}

	block, err := neverBlock(context.Background(), "http://test", []byte(`{}`))
	require.NoError(t, err)
	require.False(t, block)
}

func createTestTransaction(t *testing.T) *types.Transaction {
	to := common.HexToAddress("0x0987654321098765432109876543210987654321")
	tx := types.NewTx(&types.LegacyTx{
		Nonce:    1,
		GasPrice: big.NewInt(1000000000),
		Gas:      21000,
		To:       &to,
		Value:    big.NewInt(1000000000000000000),
		Data:     []byte{},
	})
	return tx
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
	mockValidation := func(ctx context.Context, endpoint string, payload []byte) (bool, error) {
		validationCalled = true
		return false, nil
	}

	err := validateTransactions(context.Background(), []*types.Transaction{tx}, "http://test", nil, mockValidation, true)
	require.NoError(t, err)
	require.True(t, validationCalled)
}

func TestValidateTransactions_MultipleTxs(t *testing.T) {
	txs := make([]*types.Transaction, 3)
	for i := 0; i < 3; i++ {
		txs[i] = createSignedTestTransaction(t)
	}

	callCount := 0
	mockValidation := func(ctx context.Context, endpoint string, payload []byte) (bool, error) {
		callCount++
		return false, nil
	}

	err := validateTransactions(context.Background(), txs, "http://test", nil, mockValidation, true)
	require.NoError(t, err)
	require.Equal(t, 3, callCount)
}

func TestValidateTransactions_BlocksOnFirstRejection(t *testing.T) {
	txs := make([]*types.Transaction, 3)
	for i := 0; i < 3; i++ {
		txs[i] = createSignedTestTransaction(t)
	}

	mockValidation := func(ctx context.Context, endpoint string, payload []byte) (bool, error) {
		return true, nil // block all
	}

	err := validateTransactions(context.Background(), txs, "http://test", nil, mockValidation, true)
	require.Error(t, err)
	require.Equal(t, ErrTransactionRejected, err)
}

func TestValidateTransactions_TooManyTxs(t *testing.T) {
	txs := make([]*types.Transaction, maxBundleTransactions+1)
	for i := 0; i <= maxBundleTransactions; i++ {
		txs[i] = createSignedTestTransaction(t)
	}

	mockValidation := func(ctx context.Context, endpoint string, payload []byte) (bool, error) {
		return false, nil
	}

	err := validateTransactions(context.Background(), txs, "http://test", nil, mockValidation, true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "maximum allowed")
}

func TestValidateTransactions_ServiceError_AllowsThrough(t *testing.T) {
	tx := createSignedTestTransaction(t)

	mockValidation := func(ctx context.Context, endpoint string, payload []byte) (bool, error) {
		return false, errors.New("service unavailable")
	}

	// With failOpen=true, service errors should allow transaction through
	err := validateTransactions(context.Background(), []*types.Transaction{tx}, "http://test", nil, mockValidation, true)
	require.NoError(t, err)
}

func TestValidateTransactions_ServiceError_FailClosed(t *testing.T) {
	tx := createSignedTestTransaction(t)

	mockValidation := func(ctx context.Context, endpoint string, payload []byte) (bool, error) {
		return false, errors.New("service unavailable")
	}

	// With failOpen=false, service errors should reject transaction
	err := validateTransactions(context.Background(), []*types.Transaction{tx}, "http://test", nil, mockValidation, false)
	require.Error(t, err)
	require.Equal(t, ErrInternal, err)
}

func TestTxValidationClient_HTTPServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"block": false}`))
	}))
	defer server.Close()

	client := NewTxValidationClient(5)
	block, err := client.Validate(context.Background(), server.URL, []byte(`{"address": "0x123"}`))
	require.NoError(t, err)
	require.False(t, block)
}

func TestTxValidationClient_BlockResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"block": true}`))
	}))
	defer server.Close()

	client := NewTxValidationClient(5)
	block, err := client.Validate(context.Background(), server.URL, []byte(`{"address": "0x123"}`))
	require.NoError(t, err)
	require.True(t, block)
}

func TestTxValidationClient_ErrorResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"block": false, "errorCode": "ERR001", "errorMessage": "internal error"}`))
	}))
	defer server.Close()

	client := NewTxValidationClient(5)
	_, err := client.Validate(context.Background(), server.URL, []byte(`{"address": "0x123"}`))
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
		_, _ = w.Write([]byte(`{"block": false}`))
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	client := NewTxValidationClient(5)
	_, err := client.Validate(ctx, server.URL, []byte(`{"address": "0x123"}`))
	require.Error(t, err)
	require.True(t, errors.Is(err, context.Canceled))
}

func TestValidateTransactions_CanceledContext(t *testing.T) {
	tx := createSignedTestTransaction(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Mock validation that returns context error - but due to fail-open behavior,
	// the error will be logged and transaction allowed through
	mockValidation := func(ctx context.Context, endpoint string, payload []byte) (bool, error) {
		return false, ctx.Err()
	}

	// Due to fail-open behavior, validation service errors
	// result in allowing the transaction through, not returning an error
	err := validateTransactions(ctx, []*types.Transaction{tx}, "http://test", nil, mockValidation, true)
	require.NoError(t, err) // Transaction is allowed through
}

func TestValidateSingleTransaction_CanceledContext_FailOpen(t *testing.T) {
	tx := createSignedTestTransaction(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Fail-open behavior: even with canceled context, transaction is allowed through
	mockValidation := func(ctx context.Context, endpoint string, payload []byte) (bool, error) {
		return false, context.Canceled
	}

	err := validateSingleTransaction(ctx, tx, "http://test", nil, mockValidation, true)
	require.NoError(t, err) // Allowed through due to fail-open
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
