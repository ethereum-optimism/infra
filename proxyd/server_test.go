package proxyd

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

type txsJSON struct {
	OnchainTx    string `json:"onchainTx"`
	OffchainTxV0 string `json:"offchainTxV0"`
	OffchainTxV1 string `json:"offchainTxV1"`
}

func TestConvertSendReqToSendTx_Fusaka(t *testing.T) {
	txData, err := os.ReadFile("testdata/txs.json")
	require.NoError(t, err)
	var txs txsJSON
	require.NoError(t, json.Unmarshal(txData, &txs))

	tfn := func(txHex string, proofCount int) func(t *testing.T) {
		return func(t *testing.T) {
			params, err := json.Marshal([]any{txHex})
			require.NoError(t, err)
			rpcReq := &RPCReq{
				Method: "eth_sendRawTransaction",
				Params: params,
				ID:     json.RawMessage("1"),
			}

			// Create a minimal server instance for testing
			server := &Server{enableTxHashLogging: false}
			tx, err := server.convertSendReqToSendTx(context.Background(), rpcReq)
			require.NoError(t, err)

			require.Len(t, tx.BlobTxSidecar().Blobs, 2)
			require.Len(t, tx.BlobTxSidecar().Commitments, 2)
			require.Len(t, tx.BlobTxSidecar().Proofs, proofCount)
		}
	}
	t.Run("blob without cell proofs", tfn(txs.OffchainTxV0, 2))
	t.Run("blob with cell proofs", tfn(txs.OffchainTxV1, 256))
}

func TestIsValidAPIKey(t *testing.T) {
	tests := []struct {
		name        string
		apiKey      string
		exemptKeys  []string
		expected    bool
		description string
	}{
		{
			name:        "valid API key",
			apiKey:      "valid-key-123",
			exemptKeys:  []string{"valid-key-123"},
			expected:    true,
			description: "should return true for a valid API key",
		},
		{
			name:        "invalid API key",
			apiKey:      "invalid-key",
			exemptKeys:  []string{"valid-key-123"},
			expected:    false,
			description: "should return false for an invalid API key",
		},
		{
			name:        "empty API key",
			apiKey:      "",
			exemptKeys:  []string{"valid-key-123"},
			expected:    false,
			description: "should return false for an empty API key",
		},
		{
			name:        "multiple exempt keys",
			apiKey:      "key-2",
			exemptKeys:  []string{"key-1", "key-2", "key-3"},
			expected:    true,
			description: "should return true when key is in multiple exempt keys",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{
				limExemptKeys: tt.exemptKeys,
			}

			got := s.isValidAPIKey(tt.apiKey)
			if got != tt.expected {
				t.Errorf("isValidAPIKey() = %v, want %v: %s", got, tt.expected, tt.description)
			}
		})
	}
}

func TestPopulateContextRemoteAddrFallback(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		header     string
		want       string
	}{
		{
			name:       "ipv4 host port",
			remoteAddr: "127.0.0.1:8545",
			want:       "127.0.0.1",
		},
		{
			name:       "ipv6 host port",
			remoteAddr: "[::1]:8545",
			want:       "::1",
		},
		{
			name:       "ipv6 without port",
			remoteAddr: "::1",
			want:       "::1",
		},
		{
			name:       "header takes precedence",
			remoteAddr: "[::1]:8545",
			header:     "203.0.113.1",
			want:       "203.0.113.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{rateLimitHeader: defaultRateLimitHeader}
			req := httptest.NewRequest("POST", "/", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.header != "" {
				req.Header.Set(defaultRateLimitHeader, tt.header)
			}

			ctx := s.populateContext(httptest.NewRecorder(), req)
			require.NotNil(t, ctx)
			require.Equal(t, tt.want, GetXForwardedFor(ctx))
		})
	}
}
