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
[cache.method_ttls]
eth_getBlockByNumber = "1s"
eth_gasPrice = "1s"
`, &cfg)
	require.NoError(t, err)
	require.NoError(t, cfg.Cache.validate())
	require.Equal(t, TOMLDuration(time.Second), cfg.Cache.MethodTTLs["eth_getBlockByNumber"])
	for _, tc := range []struct {
		method string
		ttl    TOMLDuration
	}{
		{"eth_call", 0},
		{"eth_call", -1},
		{"eth_call", TOMLDuration(250 * time.Millisecond)},
		{"eth_call", TOMLDuration(1500 * time.Millisecond)},
		{"eth_sendRawTransaction", TOMLDuration(time.Second)},
		{"eth_getFilterChanges", TOMLDuration(time.Second)},
	} {
		require.Error(t, (CacheConfig{MethodTTLs: map[string]TOMLDuration{tc.method: tc.ttl}}).validate())
	}
}

func TestTimedCacheArguments(t *testing.T) {
	ctx := context.Background()
	c := newConfiguredRPCCache(CacheConfig{MethodTTLs: map[string]TOMLDuration{
		"eth_call": TOMLDuration(time.Second),
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
			c := newConfiguredRPCCache(CacheConfig{MethodTTLs: map[string]TOMLDuration{
				"eth_getBlockByNumber": TOMLDuration(time.Second),
			}}, RedisConfig{}, client, client)
			if !useRedis {
				memory := c.handlers["eth_getBlockByNumber"].(*StaticMethodHandler).cache.(*cacheWithCompression).cache.(*cache)
				now := time.Unix(1000, 0)
				memory.now = func() time.Time { return now }
				advance = func(d time.Duration) { now = now.Add(d) }
			}
			req := &RPCReq{Method: "eth_getBlockByNumber", Params: json.RawMessage(`["latest",false]`)}
			require.NoError(t, c.PutRPC(ctx, req, &RPCRes{Result: "block"}))
			advance(999 * time.Millisecond)
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
	c := newConfiguredRPCCache(CacheConfig{MethodTTLs: map[string]TOMLDuration{
		"eth_gasPrice": TOMLDuration(time.Second),
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
	c := newConfiguredRPCCache(CacheConfig{MethodTTLs: map[string]TOMLDuration{
		"eth_getBlockByNumber": TOMLDuration(time.Second),
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
	memory := newMemoryCache(time.Second)
	now := time.Unix(1000, 0)
	memory.now = func() time.Time { return now }
	fallback := newFallbackCache(&errorCache{}, memory)
	require.NoError(t, fallback.Put(ctx, "key", "value"))
	value, err := fallback.Get(ctx, "key")
	require.NoError(t, err)
	require.Equal(t, "value", value)
	now = now.Add(time.Second)
	value, err = fallback.Get(ctx, "key")
	require.NoError(t, err)
	require.Empty(t, value)
}

func TestTimedCacheCachesEachBlockSelector(t *testing.T) {
	ctx := context.Background()
	c := newConfiguredRPCCache(CacheConfig{MethodTTLs: map[string]TOMLDuration{
		"eth_getBlockByNumber": TOMLDuration(time.Second),
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
