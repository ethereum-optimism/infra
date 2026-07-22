package bailiff

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ethereum/go-ethereum/log"
	"github.com/google/go-github/v66/github"
	"github.com/stretchr/testify/require"
)

type mockEvHandlerServer struct {
	callCount int
}

func (m *mockEvHandlerServer) ServeOnIssueComment(ctx context.Context, e *github.IssueCommentEvent) error {
	m.callCount++
	return nil
}

func TestServer(t *testing.T) {
	// use empty string as secret for testing purposes
	srv := NewServer(log.NewLogger(log.DiscardHandler()), "", new(mockEvHandlerServer))
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	tests := []struct {
		name      string
		eventType string
		payload   any
		expStatus int
		expBody   string
		callCount int
	}{
		{
			name:      "invalid request",
			eventType: "issue_comment",
			payload:   "this is no JSON i've ever seen",
			expStatus: 400,
			expBody:   "invalid request",
		},
		{
			name:      "no header",
			eventType: "",
			payload:   github.IssueCommentEvent{},
			expStatus: 400,
			expBody:   "invalid request",
		},
		{
			name:      "supported webhook event",
			eventType: "issue_comment",
			payload: github.IssueCommentEvent{
				Action: github.String("created"),
			},
			expStatus: 200,
			expBody:   "ok",
			callCount: 1,
		},
		{
			name:      "unsupported webhook event",
			eventType: "label",
			payload:   github.LabelEvent{},
			expStatus: 200,
			expBody:   "ok",
			callCount: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := json.Marshal(tt.payload)
			require.NoError(t, err)
			req, err := http.NewRequest(http.MethodPost, httpSrv.URL, bytes.NewReader(out))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(github.EventTypeHeader, tt.eventType)
			res, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer res.Body.Close()
			body, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			require.Equal(t, tt.expStatus, res.StatusCode)
			require.Equal(t, tt.expBody, string(body))
		})
	}
}
