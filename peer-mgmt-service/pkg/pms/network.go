package pms

import (
	"context"
	"sync"
	"time"

	"github.com/ethereum-optimism/infra/peer-mgmt-service/pkg/config"
	"github.com/ethereum-optimism/optimism/op-node/p2p"
)

type Network struct {
	name          string
	networkConfig *config.NetworkConfig
	nodesConfig   map[string]*config.NodeConfig

	config *config.Config

	state *NetworkState

	overrideConnectPeer func(ctx context.Context, nodeName string, peerName string)
	cancelFunc          context.CancelFunc
}

type NetworkState struct {
	m sync.Mutex

	// nodes is a map of node name to node state
	nodes map[string]*NodeState

	// nodesByPeerID is a map of peer id to node name
	nodesByPeerID map[string]string
}

type NodeState struct {
	self       *p2p.PeerInfo
	peers      *p2p.PeerDump
	knownPeers []string
	updatedAt  time.Time
}

func New(
	config *config.Config,
	name string,
	networkConfig *config.NetworkConfig,
	nodesConfig map[string]*config.NodeConfig) *Network {
	network := &Network{
		name:          name,
		networkConfig: networkConfig,
		nodesConfig:   nodesConfig,
		config:        config,

		state: &NetworkState{
			nodes:         make(map[string]*NodeState, len(nodesConfig)),
			nodesByPeerID: make(map[string]string, len(nodesConfig)),
		},
	}
	return network
}

func (n *Network) Start(ctx context.Context) {
	networkCtx, cancelFunc := context.WithCancel(ctx)
	n.cancelFunc = cancelFunc

	schedule(networkCtx, n.config.PollInterval, n.Tick)
}

func (n *Network) Shutdown() {
	if n.cancelFunc != nil {
		n.cancelFunc()
	}
}
