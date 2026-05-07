package proxyd

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/ethereum/go-ethereum/common/hexutil"
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

func TestHandleReadyz(t *testing.T) {
	newConsensusGroup := func(members int, latest hexutil.Uint64) *BackendGroup {
		bg := &BackendGroup{}
		cg := make([]*Backend, members)
		for i := range cg {
			cg[i] = &Backend{}
		}
		tracker := NewInMemoryConsensusTracker()
		tracker.SetState(ConsensusTrackerState{Latest: latest})
		bg.Consensus = &ConsensusPoller{
			backendGroup:   bg,
			consensusGroup: cg,
			tracker:        tracker,
		}
		return bg
	}

	tests := []struct {
		name     string
		draining bool
		groups   map[string]*BackendGroup
		want     int
	}{
		{
			name:     "draining returns 503",
			draining: true,
			groups:   map[string]*BackendGroup{"main": newConsensusGroup(2, 100)},
			want:     503,
		},
		{
			name:   "no backend groups returns 200",
			groups: map[string]*BackendGroup{},
			want:   200,
		},
		{
			name:   "group without consensus is skipped",
			groups: map[string]*BackendGroup{"main": {}},
			want:   200,
		},
		{
			name:   "empty consensus group returns 503",
			groups: map[string]*BackendGroup{"main": newConsensusGroup(0, 100)},
			want:   503,
		},
		{
			name:   "consensus group with latest=0 returns 503",
			groups: map[string]*BackendGroup{"main": newConsensusGroup(2, 0)},
			want:   503,
		},
		{
			name:   "ready consensus group returns 200",
			groups: map[string]*BackendGroup{"main": newConsensusGroup(2, 100)},
			want:   200,
		},
		{
			name: "any unready group fails the gate",
			groups: map[string]*BackendGroup{
				"main":  newConsensusGroup(2, 100),
				"other": newConsensusGroup(0, 100),
			},
			want: 503,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{BackendGroups: tt.groups}
			s.isDraining.Store(tt.draining)

			req := httptest.NewRequest("GET", "/readyz", nil)
			rec := httptest.NewRecorder()
			s.HandleReadyz(rec, req)

			require.Equal(t, tt.want, rec.Code)
		})
	}
}
