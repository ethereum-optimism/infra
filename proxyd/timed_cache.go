package proxyd

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// blockTagParam identifies methods with a positional block selector. Other
// methods can still opt into whole-method caching without a block_tag filter.
func blockTagParam(method string) (int, bool) {
	switch method {
	case "eth_getBlockByNumber", "eth_getBlockReceipts", "eth_getBlockTransactionCountByNumber", "eth_getUncleCountByBlockNumber", "eth_getTransactionByBlockNumberAndIndex", "eth_getUncleByBlockNumberAndIndex", "debug_traceBlockByNumber", "debug_getRawReceipts", "consensus_getReceipts":
		return 0, true
	case "eth_call", "eth_getBalance", "eth_getCode", "eth_getTransactionCount":
		return 1, true
	case "eth_getStorageAt", "eth_getProof":
		return 2, true
	default:
		return 0, false
	}
}

func (c CacheConfig) validate() error {
	for method, rule := range c.Methods {
		if method == "" || time.Duration(rule.TTL) < time.Millisecond {
			return fmt.Errorf("cache.methods.%s: method must be nonempty and ttl must be at least 1ms", method)
		}
		// These calls have side effects or consume connection-local state.
		if strings.HasPrefix(method, "eth_send") || strings.HasPrefix(method, "eth_new") || method == "eth_uninstallFilter" || method == "eth_getFilterChanges" || method == "eth_subscribe" || method == "eth_unsubscribe" {
			return fmt.Errorf("cache.methods.%s: method cannot be cached", method)
		}
		if rule.BlockTag != "" {
			if _, ok := blockTagParam(method); !ok {
				return fmt.Errorf("cache.methods.%s: block_tag is not supported for this method", method)
			}
			switch rule.BlockTag {
			case "latest", "safe", "finalized", "earliest":
			default:
				return fmt.Errorf("cache.methods.%s: unsupported block_tag %q", method, rule.BlockTag)
			}
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
	for method, rule := range config.Methods {
		handler := &StaticMethodHandler{
			cache: makeCache(time.Duration(rule.TTL)),
			// Separate policies, including rolling deployments with different TTLs.
			keyPrefix: fmt.Sprintf(":timed:%d:%s", rule.TTL, rule.BlockTag),
		}
		if rule.BlockTag != "" {
			pos, _ := blockTagParam(method)
			tag := rule.BlockTag
			handler.filterGet = func(req *RPCReq) bool {
				var params []json.RawMessage
				if json.Unmarshal(req.Params, &params) != nil || len(params) <= pos {
					return false
				}
				var value string
				return json.Unmarshal(params[pos], &value) == nil && value == tag
			}
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
