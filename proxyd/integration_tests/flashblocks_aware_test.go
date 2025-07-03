package integration_tests

import (
	"os"
	"testing"

	"github.com/ethereum-optimism/infra/proxyd"
	"github.com/stretchr/testify/require"
)

const flashblocksDummyRes = "{\"id\": 456, \"jsonrpc\": \"2.0\", \"result\": \"flashblocks\"}"

func TestFlashblocksAwareBackend(t *testing.T) {
	goodBackend := NewMockBackend(SingleResponseHandler(200, dummyRes))
	defer goodBackend.Close()

	flashblocksBackend := NewMockBackend(SingleResponseHandler(200, flashblocksDummyRes))
	defer flashblocksBackend.Close()

	require.NoError(t, os.Setenv("GOOD_BACKEND_RPC_URL", goodBackend.URL()))
	require.NoError(t, os.Setenv("FLASHBLOCKS_AWARE_BACKEND_RPC_URL", flashblocksBackend.URL()))

	config := ReadConfig("flashblocks")
	client := NewProxydClient("http://127.0.0.1:8545")
	_, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)
	defer shutdown()

	tests := []struct {
		name   string
		method string
		params []any
		want   string
	}{
		{
			"eth_getTransactionReceipt always routes to flashblocks backend",
			"eth_getTransactionReceipt",
			[]any{"0x01"},
			flashblocksDummyRes,
		},
		{
			"flashblocks-incompatible RPC routes to regular backend",
			"eth_chainId",
			nil,
			dummyRes,
		},
		{
			"pending eth_getBlockByNumber",
			"eth_getBlockByNumber",
			[]any{"pending", true},
			flashblocksDummyRes,
		},
		{
			"pending eth_getBlockByNumber - non-standard parameters",
			"eth_getBlockByNumber",
			[]any{"pending", "true"},
			flashblocksDummyRes,
		},
		{
			"pending eth_getBlockByNumber - no detail flag",
			"eth_getBlockByNumber",
			[]any{"pending", false},
			dummyRes,
		},
		{
			"pending eth_getBalance",
			"eth_getBalance",
			[]any{"0x01", "pending"},
			flashblocksDummyRes,
		},
		{
			"latest eth_getBalance",
			"eth_getBalance",
			[]any{"0x01", "latest"},
			dummyRes,
		},
		{
			"pending eth_getTransactionCount",
			"eth_getTransactionCount",
			[]any{"0x01", "pending"},
			flashblocksDummyRes,
		},
		{
			"latest eth_getTransactionCount",
			"eth_getTransactionCount",
			[]any{"0x01", "latest"},
			dummyRes,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, code, err := client.SendRPC(tt.method, tt.params)
			require.NoError(t, err)
			require.Equal(t, 200, code)
			RequireEqualJSON(t, []byte(tt.want), res)
		})
	}
}
