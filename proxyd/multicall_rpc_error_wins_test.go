package proxyd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/semaphore"
)

// TestExecuteMulticallRPCErrorBeatsTransportError checks that when no backend
// accepts the transaction, a backend's RPC-level error (e.g. "already known")
// is returned to the client even if a different backend's transport failure
// arrives later. A transport failure must not overwrite a real reply.
func TestExecuteMulticallRPCErrorBeatsTransportError(t *testing.T) {
	const alreadyKnown = `{"jsonrpc":"2.0","error":{"code":-32000,"message":"already known"},"id":1}`

	rpcErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(alreadyKnown))
	}))
	defer rpcErr.Close()

	// slower, so its failure is the last reply to arrive
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"down"}`))
	}))
	defer down.Close()

	bg := &BackendGroup{
		Name: "group",
		Backends: []*Backend{
			NewBackend("rpc-err", rpcErr.URL, "", semaphore.NewWeighted(100)),
			NewBackend("down", down.URL, "", semaphore.NewWeighted(100)),
		},
		multicallRPCErrorCheck: true,
	}

	req := []*RPCReq{{ID: []byte("1"), Method: "eth_sendRawTransaction", JSONRPC: "2.0"}}
	resp := bg.ExecuteMulticall(context.Background(), req, RPCRequestSourceHTTP)

	require.NoError(t, resp.error, "a transport error must not replace a backend's RPC error")
	require.Len(t, resp.RPCRes, 1)
	require.True(t, resp.RPCRes[0].IsError())
	require.Equal(t, "already known", resp.RPCRes[0].Error.Message)
	require.Equal(t, "group/rpc-err", resp.ServedBy)
}
