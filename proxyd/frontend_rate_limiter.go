package proxyd

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

type FrontendRateLimiter interface {
	// Take consumes a key, and a maximum number of requests
	// per time interval. It returns a boolean denoting if
	// the limit could be taken, or an error if a failure
	// occurred in the backing rate limit implementation.
	//
	// No error will be returned if the limit could not be taken
	// as a result of the requestor being over the limit.
	Take(ctx context.Context, key string, amount int) (bool, error)
}

// limitedKeys is a wrapper around a map that stores a truncated
// timestamp and a mutex. The map is used to keep track of rate
// limit keys, and their used limits.
type limitedKeys struct {
	truncTS int64
	keys    map[string]int
	mtx     sync.Mutex
}

func newLimitedKeys(t int64) *limitedKeys {
	return &limitedKeys{
		truncTS: t,
		keys:    make(map[string]int),
	}
}

func (l *limitedKeys) Take(key string, amount, max int) bool {
	l.mtx.Lock()
	defer l.mtx.Unlock()
	val := l.keys[key]
	l.keys[key] = val + amount
	return val < max
}

// MemoryFrontendRateLimiter is a rate limiter that stores
// all rate limiting information in local memory. It works
// by storing a limitedKeys struct that references the
// truncated timestamp at which the struct was created. If
// the current truncated timestamp doesn't match what's
// referenced, the limit is reset. Otherwise, values in
// a map are incremented to represent the limit. This will
// never return an error.
type MemoryFrontendRateLimiter struct {
	currGeneration *limitedKeys
	dur            time.Duration
	max            int
	mtx            sync.Mutex
}

func NewMemoryFrontendRateLimit(dur time.Duration, max int) FrontendRateLimiter {
	return &MemoryFrontendRateLimiter{
		dur: dur,
		max: max,
	}
}

func (m *MemoryFrontendRateLimiter) Take(ctx context.Context, key string, amount int) (bool, error) {
	m.mtx.Lock()
	// Create truncated timestamp
	truncTS := truncateNow(m.dur)

	// If there is no current rate limit map or the rate limit map reference
	// a different timestamp, reset limits.
	if m.currGeneration == nil || m.currGeneration.truncTS != truncTS {
		m.currGeneration = newLimitedKeys(truncTS)
	}

	// Pull out the limiter so we can unlock before incrementing the limit.
	limiter := m.currGeneration

	m.mtx.Unlock()

	return limiter.Take(key, amount, m.max), nil
}

// RedisFrontendRateLimiter is a rate limiter that stores data in Redis.
// It uses the basic rate limiter pattern described on the Redis best
// practices website: https://redis.com/redis-best-practices/basic-rate-limiting/.
type RedisFrontendRateLimiter struct {
	r      redis.UniversalClient
	dur    time.Duration
	max    int
	prefix string
}

func NewRedisFrontendRateLimiter(r redis.UniversalClient, dur time.Duration, max int, prefix string) FrontendRateLimiter {
	return &RedisFrontendRateLimiter{
		r:      r,
		dur:    dur,
		max:    max,
		prefix: prefix,
	}
}

func (r *RedisFrontendRateLimiter) Take(ctx context.Context, key string, amount int) (bool, error) {
	var incr *redis.IntCmd
	truncTS := truncateNow(r.dur)
	fullKey := fmt.Sprintf("rate_limit:%s:%s:%d", r.prefix, key, truncTS)
	_, err := r.r.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		incr = pipe.IncrBy(ctx, fullKey, int64(amount))
		pipe.PExpire(ctx, fullKey, r.dur-time.Millisecond)
		return nil
	})
	if err != nil {
		frontendRateLimitTakeErrors.Inc()
		return false, err
	}

	return incr.Val()-1 < int64(r.max), nil
}

type noopFrontendRateLimiter struct{}

var NoopFrontendRateLimiter = &noopFrontendRateLimiter{}

func (n *noopFrontendRateLimiter) Take(ctx context.Context, key string, amount int) (bool, error) {
	return true, nil
}

// truncateNow truncates the current timestamp
// to the specified duration.
func truncateNow(dur time.Duration) int64 {
	return time.Now().Truncate(dur).Unix()
}

// FallbackRateLimiter is a combination of a primary and secondary rate limiter.
// If the primary rate limiter fails, due to an unexpected error, the secondary
// rate limiter will be used. This is useful to reduce reliance on a single Redis
// instance for rate limiting. If both fail, the request is not let through.
type FallbackRateLimiter struct {
	primary   FrontendRateLimiter
	secondary FrontendRateLimiter
}

func NewFallbackRateLimiter(primary FrontendRateLimiter, secondary FrontendRateLimiter) FrontendRateLimiter {
	return &FallbackRateLimiter{
		primary:   primary,
		secondary: secondary,
	}
}

func (r *FallbackRateLimiter) Take(ctx context.Context, key string) (bool, error) {
	if ok, err := r.primary.Take(ctx, key); err != nil {
		return r.secondary.Take(ctx, key)
	} else {
		return ok, err
	}
}
