package proxyd

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

func TestRateLimitTrackerFlush(t *testing.T) {
	tr := newRateLimitTracker(50000)
	tr.recordLimited("1.2.3.4")
	tr.recordLimited("1.2.3.4")
	tr.recordLimited("1.2.3.4")
	tr.recordLimited("5.6.7.8")

	countBefore, sumBefore := gatherHistogram(t, "proxyd_frontend_rate_limited_requests_per_key")
	tr.flush()

	require.Equal(t, float64(2), gatherGauge(t, "proxyd_frontend_rate_limited_unique_keys"))
	count, sum := gatherHistogram(t, "proxyd_frontend_rate_limited_requests_per_key")
	require.Equal(t, countBefore+2, count)
	require.Equal(t, sumBefore+4, sum)
}

func TestRateLimitTrackerResetsBetweenWindows(t *testing.T) {
	tr := newRateLimitTracker(50000)
	tr.recordLimited("1.2.3.4")
	tr.flush()
	require.Equal(t, float64(1), gatherGauge(t, "proxyd_frontend_rate_limited_unique_keys"))

	countBefore, _ := gatherHistogram(t, "proxyd_frontend_rate_limited_requests_per_key")
	tr.flush()

	require.Equal(t, float64(0), gatherGauge(t, "proxyd_frontend_rate_limited_unique_keys"))
	count, _ := gatherHistogram(t, "proxyd_frontend_rate_limited_requests_per_key")
	require.Equal(t, countBefore, count, "an empty window must not add histogram observations")
}

func TestRateLimitTrackerCapsDistinctKeys(t *testing.T) {
	overflowBefore := gatherCounter(t, "proxyd_frontend_rate_limit_tracker_overflow_total", nil)
	tr := newRateLimitTracker(2)
	tr.recordLimited("a")
	tr.recordLimited("b")
	tr.recordLimited("c") // over the cap: dropped, counted as overflow
	tr.recordLimited("a") // already tracked: still counted past the cap

	countBefore, sumBefore := gatherHistogram(t, "proxyd_frontend_rate_limited_requests_per_key")
	tr.flush()

	require.Equal(t, float64(2), gatherGauge(t, "proxyd_frontend_rate_limited_unique_keys"))
	require.Equal(t, overflowBefore+1, gatherCounter(t, "proxyd_frontend_rate_limit_tracker_overflow_total", nil))
	count, sum := gatherHistogram(t, "proxyd_frontend_rate_limited_requests_per_key")
	require.Equal(t, countBefore+2, count)
	require.Equal(t, sumBefore+3, sum, "a=2 and b=1 rejected requests")
}

// gatherGauge returns the current value of the label-less gauge named `name`,
// or 0 if it has not been written yet.
func gatherGauge(t *testing.T, name string) float64 {
	t.Helper()
	mfs, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		require.Len(t, mf.GetMetric(), 1)
		return mf.GetMetric()[0].GetGauge().GetValue()
	}
	return 0
}

// gatherHistogram returns the sample count and sum of the label-less
// histogram named `name`, or zeros if it has not been written yet.
func gatherHistogram(t *testing.T, name string) (float64, float64) {
	t.Helper()
	mfs, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		require.Len(t, mf.GetMetric(), 1)
		h := mf.GetMetric()[0].GetHistogram()
		return float64(h.GetSampleCount()), h.GetSampleSum()
	}
	return 0, 0
}
