package metrics

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

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

	acceptanceResults = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: MetricsNamespace,
		Name:      "acceptance_results",
		Help:      "Result of acceptance tests",
	}, []string{
		"network_name",
		"run_id",
		"result",
	})

	acceptanceTestTotal = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: MetricsNamespace,
		Name:      "acceptance_test_total",
		Help:      "Total number of acceptance tests",
	}, []string{
		"network_name",
		"run_id",
	})

	acceptanceTestPassed = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: MetricsNamespace,
		Name:      "acceptance_test_passed",
		Help:      "Number of passed acceptance tests",
	}, []string{
		"network_name",
		"run_id",
	})

	acceptanceTestFailed = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: MetricsNamespace,
		Name:      "acceptance_test_failed",
		Help:      "Number of failed acceptance tests",
	}, []string{
		"network_name",
		"run_id",
	})

	acceptanceTestDuration = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: MetricsNamespace,
		Name:      "acceptance_test_duration",
		Help:      "Duration of acceptance tests",
	}, []string{
		"network_name",
		"run_id",
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

func RecordAcceptance(
	network string,
	runID string,
	result string,
	total int,
	passed int,
	failed int,
	duration time.Duration,
) {
	acceptanceResults.WithLabelValues(network, runID, result).Set(1)
	acceptanceTestTotal.WithLabelValues(network, runID).Set(float64(total))
	acceptanceTestPassed.WithLabelValues(network, runID).Set(float64(passed))
	acceptanceTestFailed.WithLabelValues(network, runID).Set(float64(failed))
	acceptanceTestDuration.WithLabelValues(network, runID).Set(duration.Seconds())
}

func isValidResult(result string) bool {
	return slices.Contains(validResults, result)
}
