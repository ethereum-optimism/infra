package proxyd

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

func (c CacheConfig) validate() error {
	for method, ttl := range c.MethodTTLs {
		if method == "" || time.Duration(ttl) < time.Millisecond {
			return fmt.Errorf("cache.method_ttls.%s: method must be nonempty and ttl must be at least 1ms", method)
		}
		// These calls have side effects or consume connection-local state.
		if strings.HasPrefix(method, "eth_send") || strings.HasPrefix(method, "eth_new") || method == "eth_uninstallFilter" || method == "eth_getFilterChanges" || method == "eth_subscribe" || method == "eth_unsubscribe" {
			return fmt.Errorf("cache.method_ttls.%s: method cannot be cached", method)
		}
	}
	return nil
}

func newConfiguredRPCCache(config CacheConfig, redisConfig RedisConfig, primary, reader redis.UniversalClient) *rpcCache {
	makeCache := func(ttl time.Duration) Cache {
		var c Cache
		if primary == nil {
			c = newMemoryCache(ttl)
		} else {
			c = newRedisCache(primary, reader, redisConfig.Namespace, ttl)
			if redisConfig.FallbackToMemory {
				c = newFallbackCache(c, newMemoryCache(ttl))
			}
		}
		return newCacheWithCompression(c)
	}
	ttl := defaultCacheTtl
	if config.TTL != 0 {
		ttl = time.Duration(config.TTL)
	}
	c := newRPCCache(makeCache(ttl))
	for method, ttl := range config.MethodTTLs {
		handler := &StaticMethodHandler{
			cache: makeCache(time.Duration(ttl)),
			// Separate policies, including rolling deployments with different TTLs.
			keyPrefix: fmt.Sprintf(":timed:%d", ttl),
		}
		handler.filterPut = func(req *RPCReq, res *RPCRes) bool {
			if res == nil || res.Error != nil || res.Result == nil || res.servedLocally {
				return false
			}
			value, err := json.Marshal(res.Result)
			return err == nil && string(value) != "null"
		}
		c.handlers[method] = handler
	}
	return c
}
