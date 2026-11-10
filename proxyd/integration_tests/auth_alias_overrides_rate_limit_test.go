package integration_tests

import (
	"fmt"
	"strings"
	"testing"

	"github.com/alicebob/miniredis"
	"github.com/ethereum-optimism/infra/proxyd"
	"github.com/stretchr/testify/require"
)

const authAliasOverridesConfig = "frontend_auth_alias_overrides"

func TestFrontendAuthAliasOverrides(t *testing.T) {
	type calls struct {
		secret string
		method string
		n      int
		status int
	}
	tests := []struct {
		name  string
		calls []calls
	}{
		{"alias without override uses global limits", []calls{
			{"default_key", "eth_baz", 1, 200},
			{"default_key", "eth_chainId", 1, 200},
			{"default_key", "eth_chainId", 1, 429},
			{"default_key", "eth_foobar", 1, 200},
			{"default_key", "eth_foobar", 1, 429},
		}},
		{"base override replaces global base and method limits", []calls{
			{"base_override_key", "eth_foobar", 2, 200},
			{"base_override_key", "eth_chainId", 2, 200},
			{"base_override_key", "eth_chainId", 1, 429},
			{"base_override_key", "eth_foobar", 1, 429},
		}},
		{"method override keeps global limits", []calls{
			{"method_override_key", "eth_baz", 3, 200},
			{"method_override_key", "eth_baz", 1, 429},
			{"method_override_key", "eth_chainId", 2, 200},
			{"method_override_key", "eth_chainId", 1, 429},
			{"method_override_key", "eth_foobar", 1, 200},
			{"method_override_key", "eth_foobar", 1, 429},
		}},
		{"infinite alias with a blocked method", []calls{
			{"infinite_key", "eth_chainId", 5, 200},
			{"infinite_key", "eth_foobar", 3, 200},
			{"infinite_key", "eth_baz", 1, 429},
		}},
		{"infinite method keeps other limits", []calls{
			{"infinite_method_key", "eth_chainId", 5, 200},
			{"infinite_method_key", "eth_baz", 2, 200},
			{"infinite_method_key", "eth_baz", 1, 429},
			{"infinite_method_key", "eth_foobar", 1, 200},
			{"infinite_method_key", "eth_foobar", 1, 429},
		}},
		{"aliases and public requests behind one IP are independent", []calls{
			{"default_key", "eth_chainId", 2, 200},
			{"default_key", "eth_chainId", 1, 429},
			{"base_override_key", "eth_chainId", 4, 200},
			{"base_override_key", "eth_chainId", 1, 429},
			{"", "eth_chainId", 2, 200},
			{"", "eth_chainId", 1, 429},
		}},
	}
	for _, useRedis := range []bool{false, true} {
		for _, tt := range tests {
			t.Run(fmt.Sprintf("redis=%t/%s", useRedis, tt.name), func(t *testing.T) {
				url := startAuthAliasTestProxy(t, authAliasTestConfigFrom(t, authAliasOverridesConfig, useRedis, nil))
				for i, c := range tt.calls {
					for j := 0; j < c.n; j++ {
						body, status, err := authAliasClient(url, c.secret, "203.0.113.1").SendRPC(c.method, nil)
						require.NoError(t, err)
						require.Equal(t, c.status, status, "calls %d, call %d: %s", i, j, body)
					}
				}
			})
		}
	}
}

func TestFrontendAuthAliasOverridesRedisSharedQuota(t *testing.T) {
	redisServer, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(redisServer.Close)
	config := authAliasTestConfigFrom(t, authAliasOverridesConfig, false, nil)
	config.RateLimit.UseRedis = true
	config.Redis.URL = "redis://" + redisServer.Addr()
	firstURL := startAuthAliasTestProxy(t, config)
	secondURL := startAuthAliasTestProxy(t, config)
	for i, c := range []struct {
		url    string
		status int
	}{
		{firstURL, 200},
		{secondURL, 200},
		{firstURL, 200},
		{secondURL, 200},
		{firstURL, 429},
	} {
		body, status, err := authAliasClient(c.url, "base_override_key", "203.0.113.1").SendRPC("eth_chainId", nil)
		require.NoError(t, err)
		require.Equal(t, c.status, status, "call %d: %s", i, body)
	}
	require.Len(t, redisServer.Keys(), 1)
	require.True(t, strings.HasPrefix(redisServer.Keys()[0], "rate_limit:auth_alias:main:base_override_alias:"), redisServer.Keys()[0])
}

func TestFrontendAuthAliasOverridesStartup(t *testing.T) {
	for _, tt := range []struct {
		name    string
		mutate  func(*proxyd.Config)
		wantErr string
	}{
		{
			name:    "requires use_auth_alias",
			mutate:  func(c *proxyd.Config) { c.RateLimit.UseAuthAlias = false },
			wantErr: "rate_limit.alias_overrides requires rate_limit.use_auth_alias = true",
		},
		{
			name:    "unknown alias",
			mutate:  func(c *proxyd.Config) { delete(c.Authentication, "base_override_key") },
			wantErr: "rate_limit.alias_overrides.base_override_alias: alias is not defined in [authentication]",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			config := authAliasTestConfigFrom(t, authAliasOverridesConfig, false, nil)
			tt.mutate(config)
			_, shutdown, err := proxyd.Start(config)
			if shutdown != nil {
				t.Cleanup(shutdown)
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}
