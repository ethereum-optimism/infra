package integration_tests

import (
	"net/http"
	"os"
	"testing"

	"github.com/ethereum-optimism/infra/proxyd"
	"github.com/stretchr/testify/require"
)

func TestTxHashLogging(t *testing.T) {
	goodBackend := NewMockBackend(SingleResponseHandler(200, `{"jsonrpc":"2.0","result":"0x1234567890abcdef","id":1}`))
	defer goodBackend.Close()

	require.NoError(t, os.Setenv("GOOD_BACKEND_RPC_URL", goodBackend.URL()))

	config := ReadConfig("interop_validation")
	// Set log level to debug to ensure we see all logs
	config.Server.LogLevel = "debug"
	client := NewProxydClient("http://127.0.0.1:8545")
	_, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)
	defer shutdown()

	t.Run("single sendRawTransaction should log tx hash", func(t *testing.T) {
		// Valid transaction from testdata
		body := `{"jsonrpc":"2.0","method":"eth_sendRawTransaction","params":["0x02f8748201a415843b9aca31843b9aca3182520894f80267194936da1e98db10bce06f3147d580a62e880de0b6b3a764000080c001a0b50ee053102360ff5fedf0933b912b7e140c90fe57fa07a0cebe70dbd72339dda072974cb7bfe5c3dc54dde110e2b049408ccab8a879949c3b4d42a3a7555a618b"],"id":1}`
		
		_, code, err := client.SendRequest([]byte(body))
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, code)
	})

	t.Run("batch sendRawTransaction should log tx hash", func(t *testing.T) {
		// Batch with sendRawTransaction
		body := `[{"jsonrpc":"2.0","method":"eth_sendRawTransaction","params":["0x02f8748201a415843b9aca31843b9aca3182520894f80267194936da1e98db10bce06f3147d580a62e880de0b6b3a764000080c001a0b50ee053102360ff5fedf0933b912b7e140c90fe57fa07a0cebe70dbd72339dda072974cb7bfe5c3dc54dde110e2b049408ccab8a879949c3b4d42a3a7555a618b"],"id":1},{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":2}]`
		
		_, code, err := client.SendRequest([]byte(body))
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, code)
	})

	t.Run("sendRawTransactionConditional should log tx hash", func(t *testing.T) {
		// Conditional transaction from testdata
		body := `{"jsonrpc":"2.0","method":"eth_sendRawTransactionConditional","params":["0x02f8748201a415843b9aca31843b9aca3182520894f80267194936da1e98db10bce06f3147d580a62e880de0b6b3a764000080c001a0b50ee053102360ff5fedf0933b912b7e140c90fe57fa07a0cebe70dbd72339dda072974cb7bfe5c3dc54dde110e2b049408ccab8a879949c3b4d42a3a7555a618b", {}],"id":1}`
		
		_, code, err := client.SendRequest([]byte(body))
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, code)
	})
}