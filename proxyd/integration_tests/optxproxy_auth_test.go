package integration_tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/ethereum-optimism/infra/proxyd"
	"github.com/stretchr/testify/require"
)

func TestOpTxProxyAuthForwarder(t *testing.T) {
	authEchoHandler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		fmt.Fprintf(w, `{"jsonrpc": "2.0", "result": "%s", "id": 999}`, r.Header.Get(proxyd.DefaultOpTxProxyAuthHeader))
	}

	goodBackend := NewMockBackend(http.HandlerFunc(authEchoHandler))
	defer goodBackend.Close()

	require.NoError(t, os.Setenv("GOOD_BACKEND_RPC_URL", goodBackend.URL()))

	config := ReadConfig("smoke")
	_, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)
	defer shutdown()

	t.Run("skips if absent", func(t *testing.T) {
		client := NewProxydClient("http://127.0.0.1:8545")
		res, code, err := client.SendRPC(ethChainID, nil)
		require.Equal(t, http.StatusOK, code)
		require.NoError(t, err)

		rpcRes := &proxyd.RPCRes{}
		require.NoError(t, json.NewDecoder(bytes.NewReader(res)).Decode(rpcRes))
		require.Equal(t, "", rpcRes.Result.(string))
	})

	t.Run("forwards if present", func(t *testing.T) {
		hdrs := http.Header{}
		hdrs.Set(proxyd.DefaultOpTxProxyAuthHeader, "foobar")

		client := NewProxydClientWithHeaders("http://127.0.0.1:8545", hdrs)
		res, code, err := client.SendRPC(ethChainID, nil)
		require.Equal(t, http.StatusOK, code)
		require.NoError(t, err)

		rpcRes := &proxyd.RPCRes{}
		require.NoError(t, json.NewDecoder(bytes.NewReader(res)).Decode(rpcRes))
		require.Equal(t, "foobar", rpcRes.Result.(string))
	})
}
