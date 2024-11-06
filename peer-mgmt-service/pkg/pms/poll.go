package pms

import (
	"context"
	"strings"
	"time"

	"github.com/ethereum-optimism/infra/peer-mgmt-service/pkg/config"
	"github.com/ethereum-optimism/infra/peer-mgmt-service/pkg/metrics"
	"github.com/ethereum-optimism/infra/peer-mgmt-service/pkg/metrics/opp2p_client"
	"github.com/ethereum/go-ethereum/log"
)

func (n *Network) Tick(ctx context.Context) {
	log.Debug("tick",
		"network", n.name)

	// clean up expired state
	n.cleanup(ctx)

	// poll members for current state
	n.poll(ctx)

	// wire up peer state by name
	n.updateGraph(ctx)

	// report state to metrics
	n.reportMetrics(ctx)

	// resolve state
	if !n.config.DryRun {
		n.resolveState(ctx)
	}

	log.Debug("tick done")
}

func (n *Network) cleanup(ctx context.Context) {
	for nodeName, nodeState := range n.state.nodes {
		if time.Since(nodeState.updatedAt) > n.config.NodeStateExpiration {
			log.Warn("node state expired",
				"node", nodeName,
				"node_peer_id", nodeState.self.PeerID.String(),
				"updated_at", nodeState.updatedAt)
			n.state.m.Lock()
			delete(n.state.nodes, nodeName)
			delete(n.state.nodesByPeerID, nodeState.self.PeerID.String())
			n.state.m.Unlock()
		}
	}
}

func (n *Network) poll(ctx context.Context) {
	for nodeName, nodeConfig := range n.nodesConfig {
		n.pollNode(ctx, nodeName, nodeConfig)
	}
}

func (n *Network) pollNode(ctx context.Context, nodeName string, nodeConfig *config.NodeConfig) {
	log.Debug("polling node",
		"name", nodeName,
		"rpc", nodeConfig.RPCAddress)

	client, err := opp2p_client.New(ctx, n.config, n.name, nodeName, nodeConfig.RPCAddress)
	if err != nil {
		return
	}

	self, err := client.Self(ctx)
	if err != nil {
		log.Error("cant get self",
			"node", nodeName,
			"err", err)
		return
	}

	log.Debug("got self", "node", nodeName, "peer_id", self.PeerID.String(), "self", self)

	peers, err := client.Peers(ctx, false)
	if err != nil {
		log.Error("cant get peers",
			"node", nodeName,
			"err", err)
		return
	}

	log.Debug("got peers", "node", nodeName, "peer_id", self.PeerID.String(), "peers", peers)

	// update state
	nodeState := &NodeState{
		self:      self,
		peers:     peers,
		updatedAt: time.Now(),
	}

	n.state.m.Lock()
	defer n.state.m.Unlock()
	oldPeerID := ""
	newPeerID := self.PeerID.String()

	// sanity check: a peer id should never change
	if s, exist := n.state.nodes[nodeName]; exist {
		oldPeerID = s.self.PeerID.String()
		if oldPeerID != newPeerID {
			// peer id changed
			delete(n.state.nodesByPeerID, oldPeerID)
			log.Warn("peer id changed",
				"node", nodeName,
				"old_peer_id", oldPeerID,
				"new_peer_id", newPeerID)
		}
	}

	n.state.nodes[nodeName] = nodeState
	n.state.nodesByPeerID[newPeerID] = nodeName
}

func (n *Network) updateGraph(ctx context.Context) {
	for _, nodeState := range n.state.nodes {
		knownPeers := make([]string, 0, len(nodeState.peers.Peers))
		for peerPeerID := range nodeState.peers.Peers {
			if peerPeerID == nodeState.self.PeerID.String() {
				continue
			}
			peerName, knownPeer := n.state.nodesByPeerID[peerPeerID]
			if knownPeer {
				knownPeers = append(knownPeers, peerName)
			}
		}

		// update graph
		n.state.m.Lock()
		nodeState.knownPeers = knownPeers
		n.state.m.Unlock()
	}

	log.Info("network mapping", "map", n.state.nodesByPeerID)
	for nodeName, nodeState := range n.state.nodes {
		log.Info("node state",
			"node", nodeName,
			"node_peer_id", nodeState.self.PeerID.String(),
			"peers", nodeState.knownPeers)
	}
}

func (n *Network) reportMetrics(ctx context.Context) {
	log.Debug("network state",
		"network", n.name,
		"nodes", len(n.state.nodes))

	healthyPeers := 0

	for nodeName, nodeState := range n.state.nodes {
		nodePeerID := nodeState.self.PeerID.String()
		// map of known/unknown, connectedness, count
		knownnessConnectedness := make(map[string]map[string]int)

		for peerPeerID, peerState := range nodeState.peers.Peers {
			if peerPeerID == nodePeerID {
				// dont report self as a peer
				continue
			}
			peerName, knownPeer := n.state.nodesByPeerID[peerPeerID]

			knownness := "known"
			if !knownPeer {
				knownness = "unknown"
			}
			connectedness := strings.ToLower(peerState.Connectedness.String())

			healthy := connectedness == "connected" && knownPeer
			if healthy {
				healthyPeers++

				metrics.RecordKnownPeerStateLatency(
					n.name,
					nodeName,
					nodeState.self.PeerID.String(),
					peerName,
					peerPeerID,
					peerState.Latency)
			}

			if _, ok := knownnessConnectedness[knownness]; !ok {
				knownnessConnectedness[knownness] = make(map[string]int)
			}
			if _, ok := knownnessConnectedness[knownness][connectedness]; !ok {
				knownnessConnectedness[knownness][connectedness] = 0
			}
			knownnessConnectedness[knownness][connectedness]++
		}

		// force reset of gauges
		for _, knownness := range []string{"known", "unknown"} {
			for _, connectedness := range []string{"notconnected", "connected", "canconnect", "cannotconnect"} {
				metrics.RecordPeerStateConnectedness(
					n.name,
					nodeName,
					nodeState.self.PeerID.String(),
					knownness,
					connectedness,
					0)
			}
		}

		// populate gauge with actual counts
		for knownness, connectednessMap := range knownnessConnectedness {
			for connectedness, count := range connectednessMap {
				metrics.RecordPeerStateConnectedness(
					n.name,
					nodeName,
					nodeState.self.PeerID.String(),
					knownness,
					connectedness,
					count)
			}
		}
	}

	metrics.RecordNetworkMemberCount(n.name, len(n.networkConfig.Members))

	// when all N members of the network are mutually connected,
	// we'll observe (N * (N-1)) connections
	expectedHealthy := float64(len(n.networkConfig.Members) * (len(n.networkConfig.Members) - 1))
	percentageHealthy := float64(healthyPeers) / expectedHealthy
	metrics.RecordNetworkPeerHealthness(n.name, percentageHealthy)

}

func (n *Network) resolveState(ctx context.Context) {
	for nodeName, nodeState := range n.state.nodes {
		// map expected peers to known state
		expectedPeers := make(map[string]bool, len(n.networkConfig.Members))
		for _, expectedPeer := range n.networkConfig.Members {
			if expectedPeer == nodeName {
				continue
			}
			expectedPeers[expectedPeer] = false
		}

		// mark healthy peers
		for peerPeerID, peer := range nodeState.peers.Peers {
			peerName, knownPeer := n.state.nodesByPeerID[peerPeerID]
			connectedness := strings.ToLower(peer.Connectedness.String())
			healthy := connectedness == "connected" && knownPeer
			if healthy {
				expectedPeers[peerName] = true
			}
		}

		// resolve state for unconnected peers
		for peerName, connected := range expectedPeers {
			if !connected {
				n.connectPeer(ctx,
					nodeName,
					peerName)
			}
		}
	}
}

func (n *Network) connectPeer(ctx context.Context, nodeName string, peerName string) {
	// short circuit if we are overriding connection to peer
	if n.overrideConnectPeer != nil {
		n.overrideConnectPeer(ctx, nodeName, peerName)
		return
	}

	nodeState := n.state.nodes[nodeName]
	nodeConfig := n.nodesConfig[nodeName]
	peerState := n.state.nodes[peerName]
	peerConfig := n.nodesConfig[peerName]

	if nodeState == nil {
		log.Error("node state not found",
			"network", n.name,
			"node", nodeName)
		return
	}

	if nodeConfig == nil {
		log.Error("node config not found",
			"network", n.name,
			"node", nodeName)
		return
	}

	if peerState == nil {
		log.Error("peer state not found",
			"network", n.name,
			"node", nodeName,
			"peer", peerName)
		return
	}

	if peerConfig == nil {
		log.Error("peer config not found",
			"network", n.name,
			"node", nodeName,
			"peer", peerName)
		return
	}

	if nodeConfig.PreventOutbound {
		log.Debug("node has outbound disabled",
			"network", n.name,
			"node", nodeName)
		return
	}

	if peerConfig.PreventInbound {
		log.Debug("peer has inbound disabled",
			"network", n.name,
			"peer", peerName)
		return
	}

	client, err := opp2p_client.New(ctx, n.config, n.name, nodeName, nodeConfig.RPCAddress)
	if err != nil {
		return
	}

	peerClient, err := opp2p_client.New(ctx, n.config, n.name, peerName, peerConfig.RPCAddress)
	if err != nil {
		return
	}

	// config peer_address is authoritative over dynamic address discovery
	peerAddr := peerConfig.PeerAddress
	if nodeConfig.Cluster == peerConfig.Cluster && peerConfig.PeerAddressLocal != "" {
		peerAddr = peerConfig.PeerAddressLocal
	}
	// when peer address is not explicit declared, we fall back to dynamic address discovery
	if peerAddr == "" {
		peerAddr = peerState.self.Addresses[0]
	}

	// config peer_id is authoritative over dynamic peer id discovery
	peerID := peerConfig.PeerID
	if peerID == "" {
		peerID = peerState.self.PeerID.String()
	}

	// special case for automatic PeerID discovery
	const PEER_ID_PLACEHOLDER = "{peer_id}"
	if strings.HasPrefix(peerAddr, "/dns4/") && strings.HasSuffix(peerAddr, "/p2p/"+PEER_ID_PLACEHOLDER) {
		peerAddr = peerAddr[0:len(peerAddr)-len(PEER_ID_PLACEHOLDER)] + peerID
	}

	metrics.RecordResolvedState(n.name, nodeName, peerName, peerID, peerAddr)

	log.Info("connecting to peer",
		"network", n.name,
		"node", nodeName,
		"node_cluster", nodeConfig.Cluster,
		"rpc_address", nodeConfig.RPCAddress,
		"peer", peerName,
		"peer_id", peerID,
		"peer_cluster", peerConfig.Cluster,
		"peer_addr", peerAddr)

	err = client.UnprotectPeer(ctx, peerState.self.PeerID)
	if err != nil {
		log.Error("cant unprotect peer",
			"network", n.name,
			"node", nodeName,
			"rpc_address", nodeConfig.RPCAddress,
			"peer", peerName,
			"peer_addr", peerAddr,
			"peer_id", peerID,
			"err", err)
		return
	}

	err = peerClient.UnprotectPeer(ctx, nodeState.self.PeerID)
	if err != nil {
		log.Error("cant unprotect peer (reverse)",
			"network", n.name,
			"node", nodeName,
			"node_id", nodeState.self.PeerID,
			"peer_rpc_address", peerConfig.RPCAddress,
			"peer", peerName,
			"err", err)
		return
	}

	err = client.UnblockPeer(ctx, peerState.self.PeerID)
	if err != nil {
		log.Error("cant disconnect peer",
			"network", n.name,
			"node", nodeName,
			"rpc_address", nodeConfig.RPCAddress,
			"peer", peerName,
			"peer_addr", peerAddr,
			"peer_id", peerID,
			"err", err)
		return
	}

	err = peerClient.UnblockPeer(ctx, nodeState.self.PeerID)
	if err != nil {
		log.Error("cant disconnect peer (reverse)",
			"network", n.name,
			"node", nodeName,
			"node_id", nodeState.self.PeerID,
			"peer_rpc_address", peerConfig.RPCAddress,
			"peer", peerName,
			"err", err)
		return
	}

	err = client.DisconnectPeer(ctx, peerState.self.PeerID)
	if err != nil {
		log.Error("cant disconnect peer",
			"network", n.name,
			"node", nodeName,
			"rpc_address", nodeConfig.RPCAddress,
			"peer", peerName,
			"peer_addr", peerAddr,
			"peer_id", peerID,
			"err", err)
		return
	}

	err = peerClient.DisconnectPeer(ctx, nodeState.self.PeerID)
	if err != nil {
		log.Error("cant disconnect peer (reverse)",
			"network", n.name,
			"node", nodeName,
			"node_id", nodeState.self.PeerID,
			"peer_rpc_address", peerConfig.RPCAddress,
			"peer", peerName,
			"err", err)
		return
	}

	err = client.ConnectPeer(ctx, peerAddr)
	if err != nil {
		log.Error("cant connect to peer",
			"network", n.name,
			"node", nodeName,
			"rpc_address", nodeConfig.RPCAddress,
			"peer", peerName,
			"peer_addr", peerAddr,
			"err", err)
		return
	}

	err = client.ProtectPeer(ctx, peerState.self.PeerID)
	if err != nil {
		log.Error("cant protect peer",
			"network", n.name,
			"node", nodeName,
			"rpc_address", nodeConfig.RPCAddress,
			"peer", peerName,
			"peer_addr", peerAddr,
			"peer_id", peerID,
			"err", err)
		return
	}

	log.Info("connected to peer",
		"network", n.name,
		"node", nodeName,
		"rpc_address", nodeConfig.RPCAddress,
		"peer", peerName,
		"peer_addr", peerAddr,
		"peer_id", peerID)
}
