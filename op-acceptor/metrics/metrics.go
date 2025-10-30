package metrics

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	MetricsNamespace = "nat"
)

var (
	Debug                bool = true
	validResults              = []types.TestStatus{types.TestStatusPass, types.TestStatusFail, types.TestStatusSkip}
	nonAlphanumericRegex      = regexp.MustCompile(`[^a-zA-Z ]+`)

	// Tracks errors that occur during execution
	errorTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: MetricsNamespace,
		Name:      "errors_total",
		Help:      "Total count of errors by type",
	}, []string{
		"error",
	})

	// Tracks each validator execution
	validationTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: MetricsNamespace,
		Name:      "validations_total",
		Help:      "Total count of validator executions",
	}, []string{
		"network_name",
		"run_id",
		"name",
		"type",
		"result",
	})

	// Run-level metrics with run_id
	testRunStatus = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: MetricsNamespace,
		Name:      "test_run_status",
		Help:      "Status of test runs (value is always 1, results indicated by the 'result' label)",
	}, []string{
		"network_name",
		"run_id",
		"result",
	})

	testRunTestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: MetricsNamespace,
		Name:      "test_run_tests_total",
		Help:      "Total number of tests in a run",
	}, []string{
		"network_name",
		"run_id",
	})

	testRunTestsPassed = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: MetricsNamespace,
		Name:      "test_run_tests_passed",
		Help:      "Number of passed tests in a run",
	}, []string{
		"network_name",
		"run_id",
	})

	testRunTestsFailed = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: MetricsNamespace,
		Name:      "test_run_tests_failed",
		Help:      "Number of failed tests in a run",
	}, []string{
		"network_name",
		"run_id",
	})

	testRunTestsSkipped = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: MetricsNamespace,
		Name:      "test_run_tests_skipped",
		Help:      "Number of skipped tests in a run",
	}, []string{
		"network_name",
		"run_id",
	})

	testRunDurationSeconds = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: MetricsNamespace,
		Name:      "test_run_duration_seconds",
		Help:      "Duration of test runs in seconds",
	}, []string{
		"network_name",
		"run_id",
	})

	// Aggregate counters without run_id for time-based aggregation
	testsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: MetricsNamespace,
		Name:      "tests_total",
		Help:      "Total number of tests run (aggregate counter without run_id)",
	}, []string{
		"network_name",
	})

	testsPassed = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: MetricsNamespace,
		Name:      "tests_passed_total",
		Help:      "Total number of passed tests (aggregate counter without run_id)",
	}, []string{
		"network_name",
	})

	testsFailed = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: MetricsNamespace,
		Name:      "tests_failed_total",
		Help:      "Total number of failed tests (aggregate counter without run_id)",
	}, []string{
		"network_name",
	})

	testsSkipped = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: MetricsNamespace,
		Name:      "tests_skipped_total",
		Help:      "Total number of skipped tests (aggregate counter without run_id)",
	}, []string{
		"network_name",
	})

	// Metrics for individual test tracking
	testStatus = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: MetricsNamespace,
		Name:      "test_status",
		Help:      "Status of individual tests (1=pass, 0=fail, -1=skip)",
	}, []string{
		"network_name",
		"run_id",
		"test_name",
		"gate",
		"suite",
	})

	testDurationSeconds = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: MetricsNamespace,
		Name:      "test_duration_seconds",
		Help:      "Duration of individual tests in seconds",
	}, []string{
		"network_name",
		"run_id",
		"test_name",
		"gate",
		"suite",
	})

	// Test duration histogram to track distribution of test execution times
	testDurationHistogram = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: MetricsNamespace,
		Name:      "test_duration_histogram_seconds",
		Help:      "Histogram of test execution durations in seconds",
		Buckets:   []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60, 120, 300, 600}, // 100ms to 10min
	}, []string{
		"network_name",
		"test_name",
		"gate",
		"suite",
	})

	// Test timeout tracking
	testTimeouts = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: MetricsNamespace,
		Name:      "test_timeouts_total",
		Help:      "Total number of tests that timed out",
	}, []string{
		"network_name",
		"run_id",
		"test_name",
		"gate",
		"suite",
	})

	// Gate-level aggregated metrics
	gateTestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: MetricsNamespace,
		Name:      "gate_tests_total",
		Help:      "Total number of tests per gate",
	}, []string{
		"network_name",
		"gate",
	})

	gateTestsPassed = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: MetricsNamespace,
		Name:      "gate_tests_passed_total",
		Help:      "Total number of passed tests per gate",
	}, []string{
		"network_name",
		"gate",
	})

	gateTestsFailed = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: MetricsNamespace,
		Name:      "gate_tests_failed_total",
		Help:      "Total number of failed tests per gate",
	}, []string{
		"network_name",
		"gate",
	})

	gateDurationSeconds = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: MetricsNamespace,
		Name:      "gate_duration_seconds",
		Help:      "Duration of gate execution in seconds",
	}, []string{
		"network_name",
		"run_id",
		"gate",
	})

	// Suite-level metrics
	suiteTestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: MetricsNamespace,
		Name:      "suite_tests_total",
		Help:      "Total number of tests per suite",
	}, []string{
		"network_name",
		"gate",
		"suite",
	})

	suiteTestsPassed = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: MetricsNamespace,
		Name:      "suite_tests_passed_total",
		Help:      "Total number of passed tests per suite",
	}, []string{
		"network_name",
		"gate",
		"suite",
	})

	suiteTestsFailed = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: MetricsNamespace,
		Name:      "suite_tests_failed_total",
		Help:      "Total number of failed tests per suite",
	}, []string{
		"network_name",
		"gate",
		"suite",
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

// RecordError increments the error counter for a specific error type
func RecordError(error string) {
	if Debug {
		log.Debug("metric inc",
			"m", "errors_total",
			"error", error,
		)
	}
	errorTotal.WithLabelValues(error).Inc()
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

// RecordValidation records metrics for a single validator execution
func RecordValidation(network string, runID string, valName string, valType string, result types.TestStatus) {
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
	validationTotal.WithLabelValues(network, runID, valName, valType, string(result)).Inc()
}

// RecordAcceptance records metrics for a complete test run
func RecordAcceptance(
	network string,
	runID string,
	result string,
	total int,
	passed int,
	failed int,
	duration time.Duration,
) {
	// Record per-run metrics with run_id
	testRunStatus.WithLabelValues(network, runID, result).Set(1)
	testRunTestsTotal.WithLabelValues(network, runID).Add(float64(total))
	testRunTestsPassed.WithLabelValues(network, runID).Add(float64(passed))
	testRunTestsFailed.WithLabelValues(network, runID).Add(float64(failed))

	// Calculate skipped tests
	skipped := total - passed - failed
	if skipped > 0 {
		testRunTestsSkipped.WithLabelValues(network, runID).Add(float64(skipped))
	}

	testRunDurationSeconds.WithLabelValues(network, runID).Set(duration.Seconds())

	// Also record to the continuous counters without run_id
	testsTotal.WithLabelValues(network).Add(float64(total))
	testsPassed.WithLabelValues(network).Add(float64(passed))
	testsFailed.WithLabelValues(network).Add(float64(failed))
	if skipped > 0 {
		testsSkipped.WithLabelValues(network).Add(float64(skipped))
	}
}

// RecordIndividualTest records metrics for an individual test
func RecordIndividualTest(
	network string,
	runID string,
	testName string,
	gate string,
	suite string,
	status types.TestStatus,
	duration time.Duration,
) {
	// Convert test status to numeric value (1=pass, 0=fail, -1=skip)
	var statusValue float64
	switch status {
	case types.TestStatusPass:
		statusValue = 1.0
	case types.TestStatusSkip:
		statusValue = -1.0
	default: // fail or any other status
		statusValue = 0.0
	}

	testStatus.WithLabelValues(network, runID, testName, gate, suite).Set(statusValue)
	testDurationSeconds.WithLabelValues(network, runID, testName, gate, suite).Set(duration.Seconds())
}

func isValidResult(result types.TestStatus) bool {
	return slices.Contains(validResults, result)
}

// RecordTestDurationHistogram records test duration in a histogram for distribution analysis
func RecordTestDurationHistogram(network string, testName string, gate string, suite string, duration time.Duration) {
	testDurationHistogram.WithLabelValues(network, testName, gate, suite).Observe(duration.Seconds())
}

// RecordTestTimeout records when a test times out
func RecordTestTimeout(network string, runID string, testName string, gate string, suite string) {
	testTimeouts.WithLabelValues(network, runID, testName, gate, suite).Inc()
}

// RecordGateMetrics records aggregated metrics for a gate
func RecordGateMetrics(network string, runID string, gate string, total int, passed int, failed int, duration time.Duration) {
	gateTestsTotal.WithLabelValues(network, gate).Add(float64(total))
	gateTestsPassed.WithLabelValues(network, gate).Add(float64(passed))
	gateTestsFailed.WithLabelValues(network, gate).Add(float64(failed))
	gateDurationSeconds.WithLabelValues(network, runID, gate).Set(duration.Seconds())
}

// RecordSuiteMetrics records aggregated metrics for a suite
func RecordSuiteMetrics(network string, gate string, suite string, total int, passed int, failed int) {
	suiteTestsTotal.WithLabelValues(network, gate, suite).Add(float64(total))
	suiteTestsPassed.WithLabelValues(network, gate, suite).Add(float64(passed))
	suiteTestsFailed.WithLabelValues(network, gate, suite).Add(float64(failed))
}
