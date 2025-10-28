package integration_tests

import (
	"os"
	"testing"

	"github.com/ethereum-optimism/infra/proxyd"
	"github.com/stretchr/testify/require"
)

func TestMaxBlockRange(t *testing.T) {
	goodBackend := NewMockBackend(BatchedResponseHandler(200, goodResponse))
	defer goodBackend.Close()

	require.NoError(t, os.Setenv("GOOD_BACKEND_RPC_URL", goodBackend.URL()))

	config := ReadConfig("max_block_range")
	client := NewProxydClient("http://127.0.0.1:8545")
	_, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)
	defer shutdown()

	t.Run("eth_getLogs within max_block_range", func(t *testing.T) {
		body := `{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"0x0","toBlock":"0x50"}],"id":1}`
		res, code, err := client.SendRequest([]byte(body))
		require.NoError(t, err)
		require.Equal(t, 200, code)
		require.NotContains(t, string(res), "block range greater than")
	})

	t.Run("eth_getLogs exceeds max_block_range", func(t *testing.T) {
		body := `{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"0x0","toBlock":"0xc8"}],"id":1}`
		res, code, err := client.SendRequest([]byte(body))
		require.NoError(t, err)
		require.Equal(t, 400, code)
		require.Contains(t, string(res), "block range greater than 100 max")
	})

	t.Run("eth_getLogs rejects latest tag", func(t *testing.T) {
		body := `{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"0x0","toBlock":"latest"}],"id":1}`
		res, code, err := client.SendRequest([]byte(body))
		require.NoError(t, err)
		require.Equal(t, 400, code)
		require.Contains(t, string(res), "block tags (latest/pending/safe/finalized) are not allowed")
	})

	t.Run("eth_newFilter within max_block_range", func(t *testing.T) {
		body := `{"jsonrpc":"2.0","method":"eth_newFilter","params":[{"fromBlock":"0x0","toBlock":"0x50"}],"id":1}`
		res, code, err := client.SendRequest([]byte(body))
		require.NoError(t, err)
		require.Equal(t, 200, code)
		require.NotContains(t, string(res), "block range greater than")
	})

	t.Run("batched eth_getLogs and eth_newFilter within range", func(t *testing.T) {
		body := `[
			{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"0x0","toBlock":"0x32"}],"id":1},
			{"jsonrpc":"2.0","method":"eth_newFilter","params":[{"fromBlock":"0x0","toBlock":"0x50"}],"id":2}
		]`
		res, code, err := client.SendRequest([]byte(body))
		require.NoError(t, err)
		require.Equal(t, 200, code)
		require.NotContains(t, string(res), "block range greater than")
	})

	t.Run("batched requests with one exceeding limit", func(t *testing.T) {
		body := `[
			{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"0x0","toBlock":"0x32"}],"id":1},
			{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"0x0","toBlock":"0xc8"}],"id":2}
		]`
		res, code, err := client.SendRequest([]byte(body))
		require.NoError(t, err)
		require.Equal(t, 200, code)
		require.Contains(t, string(res), "block range greater than 100 max")
	})

	t.Run("parseBlockParam error - invalid hex fromBlock", func(t *testing.T) {
		body := `{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"0xZZZ","toBlock":"0x64"}],"id":1}`
		res, code, err := client.SendRequest([]byte(body))
		require.NoError(t, err)
		require.Equal(t, 400, code)
		require.Contains(t, string(res), "invalid")
	})

	t.Run("parseBlockParam error - invalid hex toBlock", func(t *testing.T) {
		body := `{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"0x0","toBlock":"0xGGG"}],"id":1}`
		res, code, err := client.SendRequest([]byte(body))
		require.NoError(t, err)
		require.Equal(t, 400, code)
		require.Contains(t, string(res), "invalid")
	})

	t.Run("parseBlockParam error - malformed block number", func(t *testing.T) {
		body := `{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"not_a_number","toBlock":"0x64"}],"id":1}`
		res, code, err := client.SendRequest([]byte(body))
		require.NoError(t, err)
		require.Equal(t, 400, code)
		require.Contains(t, string(res), "hex string")
	})

	t.Run("batched requests with parseBlockParam error", func(t *testing.T) {
		body := `[
			{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"0x0","toBlock":"0x32"}],"id":1},
			{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"0xABC","toBlock":"invalid_hex"}],"id":2}
		]`
		res, code, err := client.SendRequest([]byte(body))
		require.NoError(t, err)
		require.Equal(t, 200, code)
		require.Contains(t, string(res), "hex string")
	})
}

func TestMaxBlockRangeWithoutConsensus(t *testing.T) {
	goodBackend := NewMockBackend(BatchedResponseHandler(200, goodResponse))
	defer goodBackend.Close()

	require.NoError(t, os.Setenv("GOOD_BACKEND_RPC_URL", goodBackend.URL()))

	config := ReadConfig("max_block_range")
	client := NewProxydClient("http://127.0.0.1:8545")
	_, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)
	defer shutdown()

	t.Run("eth_getLogs within max_block_range - no consensus", func(t *testing.T) {
		body := `{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"0x0","toBlock":"0x50"}],"id":1}`
		res, code, err := client.SendRequest([]byte(body))
		require.NoError(t, err)
		require.Equal(t, 200, code)
		require.NotContains(t, string(res), "block range greater than")
	})

	t.Run("eth_getLogs exceeds max_block_range - no consensus", func(t *testing.T) {
		body := `{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"0x0","toBlock":"0xc8"}],"id":1}`
		res, code, err := client.SendRequest([]byte(body))
		require.NoError(t, err)
		require.Equal(t, 400, code)
		require.Contains(t, string(res), "block range greater than 100 max")
	})

	t.Run("eth_getLogs rejects latest tag - no consensus", func(t *testing.T) {
		body := `{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"fromBlock":"0x0","toBlock":"latest"}],"id":1}`
		res, code, err := client.SendRequest([]byte(body))
		require.NoError(t, err)
		require.Equal(t, 400, code)
		require.Contains(t, string(res), "block tags (latest/pending/safe/finalized) are not allowed")
	})
}
