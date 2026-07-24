package proxyd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStatusCodeForRPCRes(t *testing.T) {
	tests := []struct {
		name string
		res  *RPCRes
		want string
	}{
		{"success with result", &RPCRes{Result: "0x1"}, "200"},
		{"success with nil result", &RPCRes{}, "200"},
		{"rate limited", &RPCRes{Error: ErrOverRateLimit}, "429"},
		{"client canceled", &RPCRes{Error: ErrContextCanceled}, "499"},
		{"backend offline", &RPCRes{Error: ErrBackendOffline}, "503"},
		{"method not whitelisted", &RPCRes{Error: ErrMethodNotWhitelisted}, "403"},
		{"gateway timeout", &RPCRes{Error: ErrGatewayTimeout}, "504"},
		{"body too large", &RPCRes{Error: ErrRequestBodyTooLarge}, "413"},
		{"proxyd internal", &RPCRes{Error: ErrInternal}, "500"},
		{
			"upstream non-200 stamped per element",
			&RPCRes{Error: &RPCErr{Code: -32000, Message: "boom", HTTPErrorCode: 500}},
			"500",
		},
		// The critical cases: backend answered HTTP 200 carrying a JSON-RPC error,
		// so HTTPErrorCode was never stamped. These MUST NOT be 5xx.
		{
			"already known",
			&RPCRes{Error: &RPCErr{Code: -32000, Message: "already known"}},
			"400",
		},
		{
			"nonce too low",
			&RPCRes{Error: &RPCErr{Code: -32000, Message: "nonce too low"}},
			"400",
		},
		{
			"replacement transaction underpriced",
			&RPCRes{Error: &RPCErr{Code: -32000, Message: "replacement transaction underpriced"}},
			"400",
		},
		{
			"upstream method not found",
			&RPCRes{Error: &RPCErr{Code: -32601, Message: "the method does not exist"}},
			"400",
		},
		{
			"too many batch requests has no http code",
			&RPCRes{Error: ErrTooManyBatchRequests},
			"400",
		},
		{"nil response treated as internal error", nil, "500"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, statusCodeForRPCRes(tt.res))
		})
	}
}

func TestBackendNameFromServedBy(t *testing.T) {
	tests := []struct {
		name     string
		servedBy string
		want     string
	}{
		{"group and backend", "replicas/reth-0", "reth-0"},
		{"bare backend name", "reth-0", "reth-0"},
		{"empty falls back to proxyd", "", "proxyd"},
		{"trailing slash falls back to proxyd", "replicas/", "proxyd"},
		{"nested slashes take the last segment", "a/b/c", "c"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, backendNameFromServedBy(tt.servedBy))
		})
	}
}
