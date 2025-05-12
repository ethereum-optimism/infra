package addons

import (
	"context"

	"github.com/ethereum-optimism/infra/op-acceptor/addons/faucet"
	"github.com/ethereum-optimism/optimism/devnet-sdk/shell/env"
)

type Addon interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

type AddonsManager struct {
	addons []Addon
}

type addonCfg struct {
	useFaucet bool
}

type Option func(*addonCfg)

func WithFaucet() Option {
	return func(cfg *addonCfg) {
		cfg.useFaucet = true
	}
}

func NewAddonsManager(ctx context.Context, env *env.DevnetEnv, opts ...Option) (*AddonsManager, error) {
	cfg := &addonCfg{}
	for _, opt := range opts {
		opt(cfg)
	}

	addons := []Addon{}
	if cfg.useFaucet {
		addons = append(addons, faucet.NewFaucet(env))
	}

	return &AddonsManager{
		addons: addons,
	}, nil
}

func (m *AddonsManager) Start(ctx context.Context) error {
	for _, addon := range m.addons {
		if err := addon.Start(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (m *AddonsManager) Stop(ctx context.Context) error {
	for _, addon := range m.addons {
		if err := addon.Stop(ctx); err != nil {
			return err
		}
	}
	return nil
}
