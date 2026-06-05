package pms

import (
	"context"
	"testing"
	"time"

	"github.com/ethereum-optimism/infra/peer-mgmt-service/pkg/config"
	"github.com/ethereum-optimism/optimism/op-node/p2p"
	"github.com/stretchr/testify/require"
)

func TestNetwork_cleanupState(t *testing.T) {
	t.Run("should remove expired state", func(t *testing.T) {
		n := &Network{
			config: &config.Config{NodeStateExpiration: 10 * time.Hour},
			state: &NetworkState{nodes: map[string]*NodeState{
				"clean_me": {updatedAt: time.Now().Add(-11 * time.Hour), self: &p2p.PeerInfo{PeerID: "clean_me"}},
				"keep_me":  {updatedAt: time.Now(), self: &p2p.PeerInfo{PeerID: "keep_me"}},
			}},
		}
		ctx := context.Background()
		require.Equal(t, 2, len(n.state.nodes))
		n.cleanup(ctx)
		require.Equal(t, 1, len(n.state.nodes))
		require.NotNil(t, n.state.nodes["keep_me"])
	})

}

func TestNetwork_updateGraph(t *testing.T) {
	t.Run("should update graph with known peers", func(t *testing.T) {
		n := &Network{
			state: &NetworkState{
				nodes: map[string]*NodeState{
					"p1": {
						self: &p2p.PeerInfo{PeerID: "peer_id_1"},
						peers: &p2p.PeerDump{
							Peers: map[string]*p2p.PeerInfo{
								"peer_id_2": {PeerID: "peer_id_2"},
							},
						},
					},
					"p2": {
						self: &p2p.PeerInfo{PeerID: "peer_id_2"},
						peers: &p2p.PeerDump{
							Peers: map[string]*p2p.PeerInfo{
								"peer_id_1": {PeerID: "peer_id_1"},
							},
						},
					},
					"p3": {
						self:  &p2p.PeerInfo{PeerID: "peer_id_3"},
						peers: &p2p.PeerDump{},
					},
				},
				nodesByPeerID: map[string]string{
					"peer_id_1": "p1",
					"peer_id_2": "p2",
					"peer_id_3": "p3",
				},
			},
		}
		ctx := context.Background()
		require.Equal(t, 3, len(n.state.nodes))
		n.updateGraph(ctx)
		require.Equal(t, 3, len(n.state.nodes))
		require.Equal(t, 1, len(n.state.nodes["p1"].knownPeers))
		require.Equal(t, "p2", n.state.nodes["p1"].knownPeers[0])
		require.Equal(t, 1, len(n.state.nodes["p2"].knownPeers))
		require.Equal(t, "p1", n.state.nodes["p2"].knownPeers[0])
		require.Equal(t, 0, len(n.state.nodes["p3"].knownPeers))
	})
}

func TestNetwork_New_PreregistersExternalPeers(t *testing.T) {
	cfg := &config.Config{}
	netCfg := &config.NetworkConfig{Members: []string{"internal", "external"}}
	nodes := map[string]*config.NodeConfig{
		"internal": {RPCAddress: "http://internal:9545"},
		"external": {PeerID: "ext-peer-id", PeerAddress: "/dns4/ext/tcp/9003/p2p/ext-peer-id"},
	}

	n := New(cfg, "net", netCfg, nodes)

	require.Equal(t, "external", n.state.nodesByPeerID["ext-peer-id"])
	_, hasInternal := n.state.nodesByPeerID["internal"]
	require.False(t, hasInternal, "internal nodes should not be pre-registered (their peer_id is discovered)")
}

func TestNetwork_resolveState_DialsExternalPeer(t *testing.T) {
	type args struct{ node, peer string }
	calls := []args{}

	n := &Network{
		overrideConnectPeer: func(ctx context.Context, nodeName, peerName string) {
			calls = append(calls, args{nodeName, peerName})
		},
		networkConfig: &config.NetworkConfig{Members: []string{"internal", "external"}},
		nodesConfig: map[string]*config.NodeConfig{
			"internal": {RPCAddress: "http://internal:9545"},
			"external": {PeerID: "ext-peer-id", PeerAddress: "/dns4/ext/tcp/9003/p2p/ext-peer-id"},
		},
		state: &NetworkState{
			nodes: map[string]*NodeState{
				"internal": {
					self:  &p2p.PeerInfo{PeerID: "internal-peer-id"},
					peers: &p2p.PeerDump{Peers: map[string]*p2p.PeerInfo{}},
				},
			},
			nodesByPeerID: map[string]string{
				"internal-peer-id": "internal",
				"ext-peer-id":      "external",
			},
		},
	}

	n.resolveState(context.Background())

	require.Equal(t, []args{{"internal", "external"}}, calls)
}

func TestNetwork_resolveState(t *testing.T) {
	t.Run("should connect to known peers", func(t *testing.T) {
		type connectPeerArgs struct {
			nodeName string
			peerName string
		}
		connectPeerExpected := map[connectPeerArgs]bool{
			{"p1", "p3"}: false,
			{"p2", "p3"}: false,
			{"p3", "p1"}: false,
			{"p3", "p2"}: false,
		}
		n := &Network{
			overrideConnectPeer: func(ctx context.Context, nodeName string, peerName string) {
				connectPeerExpected[connectPeerArgs{nodeName, peerName}] = true
			},
			networkConfig: &config.NetworkConfig{
				Members: []string{"p1", "p2", "p3"},
			},
			state: &NetworkState{
				nodes: map[string]*NodeState{
					"p1": {
						self: &p2p.PeerInfo{PeerID: "peer_id_1"},
						peers: &p2p.PeerDump{
							Peers: map[string]*p2p.PeerInfo{
								"peer_id_2": {PeerID: "peer_id_2"},
							},
						},
					},
					"p2": {
						self: &p2p.PeerInfo{PeerID: "peer_id_2"},
						peers: &p2p.PeerDump{
							Peers: map[string]*p2p.PeerInfo{
								"peer_id_1": {PeerID: "peer_id_1"},
							},
						},
					},
					"p3": {
						self:  &p2p.PeerInfo{PeerID: "peer_id_3"},
						peers: &p2p.PeerDump{},
					},
				},
				nodesByPeerID: map[string]string{
					"peer_id_1": "p1",
					"peer_id_2": "p2",
					"peer_id_3": "p3",
				},
			},
		}
		ctx := context.Background()
		n.resolveState(ctx)
		require.Equal(t, 3, len(n.state.nodes))
		for args, connected := range connectPeerExpected {
			require.True(t, connected, "expected connectPeer to be called with args: %v", args)
		}
	})
}

func TestNetwork_internalMemberCount(t *testing.T) {
	n := &Network{
		networkConfig: &config.NetworkConfig{Members: []string{"a", "b", "ext"}},
		nodesConfig: map[string]*config.NodeConfig{
			"a":   {RPCAddress: "http://a:9545"},
			"b":   {RPCAddress: "http://b:9545"},
			"ext": {PeerID: "x", PeerAddress: "/dns4/ext/tcp/9003/p2p/x"},
		},
	}
	require.Equal(t, 2, n.internalMemberCount())
}
