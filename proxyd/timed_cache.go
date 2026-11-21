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
		if method == "" || time.Duration(ttl) < time.Second || time.Duration(ttl)%time.Second != 0 {
			return fmt.Errorf("cache.method_ttls.%s: method must be nonempty and ttl must be a positive whole number of seconds", method)
		}
		// These calls have side effects or consume connection-local state.
		if strings.HasPrefix(method, "eth_send") || strings.HasPrefix(method, "eth_new") || method == "eth_uninstallFilter" || method == "eth_getFilterChanges" || method == "eth_subscribe" || method == "eth_unsubscribe" {
			return fmt.Errorf("cache.method_ttls.%s: method cannot be cached", method)
		}
	}
	return nil
}

func newConfiguredRPCCache(config CacheConfig, redisConfig RedisConfig, primary, reader redis.UniversalClient) *rpcCache {
	var storage Cache
	if primary == nil {
		storage = newMemoryCache()
	} else {
		ttl := defaultCacheTtl
		if config.TTL != 0 {
			ttl = time.Duration(config.TTL)
		}
		storage = newRedisCache(primary, reader, redisConfig.Namespace, ttl)
		if redisConfig.FallbackToMemory {
			storage = newFallbackCache(storage, newMemoryCache())
		}
	}
	c := newRPCCache(newCacheWithCompression(storage))
	for method, ttl := range config.MethodTTLs {
		// Never fall back to the non-expiring legacy cache for a timed method.
		if primary == nil {
			delete(c.handlers, method)
			continue
		}
		handler := &StaticMethodHandler{
			cache: newCacheWithCompression(newRedisCache(primary, reader, redisConfig.Namespace, time.Duration(ttl))),
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
