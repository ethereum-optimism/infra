package proxyd

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type txsJSON struct {
	OnchainTx    string `json:"onchainTx"`
	OffchainTxV0 string `json:"offchainTxV0"`
	OffchainTxV1 string `json:"offchainTxV1"`
}

func TestConvertSendReqToSendTx_Fusaka(t *testing.T) {
	txData, err := os.ReadFile("testdata/txs.json")
	require.NoError(t, err)
	var txs txsJSON
	require.NoError(t, json.Unmarshal(txData, &txs))

	tfn := func(txHex string, proofCount int) func(t *testing.T) {
		return func(t *testing.T) {
			params, err := json.Marshal([]any{txHex})
			require.NoError(t, err)
			rpcReq := &RPCReq{
				Method: "eth_sendRawTransaction",
				Params: params,
				ID:     json.RawMessage("1"),
			}

			// Create a minimal server instance for testing
			server := &Server{enableTxHashLogging: false}
			tx, err := server.convertSendReqToSendTx(context.Background(), rpcReq)
			require.NoError(t, err)

			require.Len(t, tx.BlobTxSidecar().Blobs, 2)
			require.Len(t, tx.BlobTxSidecar().Commitments, 2)
			require.Len(t, tx.BlobTxSidecar().Proofs, proofCount)
		}
	}
	t.Run("blob without cell proofs", tfn(txs.OffchainTxV0, 2))
	t.Run("blob with cell proofs", tfn(txs.OffchainTxV1, 256))
}

func TestIsValidAPIKey(t *testing.T) {
	tests := []struct {
		name        string
		apiKey      string
		exemptKeys  []string
		expected    bool
		description string
	}{
		{
			name:        "valid API key",
			apiKey:      "valid-key-123",
			exemptKeys:  []string{"valid-key-123"},
			expected:    true,
			description: "should return true for a valid API key",
		},
		{
			name:        "invalid API key",
			apiKey:      "invalid-key",
			exemptKeys:  []string{"valid-key-123"},
			expected:    false,
			description: "should return false for an invalid API key",
		},
		{
			name:        "empty API key",
			apiKey:      "",
			exemptKeys:  []string{"valid-key-123"},
			expected:    false,
			description: "should return false for an empty API key",
		},
		{
			name:        "multiple exempt keys",
			apiKey:      "key-2",
			exemptKeys:  []string{"key-1", "key-2", "key-3"},
			expected:    true,
			description: "should return true when key is in multiple exempt keys",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{
				limExemptKeys: tt.exemptKeys,
			}

			got := s.isValidAPIKey(tt.apiKey)
			if got != tt.expected {
				t.Errorf("isValidAPIKey() = %v, want %v: %s", got, tt.expected, tt.description)
			}
		})
	}
}

func TestRateLimitedRequestIsTracked(t *testing.T) {
	s := &Server{
		rateLimitHeader:  defaultRateLimitHeader,
		mainLim:          NewMemoryFrontendRateLimit(time.Minute, 1),
		rateLimitTracker: newRateLimitTracker(10),
	}
	req := httptest.NewRequest("POST", "/", nil)
	req.Header.Set(defaultRateLimitHeader, "203.0.113.9")
	ctx := s.populateContext(httptest.NewRecorder(), req)
	require.NotNil(t, ctx)

	isLimited, err := s.limiterForRequest(ctx, req, false)
	require.NoError(t, err)

	require.False(t, isLimited(""), "first request is under the limit")
	require.Empty(t, s.rateLimitTracker.limited, "allowed requests must not be tracked")

	require.True(t, isLimited(""), "second request is over the limit")
	require.Len(t, s.rateLimitTracker.limited, 1, "one distinct key tracked")
	for _, n := range s.rateLimitTracker.limited {
		require.Equal(t, 1, n, "one rejected request recorded for the key")
	}
}

func TestRateLimitedRequestWithoutTracker(t *testing.T) {
	s := &Server{
		rateLimitHeader: defaultRateLimitHeader,
		mainLim:         NewMemoryFrontendRateLimit(time.Minute, 0),
	}
	req := httptest.NewRequest("POST", "/", nil)
	req.Header.Set(defaultRateLimitHeader, "203.0.113.9")
	ctx := s.populateContext(httptest.NewRecorder(), req)
	require.NotNil(t, ctx)

	isLimited, err := s.limiterForRequest(ctx, req, false)
	require.NoError(t, err)
	require.True(t, isLimited(""), "a nil tracker must not panic")
}

func TestPopulateContextRemoteAddrFallback(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		header     string
		want       string
	}{
		{
			name:       "ipv4 host port",
			remoteAddr: "127.0.0.1:8545",
			want:       "127.0.0.1",
		},
		{
			name:       "ipv6 host port",
			remoteAddr: "[::1]:8545",
			want:       "::1",
		},
		{
			name:       "ipv6 without port",
			remoteAddr: "::1",
			want:       "::1",
		},
		{
			name:       "header takes precedence",
			remoteAddr: "[::1]:8545",
			header:     "203.0.113.1",
			want:       "203.0.113.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{rateLimitHeader: defaultRateLimitHeader}
			req := httptest.NewRequest("POST", "/", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.header != "" {
				req.Header.Set(defaultRateLimitHeader, tt.header)
			}

			ctx := s.populateContext(httptest.NewRecorder(), req)
			require.NotNil(t, ctx)
			require.Equal(t, tt.want, GetXForwardedFor(ctx))
		})
	}
}

func TestAuthAliasLimiterMissingIP(t *testing.T) {
	for _, tt := range []struct {
		name          string
		useAuthAlias  bool
		authenticated bool
		wantError     bool
	}{
		{"default authenticated", false, true, true},
		{"alias authenticated", true, true, false},
		{"alias public", true, false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{mainLim: NoopFrontendRateLimiter}
			if tt.useAuthAlias {
				s.authLims = &limiterSet{base: NewMemoryFrontendRateLimit(time.Hour, 1)}
			}
			ctx := context.Background()
			if tt.authenticated {
				// Authentication is determined by context presence, not the metrics sentinel.
				ctx = context.WithValue(ctx, ContextKeyAuth, "none") // nolint:staticcheck
			}
			isLimited, err := s.limiterForRequest(ctx, httptest.NewRequest("POST", "/", nil), false)
			if tt.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.False(t, isLimited(""))
			require.True(t, isLimited(""))
		})
	}
}

func TestAuthAliasLimiterExemptions(t *testing.T) {
	for _, tt := range []struct {
		name         string
		bypass       bool
		methodGlobal bool
	}{
		{name: "exempt origin"},
		{name: "global ignores origin exemption", methodGlobal: true},
		{name: "exempt key bypasses global", bypass: true, methodGlobal: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{
				authLims: &limiterSet{
					base:    NewMemoryFrontendRateLimit(time.Hour, 0),
					methods: map[string]FrontendRateLimiter{"eth_getProof": NewMemoryFrontendRateLimit(time.Hour, 0)},
				},
				limExemptOrigins:       []*regexp.Regexp{regexp.MustCompile("^exempt$")},
				globallyLimitedMethods: map[string]bool{"eth_getProof": tt.methodGlobal},
			}
			req := httptest.NewRequest("POST", "/", nil)
			req.Header.Set("Origin", "exempt")
			ctx := context.WithValue(req.Context(), ContextKeyAuth, "alpha") // nolint:staticcheck
			isLimited, err := s.limiterForRequest(ctx, req, tt.bypass)
			require.NoError(t, err)
			require.Equal(t, tt.methodGlobal && !tt.bypass, isLimited("eth_getProof"))
			require.False(t, isLimited(""))
		})
	}
}

func TestAuthAliasLimiterErrorsAreTracked(t *testing.T) {
	s := &Server{
		mainLim:          &errorFrontend{},
		authLims:         &limiterSet{base: &errorFrontend{}},
		rateLimitTracker: newRateLimitTracker(10),
	}
	req := httptest.NewRequest("POST", "/", nil)
	ctx := context.WithValue(req.Context(), ContextKeyAuth, "203.0.113.1") // nolint:staticcheck
	authLimited, err := s.limiterForRequest(ctx, req, false)
	require.NoError(t, err)
	require.True(t, authLimited(""), "limiter errors fail closed")

	publicCtx := context.WithValue(req.Context(), ContextKeyXForwardedFor, "203.0.113.1") // nolint:staticcheck
	publicLimited, err := s.limiterForRequest(publicCtx, req, false)
	require.NoError(t, err)
	require.True(t, publicLimited(""))
	require.Len(t, s.rateLimitTracker.limited, 2, "IP and alias are distinct tracked identities")
}

func TestAliasOverrideLimiterSelection(t *testing.T) {
	type call struct {
		method  string
		limited bool
	}
	for _, tt := range []struct {
		name  string
		alias string
		calls []call
	}{
		{"override alias uses its own limits", "alpha", []call{
			{"eth_baz", true},
			{"eth_chainId", false},
			{"eth_chainId", false},
			{"eth_chainId", true},
		}},
		{"other alias uses default alias limits", "beta", []call{
			{"eth_baz", false},
			{"eth_chainId", true},
		}},
		{"public request uses IP limits", "", []call{
			{"eth_baz", false},
			{"eth_chainId", true},
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{
				mainLim:  NewMemoryFrontendRateLimit(time.Hour, 1),
				authLims: &limiterSet{base: NewMemoryFrontendRateLimit(time.Hour, 1)},
				aliasLims: map[string]*limiterSet{"alpha": {
					base:    NewMemoryFrontendRateLimit(time.Hour, 2),
					methods: map[string]FrontendRateLimiter{"eth_baz": NewMemoryFrontendRateLimit(time.Hour, 0)},
				}},
			}
			req := httptest.NewRequest("POST", "/", nil)
			ctx := context.WithValue(req.Context(), ContextKeyXForwardedFor, "203.0.113.1") // nolint:staticcheck
			if tt.alias != "" {
				ctx = context.WithValue(ctx, ContextKeyAuth, tt.alias) // nolint:staticcheck
			}
			isLimited, err := s.limiterForRequest(ctx, req, false)
			require.NoError(t, err)
			for i, c := range tt.calls {
				require.Equal(t, c.limited, isLimited(c.method), "call %d: %s", i, c.method)
			}
		})
	}
}

type recordedLimiter struct {
	dur    time.Duration
	max    int
	prefix string
}

func (l *recordedLimiter) Take(context.Context, string) (bool, error) {
	return true, nil
}

func recordingLimiterFactory(dur time.Duration, max int, prefix string) FrontendRateLimiter {
	return &recordedLimiter{dur: dur, max: max, prefix: prefix}
}

func ptr[T any](v T) *T {
	return &v
}

func TestNewAliasLimiters(t *testing.T) {
	second := ptr(TOMLDuration(time.Second))
	globalBase := recordingLimiterFactory(time.Minute, 100, "auth_alias:main")
	globalFoobar := recordingLimiterFactory(time.Minute, 5, "auth_alias:eth_foobar")
	for _, tt := range []struct {
		name        string
		override    *AliasRateLimitConfig
		wantBase    FrontendRateLimiter
		wantMethods map[string]FrontendRateLimiter
	}{
		{
			name: "method override keeps global limits",
			override: &AliasRateLimitConfig{MethodOverrides: map[string]*AliasRateLimitMethodOverride{
				"eth_baz": {Limit: ptr(3), Interval: second},
			}},
			wantBase: globalBase,
			wantMethods: map[string]FrontendRateLimiter{
				"eth_foobar": globalFoobar,
				"eth_baz":    recordingLimiterFactory(time.Second, 3, "auth_alias:eth_baz"),
			},
		},
		{
			name: "method override replaces global method limit",
			override: &AliasRateLimitConfig{MethodOverrides: map[string]*AliasRateLimitMethodOverride{
				"eth_foobar": {Limit: ptr(50), Interval: second},
			}},
			wantBase:    globalBase,
			wantMethods: map[string]FrontendRateLimiter{"eth_foobar": recordingLimiterFactory(time.Second, 50, "auth_alias:eth_foobar")},
		},
		{
			name:        "base override drops global method limits",
			override:    &AliasRateLimitConfig{BaseRate: ptr(1000), BaseInterval: second},
			wantBase:    recordingLimiterFactory(time.Second, 1000, "auth_alias:main"),
			wantMethods: map[string]FrontendRateLimiter{},
		},
		{
			name: "base override with method override",
			override: &AliasRateLimitConfig{BaseRate: ptr(1000), BaseInterval: second, MethodOverrides: map[string]*AliasRateLimitMethodOverride{
				"eth_baz": {Limit: ptr(3), Interval: second},
			}},
			wantBase:    recordingLimiterFactory(time.Second, 1000, "auth_alias:main"),
			wantMethods: map[string]FrontendRateLimiter{"eth_baz": recordingLimiterFactory(time.Second, 3, "auth_alias:eth_baz")},
		},
		{
			name:        "infinite alias",
			override:    &AliasRateLimitConfig{InfiniteRateLimit: true},
			wantBase:    NoopFrontendRateLimiter,
			wantMethods: map[string]FrontendRateLimiter{},
		},
		{
			name: "infinite alias with method limit",
			override: &AliasRateLimitConfig{InfiniteRateLimit: true, MethodOverrides: map[string]*AliasRateLimitMethodOverride{
				"eth_foobar": {Limit: ptr(0), Interval: second},
			}},
			wantBase:    NoopFrontendRateLimiter,
			wantMethods: map[string]FrontendRateLimiter{"eth_foobar": recordingLimiterFactory(time.Second, 0, "auth_alias:eth_foobar")},
		},
		{
			name: "infinite method",
			override: &AliasRateLimitConfig{MethodOverrides: map[string]*AliasRateLimitMethodOverride{
				"eth_foobar": {InfiniteRateLimit: true},
			}},
			wantBase:    globalBase,
			wantMethods: map[string]FrontendRateLimiter{"eth_foobar": NoopFrontendRateLimiter},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			defaults := &limiterSet{base: globalBase, methods: map[string]FrontendRateLimiter{"eth_foobar": globalFoobar}}
			cfg := RateLimitConfig{UseAuthAlias: true, AliasOverrides: map[string]*AliasRateLimitConfig{"alpha": tt.override}}
			aliasLims, err := newAliasLimiters(cfg, defaults, map[string]bool{"alpha": true}, recordingLimiterFactory)
			require.NoError(t, err)
			require.Equal(t, tt.wantBase, aliasLims["alpha"].base)
			require.Equal(t, tt.wantMethods, aliasLims["alpha"].methods)
			require.Equal(t, map[string]FrontendRateLimiter{"eth_foobar": globalFoobar}, defaults.methods, "default alias limits are unchanged")
		})
	}
}

func TestNewAliasLimitersValidation(t *testing.T) {
	second := ptr(TOMLDuration(time.Second))
	zero := ptr(TOMLDuration(0))
	method := func(override *AliasRateLimitMethodOverride) map[string]*AliasRateLimitMethodOverride {
		return map[string]*AliasRateLimitMethodOverride{"eth_call": override}
	}
	for _, tt := range []struct {
		name     string
		disabled bool
		alias    string
		override *AliasRateLimitConfig
		wantErr  string
	}{
		{
			name:     "requires use_auth_alias",
			disabled: true,
			override: &AliasRateLimitConfig{InfiniteRateLimit: true},
			wantErr:  "rate_limit.alias_overrides requires rate_limit.use_auth_alias = true",
		},
		{
			name:     "unknown alias",
			alias:    "gamma",
			override: &AliasRateLimitConfig{InfiniteRateLimit: true},
			wantErr:  "rate_limit.alias_overrides.gamma: alias is not defined in [authentication]",
		},
		{
			name:     "infinite with base_rate",
			override: &AliasRateLimitConfig{InfiniteRateLimit: true, BaseRate: ptr(1), BaseInterval: second},
			wantErr:  "rate_limit.alias_overrides.alpha: infinite_rate_limit cannot be combined with base_rate or base_interval",
		},
		{
			name:     "infinite with base_interval",
			override: &AliasRateLimitConfig{InfiniteRateLimit: true, BaseInterval: second},
			wantErr:  "rate_limit.alias_overrides.alpha: infinite_rate_limit cannot be combined with base_rate or base_interval",
		},
		{
			name:     "base_rate without base_interval",
			override: &AliasRateLimitConfig{BaseRate: ptr(1)},
			wantErr:  "rate_limit.alias_overrides.alpha: base_rate and base_interval must be set together",
		},
		{
			name:     "base_interval without base_rate",
			override: &AliasRateLimitConfig{BaseInterval: second},
			wantErr:  "rate_limit.alias_overrides.alpha: base_rate and base_interval must be set together",
		},
		{
			name:     "zero base_rate",
			override: &AliasRateLimitConfig{BaseRate: ptr(0), BaseInterval: second},
			wantErr:  "rate_limit.alias_overrides.alpha: base_rate must be greater than 0",
		},
		{
			name:     "negative base_rate",
			override: &AliasRateLimitConfig{BaseRate: ptr(-1), BaseInterval: second},
			wantErr:  "rate_limit.alias_overrides.alpha: base_rate must be greater than 0",
		},
		{
			name:     "zero base_interval",
			override: &AliasRateLimitConfig{BaseRate: ptr(1), BaseInterval: zero},
			wantErr:  "rate_limit.alias_overrides.alpha: base_interval must be greater than 0",
		},
		{
			name:     "empty override",
			override: &AliasRateLimitConfig{},
			wantErr:  "rate_limit.alias_overrides.alpha: must set base_rate and base_interval, infinite_rate_limit, or method_overrides",
		},
		{
			name:     "infinite method with limit",
			override: &AliasRateLimitConfig{MethodOverrides: method(&AliasRateLimitMethodOverride{InfiniteRateLimit: true, Limit: ptr(1)})},
			wantErr:  "rate_limit.alias_overrides.alpha: method_overrides.eth_call: infinite_rate_limit cannot be combined with limit or interval",
		},
		{
			name:     "method without interval",
			override: &AliasRateLimitConfig{MethodOverrides: method(&AliasRateLimitMethodOverride{Limit: ptr(1)})},
			wantErr:  "rate_limit.alias_overrides.alpha: method_overrides.eth_call: limit and interval must be set unless infinite_rate_limit = true",
		},
		{
			name:     "empty method override",
			override: &AliasRateLimitConfig{MethodOverrides: method(&AliasRateLimitMethodOverride{})},
			wantErr:  "rate_limit.alias_overrides.alpha: method_overrides.eth_call: limit and interval must be set unless infinite_rate_limit = true",
		},
		{
			name:     "negative method limit",
			override: &AliasRateLimitConfig{MethodOverrides: method(&AliasRateLimitMethodOverride{Limit: ptr(-1), Interval: second})},
			wantErr:  "rate_limit.alias_overrides.alpha: method_overrides.eth_call: limit must not be negative",
		},
		{
			name:     "zero method interval",
			override: &AliasRateLimitConfig{MethodOverrides: method(&AliasRateLimitMethodOverride{Limit: ptr(1), Interval: zero})},
			wantErr:  "rate_limit.alias_overrides.alpha: method_overrides.eth_call: interval must be greater than 0",
		},
		{
			name:     "zero method limit blocks the method",
			override: &AliasRateLimitConfig{MethodOverrides: method(&AliasRateLimitMethodOverride{Limit: ptr(0), Interval: second})},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			alias := tt.alias
			if alias == "" {
				alias = "alpha"
			}
			cfg := RateLimitConfig{UseAuthAlias: !tt.disabled, AliasOverrides: map[string]*AliasRateLimitConfig{alias: tt.override}}
			_, err := newAliasLimiters(cfg, &limiterSet{base: NoopFrontendRateLimiter}, map[string]bool{"alpha": true}, recordingLimiterFactory)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tt.wantErr)
		})
	}
}
