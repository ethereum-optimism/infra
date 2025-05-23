package service

import (
	"context"
	"net/http"

	"github.com/ethereum/go-ethereum/log"
	"github.com/rs/cors"
)

type HealthzServer struct {
	ctx    context.Context
	server *http.Server
	log    log.Logger
}

func NewHealthzServer(logger log.Logger) *HealthzServer {
	return &HealthzServer{
		log: logger,
	}
}

func (h *HealthzServer) Start(ctx context.Context, addr string) error {
	hdlr := http.NewServeMux()
	hdlr.HandleFunc("/healthz", h.Handle)
	c := cors.New(cors.Options{
		AllowedOrigins: []string{"*"},
	})
	server := &http.Server{
		Handler: c.Handler(hdlr),
		Addr:    addr,
	}
	h.server = server
	h.ctx = ctx
	return h.server.ListenAndServe()
}

func (h *HealthzServer) Shutdown() error {
	return h.server.Shutdown(h.ctx)
}

func (h *HealthzServer) Handle(w http.ResponseWriter, r *http.Request) {
	h.log.Debug("Received health check request", "path", r.URL.Path)
	w.Write([]byte("OK")) //nolint:errcheck
}
