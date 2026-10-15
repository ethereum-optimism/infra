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

func TestListWorkflowJobsPagePassesPageToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/workflow/workflow-id/job" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("page-token"); got != "next-page" {
			t.Errorf("page-token = %q, want next-page", got)
		}
		if got := r.Header.Get("Circle-Token"); got != "token" {
			t.Errorf("Circle-Token = %q, want token", got)
		}
		_, _ = w.Write([]byte(`{"items":[{"id":"job-2"}],"next_page_token":"last-page"}`))
	}))
	defer server.Close()

	client, err := newClient(server.URL, "token")
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	page, err := client.ListWorkflowJobsPage(context.Background(), "workflow-id", "next-page")
	if err != nil {
		t.Fatalf("failed to fetch jobs page: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "job-2" || page.NextPageToken != "last-page" {
		t.Fatalf("page = %+v", page)
	}
}
