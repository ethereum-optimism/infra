package metrics

import (
	"fmt"
	"regexp"
	"slices"
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
	validResults              = []string{"pass", "fail", "skip"}
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
		"run_id",
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
		"run_id",
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

func RecordValidation(network string, runID string, valName string, valType string, result string) {
	if !isValidResult(result) {
		log.Error("RecordValidation - invalid result", "result", result)
		return
	}
	if Debug {
		log.Debug("metric inc",
			"m", "validations_total",
			"network", network,
			"run_id", runID,
			"validator", valName,
			"type", valType,
			"result", result)
	}
	validationsTotal.WithLabelValues(network, runID, valName, valType, result).Inc()
}

func RecordAcceptance(network string, runID string, result string) {
	if !isValidResult(result) {
		log.Error("RecordAcceptance - invalid result", "result", result)
		return
	}
	if Debug {
		log.Debug("metric inc",
			"m", "acceptances_total",
			"network", network,
			"run_id", runID,
			"result", result,
		)
	}
	acceptancesTotal.WithLabelValues(network, runID, result).Inc()
}

func isValidResult(result string) bool {
	if slices.Contains(validResults, result) {
		return true
	}
	return false
}
