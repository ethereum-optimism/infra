package monitor

import (
	"context"
	"testing"
	"time"

	"github.com/ethereum-optimism/optimism/op-conductor-mon/pkg/config"
	"github.com/ethereum-optimism/optimism/op-conductor/consensus"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

func TestNetwork_cleanupState(t *testing.T) {
	t.Run("should remove expired state", func(t *testing.T) {
		n := &Poller{
			config: &config.Config{NodeStateExpiration: 10 * time.Hour},
			state: map[string]*NodeState{
				"clean_me": {updatedAt: time.Now().Add(-11 * time.Hour)},
				"keep_me":  {updatedAt: time.Now()},
			},
		}
		ctx := context.Background()
		require.Equal(t, 2, len(n.state))
		n.cleanup(ctx)
		require.Equal(t, 1, len(n.state))
		require.NotNil(t, n.state["keep_me"])
	})
}

func TestNetwork_reportMetrics(t *testing.T) {
	membership := &consensus.ClusterMembership{
		Servers: []consensus.ServerInfo{
			{ID: "node_a", Suffrage: consensus.Voter},
			{ID: "node_b", Suffrage: consensus.Voter},
		},
	}
	newPoller := func(leaderWithID *consensus.ServerInfo) *Poller {
		return &Poller{
			nodesConfig: map[string]*config.NodeConfig{"node_a": {}, "node_b": {}},
			state: map[string]*NodeState{
				"node_a": {
					leaderWithID:      leaderWithID,
					clusterMembership: membership,
					updatedAt:         time.Now(),
				},
			},
		}
	}

	t.Run("should report the leader a node follows", func(t *testing.T) {
		newPoller(&consensus.ServerInfo{ID: "node_b"}).reportMetrics(context.Background())

		require.Equal(t, map[string]float64{"node_a": 0, "node_b": 1}, nodeLeaders(t, "node_a"))
	})

	t.Run("should report no leader while the leader is overridden", func(t *testing.T) {
		newPoller(&consensus.ServerInfo{ID: "node_b"}).reportMetrics(context.Background())
		newPoller(&consensus.ServerInfo{ID: leaderOverriddenID, Addr: "N/A"}).reportMetrics(context.Background())

		require.Equal(t, map[string]float64{"node_a": 0, "node_b": 0}, nodeLeaders(t, "node_a"))
	})

	t.Run("should report no leader while the cluster has none", func(t *testing.T) {
		newPoller(&consensus.ServerInfo{ID: "node_b"}).reportMetrics(context.Background())
		newPoller(&consensus.ServerInfo{}).reportMetrics(context.Background())

		require.Equal(t, map[string]float64{"node_a": 0, "node_b": 0}, nodeLeaders(t, "node_a"))
	})
}

// nodeLeaders returns the value of the node_leader metric for a node, keyed by leader.
func nodeLeaders(t *testing.T, node string) map[string]float64 {
	t.Helper()

	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)

	leaders := map[string]float64{}
	for _, family := range families {
		if family.GetName() != "op_conductor_mon_node_leader" {
			continue
		}
		for _, metric := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range metric.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["node"] == node {
				leaders[labels["leader"]] = metric.GetGauge().GetValue()
			}
		}
	}
	return leaders
}
