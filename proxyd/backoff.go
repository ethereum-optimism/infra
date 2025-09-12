package proxyd

import "time"

type BackoffStrategy interface {
	Backoff()
	WithinBackoff() bool
	LastBackoffTime() time.Time
	Reset()
	BackoffWait() time.Duration
}

type StaticBackoff struct {
	lastBackoffTime time.Time
	interval        time.Duration
}

func (b *StaticBackoff) WithinBackoff() bool {
	if b.lastBackoffTime.IsZero() {
		return false
	}
	return time.Since(b.lastBackoffTime) < b.interval
}

func (b *StaticBackoff) LastBackoffTime() time.Time {
	return b.lastBackoffTime
}

func (b *StaticBackoff) Reset() {
	b.lastBackoffTime = time.Time{}
}

func (b *StaticBackoff) BackoffWait() time.Duration {
	if b.WithinBackoff() {
		return b.interval - time.Since(b.lastBackoffTime)
	}
	return 0
}

type IncrementalBackoff struct {
	lastBackoffTime time.Time
	maxDuration     time.Duration
	stepInterval    time.Duration
	stepCount       int
}

func (b *IncrementalBackoff) nextWhen() {
	dur := b.stepInterval * time.Duration(b.stepCount)
	if dur > b.maxDuration {
		return b.maxDuration
	}
	return dur
}

func (b *IncrementalBackoff) WithinBackoff() bool {
	if b.lastBackoffTime.IsZero() {
		return false
	}
	return time.Since(b.lastBackoffTime) < b.maxDuration
}

func (b *IncrementalBackoff) LastBackoffTime() time.Time {
	return b.lastBackoffTime
}

func (b *IncrementalBackoff) Reset() {
	b.lastBackoffTime = time.Time{}
	b.stepCount = 0
}

func (b *IncrementalBackoff) BackoffWait() time.Duration {
	if b.WithinBackoff() {
		return b.maxDuration - time.Since(b.lastBackoffTime)
	}
	return 0
}
