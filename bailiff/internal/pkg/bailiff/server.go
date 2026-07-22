package bailiff

import (
	"context"
	"net/http"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/google/go-github/v66/github"
)

type Server struct {
	lgr           log.Logger
	webhookSecret string
	evHdlr        EventHandlerServer
}

func NewServer(lgr log.Logger, secret string, evHdlr EventHandlerServer) *Server {
	return &Server{
		lgr:           lgr,
		webhookSecret: secret,
		evHdlr:        evHdlr,
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	ctx := ReqIDContext(r.Context())
	l := ReqIDLogger(ctx, s.lgr)

	defer func() {
		l.Info(
			"served HTTP request",
			"url", r.URL.String(),
			"duration", time.Since(start),
			"userAgent", r.UserAgent(),
			"remoteIP", r.RemoteAddr,
		)
	}()

	if r.Method == "GET" && r.URL.Path == "/healthz" {
		writeStatus(w, http.StatusOK, "ok")
		return
	}

	payload, err := github.ValidatePayload(r, []byte(s.webhookSecret))
	if err != nil {
		l.Error("payload validation failed", "error", err)
		writeStatus(w, http.StatusBadRequest, "invalid request")
		return
	}

	event, err := github.ParseWebHook(github.WebHookType(r), payload)
	if err != nil {
		l.Error("invalid webhook payload", "error", err)
		writeStatus(w, http.StatusBadRequest, "invalid request")
		return
	}

	RecordReceivedWebhook(github.WebHookType(r))

	switch e := event.(type) {
	case *github.IssueCommentEvent:
		ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()

		if err := s.evHdlr.ServeOnIssueComment(ctx, e); err != nil {
			l.Error("error in issue comment handler", "error", err)
			writeStatus(w, http.StatusInternalServerError, "failed to process issue comment")
			return
		}

		writeStatus(w, http.StatusOK, "ok")
	default:
		l.Info("ignoring unsupported event", "event", github.WebHookType(r))
		writeStatus(w, http.StatusOK, "ok")
	}
}

func writeStatus(w http.ResponseWriter, code int, msg string) {
	RecordHTTPRequest(code)
	w.WriteHeader(code)
	_, _ = w.Write([]byte(msg))
}
