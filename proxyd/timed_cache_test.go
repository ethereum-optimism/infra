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
	c, _ := newTestTimedCache(t, CacheConfig{MethodTTLs: map[string]TOMLDuration{
		"eth_call": TOMLDuration(time.Second),
	}})
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
	c, srv := newTestTimedCache(t, CacheConfig{MethodTTLs: map[string]TOMLDuration{
		"eth_getBlockByNumber": TOMLDuration(time.Second),
	}})
	req := &RPCReq{Method: "eth_getBlockByNumber", Params: json.RawMessage(`["latest",false]`)}
	require.NoError(t, c.PutRPC(ctx, req, &RPCRes{Result: "block"}))
	srv.FastForward(999 * time.Millisecond)
	hit, err := c.GetRPC(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, hit)
	srv.FastForward(time.Millisecond)
	hit, err = c.GetRPC(ctx, req)
	require.NoError(t, err)
	require.Nil(t, hit)
}

func TestTimedCacheSkipsUnusableResults(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestTimedCache(t, CacheConfig{MethodTTLs: map[string]TOMLDuration{
		"eth_gasPrice": TOMLDuration(time.Second),
	}})
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
	c, _ := newTestTimedCache(t, CacheConfig{MethodTTLs: map[string]TOMLDuration{
		"eth_getBlockByNumber": TOMLDuration(time.Second),
	}})
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

func TestTimedCacheCachesEachBlockSelector(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestTimedCache(t, CacheConfig{MethodTTLs: map[string]TOMLDuration{
		"eth_getBlockByNumber": TOMLDuration(time.Second),
	}})
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

func newTestTimedCache(t *testing.T, config CacheConfig) (*rpcCache, *miniredis.Miniredis) {
	t.Helper()
	srv, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(srv.Close)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return newConfiguredRPCCache(config, RedisConfig{}, client, client), srv
}

func TestTimedCacheWithoutRedis(t *testing.T) {
	ctx := context.Background()
	c := newConfiguredRPCCache(CacheConfig{MethodTTLs: map[string]TOMLDuration{
		"eth_getBalance": TOMLDuration(time.Second),
		"eth_chainId":    TOMLDuration(time.Second),
	}}, RedisConfig{FallbackToMemory: true}, nil, nil)
	for _, method := range []string{"eth_getBalance", "eth_chainId"} {
		req := &RPCReq{Method: method}
		require.NoError(t, c.PutRPC(ctx, req, &RPCRes{Result: "0x1"}))
		hit, err := c.GetRPC(ctx, req)
		require.NoError(t, err)
		require.Nil(t, hit, "timed methods must not use legacy memory storage")
	}
	req := &RPCReq{Method: "net_version"}
	require.NoError(t, c.PutRPC(ctx, req, &RPCRes{Result: "10"}))
	hit, err := c.GetRPC(ctx, req)
	require.NoError(t, err)
	require.Equal(t, "10", hit.Result, "unconfigured immutable methods retain memory caching")
}

func TestTimedCacheRedisFailurePreservesLegacyFallback(t *testing.T) {
	ctx := context.Background()
	srv, err := miniredis.Run()
	require.NoError(t, err)
	defer srv.Close()
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	c := newConfiguredRPCCache(CacheConfig{MethodTTLs: map[string]TOMLDuration{
		"eth_getBalance": TOMLDuration(time.Second),
	}}, RedisConfig{FallbackToMemory: true}, client, client)
	require.NoError(t, client.Close())
	req := &RPCReq{Method: "eth_getBalance"}
	require.Error(t, c.PutRPC(ctx, req, &RPCRes{Result: "0x1"}))
	hit, err := c.GetRPC(ctx, req)
	require.Error(t, err)
	require.Nil(t, hit)
	req = &RPCReq{Method: "eth_chainId"}
	require.NoError(t, c.PutRPC(ctx, req, &RPCRes{Result: "0xa"}))
	hit, err = c.GetRPC(ctx, req)
	require.NoError(t, err)
	require.Equal(t, "0xa", hit.Result, "legacy immutable cache still falls back to memory")
}
