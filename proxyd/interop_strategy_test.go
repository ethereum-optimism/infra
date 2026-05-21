package proxyd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ethereum-optimism/optimism/op-service/eth"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestPerformCheckAccessListOpUsesInteropNamespace(t *testing.T) {
	methods := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var req RPCReq
		require.NoError(t, json.Unmarshal(body, &req))
		methods <- req.Method

		w.Header().Set("Content-Type", "application/json")
		_, err = w.Write([]byte(`{"jsonrpc":"2.0","result":null,"id":` + string(req.ID) + `}`))
		require.NoError(t, err)
	}))
	defer server.Close()

	httpCode, rpcErrorCode, err := performCheckAccessListOp(
		context.Background(),
		[]common.Hash{common.HexToHash("0x01")},
		server.URL,
		eth.ChainIDFromUInt64(900),
	)

	require.NoError(t, err)
	require.Equal(t, 200, httpCode)
	require.Equal(t, "-", rpcErrorCode)
	require.Equal(t, "interop_checkAccessList", <-methods)
}
