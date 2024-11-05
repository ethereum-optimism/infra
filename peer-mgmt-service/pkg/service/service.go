package service

import (
	"context"
	"errors"
	"net"
	"net/http"

	"github.com/ethereum-optimism/infrastructure-services/peer-mgmt-service/pkg/config"
	"github.com/ethereum-optimism/infrastructure-services/peer-mgmt-service/pkg/metrics"
	"github.com/ethereum-optimism/infrastructure-services/peer-mgmt-service/pkg/pms"
	"github.com/ethereum/go-ethereum/log"
)

type Service struct {
	Config  *config.Config
	Healthz *HealthzServer
	Metrics *MetricsServer
}

func New(cfg *config.Config) *Service {
	s := &Service{
		Config:  cfg,
		Healthz: &HealthzServer{},
		Metrics: &MetricsServer{},
	}
	return s
}

func (s *Service) Start(ctx context.Context) {
	log.Info("service starting")
	if s.Config.Healthz.Enabled {
		addr := net.JoinHostPort(s.Config.Healthz.Host, s.Config.Healthz.Port)
		log.Info("starting healthz server",
			"addr", addr)
		go func() {
			if err := s.Healthz.Start(ctx, addr); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("error starting healthz server",
					"err", err)
			}
		}()
	}

	metrics.Debug = s.Config.Metrics.Debug
	if s.Config.Metrics.Enabled {
		addr := net.JoinHostPort(s.Config.Metrics.Host, s.Config.Metrics.Port)
		log.Info("starting metrics server",
			"addr", addr)
		go func() {
			if err := s.Metrics.Start(ctx, addr); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("error starting metrics server",
					"err", err)
			}
		}()
	}

	for network := range s.Config.Networks {
		networkNodes := make(map[string]*config.NodeConfig)
		for _, m := range s.Config.Networks[network].Members {
			networkNodes[m] = s.Config.Nodes[m]
		}
		n := pms.New(s.Config, network, s.Config.Networks[network], networkNodes)
		n.Start(ctx)
	}

	log.Info("service started")
}

func (s *Service) Shutdown() {
	log.Info("service shutting down")
	if s.Config.Healthz.Enabled {
		s.Healthz.Shutdown() //nolint:errcheck
		log.Info("healthz stopped")
	}
	if s.Config.Metrics.Enabled {
		s.Metrics.Shutdown() //nolint:errcheck
		log.Info("metrics stopped")
	}
	log.Info("service stopped")
}
