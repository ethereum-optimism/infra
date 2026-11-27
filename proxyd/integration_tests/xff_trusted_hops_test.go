package integration_tests

import (
	"net/http"
	"os"
	"testing"

	"github.com/ethereum-optimism/infra/proxyd"
	"github.com/stretchr/testify/require"
)

func TestFrontendRateLimitXFFTrustedHops(t *testing.T) {
	goodBackend := NewMockBackend(BatchedResponseHandler(200, goodResponse))
	defer goodBackend.Close()

	require.NoError(t, os.Setenv("GOOD_BACKEND_RPC_URL", goodBackend.URL()))

	config := ReadConfig("frontend_rate_limit_xff_hops")
	_, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)
	defer shutdown()

	clientWithXFF := func(xff string) *ProxydHTTPClient {
		h := make(http.Header)
		h.Set("X-Forwarded-For", xff)
		return NewProxydClientWithHeaders("http://127.0.0.1:8545", h)
	}

	t.Run("client-supplied entries share the real client's limit", func(t *testing.T) {
		_, codes := spamReqs(t, clientWithXFF("0.0.0.0, 203.0.113.7, 34.0.0.1"), ethChainID, 429, 2)
		require.Equal(t, 2, codes[200])

		_, code, err := clientWithXFF("1.1.1.1, 203.0.113.7, 34.0.0.1").SendRPC(ethChainID, nil)
		require.NoError(t, err)
		require.Equal(t, 429, code)

		_, code, err = clientWithXFF("0.0.0.0, 203.0.113.8, 34.0.0.1").SendRPC(ethChainID, nil)
		require.NoError(t, err)
		require.Equal(t, 200, code)
	})

	t.Run("header shorter than the trusted hops falls back to the peer address", func(t *testing.T) {
		_, codes := spamReqs(t, clientWithXFF("198.51.100.1"), ethChainID, 429, 2)
		require.Equal(t, 2, codes[200])

		_, code, err := clientWithXFF("198.51.100.2").SendRPC(ethChainID, nil)
		require.NoError(t, err)
		require.Equal(t, 429, code)
	})
}
