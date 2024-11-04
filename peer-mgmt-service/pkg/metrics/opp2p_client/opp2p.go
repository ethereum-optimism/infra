package opp2p_client

import (
	"context"
	"net/http"
	"time"

	"github.com/ethereum-optimism/infrastructure-services/peer-mgmt-service/pkg/config"
	"github.com/ethereum-optimism/infrastructure-services/peer-mgmt-service/pkg/metrics"
	opp2p "github.com/ethereum-optimism/optimism/op-node/p2p"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/pkg/errors"
)

type InstrumentedOpP2PClient struct {
	c       *opp2p.Client
	network string
	node    string
	rpcUrl  string
}

func New(ctx context.Context, config *config.Config, network string, nodeName string, rpcUrl string) (*InstrumentedOpP2PClient, error) {
	pc, err := rpc.DialOptions(ctx, rpcUrl, rpc.WithHTTPClient(&http.Client{
		Timeout: config.RPCTimeout,
	}))
	if err != nil {
		metrics.RecordNetworkErrorDetails(network, nodeName, "opp2p.New", err)
		log.Error("cant create opp2p client",
			"err", err)
		return nil, errors.Errorf("failed to create p2p rpc client with network [%s], nodeName [%s], rpcUrl [%s]: %v", network, nodeName, rpcUrl, err)
	}
	p2pClient := opp2p.NewClient(pc)

	return &InstrumentedOpP2PClient{
		c:       p2pClient,
		network: network,
		node:    nodeName,
		rpcUrl:  rpcUrl,
	}, nil
}

func (i *InstrumentedOpP2PClient) Self(ctx context.Context) (*opp2p.PeerInfo, error) {
	start := time.Now()
	log.Debug("opp2p.Self", "rpc_address", i.rpcUrl)
	peerInfo, err := i.c.Self(ctx)
	if err != nil {
		metrics.RecordNetworkErrorDetails(i.network, i.node, "opp2p.Self", err)
		return nil, err
	}
	metrics.RecordRPCLatency(i.network, i.node, "opp2p.Self", time.Since(start))
	return peerInfo, err
}

func (i *InstrumentedOpP2PClient) Peers(ctx context.Context, connected bool) (*opp2p.PeerDump, error) {
	start := time.Now()
	log.Debug("opp2p.Peers", "rpc_address", i.rpcUrl, "connected", connected)
	peerDump, err := i.c.Peers(ctx, connected)
	if err != nil {
		metrics.RecordNetworkErrorDetails(i.network, i.node, "opp2p.Peers", err)
		return nil, err
	}
	metrics.RecordRPCLatency(i.network, i.node, "opp2p.Peers", time.Since(start))
	return peerDump, err
}

func (i *InstrumentedOpP2PClient) PeerStats(ctx context.Context) (*opp2p.PeerStats, error) {
	start := time.Now()
	log.Debug("opp2p.PeerStats", "rpc_address", i.rpcUrl)
	peerStats, err := i.c.PeerStats(ctx)
	if err != nil {
		metrics.RecordNetworkErrorDetails(i.network, i.node, "opp2p.PeerStats", err)
		return nil, err
	}
	metrics.RecordRPCLatency(i.network, i.node, "opp2p.PeerStats", time.Since(start))
	return peerStats, err
}

func (i *InstrumentedOpP2PClient) ConnectPeer(ctx context.Context, addr string) error {
	start := time.Now()
	log.Debug("opp2p.ConnectPeer", "rpc_address", i.rpcUrl, "addr", addr)
	err := i.c.ConnectPeer(ctx, addr)
	if err != nil {
		metrics.RecordNetworkErrorDetails(i.network, i.node, "opp2p.ConnectPeer", err)
		return err
	}
	metrics.RecordRPCLatency(i.network, i.node, "opp2p.ConnectPeer", time.Since(start))
	return err
}

func (i *InstrumentedOpP2PClient) DisconnectPeer(ctx context.Context, p peer.ID) error {
	start := time.Now()
	log.Debug("opp2p.DisconnectPeer", "rpc_address", i.rpcUrl, "peer", p)
	err := i.c.DisconnectPeer(ctx, p)
	if err != nil {
		metrics.RecordNetworkErrorDetails(i.network, i.node, "opp2p.DisconnectPeer", err)
		return err
	}
	metrics.RecordRPCLatency(i.network, i.node, "opp2p.DisconnectPeer", time.Since(start))
	return err
}

func (i *InstrumentedOpP2PClient) UnblockPeer(ctx context.Context, p peer.ID) error {
	start := time.Now()
	log.Debug("opp2p.UnblockPeer", "rpc_address", i.rpcUrl, "peer", p)
	err := i.c.UnblockPeer(ctx, p)
	if err != nil {
		metrics.RecordNetworkErrorDetails(i.network, i.node, "opp2p.UnblockPeer", err)
		return err
	}
	metrics.RecordRPCLatency(i.network, i.node, "opp2p.UnblockPeer", time.Since(start))
	return err
}

func (i *InstrumentedOpP2PClient) ProtectPeer(ctx context.Context, p peer.ID) error {
	start := time.Now()
	log.Debug("opp2p.ProtectPeer", "rpc_address", i.rpcUrl, "peer", p)
	err := i.c.ProtectPeer(ctx, p)
	if err != nil {
		metrics.RecordNetworkErrorDetails(i.network, i.node, "opp2p.ProtectPeer", err)
		return err
	}
	metrics.RecordRPCLatency(i.network, i.node, "opp2p.ProtectPeer", time.Since(start))
	return err
}

func (i *InstrumentedOpP2PClient) UnprotectPeer(ctx context.Context, p peer.ID) error {
	start := time.Now()
	log.Debug("opp2p.UnprotectPeer", "rpc_address", i.rpcUrl, "peer", p)
	err := i.c.UnprotectPeer(ctx, p)
	if err != nil {
		metrics.RecordNetworkErrorDetails(i.network, i.node, "opp2p.UnprotectPeer", err)
		return err
	}
	metrics.RecordRPCLatency(i.network, i.node, "opp2p.UnprotectPeer", time.Since(start))
	return err
}
