package proxyd

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRPCTiming_BasicFlow(t *testing.T) {
	timing := NewRPCTiming()
	require.NotNil(t, timing)
	require.Equal(t, "ok", timing.Status)

	// Test setting batch info
	timing.SetBatchInfo(true, 5)
	require.True(t, timing.IsBatch)
	require.Equal(t, 5, timing.BatchSize)

	// Test setting served by
	timing.SetServedBy("backend1")
	require.Equal(t, "backend1", timing.ServedBy)

	// Test cache counters
	timing.IncrCacheHit()
	timing.IncrCacheHit()
	timing.IncrCacheMiss()
	require.Equal(t, 2, timing.CacheHitCount)
	require.Equal(t, 1, timing.CacheMissCount)

	// Test status and error
	timing.SetStatus("error")
	timing.SetError("test error")
	require.Equal(t, "error", timing.Status)
	require.Equal(t, "test error", timing.Error)
}

func TestRPCTiming_Steps(t *testing.T) {
	timing := NewRPCTiming()

	// Test starting and ending a step
	endStep := timing.StartStep("test_step", "parent_phase", map[string]any{"key": "value"})
	time.Sleep(10 * time.Millisecond)
	endStep()

	require.Len(t, timing.Steps, 1)
	require.Equal(t, "test_step", timing.Steps[0].Name)
	require.Equal(t, "parent_phase", timing.Steps[0].Parent)
	require.Equal(t, 0, timing.Steps[0].Seq)
	require.GreaterOrEqual(t, timing.Steps[0].DurationMs, int64(10))
	require.Equal(t, "value", timing.Steps[0].Meta["key"])

	// Test recording a step directly
	timing.RecordStep("direct_step", "another_phase", 100, nil)
	require.Len(t, timing.Steps, 2)
	require.Equal(t, "direct_step", timing.Steps[1].Name)
	require.Equal(t, int64(100), timing.Steps[1].DurationMs)
	require.Equal(t, 1, timing.Steps[1].Seq)
}

func TestRPCTiming_PhaseMarkers(t *testing.T) {
	timing := NewRPCTiming()

	// Simulate a request flow
	time.Sleep(10 * time.Millisecond)
	timing.MarkUpstreamStart()

	time.Sleep(20 * time.Millisecond)
	timing.MarkUpstreamEnd()

	time.Sleep(5 * time.Millisecond)
	timing.MarkDownstreamStart()

	time.Sleep(10 * time.Millisecond)
	timing.MarkDownstreamEnd()

	// Compute phase durations
	recvToUpstream, upstreamRoundtrip, proxyRecvToDownstream, downstreamSend := timing.ComputePhaseDurations()

	require.GreaterOrEqual(t, recvToUpstream, int64(10))
	require.GreaterOrEqual(t, upstreamRoundtrip, int64(20))
	require.GreaterOrEqual(t, proxyRecvToDownstream, int64(35))
	require.GreaterOrEqual(t, downstreamSend, int64(10))
}

func TestRPCTiming_ContextPassing(t *testing.T) {
	timing := NewRPCTiming()
	ctx := context.Background()

	// Test context without timing
	require.Nil(t, GetTiming(ctx))

	// Add timing to context
	ctx = WithTiming(ctx, timing)

	// Retrieve timing from context
	retrieved := GetTiming(ctx)
	require.NotNil(t, retrieved)
	require.Equal(t, timing, retrieved)
}

func TestRPCTiming_ConcurrentAccess(t *testing.T) {
	timing := NewRPCTiming()
	done := make(chan bool)

	// Concurrent step additions
	for i := 0; i < 10; i++ {
		go func(idx int) {
			endStep := timing.StartStep("concurrent_step", "phase", map[string]any{"idx": idx})
			time.Sleep(time.Millisecond)
			endStep()
			done <- true
		}(i)
	}

	// Concurrent cache counter increments
	for i := 0; i < 10; i++ {
		go func() {
			timing.IncrCacheHit()
			timing.IncrCacheMiss()
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 20; i++ {
		<-done
	}

	require.Equal(t, 10, timing.CacheHitCount)
	require.Equal(t, 10, timing.CacheMissCount)
	require.Len(t, timing.Steps, 10)
}
