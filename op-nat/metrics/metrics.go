package metrics

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/ethereum/go-ethereum/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	MetricsNamespace = "nat"
)

var (
	Debug                bool = true
	nonAlphanumericRegex      = regexp.MustCompile(`[^a-zA-Z ]+`)

	errorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: MetricsNamespace,
		Name:      "errors_total",
		Help:      "Count of errors",
	}, []string{
		"error",
	})

	validationsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: MetricsNamespace,
		Name:      "validations_total",
		Help:      "Count of validations",
	}, []string{
		"network_name",
		"name",
		"type",
		"result",
	})

	acceptancesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: MetricsNamespace,
		Name:      "acceptances_total",
		Help:      "Count of acceptance tests",
	}, []string{
		"network_name",
		"result",
	})
)

// errToLabel tries to make the error string a more valid Prometheus label
func errToLabel(err error) string {
	if err == nil {
		return "nil"
	}
	errClean := nonAlphanumericRegex.ReplaceAllString(err.Error(), "")
	errClean = strings.ReplaceAll(errClean, " ", "_")
	errClean = strings.ReplaceAll(errClean, "__", "_")
	return errClean
}

func RecordError(error string) {
	if Debug {
		log.Debug("metric inc",
			"m", "errors_total",
			"error", error,
		)
	}
	errorsTotal.WithLabelValues(error).Inc()
}

// RecordErrorDetails concats the error message to the label
// and also tries to clean the label to be a valid Prometheus label
func RecordErrorDetails(label string, err error) {
	if err == nil {
		return
	}
	label = fmt.Sprintf("%s.%s", label, errToLabel(err))
	RecordError(label)
}

func RecordValidation(network string, valName string, valType string, result string, valErr error) {
	if Debug {
		log.Debug("metric inc",
			"m", "validations_total",
			"network", network,
			"validator", valName,
			"type", valType,
			"result", result)
	}
	validationsTotal.WithLabelValues(network, valName, valType, result).Inc()
}

func RecordAcceptance(network string, result string, err error) {
	if Debug {
		log.Debug("metric inc",
			"m", "acceptances_total",
			"network", network,
			"result", result,
			"error", err)
	}
	acceptancesTotal.WithLabelValues(network, result).Inc()
}
