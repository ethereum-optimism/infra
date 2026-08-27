package proxyd

import (
	"encoding/json"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

// buildRawTx returns a signed tx's raw binary encoding and its hash.
func buildRawTx(t *testing.T, nonce uint64) (hexutil.Bytes, common.Hash) {
	t.Helper()
	key, err := crypto.GenerateKey()
	require.NoError(t, err)
	to := common.HexToAddress("0x1111111111111111111111111111111111111111")
	tx := types.MustSignNewTx(key, types.LatestSignerForChainID(big.NewInt(901)), &types.DynamicFeeTx{
		ChainID: big.NewInt(901), Nonce: nonce, To: &to,
		Gas: 21000, GasFeeCap: big.NewInt(1e9), GasTipCap: big.NewInt(1e9),
	})
	raw, err := tx.MarshalBinary()
	require.NoError(t, err)
	return raw, tx.Hash()
}

func TestParseSubblockTxHashes(t *testing.T) {
	raw1, h1 := buildRawTx(t, 0)
	raw2, h2 := buildRawTx(t, 1)
	payload := map[string]any{
		"payload_id": "0x0000000000000001",
		"index":      1, // index>0: no "base" field — must still parse
		"diff": map[string]any{
			"transactions": []hexutil.Bytes{raw1, raw2},
		},
		"metadata": map[string]any{},
	}
	msg, err := json.Marshal(payload)
	require.NoError(t, err)

	hashes, err := parseSubblockTxHashes(msg)
	require.NoError(t, err)
	require.Equal(t, []common.Hash{h1, h2}, hashes)
}

func TestParseSubblockTxHashesSkipsGarbageTx(t *testing.T) {
	raw1, h1 := buildRawTx(t, 0)
	msg := []byte(`{"index":0,"diff":{"transactions":["0xdeadbeef","` + raw1.String() + `"]}}`)
	hashes, err := parseSubblockTxHashes(msg)
	require.NoError(t, err)
	require.Equal(t, []common.Hash{h1}, hashes, "undecodable txs are skipped, not fatal")
}

func TestParseSubblockTxHashesBadJSON(t *testing.T) {
	_, err := parseSubblockTxHashes([]byte("not json"))
	require.Error(t, err)
}
