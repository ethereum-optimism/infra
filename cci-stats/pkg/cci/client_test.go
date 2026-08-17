package cci

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestClientRetriesThroughEveryCall covers the wiring rather than the retry
// logic: any call on the client has to inherit the retries, because a new call
// site cannot opt into them.
func TestClientRetriesThroughEveryCall(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"message":"An invalid response was received from the upstream server"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"pipeline-id","number":1,"state":"created"}`))
	}))
	defer server.Close()

	// Waits out one real backoff, which is the first delay only: sub-second.
	client, err := newClient(server.URL, "token")
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	pipeline, err := client.Pipelines.Get(context.Background(), "pipeline-id")
	if err != nil {
		t.Fatalf("call failed despite a retryable status: %v", err)
	}
	if pipeline.ID != "pipeline-id" {
		t.Errorf("pipeline ID = %q, want pipeline-id", pipeline.ID)
	}
	if calls != 2 {
		t.Errorf("server saw %d calls, want 2", calls)
	}
}
