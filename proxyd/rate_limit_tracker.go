package proxyd

import (
	"sync"
	"time"
)

// rateLimitTracker aggregates frontend rate-limit rejections into
// fixed-cardinality metrics: a gauge of distinct limited keys per window and a
// histogram of rejected-request counts per key, with no per-IP labels.
type rateLimitTracker struct {
	mtx     sync.Mutex
	limited map[string]int
	maxKeys int

	done chan struct{}
	once sync.Once
}

func newRateLimitTracker(maxKeys int) *rateLimitTracker {
	return &rateLimitTracker{
		limited: make(map[string]int),
		maxKeys: maxKeys,
		done:    make(chan struct{}),
	}
}

func (t *rateLimitTracker) recordLimited(key string) {
	t.mtx.Lock()
	defer t.mtx.Unlock()
	if _, ok := t.limited[key]; !ok && len(t.limited) >= t.maxKeys {
		frontendRateLimitTrackerOverflowTotal.Inc()
		return
	}
	t.limited[key]++
}

func (t *rateLimitTracker) flush() {
	t.mtx.Lock()
	limited := t.limited
	t.limited = make(map[string]int, len(limited))
	t.mtx.Unlock()

	frontendRateLimitedUniqueKeys.Set(float64(len(limited)))
	for _, n := range limited {
		frontendRateLimitedRequestsPerKey.Observe(float64(n))
	}
}

// Start flushes the tracker every interval until Stop is called.
func (t *rateLimitTracker) Start(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				t.flush()
			case <-t.done:
				return
			}
		}
	}()
}

func (t *rateLimitTracker) Stop() {
	t.once.Do(func() { close(t.done) })
}
