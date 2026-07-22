package bailiff

import (
	"slices"
	"strconv"
	"strings"

	opmetrics "github.com/ethereum-optimism/optimism/op-service/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

var MetricsRegistry = opmetrics.NewRegistry()

const metricsNamespace = "bailiff"

var (
	httpRequestsCount = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Name:      "http_requests_total",
		Help:      "Number of HTTP requests made",
	}, []string{"status_code"})
	receivedWebhooksCount = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Name:      "received_webhooks_total",
		Help:      "Number of received webhooks",
	}, []string{"event"})
	processedPRsCount = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Name:      "processed_prs_total",
		Help:      "Number of PRs processed by result",
	}, []string{"result"})
)

func RecordHTTPRequest(code int) {
	httpRequestsCount.WithLabelValues(strconv.Itoa(code)).Inc()
}

func RecordReceivedWebhook(event string) {
	receivedWebhooksCount.WithLabelValues(event).Inc()
}

func RecordProcessedPR(result error) {
	var errStr string
	if result == nil {
		errStr = "success"
	} else if slices.Contains(handlerExpectedErrors, result) {
		errStr = strings.ToLower(strings.ReplaceAll(result.Error(), " ", "-"))
	} else {
		errStr = "unknown"
	}
	processedPRsCount.WithLabelValues(errStr).Inc()
}

func init() {
	MetricsRegistry.MustRegister(
		httpRequestsCount,
		receivedWebhooksCount,
		processedPRsCount,
	)
}
