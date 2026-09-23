package proxyd

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/alicebob/miniredis"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestTimedCacheConfig(t *testing.T) {
	var cfg Config
	_, err := toml.Decode(`[cache]
enabled = true
[cache.methods.eth_getBlockByNumber]
ttl = "250ms"
block_tag = "latest"
[cache.methods.eth_gasPrice]
ttl = "1s"
`, &cfg)
	require.NoError(t, err)
	require.NoError(t, cfg.Cache.validate())
	require.Equal(t, TOMLDuration(250*time.Millisecond), cfg.Cache.Methods["eth_getBlockByNumber"].TTL)
	for _, tc := range []struct {
		method string
		rule   CacheMethodConfig
	}{
		{"eth_call", CacheMethodConfig{}},
		{"eth_call", CacheMethodConfig{TTL: -1}},
		{"eth_call", CacheMethodConfig{TTL: TOMLDuration(time.Microsecond)}},
		{"eth_sendRawTransaction", CacheMethodConfig{TTL: TOMLDuration(time.Second)}},
		{"eth_getFilterChanges", CacheMethodConfig{TTL: TOMLDuration(time.Second)}},
		{"eth_getLogs", CacheMethodConfig{TTL: TOMLDuration(time.Second), BlockTag: "latest"}},
		{"eth_call", CacheMethodConfig{TTL: TOMLDuration(time.Second), BlockTag: "pending"}},
	} {
		require.Error(t, (CacheConfig{Methods: map[string]CacheMethodConfig{tc.method: tc.rule}}).validate())
	}
}

func TestTimedCacheScopeAndArguments(t *testing.T) {
	ctx := context.Background()
	c := newConfiguredRPCCache(CacheConfig{Methods: map[string]CacheMethodConfig{
		"eth_call": {TTL: TOMLDuration(time.Second), BlockTag: "latest"},
	}}, RedisConfig{}, nil, nil)
	req := &RPCReq{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "eth_call", Params: json.RawMessage(`[{"to":"0x123","data":"0x456"},"latest"]`)}
	require.NoError(t, c.PutRPC(ctx, req, &RPCRes{Result: "0xbeef"}))
	req.ID = json.RawMessage(`2`)
	hit, err := c.GetRPC(ctx, req)
	require.NoError(t, err)
	require.Equal(t, "0xbeef", hit.Result)
	require.Equal(t, req.ID, hit.ID)
	for _, params := range []string{
		`[{"to":"0x999","data":"0x456"},"latest"]`,
		`[{"to":"0x123","data":"0x999"},"latest"]`,
		`[{"to":"0x123","data":"0x456"},"latest",{}]`,
		`[{"to":"0x123","data":"0x456"},"pending"]`,
		`[{"to":"0x123","data":"0x456"},"0x10"]`,
		`[{"to":"0x123","data":"0x456"}]`,
	} {
		other := *req
		other.Params = json.RawMessage(params)
		hit, err := c.GetRPC(ctx, &other)
		require.NoError(t, err)
		require.Nil(t, hit, params)
	}
	for _, params := range []string{`[{},"pending"]`, `[{},"safe"]`, `[{},"0x10"]`, `[{}]`} {
		other := *req
		other.Params = json.RawMessage(params)
		require.NoError(t, c.PutRPC(ctx, &other, &RPCRes{Result: "0x1"}))
		hit, err := c.GetRPC(ctx, &other)
		require.NoError(t, err)
		require.Nil(t, hit)
	}
}

func TestTimedCacheExpiry(t *testing.T) {
	ctx := context.Background()
	for _, useRedis := range []bool{false, true} {
		name := "memory"
		if useRedis {
			name = "redis"
		}
		t.Run(name, func(t *testing.T) {
			var client redis.UniversalClient
			var advance func(time.Duration)
			if useRedis {
				srv, err := miniredis.Run()
				require.NoError(t, err)
				defer srv.Close()
				client = redis.NewClient(&redis.Options{Addr: srv.Addr()})
				defer client.Close()
				advance = srv.FastForward
			}
			c := newConfiguredRPCCache(CacheConfig{Methods: map[string]CacheMethodConfig{
				"eth_getBlockByNumber": {TTL: TOMLDuration(250 * time.Millisecond), BlockTag: "latest"},
			}}, RedisConfig{}, client, client)
			if !useRedis {
				memory := c.handlers["eth_getBlockByNumber"].(*StaticMethodHandler).cache.(*cacheWithCompression).cache.(*cache)
				now := time.Unix(1000, 0)
				memory.now = func() time.Time { return now }
				advance = func(d time.Duration) { now = now.Add(d) }
			}
			req := &RPCReq{Method: "eth_getBlockByNumber", Params: json.RawMessage(`["latest",false]`)}
			require.NoError(t, c.PutRPC(ctx, req, &RPCRes{Result: "block"}))
			advance(249 * time.Millisecond)
			hit, err := c.GetRPC(ctx, req)
			require.NoError(t, err)
			require.NotNil(t, hit)
			advance(time.Millisecond)
			hit, err = c.GetRPC(ctx, req)
			require.NoError(t, err)
			require.Nil(t, hit)
		})
	}
}

func TestTimedCacheSkipsUnusableResults(t *testing.T) {
	ctx := context.Background()
	c := newConfiguredRPCCache(CacheConfig{Methods: map[string]CacheMethodConfig{
		"eth_gasPrice": {TTL: TOMLDuration(time.Second)},
	}}, RedisConfig{}, nil, nil)
	req := &RPCReq{Method: "eth_gasPrice"}
	for _, res := range []*RPCRes{
		{Error: ErrOverRateLimit}, {}, {Result: json.RawMessage(`null`)},
		{Result: "local", servedLocally: true},
	} {
		require.NoError(t, c.PutRPC(ctx, req, res))
		hit, err := c.GetRPC(ctx, req)
		require.NoError(t, err)
		require.Nil(t, hit)
	}
	require.NoError(t, c.PutRPC(ctx, req, &RPCRes{Result: "0x1"}))
	hit, err := c.GetRPC(ctx, req)
	require.NoError(t, err)
	require.Equal(t, "0x1", hit.Result)
}

func TestCacheRequestSurvivesBlockRewrite(t *testing.T) {
	ctx := context.Background()
	c := newConfiguredRPCCache(CacheConfig{Methods: map[string]CacheMethodConfig{
		"eth_getBlockByNumber": {TTL: TOMLDuration(time.Second), BlockTag: "latest"},
	}}, RedisConfig{}, nil, nil)
	req := &RPCReq{Method: "eth_getBlockByNumber", Params: json.RawMessage(`["latest",false]`)}
	batch := createBatchRequest([]batchElem{{Req: req}})
	result, err := RewriteRequest(RewriteContext{latest: 100, consensusMode: true}, batch[0], &RPCRes{})
	require.NoError(t, err)
	require.Equal(t, RewriteOverrideRequest, result)
	require.JSONEq(t, `["0x64",false]`, string(batch[0].Params))
	require.JSONEq(t, `["latest",false]`, string(req.Params))
	require.NoError(t, c.PutRPC(ctx, req, &RPCRes{Result: "block"}))
	hit, err := c.GetRPC(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, hit)
}

func TestMemoryFallbackExpires(t *testing.T) {
	ctx := context.Background()
	memory := newMemoryCache(250 * time.Millisecond)
	now := time.Unix(1000, 0)
	memory.now = func() time.Time { return now }
	fallback := newFallbackCache(&errorCache{}, memory)
	require.NoError(t, fallback.Put(ctx, "key", "value"))
	value, err := fallback.Get(ctx, "key")
	require.NoError(t, err)
	require.Equal(t, "value", value)
	now = now.Add(250 * time.Millisecond)
	value, err = fallback.Get(ctx, "key")
	require.NoError(t, err)
	require.Empty(t, value)
}

func TestTimedCacheWithoutTagCachesEachBlockSelector(t *testing.T) {
	ctx := context.Background()
	c := newConfiguredRPCCache(CacheConfig{Methods: map[string]CacheMethodConfig{
		"eth_getBlockByNumber": {TTL: TOMLDuration(250 * time.Millisecond)},
	}}, RedisConfig{}, nil, nil)
	for _, block := range []string{"latest", "safe", "finalized", "pending", "0x1234"} {
		for _, full := range []bool{false, true} {
			req := &RPCReq{Method: "eth_getBlockByNumber", Params: mustMarshalJSON([]interface{}{block, full})}
			hit, err := c.GetRPC(ctx, req)
			require.NoError(t, err)
			require.Nil(t, hit, "different selectors and full-transaction flags must not collide")
			require.NoError(t, c.PutRPC(ctx, req, &RPCRes{Result: block}))
			hit, err = c.GetRPC(ctx, req)
			require.NoError(t, err)
			require.Equal(t, block, hit.Result)
		}
	}
}

func TestConfiguredCachePreservesLegacyPolicy(t *testing.T) {
	ctx := context.Background()
	srv, err := miniredis.Run()
	require.NoError(t, err)
	defer srv.Close()
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	defer client.Close()
	storage := newCacheWithCompression(newRedisCache(client, client, "test", time.Hour))
	legacy := newRPCCache(storage)
	req := &RPCReq{Method: "eth_chainId"}
	require.NoError(t, legacy.PutRPC(ctx, req, &RPCRes{Result: "0xa"}))
	configured := newConfiguredRPCCache(CacheConfig{Enabled: true}, RedisConfig{Namespace: "test"}, client, client)
	hit, err := configured.GetRPC(ctx, req)
	require.NoError(t, err)
	require.Equal(t, "0xa", hit.Result, "existing Redis entries must remain readable")
	req = &RPCReq{Method: "eth_getBalance", Params: json.RawMessage(`["0x123","latest"]`)}
	require.NoError(t, configured.PutRPC(ctx, req, &RPCRes{Result: "0xff"}))
	hit, err = configured.GetRPC(ctx, req)
	require.NoError(t, err)
	require.Nil(t, hit, "unconfigured methods must remain uncached")
}
