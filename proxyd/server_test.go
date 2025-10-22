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

func TestXFFVerification(t *testing.T) {
	tests := []struct {
		name                  string
		enableXFFVerification bool
		rateLimitHeader       string
		headerValue           string
		remoteAddr            string
		expectedXFF           string
	}{
		{
			name:                  "verification enabled - matching last XFF and remote addr",
			enableXFFVerification: true,
			rateLimitHeader:       "X-Forwarded-For",
			headerValue:           "1.2.3.4, 5.6.7.8",
			remoteAddr:            "5.6.7.8:12345",
			expectedXFF:           "1.2.3.4, 5.6.7.8",
		},
		{
			name:                  "verification enabled - non-matching last XFF and remote addr",
			enableXFFVerification: true,
			rateLimitHeader:       "X-Forwarded-For",
			headerValue:           "1.2.3.4, 5.6.7.8",
			remoteAddr:            "9.10.11.12:12345",
			expectedXFF:           "9.10.11.12",
		},
		{
			name:                  "verification disabled - non-matching last XFF and remote addr",
			enableXFFVerification: false,
			rateLimitHeader:       "X-Forwarded-For",
			headerValue:           "1.2.3.4, 5.6.7.8",
			remoteAddr:            "9.10.11.12:12345",
			expectedXFF:           "1.2.3.4, 5.6.7.8",
		},
		{
			name:                  "verification enabled - single IP XFF matching remote addr",
			enableXFFVerification: true,
			rateLimitHeader:       "X-Forwarded-For",
			headerValue:           "1.2.3.4",
			remoteAddr:            "1.2.3.4:12345",
			expectedXFF:           "1.2.3.4",
		},
		{
			name:                  "verification enabled - single IP XFF not matching remote addr",
			enableXFFVerification: true,
			rateLimitHeader:       "X-Forwarded-For",
			headerValue:           "1.2.3.4",
			remoteAddr:            "5.6.7.8:12345",
			expectedXFF:           "5.6.7.8",
		},
		{
			name:                  "no XFF header",
			enableXFFVerification: true,
			rateLimitHeader:       "X-Forwarded-For",
			headerValue:           "",
			remoteAddr:            "1.2.3.4:12345",
			expectedXFF:           "1.2.3.4",
		},
		{
			name:                  "verification disabled with CF-Connecting-IP",
			enableXFFVerification: false,
			rateLimitHeader:       "CF-Connecting-IP",
			headerValue:           "1.2.3.4",
			remoteAddr:            "5.6.7.8:12345",
			expectedXFF:           "1.2.3.4",
		},
		{
			name:                  "verification disabled with custom header",
			enableXFFVerification: false,
			rateLimitHeader:       "X-Real-IP",
			headerValue:           "1.2.3.4",
			remoteAddr:            "5.6.7.8:12345",
			expectedXFF:           "1.2.3.4",
		},
		{
			name:                  "verification enabled with lowercase x-forwarded-for - should verify",
			enableXFFVerification: true,
			rateLimitHeader:       "x-forwarded-for",
			headerValue:           "1.2.3.4, 5.6.7.8",
			remoteAddr:            "9.10.11.12:12345",
			expectedXFF:           "9.10.11.12",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := &Server{
				enableXFFVerification: tt.enableXFFVerification,
				rateLimitHeader:       tt.rateLimitHeader,
				authenticatedPaths:    make(map[string]string),
			}

			req := httptest.NewRequest("POST", "/", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.headerValue != "" {
				req.Header.Set(tt.rateLimitHeader, tt.headerValue)
			}

			w := httptest.NewRecorder()
			ctx := server.populateContext(w, req)
			require.NotNil(t, ctx)

			actualXFF := GetXForwardedFor(ctx)
			require.Equal(t, tt.expectedXFF, actualXFF)
		})
	}
}
