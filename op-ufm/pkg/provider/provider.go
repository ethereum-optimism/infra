package provider

import (
	"context"
	"sync"
	"time"

	"github.com/ethereum-optimism/optimism/op-service/tls"
	"github.com/ethereum-optimism/optimism/op-ufm/pkg/config"
	iclients "github.com/ethereum-optimism/optimism/op-ufm/pkg/metrics/clients"
	"github.com/ethereum/go-ethereum/log"
)

type Provider struct {
	name         string
	config       *config.ProviderConfig
	signerConfig *config.SignerServiceConfig
	walletConfig *config.WalletConfig
	txPool       *NetworkTransactionPool

	cancelFunc context.CancelFunc

	clientMu     sync.Mutex
	ethClient    *iclients.InstrumentedEthClient
	signerClient *iclients.InstrumentedSignerClient
}

func New(name string, cfg *config.ProviderConfig,
	signerConfig *config.SignerServiceConfig,
	walletConfig *config.WalletConfig,
	txPool *NetworkTransactionPool) *Provider {
	p := &Provider{
		name:         name,
		config:       cfg,
		signerConfig: signerConfig,
		walletConfig: walletConfig,
		txPool:       txPool,
	}
	return p
}

func (p *Provider) Start(ctx context.Context) {
	providerCtx, cancelFunc := context.WithCancel(ctx)
	p.cancelFunc = cancelFunc

	schedule(providerCtx, time.Duration(p.config.ReadInterval), p.Heartbeat)
	if !p.config.ReadOnly {
		schedule(providerCtx, time.Duration(p.config.SendInterval), p.RoundTrip)
	}
}

func (p *Provider) Shutdown() {
	if p.cancelFunc != nil {
		p.cancelFunc()
	}
	p.clientMu.Lock()
	defer p.clientMu.Unlock()
	if p.ethClient != nil {
		p.ethClient.Close()
		p.ethClient = nil
	}
	p.signerClient = nil
}

// ethClientOrDial returns the cached eth client, dialing lazily on first use.
// The client is reused across Heartbeat and RoundTrip cycles so we do not open
// a fresh HTTP connection (and its transport goroutines) every interval.
func (p *Provider) ethClientOrDial() (*iclients.InstrumentedEthClient, error) {
	p.clientMu.Lock()
	defer p.clientMu.Unlock()
	if p.ethClient != nil {
		return p.ethClient, nil
	}
	c, err := iclients.Dial(p.name, p.config.URL)
	if err != nil {
		return nil, err
	}
	p.ethClient = c
	return c, nil
}

// signerClientOrDial returns the cached signer client, initialising it lazily.
// The upstream SignerClient does not expose Close(), so we simply reuse the
// single instance for the lifetime of the provider.
func (p *Provider) signerClientOrDial() (*iclients.InstrumentedSignerClient, error) {
	p.clientMu.Lock()
	defer p.clientMu.Unlock()
	if p.signerClient != nil {
		return p.signerClient, nil
	}
	tlsConfig := tls.CLIConfig{
		TLSCaCert: p.signerConfig.TLSCaCert,
		TLSCert:   p.signerConfig.TLSCert,
		TLSKey:    p.signerConfig.TLSKey,
	}
	c, err := iclients.NewSignerClient(p.name, log.Root(), p.signerConfig.URL, tlsConfig)
	if err != nil {
		return nil, err
	}
	p.signerClient = c
	return c, nil
}

func (p *Provider) Name() string {
	return p.name
}

func (p *Provider) URL() string {
	return p.config.URL
}

func schedule(ctx context.Context, interval time.Duration, handler func(ctx context.Context)) {
	go func() {
		for {
			timer := time.NewTimer(interval)
			handler(ctx)

			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return
			}
		}
	}()
}
