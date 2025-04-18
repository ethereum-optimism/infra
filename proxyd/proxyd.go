package proxyd

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"os"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/semaphore"
)

func SetLogLevel(logLevel slog.Leveler) {
	log.SetDefault(log.NewLogger(slog.NewJSONHandler(
		os.Stdout, &slog.HandlerOptions{Level: logLevel})))
}

func Start(config *Config) (*Server, func(), error) {
	if len(config.Backends) == 0 {
		return nil, nil, errors.New("must define at least one backend")
	}
	if len(config.BackendGroups) == 0 {
		return nil, nil, errors.New("must define at least one backend group")
	}
	if len(config.RPCMethodMappings) == 0 {
		return nil, nil, errors.New("must define at least one RPC method mapping")
	}

	for authKey := range config.Authentication {
		if authKey == "none" {
			return nil, nil, errors.New("cannot use none as an auth key")
		}
	}

	// redis primary client
	var redisClient redis.UniversalClient
	if config.Redis.URL != "" {
		rURL, err := ReadFromEnvOrConfig(config.Redis.URL)
		if err != nil {
			return nil, nil, err
		}
		redisClient, err = NewRedisClient(rURL, config.Redis.RedisCluster)
		if err != nil {
			return nil, nil, err
		}
		if err := CheckRedisConnection(redisClient); err != nil {
			if config.Redis.FallbackToMemory {
				log.Warn("failed to connect to redis, may fall back to in-memory cache", "err", err)
			} else {
				return nil, nil, err
			}
		}
	}

	// redis read replica client
	// if read endpoint is not set, use primary endpoint
	var redisReadClient = redisClient
	if config.Redis.ReadURL != "" {
		if redisClient == nil {
			return nil, nil, errors.New("must specify a Redis primary URL. only read endpoint is set")
		}
		rURL, err := ReadFromEnvOrConfig(config.Redis.ReadURL)
		if err != nil {
			return nil, nil, err
		}
		redisReadClient, err = NewRedisClient(rURL, config.Redis.RedisCluster)
		if err != nil {
			return nil, nil, err
		}

		if err := CheckRedisConnection(redisClient); err != nil {
			if config.Redis.FallbackToMemory {
				log.Warn("failed to connect to redis, may fall back to in-memory cache", "err", err)
			} else {
				return nil, nil, err
			}
		}
	}

	if redisClient == nil && config.RateLimit.UseRedis {
		return nil, nil, errors.New("must specify a Redis URL if UseRedis is true in rate limit config")
	}

	// While modifying shared globals is a bad practice, the alternative
	// is to clone these errors on every invocation. This is inefficient.
	// We'd also have to make sure that errors.Is and errors.As continue
	// to function properly on the cloned errors.
	if config.RateLimit.ErrorMessage != "" {
		ErrOverRateLimit.Message = config.RateLimit.ErrorMessage
	}
	if config.WhitelistErrorMessage != "" {
		ErrMethodNotWhitelisted.Message = config.WhitelistErrorMessage
	}
	if config.BatchConfig.ErrorMessage != "" {
		ErrTooManyBatchRequests.Message = config.BatchConfig.ErrorMessage
	}

	if config.SenderRateLimit.Enabled {
		if config.SenderRateLimit.Limit <= 0 {
			return nil, nil, errors.New("limit in sender_rate_limit must be > 0")
		}
		if time.Duration(config.SenderRateLimit.Interval) < time.Second {
			return nil, nil, errors.New("interval in sender_rate_limit must be >= 1s")
		}
	}

	maxConcurrentRPCs := config.Server.MaxConcurrentRPCs
	var rpcRequestSemaphore *semaphore.Weighted
	if config.Server.DisableConcurrentRequestSemaphore {
		rpcRequestSemaphore = nil
		log.Info("Using unlimited RPC concurrency")
	} else {
		if maxConcurrentRPCs == 0 {
			maxConcurrentRPCs = math.MaxInt64
		}
		rpcRequestSemaphore = semaphore.NewWeighted(maxConcurrentRPCs)
		log.Info("Using max concurrent RPCs of", "maxConcurrentRPCs", maxConcurrentRPCs)
	}

	backendNames := make([]string, 0)
	backendsByName := make(map[string]*Backend)
	for name, cfg := range config.Backends {
		opts := make([]BackendOpt, 0)

		rpcURL, err := ReadFromEnvOrConfig(cfg.RPCURL)
		if err != nil {
			return nil, nil, err
		}
		wsURL, err := ReadFromEnvOrConfig(cfg.WSURL)
		if err != nil {
			return nil, nil, err
		}
		if rpcURL == "" {
			return nil, nil, fmt.Errorf("must define an RPC URL for backend %s", name)
		}

		if config.BackendOptions.ResponseTimeoutMilliseconds != 0 {
			timeout := millisecondsToDuration(config.BackendOptions.ResponseTimeoutMilliseconds)
			opts = append(opts, WithTimeout(timeout))
		} else if config.BackendOptions.ResponseTimeoutSeconds != 0 {
			timeout := secondsToDuration(config.BackendOptions.ResponseTimeoutSeconds)
			opts = append(opts, WithTimeout(timeout))
		}
		if config.BackendOptions.MaxRetries != 0 {
			opts = append(opts, WithMaxRetries(config.BackendOptions.MaxRetries))
		}
		if config.BackendOptions.MaxResponseSizeBytes != 0 {
			opts = append(opts, WithMaxResponseSize(config.BackendOptions.MaxResponseSizeBytes))
		}
		if config.BackendOptions.OutOfServiceSeconds != 0 {
			opts = append(opts, WithOutOfServiceDuration(secondsToDuration(config.BackendOptions.OutOfServiceSeconds)))
		}
		if config.BackendOptions.MaxDegradedLatencyThreshold > 0 {
			opts = append(opts, WithMaxDegradedLatencyThreshold(time.Duration(config.BackendOptions.MaxDegradedLatencyThreshold)))
		}
		if config.BackendOptions.MaxLatencyThreshold > 0 {
			opts = append(opts, WithMaxLatencyThreshold(time.Duration(config.BackendOptions.MaxLatencyThreshold)))
		}
		if config.BackendOptions.MaxErrorRateThreshold > 0 {
			opts = append(opts, WithMaxErrorRateThreshold(config.BackendOptions.MaxErrorRateThreshold))
		}
		if cfg.MaxRPS != 0 {
			opts = append(opts, WithMaxRPS(cfg.MaxRPS))
		}
		if cfg.MaxWSConns != 0 {
			opts = append(opts, WithMaxWSConns(cfg.MaxWSConns))
		}
		if cfg.Password != "" {
			passwordVal, err := ReadFromEnvOrConfig(cfg.Password)
			if err != nil {
				return nil, nil, err
			}
			opts = append(opts, WithBasicAuth(cfg.Username, passwordVal))
		}

		headers := map[string]string{}
		for headerName, headerValue := range cfg.Headers {
			headerValue, err := ReadFromEnvOrConfig(headerValue)
			if err != nil {
				return nil, nil, err
			}

			headers[headerName] = headerValue
		}
		opts = append(opts, WithHeaders(headers))

		tlsConfig, err := configureBackendTLS(cfg)
		if err != nil {
			return nil, nil, err
		}
		if tlsConfig != nil {
			log.Info("using custom TLS config for backend", "name", name)
			opts = append(opts, WithTLSConfig(tlsConfig))
		}
		if cfg.StripTrailingXFF {
			opts = append(opts, WithStrippedTrailingXFF())
		}
		if cfg.ResponseTimeoutMilliseconds != 0 {
			opts = append(opts, WithTimeout(millisecondsToDuration(cfg.ResponseTimeoutMilliseconds)))
		}
		if cfg.MaxRetries != nil {
			opts = append(opts, WithMaxRetries(*cfg.MaxRetries))
		}
		opts = append(opts, WithProxydIP(os.Getenv("PROXYD_IP")))
		opts = append(opts, WithSkipIsSyncingCheck(cfg.SkipIsSyncingCheck))
		opts = append(opts, WithSafeBlockDriftThreshold(cfg.SafeBlockDriftThreshold))
		opts = append(opts, WithFinalizedBlockDriftThreshold(cfg.FinalizedBlockDriftThreshold))
		opts = append(opts, WithConsensusSkipPeerCountCheck(cfg.ConsensusSkipPeerCountCheck))
		opts = append(opts, WithConsensusForcedCandidate(cfg.ConsensusForcedCandidate))
		opts = append(opts, WithWeight(cfg.Weight))
		if len(cfg.AllowedStatusCodes) > 0 {
			opts = append(opts, WithAllowedStatusCodes(cfg.AllowedStatusCodes))
		}

		receiptsTarget, err := ReadFromEnvOrConfig(cfg.ConsensusReceiptsTarget)
		if err != nil {
			return nil, nil, err
		}
		receiptsTarget, err = validateReceiptsTarget(receiptsTarget)
		if err != nil {
			return nil, nil, err
		}
		opts = append(opts, WithConsensusReceiptTarget(receiptsTarget))

		back := NewBackend(name, rpcURL, wsURL, rpcRequestSemaphore, opts...)
		backendNames = append(backendNames, name)
		backendsByName[name] = back
		log.Info("configured backend",
			"name", name,
			"backend_names", backendNames,
			"rpc_url", rpcURL,
			"ws_url", wsURL)
	}

	if config.InteropValidationConfig.Strategy == "" {
		log.Warn("no interop validation strategy provided, using default strategy", "strategy", defaultInteropValidationStrategy)
		config.InteropValidationConfig.Strategy = defaultInteropValidationStrategy
	}

	if config.InteropValidationConfig.LoadBalancingUnhealthinessTimeout == 0 && config.InteropValidationConfig.Strategy == HealthAwareLoadBalancingStrategy {
		log.Warn("no interop validation load balancing unhealthiness timeout provided for health aware strategy, using default timeout", "timeout", defaultInteropLoadBalancingUnhealthinessTimeout)
		config.InteropValidationConfig.LoadBalancingUnhealthinessTimeout = defaultInteropLoadBalancingUnhealthinessTimeout
	}

	if config.InteropValidationConfig.ReqSizeLimit == 0 {
		log.Warn("no interop validation request size limit provided, using default size limit", "size_limit", defaultInteropReqSizeLimit)
		config.InteropValidationConfig.ReqSizeLimit = defaultInteropReqSizeLimit
	}

	if config.InteropValidationConfig.AccessListSizeLimit == 0 {
		log.Warn("no interop validation access list size limit provided, using default size limit", "size_limit", defaultInteropAccessListSizeLimit)
		config.InteropValidationConfig.AccessListSizeLimit = defaultInteropAccessListSizeLimit
	}

	log.Info("configured interop validation urls", "urls", config.InteropValidationConfig.Urls)
	log.Info("configured interop validation strategy", "strategy", config.InteropValidationConfig.Strategy)

	backendGroups := make(map[string]*BackendGroup)
	for bgName, bg := range config.BackendGroups {
		backends := make([]*Backend, 0)
		fallbackBackends := make(map[string]bool)
		fallbackCount := 0
		for _, bName := range bg.Backends {
			if backendsByName[bName] == nil {
				return nil, nil, fmt.Errorf("backend %s is not defined", bName)
			}
			backends = append(backends, backendsByName[bName])

			for _, fb := range bg.Fallbacks {
				if bName == fb {
					fallbackBackends[bName] = true
					log.Info("configured backend as fallback",
						"backend_name", bName,
						"backend_group", bgName,
					)
					fallbackCount++
				}
			}

			if _, ok := fallbackBackends[bName]; !ok {
				fallbackBackends[bName] = false
				log.Info("configured backend as primary",
					"backend_name", bName,
					"backend_group", bgName,
				)
			}
		}

		if fallbackCount != len(bg.Fallbacks) {
			return nil, nil,
				fmt.Errorf(
					"error: number of fallbacks instantiated (%d) did not match configured (%d) for backend group %s",
					fallbackCount, len(bg.Fallbacks), bgName,
				)
		}

		maxBlockRange := bg.ConsensusMaxBlockRange
		if bg.MaxBlockRange > 0 {
			log.Info("Overridding consensus max block range with max block range")
			maxBlockRange = bg.MaxBlockRange
		}

		backendGroups[bgName] = &BackendGroup{
			Name:                   bgName,
			Backends:               backends,
			WeightedRouting:        bg.WeightedRouting,
			FallbackBackends:       fallbackBackends,
			routingStrategy:        bg.RoutingStrategy,
			multicallRPCErrorCheck: bg.MulticallRPCErrorCheck,
			maxBlockRange:          maxBlockRange,
		}
	}

	var wsBackendGroup *BackendGroup
	if config.WSBackendGroup != "" {
		wsBackendGroup = backendGroups[config.WSBackendGroup]
		if wsBackendGroup == nil {
			return nil, nil, fmt.Errorf("ws backend group %s does not exist", config.WSBackendGroup)
		}
	}

	if wsBackendGroup == nil && config.Server.WSPort != 0 {
		return nil, nil, fmt.Errorf("a ws port was defined, but no ws group was defined")
	}

	for _, bg := range config.RPCMethodMappings {
		if backendGroups[bg] == nil {
			return nil, nil, fmt.Errorf("undefined backend group %s", bg)
		}
	}

	var resolvedAuth map[string]string

	if config.Authentication != nil {
		resolvedAuth = make(map[string]string)
		for secret, alias := range config.Authentication {
			resolvedSecret, err := ReadFromEnvOrConfig(secret)
			if err != nil {
				return nil, nil, err
			}
			resolvedAuth[resolvedSecret] = alias
		}
	}

	var (
		cache    Cache
		rpcCache RPCCache
	)
	if config.Cache.Enabled {
		if redisClient == nil {
			log.Warn("redis is not configured, using in-memory cache")
			cache = newMemoryCache()
		} else {
			ttl := defaultCacheTtl
			if config.Cache.TTL != 0 {
				ttl = time.Duration(config.Cache.TTL)
			}
			cache = newRedisCache(redisClient, redisReadClient, config.Redis.Namespace, ttl)

			if config.Redis.FallbackToMemory {
				cache = newFallbackCache(cache, newMemoryCache())
			}
		}
		rpcCache = newRPCCache(newCacheWithCompression(cache))
	}

	limiterFactory := func(dur time.Duration, max int, prefix string) FrontendRateLimiter {
		if config.RateLimit.UseRedis {
			limiter := NewRedisFrontendRateLimiter(redisClient, dur, max, prefix)

			if config.Redis.FallbackToMemory {
				limiter = NewFallbackRateLimiter(
					limiter,
					NewMemoryFrontendRateLimit(dur, max),
				)
			}

			return limiter
		}

		return NewMemoryFrontendRateLimit(dur, max)
	}

	var interopStrategy InteropStrategy

	opts := CommonStrategyOpts(
		WithReqSizeLimit(config.InteropValidationConfig.ReqSizeLimit),
		WithAccessListSizeLimit(config.InteropValidationConfig.AccessListSizeLimit),
	)

	switch config.InteropValidationConfig.Strategy {
	case FirstSupervisorStrategy, EmptyStrategy:
		interopStrategy = NewFirstSupervisorStrategy(
			config.InteropValidationConfig.Urls,
			opts...,
		)
	case MulticallStrategy:
		interopStrategy = NewMulticallStrategy(
			config.InteropValidationConfig.Urls,
			opts...,
		)
	case HealthAwareLoadBalancingStrategy:
		interopStrategy = NewHealthAwareLoadBalancingStrategy(
			config.InteropValidationConfig.Urls,
			config.InteropValidationConfig.LoadBalancingUnhealthinessTimeout,
			opts...,
		)
	default:
		return nil, nil, fmt.Errorf("invalid interop validating strategy: %s", config.InteropValidationConfig.Strategy)
	}

	srv, err := NewServer(
		backendGroups,
		wsBackendGroup,
		NewStringSetFromStrings(config.WSMethodWhitelist),
		config.RPCMethodMappings,
		config.Server.MaxBodySizeBytes,
		resolvedAuth,
		config.Server.PublicAccess,
		secondsToDuration(config.Server.TimeoutSeconds),
		config.Server.MaxUpstreamBatchSize,
		config.Server.EnableXServedByHeader,
		rpcCache,
		config.RateLimit,
		config.SenderRateLimit,
		config.InteropValidationConfig.RateLimit,
		config.Server.EnableRequestLog,
		config.Server.MaxRequestBodyLogLen,
		config.BatchConfig.MaxSize,
		limiterFactory,
		config.InteropValidationConfig,
		interopStrategy,
		config.Server.EnableTxHashLogging,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("error creating server: %w", err)
	}

	// Enable to support browser websocket connections.
	// See https://pkg.go.dev/github.com/gorilla/websocket#hdr-Origin_Considerations
	if config.Server.AllowAllOrigins {
		srv.upgrader.CheckOrigin = func(r *http.Request) bool {
			return true
		}
	}

	if config.Metrics.Enabled {
		addr := fmt.Sprintf("%s:%d", config.Metrics.Host, config.Metrics.Port)
		log.Info("starting metrics server", "addr", addr)
		go func() {
			if err := http.ListenAndServe(addr, promhttp.Handler()); err != nil {
				log.Error("error starting metrics server", "err", err)
			}
		}()
	}

	// To allow integration tests to cleanly come up, wait
	// 10ms to give the below goroutines enough time to
	// encounter an error creating their servers
	errTimer := time.NewTimer(10 * time.Millisecond)

	if config.Server.RPCPort != 0 {
		go func() {
			if err := srv.RPCListenAndServe(config.Server.RPCHost, config.Server.RPCPort); err != nil {
				if errors.Is(err, http.ErrServerClosed) {
					log.Info("RPC server shut down")
					return
				}
				log.Crit("error starting RPC server", "err", err)
			}
		}()
	}

	if config.Server.WSPort != 0 {
		go func() {
			if err := srv.WSListenAndServe(config.Server.WSHost, config.Server.WSPort); err != nil {
				if errors.Is(err, http.ErrServerClosed) {
					log.Info("WS server shut down")
					return
				}
				log.Crit("error starting WS server", "err", err)
			}
		}()
	} else {
		log.Info("WS server not enabled (ws_port is set to 0)")
	}

	for bgName, bg := range backendGroups {
		bgcfg := config.BackendGroups[bgName]

		if !bgcfg.ValidateRoutingStrategy(bgName) {
			log.Crit("Invalid routing strategy provided. Valid options: fallback, multicall, consensus_aware, \"\"", "name", bgName)
		}

		log.Info("configuring routing strategy for backend_group", "name", bgName, "routing_strategy", bgcfg.RoutingStrategy)

		if bgcfg.RoutingStrategy == ConsensusAwareRoutingStrategy {
			log.Info("creating poller for consensus aware backend_group", "name", bgName)

			copts := make([]ConsensusOpt, 0)

			if bgcfg.ConsensusAsyncHandler == "noop" {
				copts = append(copts, WithAsyncHandler(NewNoopAsyncHandler()))
			}
			if bgcfg.ConsensusBanPeriod > 0 {
				copts = append(copts, WithBanPeriod(time.Duration(bgcfg.ConsensusBanPeriod)))
			}
			if bgcfg.ConsensusMaxUpdateThreshold > 0 {
				copts = append(copts, WithMaxUpdateThreshold(time.Duration(bgcfg.ConsensusMaxUpdateThreshold)))
			}
			if bgcfg.ConsensusMaxBlockLag > 0 {
				copts = append(copts, WithMaxBlockLag(bgcfg.ConsensusMaxBlockLag))
			}
			if bgcfg.ConsensusMinPeerCount > 0 {
				copts = append(copts, WithMinPeerCount(uint64(bgcfg.ConsensusMinPeerCount)))
			}
			if bg.maxBlockRange > 0 {
				copts = append(copts, WithMaxBlockRange(bg.maxBlockRange))
			}
			if bgcfg.ConsensusPollerInterval > 0 {
				copts = append(copts, WithPollerInterval(time.Duration(bgcfg.ConsensusPollerInterval)))
			}

			for _, be := range bgcfg.Backends {
				if fallback, ok := bg.FallbackBackends[be]; !ok {
					log.Crit("error backend not found in backend fallback configurations", "backend_name", be)
				} else {
					log.Debug("configuring new backend for group", "backend_group", bgName, "backend_name", be, "fallback", fallback)
					RecordBackendGroupFallbacks(bg, be, fallback)
				}
			}

			var tracker ConsensusTracker
			if bgcfg.ConsensusHA {
				if bgcfg.ConsensusHARedis.URL == "" {
					log.Crit("must specify a consensus_ha_redis config when consensus_ha is true")
				}
				topts := make([]RedisConsensusTrackerOpt, 0)
				if bgcfg.ConsensusHALockPeriod > 0 {
					topts = append(topts, WithLockPeriod(time.Duration(bgcfg.ConsensusHALockPeriod)))
				}
				if bgcfg.ConsensusHAHeartbeatInterval > 0 {
					topts = append(topts, WithHeartbeatInterval(time.Duration(bgcfg.ConsensusHAHeartbeatInterval)))
				}
				consensusHARedisClient, err := NewRedisClient(bgcfg.ConsensusHARedis.URL, bgcfg.ConsensusHARedis.RedisCluster)
				if err != nil {
					return nil, nil, err
				}
				if err := CheckRedisConnection(consensusHARedisClient); err != nil {
					return nil, nil, err
				}
				ns := fmt.Sprintf("%s:%s", bgcfg.ConsensusHARedis.Namespace, bg.Name)
				tracker = NewRedisConsensusTracker(context.Background(), consensusHARedisClient, bg, ns, topts...)
				copts = append(copts, WithTracker(tracker))
			}

			cp := NewConsensusPoller(bg, copts...)
			bg.Consensus = cp

			if bgcfg.ConsensusHA {
				tracker.(*RedisConsensusTracker).Init()
			}
		}
	}

	<-errTimer.C
	log.Info("started proxyd")

	shutdownFunc := func() {
		log.Info("draining proxyd")
		srv.Drain()
		log.Info("shutting down proxyd")
		srv.Shutdown()
		log.Info("goodbye")
	}

	return srv, shutdownFunc, nil
}

func validateReceiptsTarget(val string) (string, error) {
	if val == "" {
		val = ReceiptsTargetDebugGetRawReceipts
	}
	switch val {
	case ReceiptsTargetDebugGetRawReceipts,
		ReceiptsTargetAlchemyGetTransactionReceipts,
		ReceiptsTargetEthGetTransactionReceipts,
		ReceiptsTargetParityGetTransactionReceipts:
		return val, nil
	default:
		return "", fmt.Errorf("invalid receipts target: %s", val)
	}
}

func secondsToDuration(seconds int) time.Duration {
	return time.Duration(seconds) * time.Second
}

func millisecondsToDuration(ms int) time.Duration {
	return time.Duration(ms) * time.Millisecond
}

func configureBackendTLS(cfg *BackendConfig) (*tls.Config, error) {
	if cfg.CAFile == "" {
		return nil, nil
	}

	tlsConfig, err := CreateTLSClient(cfg.CAFile)
	if err != nil {
		return nil, err
	}

	if cfg.ClientCertFile != "" && cfg.ClientKeyFile != "" {
		cert, err := ParseKeyPair(cfg.ClientCertFile, cfg.ClientKeyFile)
		if err != nil {
			return nil, err
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	return tlsConfig, nil
}
