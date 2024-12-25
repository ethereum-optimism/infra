package proxyd

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestFrontendRateLimiter(t *testing.T) {
	redisServer, err := miniredis.Run()
	require.NoError(t, err)
	defer redisServer.Close()

	redisClient := redis.NewClient(&redis.Options{
		Addr: fmt.Sprintf("127.0.0.1:%s", redisServer.Port()),
	})

	max := 2
	lims := []struct {
		name string
		frl  FrontendRateLimiter
	}{
		{"memory", NewMemoryFrontendRateLimit(2*time.Second, max)},
		{"redis", NewRedisFrontendRateLimiter(redisClient, 2*time.Second, max, "")},
		{"fallback", NewFallbackRateLimiter(NewMemoryFrontendRateLimit(2*time.Second, max), NewRedisFrontendRateLimiter(redisClient, 2*time.Second, max, ""))},
	}

	for _, cfg := range lims {
		frl := cfg.frl
		ctx := context.Background()
		t.Run(cfg.name, func(t *testing.T) {
			// Test with increment of 1
			for i := 0; i < 4; i++ {
				ok, err := frl.Take(ctx, "foo", 1)
				require.NoError(t, err)
				require.Equal(t, i < max, ok)
				ok, err = frl.Take(ctx, "bar", 1)
				require.NoError(t, err)
				require.Equal(t, i < max, ok)
			}
			time.Sleep(2 * time.Second)
			for i := 0; i < 4; i++ {
				ok, _ := frl.Take(ctx, "foo", 1)
				require.Equal(t, i < max, ok)
				ok, _ = frl.Take(ctx, "bar", 1)
				require.Equal(t, i < max, ok)
			}

			// Test with increment of 2
			time.Sleep(2 * time.Second)
			ok, err := frl.Take(ctx, "baz", 2)
			require.NoError(t, err)
			require.True(t, ok)
			ok, err = frl.Take(ctx, "baz", 1)
			require.NoError(t, err)
			require.False(t, ok)

			// Test with increment of 3
			time.Sleep(2 * time.Second)
			ok, err = frl.Take(ctx, "baz", 3)
			require.NoError(t, err)
			require.False(t, ok)
		})
	}
}

type errorFrontend struct{}

func (e *errorFrontend) Take(ctx context.Context, key string, amount int) (bool, error) {
	return false, fmt.Errorf("test error")
}

var _ FrontendRateLimiter = &errorFrontend{}

func TestFallbackRateLimiter(t *testing.T) {
	shouldSucceed := []FrontendRateLimiter{
		NewFallbackRateLimiter(NoopFrontendRateLimiter, NoopFrontendRateLimiter),
		NewFallbackRateLimiter(NoopFrontendRateLimiter, &errorFrontend{}),
		NewFallbackRateLimiter(&errorFrontend{}, NoopFrontendRateLimiter),
	}

	shouldFail := []FrontendRateLimiter{
		NewFallbackRateLimiter(&errorFrontend{}, &errorFrontend{}),
	}

	ctx := context.Background()
	for _, frl := range shouldSucceed {
		ok, err := frl.Take(ctx, "foo", 1)
		require.NoError(t, err)
		require.True(t, ok)
	}
	for _, frl := range shouldFail {
		ok, err := frl.Take(ctx, "foo", 1)
		require.Error(t, err)
		require.False(t, ok)
	}
}
