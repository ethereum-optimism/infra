package proxyd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"golang.org/x/sync/semaphore"
)

func TestNonConsensusPoller(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req RPCReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		resp := RPCRes{
			JSONRPC: "2.0",
			ID:      req.ID,
		}

		switch req.Method {
		case "eth_getBlockByNumber":
			resp.Result = map[string]string{"number": "0x64"} // Block 100
		default:
			http.Error(w, "unsupported method", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()
	bg := &BackendGroup{
		Backends: []*Backend{
			NewBackend("name", srv.URL, srv.URL, semaphore.NewWeighted(100)),
		},
	}

	poller := NewNonconsensusPoller(bg, WithPollingInterval(100*time.Millisecond))
	defer poller.Shutdown()

	_, ok := poller.GetLatestBlockNumber()
	assert.False(t, ok)

	_, ok = poller.GetSafeBlockNumber()
	assert.False(t, ok)

	_, ok = poller.GetFinalizedBlockNumber()
	assert.False(t, ok)

	// Give the poller time to poll
	time.Sleep(500 * time.Millisecond)

	latest, ok := poller.GetLatestBlockNumber()
	assert.True(t, ok)
	assert.Equal(t, uint64(100), latest)

	safe, ok := poller.GetSafeBlockNumber()
	assert.True(t, ok)
	assert.Equal(t, uint64(100), safe)

	finalized, ok := poller.GetFinalizedBlockNumber()
	assert.True(t, ok)
	assert.Equal(t, uint64(100), finalized)
}
