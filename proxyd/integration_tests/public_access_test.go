package integration_tests

import (
	"net/http"
	"os"
	"testing"

	"github.com/ethereum-optimism/infra/proxyd"
	"github.com/ethereum/go-ethereum/log"
	"github.com/stretchr/testify/require"
)

func TestPublicAccess(t *testing.T) {
	goodBackend := NewMockBackend(SingleResponseHandler(200, `{"jsonrpc":"2.0","result":"0x1","id":999}`))
	defer goodBackend.Close()

	require.NoError(t, os.Setenv("GOOD_BACKEND_RPC_URL", goodBackend.URL()))

	proxyd.SetLogLevel(log.LevelDebug)

	config := ReadConfig("public_access")
	_, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)
	defer shutdown()

	t.Run("allows unauthenticated requests when public_access is enabled", func(t *testing.T) {
		client := NewProxydClient("http://127.0.0.1:8545")
		_, code, err := client.SendRPC("eth_chainId", nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, code)
	})

	t.Run("allows authenticated requests when public_access is enabled", func(t *testing.T) {
		client := NewProxydClient("http://127.0.0.1:8545/secret")
		_, code, err := client.SendRPC("eth_chainId", nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, code)
	})

	t.Run("rejects invalid authentication even when public_access is enabled", func(t *testing.T) {
		client := NewProxydClient("http://127.0.0.1:8545/invalid_auth_key")
		_, code, err := client.SendRPC("eth_chainId", nil)
		require.NoError(t, err)
		t.Logf("Response code: %d", code)
		require.Equal(t, http.StatusUnauthorized, code)
	})

}
