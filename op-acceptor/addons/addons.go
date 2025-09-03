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
	addonGenerators []func(env *env.DevnetEnv) Addon
}

type Option func(*addonCfg)

func WithFaucet() Option {
	return func(cfg *addonCfg) {
		cfg.addonGenerators = append(cfg.addonGenerators, func(env *env.DevnetEnv) Addon {
			return faucet.NewFaucet(env)
		})
	}
}

func NewAddonsManager(ctx context.Context, env *env.DevnetEnv, opts ...Option) (*AddonsManager, error) {
	cfg := &addonCfg{}
	for _, opt := range opts {
		opt(cfg)
	}

	addons := []Addon{}
	for _, generator := range cfg.addonGenerators {
		addons = append(addons, generator(env))
	}

	return &AddonsManager{
		addons: addons,
	}, nil
}

func (m *AddonsManager) Start(ctx context.Context) error {
	if m == nil {
		return nil
	}
	for _, addon := range m.addons {
		if err := addon.Start(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (m *AddonsManager) Stop(ctx context.Context) error {
	if m == nil {
		return nil
	}
	for _, addon := range m.addons {
		if err := addon.Stop(ctx); err != nil {
			return err
		}
	}
	return nil
}
