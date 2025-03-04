package service

import (
	"context"
	"errors"
	"net"
	"net/http"

	"github.com/ethereum-optimism/infra/op-acceptor/metrics"
	"github.com/ethereum/go-ethereum/log"
)

const (
	HealthzHost = "0.0.0.0"
	HealthzPort = "8080"

	MetricsHost = "0.0.0.0"
	MetricsPort = "7300"
)

type Service struct {
	Healthz *HealthzServer
	Metrics *MetricsServer
}

func New() *Service {
	s := &Service{
		Healthz: &HealthzServer{},
		Metrics: &MetricsServer{},
	}
	return s
}

func (s *Service) Start(ctx context.Context) {
	log.Info("service starting")

	go func() {
		addr := net.JoinHostPort(HealthzHost, HealthzPort)
		log.Info("starting healthz server", "addr", addr)
		if err := s.Healthz.Start(ctx, addr); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("error starting healthz server", "err", err)
			metrics.RecordErrorDetails("error starting healthz server", err)
		}
	}()

	go func() {
		addr := net.JoinHostPort(MetricsHost, MetricsPort)
		log.Info("starting metrics server", "addr", addr)
		if err := s.Metrics.Start(ctx, addr); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("error starting metrics server", "err", err)
			metrics.RecordErrorDetails("error starting healthz server", err)
		}
	}()

	log.Info("service started")
}

func (s *Service) Shutdown() {
	log.Info("service shutting down")

	_ = s.Healthz.Shutdown()
	log.Info("healthz stopped")

	_ = s.Metrics.Shutdown()
	log.Info("metrics stopped")

	log.Info("service stopped")
}
