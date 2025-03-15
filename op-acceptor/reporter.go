package nat

import (
	"github.com/ethereum-optimism/infra/op-acceptor/metrics"
	"github.com/ethereum-optimism/infra/op-acceptor/runner"
)

// MetricsReporter is responsible for reporting metrics from test results.
type MetricsReporter interface {
	ReportResults(runID string, result *runner.RunnerResult)
}

// DefaultMetricsReporter implements the MetricsReporter interface.
type DefaultMetricsReporter struct{}

// NewDefaultMetricsReporter creates a new DefaultMetricsReporter.
func NewDefaultMetricsReporter() *DefaultMetricsReporter {
	return &DefaultMetricsReporter{}
}

// ReportResults reports the test results to metrics systems.
func (r *DefaultMetricsReporter) ReportResults(runID string, result *runner.RunnerResult) {
	metrics.RecordAcceptance(
		"todo",
		runID,
		string(result.Status),
		result.Stats.Total,
		result.Stats.Passed,
		result.Stats.Failed,
		result.Duration,
	)
}
