package pms

import (
	"context"
	"testing"
	"time"

	"github.com/ethereum-optimism/infrastructure-services/peer-mgmt-service/pkg/config"
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
