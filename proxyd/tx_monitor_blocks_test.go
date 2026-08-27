package proxyd

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestParseRPCBlockResult(t *testing.T) {
	h1 := common.HexToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	result := map[string]interface{}{
		"number":       "0x10",
		"transactions": []interface{}{h1.Hex()},
	}
	num, hashes, err := parseRPCBlockResult(result)
	require.NoError(t, err)
	require.Equal(t, uint64(16), num)
	require.Equal(t, []common.Hash{h1}, hashes)
}

func TestParseRPCBlockResultEmptyBlock(t *testing.T) {
	num, hashes, err := parseRPCBlockResult(map[string]interface{}{
		"number":       "0x2a",
		"transactions": []interface{}{},
	})
	require.NoError(t, err)
	require.Equal(t, uint64(42), num)
	require.Empty(t, hashes)
}

func TestParseRPCBlockResultMalformed(t *testing.T) {
	_, _, err := parseRPCBlockResult("nope")
	require.Error(t, err)
	_, _, err = parseRPCBlockResult(map[string]interface{}{"transactions": []interface{}{}})
	require.Error(t, err, "missing number is an error")
}
