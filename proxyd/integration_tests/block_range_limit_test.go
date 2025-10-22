package integration_tests

import (
	"os"
	"testing"

	"github.com/ethereum-optimism/infra/proxyd"
	"github.com/stretchr/testify/require"
)

func TestEthGetLogsBlockRangeLimit(t *testing.T) {
	goodBackend := NewMockBackend(BatchedResponseHandler(200, goodResponse))
	defer goodBackend.Close()

	require.NoError(t, os.Setenv("GOOD_BACKEND_RPC_URL", goodBackend.URL()))

	config := ReadConfig("eth_getlogs_limit")
	client := NewProxydClient("http://127.0.0.1:8545")
	_, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)
	defer shutdown()

	tests := []struct {
		name         string
		method       string
		params       string
		expectError  bool
		errorMessage string
	}{
		{
			name:        "eth_getLogs within range (50 blocks)",
			method:      "eth_getLogs",
			params:      `[{"fromBlock":"0x0","toBlock":"0x32"}]`,
			expectError: false,
		},
		{
			name:        "eth_getLogs at limit (100 blocks)",
			method:      "eth_getLogs",
			params:      `[{"fromBlock":"0x0","toBlock":"0x64"}]`,
			expectError: false,
		},
		{
			name:         "eth_getLogs exceeds limit (200 blocks)",
			method:       "eth_getLogs",
			params:       `[{"fromBlock":"0x0","toBlock":"0xc8"}]`,
			expectError:  true,
			errorMessage: "block range greater than 100 max",
		},
		{
			name:         "eth_getLogs exceeds limit (1000 blocks)",
			method:       "eth_getLogs",
			params:       `[{"fromBlock":"0x0","toBlock":"0x3e8"}]`,
			expectError:  true,
			errorMessage: "block range greater than 100 max",
		},
		{
			name:        "eth_newFilter within range (50 blocks)",
			method:      "eth_newFilter",
			params:      `[{"fromBlock":"0x0","toBlock":"0x32"}]`,
			expectError: false,
		},
		{
			name:         "eth_newFilter exceeds limit (200 blocks)",
			method:       "eth_newFilter",
			params:       `[{"fromBlock":"0x0","toBlock":"0xc8"}]`,
			expectError:  true,
			errorMessage: "block range greater than 100 max",
		},
		{
			name:         "eth_getLogs with only fromBlock (defaults toBlock to latest, exceeds limit)",
			method:       "eth_getLogs",
			params:       `[{"fromBlock":"0x0"}]`,
			expectError:  true,
			errorMessage: "block range greater than 100 max",
		},
		{
			name:        "eth_getLogs with only toBlock (should default fromBlock to latest)",
			method:      "eth_getLogs",
			params:      `[{"toBlock":"0x64"}]`,
			expectError: false,
		},
		{
			name:        "eth_getLogs with no block params (should work)",
			method:      "eth_getLogs",
			params:      `[{}]`,
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := `{"jsonrpc":"2.0","method":"` + tt.method + `","params":` + tt.params + `,"id":1}`
			res, code, err := client.SendRequest([]byte(body))
			require.NoError(t, err)

			if tt.expectError {
				// Should receive error response
				require.Contains(t, string(res), tt.errorMessage)
				// Invalid params errors return 400 status
				require.Equal(t, 400, code)
			} else {
				// Should succeed (either processed by backend or passed through)
				// We don't check the exact response since the mock backend will handle it
				require.NotContains(t, string(res), "block range greater than")
			}
		})
	}
}
