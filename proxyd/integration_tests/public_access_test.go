package integration_tests

import (
	"net/http"
	"os"
	"testing"

	"github.com/ethereum-optimism/infra/proxyd"
	"github.com/stretchr/testify/require"
)

func TestPublicAccess(t *testing.T) {
	goodBackend := NewMockBackend(SingleResponseHandler(200, `{"jsonrpc":"2.0","result":"0x1","id":999}`))
	defer goodBackend.Close()

	require.NoError(t, os.Setenv("GOOD_BACKEND_RPC_URL", goodBackend.URL()))

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
		hdrs := http.Header{}
		hdrs.Set("Authorization", "test_alias")

		client := NewProxydClientWithHeaders("http://127.0.0.1:8545", hdrs)
		_, code, err := client.SendRPC("eth_chainId", nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, code)
	})

	t.Run("allows invalid authentication when public_access is enabled", func(t *testing.T) {
		hdrs := http.Header{}
		hdrs.Set("Authorization", "invalid_auth")

		client := NewProxydClientWithHeaders("http://127.0.0.1:8545", hdrs)
		_, code, err := client.SendRPC("eth_chainId", nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, code)
	})
}

func TestAuthenticationWithoutPublicAccess(t *testing.T) {
	goodBackend := NewMockBackend(SingleResponseHandler(200, `{"jsonrpc":"2.0","result":"0x1","id":999}`))
	defer goodBackend.Close()

	require.NoError(t, os.Setenv("GOOD_BACKEND_RPC_URL", goodBackend.URL()))

	// Use smoke config which has authentication but no public_access
	config := ReadConfig("public_access")
	config.Server.PublicAccess = false // Override to disable public access
	_, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)
	defer shutdown()

	t.Run("rejects invalid authentication when public_access is disabled", func(t *testing.T) {
		hdrs := http.Header{}
		hdrs.Set("Authorization", "invalid_auth")

		client := NewProxydClientWithHeaders("http://127.0.0.1:8545", hdrs)
		_, code, err := client.SendRPC("eth_chainId", nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusUnauthorized, code)
	})

	t.Run("rejects unauthenticated requests when public_access is disabled", func(t *testing.T) {
		client := NewProxydClient("http://127.0.0.1:8545")
		_, code, err := client.SendRPC("eth_chainId", nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusUnauthorized, code)
	})
}