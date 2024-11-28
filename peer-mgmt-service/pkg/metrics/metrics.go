package metrics

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	MetricsNamespace = "pms"
)

var (
	Debug                bool
	nonAlphanumericRegex = regexp.MustCompile(`[^a-zA-Z ]+`)

	errorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: MetricsNamespace,
		Name:      "errors_total",
		Help:      "Count of errors",
	}, []string{
		"error",
		"method",
		"network",
		"node",
	})

	rpcLatency = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: MetricsNamespace,
		Name:      "rpc_latency",
		Help:      "RPC latency per network, node and method (ms)",
	}, []string{
		"network",
		"node",
		"method",
	})

	networkMemberCount = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: MetricsNamespace,
		Name:      "network_member_count",
		Help:      "Member count per network",
	}, []string{
		"network",
	})

	_ = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: MetricsNamespace,
		Name:      "network_known_peer_count",
		Help:      "Known peer count per network",
	}, []string{
		"network",
	})

	networkPeerHealthness = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: MetricsNamespace,
		Name:      "network_peer_healthness",
		Help:      "Percentage of health peer in the network",
	}, []string{
		"network",
	})

	knownPeerStateLatency = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: MetricsNamespace,
		Name:      "known_peer_state_latency",
		Help:      "Known peer state latency per network, node and peer (ms)",
	}, []string{
		"network",
		"node",
		"node_peer_id",
		"peer",
		"peer_id",
	})

	peerStateConnectedness = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: MetricsNamespace,
		Name:      "peer_state_connectedness",
		Help:      "Peer state connectedness per network, node, knownness, connectedness",
	}, []string{
		"network",
		"node",
		"node_peer_id",
		"knowness",
		"connectedness",
	})

	resolvedState = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: MetricsNamespace,
		Name:      "resolved_state",
		Help:      "Count of resolved state events",
	}, []string{
		"network",
		"node",
		"node_peer_id",
		"peer",
		"peer_peer_id",
	})
)

func errLabel(err error) string {
	errClean := nonAlphanumericRegex.ReplaceAllString(err.Error(), "")
	errClean = strings.ReplaceAll(errClean, " ", "_")
	errClean = strings.ReplaceAll(errClean, "__", "_")
	return errClean
}

func RecordError(error string) {
	if Debug {
		log.Debug("metric inc",
			"m", "errors_total",
			"error", error)
	}
	errorsTotal.WithLabelValues(error).Inc()
}

// RecordErrorDetails concats the error message to the label removing non-alpha chars
func RecordErrorDetails(label string, err error) {
	label = fmt.Sprintf("%s.%s", label, errLabel(err))
	RecordError(label)
}

func RecordNetworkErrorDetails(network string, node string, method string, err error) {
	if Debug {
		log.Debug("metric inc",
			"m", "errors_total",
			"network", network,
			"node", node,
			"error", errLabel(err))
	}
	errorsTotal.WithLabelValues(errLabel(err), method, network, node).Inc()
}

func RecordRPCLatency(network string, node string, method string, latency time.Duration) {
	if Debug {
		log.Debug("metric set",
			"m", "rpc_latency",
			"network", network,
			"node", node,
			"method", method,
			"latency", latency)
	}
	rpcLatency.WithLabelValues(network, node, method).Set(float64(latency.Milliseconds()))
}

func RecordNetworkMemberCount(network string, count int) {
	if Debug {
		log.Debug("metric set",
			"m", "network_member_count",
			"network", network,
			"count", count)
	}
	networkMemberCount.WithLabelValues(network).Set(float64(count))
}

func RecordNetworkPeerHealthness(network string, percentage float64) {
	if Debug {
		log.Debug("metric set",
			"m", "network_peer_healthness",
			"network", network,
			"percentage", percentage)
	}
	networkPeerHealthness.WithLabelValues(network).Set(percentage)
}

func RecordKnownPeerStateLatency(network string, node string, nodePeerID string, peer string, peerPeerID string, latency time.Duration) {
	if Debug {
		log.Debug("metric set",
			"m", "known_peer_state_latency",
			"network", network,
			"node", node,
			"node_peer_id", nodePeerID,
			"peer", peer,
			"peer_peer_id", peerPeerID,
			"latency", latency)
	}
	knownPeerStateLatency.WithLabelValues(network, node, nodePeerID, peer, peerPeerID).Set(float64(latency.Milliseconds()))
}

func RecordPeerStateConnectedness(network string, node string, nodePeerID string, knowness string, connectedness string, count int) {
	if Debug {
		log.Debug("metric set",
			"m", "peer_state_connectedness",
			"network", network,
			"node", node,
			"node_peer_id", nodePeerID,
			"knowness", knowness,
			"connectedness", connectedness,
			"count", count)
	}
	peerStateConnectedness.WithLabelValues(network, node, nodePeerID, knowness, connectedness).Set(float64(count))
}

func RecordResolvedState(network string, nodeName string, peerName string, peerID string, peerAddr string) {
	if Debug {
		log.Debug("metric inc",
			"m", "resolved_state",
			"network", network,
			"node", nodeName,
			"peer", peerName,
			"peer_id", peerID,
			"peer_addr", peerAddr)
	}
	resolvedState.WithLabelValues(network, nodeName, peerName, peerID, peerAddr).Inc()
}
