package integration_tests

import (
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"os"
	"testing"

	"github.com/ethereum-optimism/infra/proxyd"
	interopErrors "github.com/ethereum-optimism/optimism/op-core/interop"
	"github.com/ethereum-optimism/optimism/op-core/interop/messages"
	"github.com/ethereum-optimism/optimism/op-service/eth"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	"github.com/stretchr/testify/require"
)

const filterChainID = 420120003

func newSigner(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := crypto.GenerateKey()
	require.NoError(t, err)
	return key
}

// interopAccessListEntries builds a parseable interop access list so the
// interop module reaches the strategy (rather than failing to parse).
func interopAccessListEntries() []common.Hash {
	checksumArgs := messages.ChecksumArgs{
		BlockNumber: 3519561,
		Timestamp:   1746536469,
		LogIndex:    1,
		ChainID:     eth.ChainIDFromUInt64(filterChainID),
		LogHash: messages.PayloadHashToLogHash(
			crypto.Keccak256Hash([]byte("Hello, World!")),
			common.HexToAddress("0x7A23c3fC3dA9a5364b97E0e4c47E7777BaE5C8Cd"),
		),
	}
	return messages.EncodeAccessList([]messages.Access{checksumArgs.Access()})
}

// filterInteropTx builds a signed tx carrying an interop access list, so the
// filter's interop module validates it against the interop-filter backend.
func filterInteropTx(t *testing.T, key *ecdsa.PrivateKey, chainID int64, nonce uint64) *types.Transaction {
	t.Helper()
	to := common.HexToAddress("0x8f3Ddd0FBf3e78CA1D6cd17379eD88E261249B53")
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   big.NewInt(chainID),
		Nonce:     nonce,
		GasFeeCap: big.NewInt(1000000000),
		GasTipCap: big.NewInt(1000000000),
		Gas:       21000,
		To:        &to,
		Value:     big.NewInt(0),
		AccessList: types.AccessList{
			{
				Address:     params.InteropCrossL2InboxAddress,
				StorageKeys: interopAccessListEntries(),
			},
		},
	})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(big.NewInt(chainID)), key)
	require.NoError(t, err)
	return signed
}

// filterPlainTx builds a signed non-interop tx (no access list).
func filterPlainTx(t *testing.T, key *ecdsa.PrivateKey, chainID int64, nonce uint64) *types.Transaction {
	t.Helper()
	to := common.HexToAddress("0x0987654321098765432109876543210987654321")
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   big.NewInt(chainID),
		Nonce:     nonce,
		GasFeeCap: big.NewInt(1000000000),
		GasTipCap: big.NewInt(1000000000),
		Gas:       21000,
		To:        &to,
		Value:     big.NewInt(1),
	})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(big.NewInt(chainID)), key)
	require.NoError(t, err)
	return signed
}

func txHex(t *testing.T, tx *types.Transaction) string {
	t.Helper()
	raw, err := tx.MarshalBinary()
	require.NoError(t, err)
	return hexutil.Encode(raw)
}

func makeSendBundle(t *testing.T, txs ...*types.Transaction) []byte {
	t.Helper()
	hexes := make([]string, len(txs))
	for i, tx := range txs {
		hexes[i] = txHex(t, tx)
	}
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "eth_sendBundle",
		"params":  []any{map[string]any{"txs": hexes}},
		"id":      1,
	})
	require.NoError(t, err)
	return body
}

func startFilterProxyd(t *testing.T, config *proxyd.Config) func() {
	t.Helper()
	_, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)
	return shutdown
}

// TestTxFilter_Bundle_InteropEnforced proves the bundle->interop gap is closed:
// a bundle with one interop tx the filter rejects is rejected whole, and the
// backend is never called. (B1)
func TestTxFilter_Bundle_InteropEnforced(t *testing.T) {
	goodBackend := NewMockBackend(SingleResponseHandler(200, dummyHealthyRes))
	defer goodBackend.Close()
	require.NoError(t, os.Setenv("GOOD_BACKEND_RPC_URL", goodBackend.URL()))

	errResp := fmt.Sprintf(errResTmpl, -32000, interopErrors.ErrConflict.Error())
	rejectingFilter := NewMockBackend(SingleResponseHandler(409, errResp))
	defer rejectingFilter.Close()

	config := ReadConfig("tx_filter")
	config.InteropValidationConfig.Urls = []string{rejectingFilter.URL()}

	shutdown := startFilterProxyd(t, config)
	defer shutdown()

	client := NewProxydClient("http://127.0.0.1:8545")
	key := newSigner(t)
	bundle := makeSendBundle(t,
		filterPlainTx(t, key, filterChainID, 0),
		filterInteropTx(t, key, filterChainID, 1),
	)
	resp, _, err := client.SendRequest(bundle)
	require.NoError(t, err)
	require.Contains(t, string(resp), interopErrors.ErrConflict.Error(), "bundle rejected by interop: %s", string(resp))
	require.Empty(t, goodBackend.Requests(), "rejected bundle must not reach the backend")
}

// TestTxFilter_Bundle_InteropEnforced_MiddlewareDisabled proves interop runs on
// bundles even with the tx-validation middleware disabled, and that the bundle
// size cap is enforced regardless. (B1 + cap)
func TestTxFilter_Bundle_InteropEnforced_MiddlewareDisabled(t *testing.T) {
	goodBackend := NewMockBackend(SingleResponseHandler(200, dummyHealthyRes))
	defer goodBackend.Close()
	require.NoError(t, os.Setenv("GOOD_BACKEND_RPC_URL", goodBackend.URL()))

	errResp := fmt.Sprintf(errResTmpl, -32000, interopErrors.ErrConflict.Error())
	rejectingFilter := NewMockBackend(SingleResponseHandler(409, errResp))
	defer rejectingFilter.Close()

	config := ReadConfig("tx_filter")
	config.InteropValidationConfig.Urls = []string{rejectingFilter.URL()}
	require.False(t, config.TxValidationMiddlewareConfig.Enabled, "middleware must be disabled in this config")

	shutdown := startFilterProxyd(t, config)
	defer shutdown()

	client := NewProxydClient("http://127.0.0.1:8545")
	key := newSigner(t)

	// Interop still enforced with middleware off.
	bundle := makeSendBundle(t, filterInteropTx(t, key, filterChainID, 0))
	resp, _, err := client.SendRequest(bundle)
	require.NoError(t, err)
	require.Contains(t, string(resp), interopErrors.ErrConflict.Error(), "interop still enforced with middleware off: %s", string(resp))
	require.Empty(t, goodBackend.Requests())

	// Bundle over the size cap is rejected even with middleware off.
	bigTxs := make([]*types.Transaction, 101)
	for i := range bigTxs {
		bigTxs[i] = filterPlainTx(t, key, filterChainID, uint64(i))
	}
	resp, _, err = client.SendRequest(makeSendBundle(t, bigTxs...))
	require.NoError(t, err)
	require.Contains(t, string(resp), "maximum allowed")
	require.Empty(t, goodBackend.Requests())
}

// TestTxFilter_Bundle_ChainIDAndSenderRateLimit proves chain-ID and sender
// rate-limiting apply per-bundle-tx. (B2)
func TestTxFilter_Bundle_ChainIDAndSenderRateLimit(t *testing.T) {
	goodBackend := NewMockBackend(SingleResponseHandler(200, dummyHealthyRes))
	defer goodBackend.Close()
	require.NoError(t, os.Setenv("GOOD_BACKEND_RPC_URL", goodBackend.URL()))

	goodFilter := NewMockBackend(SingleResponseHandler(200, dummyHealthyRes))
	defer goodFilter.Close()

	config := ReadConfig("tx_filter")
	config.InteropValidationConfig.Urls = []string{goodFilter.URL()}

	t.Run("wrong chain id in bundle rejected", func(t *testing.T) {
		shutdown := startFilterProxyd(t, config)
		defer shutdown()
		client := NewProxydClient("http://127.0.0.1:8545")
		key := newSigner(t)
		bundle := makeSendBundle(t,
			filterPlainTx(t, key, filterChainID, 0),
			filterPlainTx(t, key, 9999, 1), // not in allowed_chain_ids
		)
		resp, _, err := client.SendRequest(bundle)
		require.NoError(t, err)
		require.Contains(t, string(resp), "invalid sender", "wrong-chain bundle must be rejected: %s", string(resp))
		require.Empty(t, goodBackend.Requests())
	})

	t.Run("per-tx sender rate limit applies across bundle", func(t *testing.T) {
		rlConfig := ReadConfig("tx_filter")
		rlConfig.InteropValidationConfig.Urls = []string{goodFilter.URL()}
		rlConfig.SenderRateLimit.Enabled = true
		rlConfig.SenderRateLimit.Limit = 1
		shutdown := startFilterProxyd(t, rlConfig)
		defer shutdown()
		client := NewProxydClient("http://127.0.0.1:8545")
		key := newSigner(t)
		// Same sender:nonce twice in one bundle, limit of 1 -> the second tx
		// trips the per-sender:nonce limit and the whole bundle is rejected.
		dup := filterPlainTx(t, key, filterChainID, 0)
		bundle := makeSendBundle(t, dup, dup)
		resp, _, err := client.SendRequest(bundle)
		require.NoError(t, err)
		require.Contains(t, string(resp), "sender is over rate limit", "second tx in bundle over sender limit: %s", string(resp))
	})
}

// TestTxFilter_Order_ShortCircuit proves a chain-ID failure short-circuits the
// pipeline: interop is never consulted, so the interop backend gets no request.
func TestTxFilter_Order_ShortCircuit(t *testing.T) {
	goodBackend := NewMockBackend(SingleResponseHandler(200, dummyHealthyRes))
	defer goodBackend.Close()
	require.NoError(t, os.Setenv("GOOD_BACKEND_RPC_URL", goodBackend.URL()))

	interopFilter := NewMockBackend(SingleResponseHandler(200, dummyHealthyRes))
	defer interopFilter.Close()

	config := ReadConfig("tx_filter")
	config.InteropValidationConfig.Urls = []string{interopFilter.URL()}

	shutdown := startFilterProxyd(t, config)
	defer shutdown()

	client := NewProxydClient("http://127.0.0.1:8545")
	key := newSigner(t)
	// Interop tx but on a disallowed chain id: chain-ID module rejects first.
	tx := filterInteropTx(t, key, 9999, 0)
	resp, _, err := client.SendRequest(makeSendRawTransaction(txHex(t, tx)))
	require.NoError(t, err)
	require.Contains(t, string(resp), "invalid sender", "chain-id rejection expected: %s", string(resp))
	require.Empty(t, interopFilter.Requests(), "interop must not run after chain-id reject")
	require.Empty(t, goodBackend.Requests())
}

// TestTxFilter_ChainIDEnforcedWithoutRateLimiter proves chain-ID enforcement is
// independent of the sender rate limiter. (B3)
func TestTxFilter_ChainIDEnforcedWithoutRateLimiter(t *testing.T) {
	goodBackend := NewMockBackend(SingleResponseHandler(200, dummyHealthyRes))
	defer goodBackend.Close()
	require.NoError(t, os.Setenv("GOOD_BACKEND_RPC_URL", goodBackend.URL()))

	goodFilter := NewMockBackend(SingleResponseHandler(200, dummyHealthyRes))
	defer goodFilter.Close()

	config := ReadConfig("tx_filter")
	config.InteropValidationConfig.Urls = []string{goodFilter.URL()}
	// Disable BOTH rate limiters; chain-ID must still be enforced.
	config.SenderRateLimit.Enabled = false
	config.InteropValidationConfig.RateLimit.Enabled = false
	require.NotEmpty(t, config.SenderRateLimit.AllowedChainIds)

	shutdown := startFilterProxyd(t, config)
	defer shutdown()

	client := NewProxydClient("http://127.0.0.1:8545")
	key := newSigner(t)
	tx := filterPlainTx(t, key, 9999, 0) // disallowed chain id
	resp, _, err := client.SendRequest(makeSendRawTransaction(txHex(t, tx)))
	require.NoError(t, err)
	require.Contains(t, string(resp), "invalid sender", "wrong-chain tx must be rejected with rate limiters off: %s", string(resp))
	require.Empty(t, goodBackend.Requests())
}

// TestTxFilter_Middleware_RespectsMethodConfig proves the configurable
// tx_validation.methods set still gates the middleware module: a method not in
// the set skips the middleware but still runs interop/chain-ID. This is the
// first server-path integration test for the middleware.
func TestTxFilter_Middleware_RespectsMethodConfig(t *testing.T) {
	goodBackend := NewMockBackend(SingleResponseHandler(200, dummyHealthyRes))
	defer goodBackend.Close()
	require.NoError(t, os.Setenv("GOOD_BACKEND_RPC_URL", goodBackend.URL()))

	goodFilter := NewMockBackend(SingleResponseHandler(200, dummyHealthyRes))
	defer goodFilter.Close()

	// Middleware that rejects everything it sees.
	middleware := NewMockBackend(SingleResponseHandler(200, `{"unauthorized":{}}`))
	defer middleware.Close()

	config := ReadConfig("tx_filter")
	config.InteropValidationConfig.Urls = []string{goodFilter.URL()}
	config.InteropValidationConfig.RateLimit.Limit = math.MaxInt
	config.TxValidationMiddlewareConfig.Enabled = true
	config.TxValidationMiddlewareConfig.Endpoint = middleware.URL()
	// Only gate eth_sendRawTransaction; eth_sendBundle should skip middleware.
	config.TxValidationMiddlewareConfig.Methods = []string{"eth_sendRawTransaction"}

	shutdown := startFilterProxyd(t, config)
	defer shutdown()

	client := NewProxydClient("http://127.0.0.1:8545")
	key := newSigner(t)

	// eth_sendBundle is not in Methods -> middleware skipped -> reaches backend.
	bundle := makeSendBundle(t, filterPlainTx(t, key, filterChainID, 0))
	resp, code, err := client.SendRequest(bundle)
	require.NoError(t, err)
	require.Equal(t, 200, code, "bundle should skip middleware and forward: %s", string(resp))
	require.Empty(t, middleware.Requests(), "middleware must not be consulted for a non-configured method")
	require.NotEmpty(t, goodBackend.Requests())
}
