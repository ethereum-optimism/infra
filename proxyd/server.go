package proxyd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/txpool"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"github.com/rs/cors"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	ContextKeyAuth               = "authorization"
	ContextKeyReqID              = "req_id"
	ContextKeyXForwardedFor      = "x_forwarded_for"
	ContextKeyReferer            = "referer"
	ContextKeyUserAgent          = "user_agent"
	DefaultMaxBatchRPCCallsLimit = 100
	MaxBatchRPCCallsHardLimit    = 1000
	cacheStatusHdr               = "X-Proxyd-Cache-Status"
	defaultRPCTimeout            = 10 * time.Second
	defaultBodySizeLimit         = 256 * opt.KiB
	defaultWSHandshakeTimeout    = 10 * time.Second
	defaultWSReadTimeout         = 2 * time.Minute
	defaultWSWriteTimeout        = 10 * time.Second
	defaultCacheTtl              = 1 * time.Hour
	maxRequestBodyLogLen         = 2000
	defaultMaxUpstreamBatchSize  = 10
	defaultRateLimitHeader       = "X-Forwarded-For"
)

var emptyArrayResponse = json.RawMessage("[]")
var tracer = otel.Tracer("proxyd-server")

type Server struct {
	BackendGroups          map[string]*BackendGroup
	wsBackendGroup         *BackendGroup
	wsMethodWhitelist      *StringSet
	rpcMethodMappings      map[string]string
	maxBodySize            int64
	enableRequestLog       bool
	maxRequestBodyLogLen   int
	authenticatedPaths     map[string]string
	timeout                time.Duration
	maxUpstreamBatchSize   int
	maxBatchSize           int
	enableServedByHeader   bool
	upgrader               *websocket.Upgrader
	mainLim                FrontendRateLimiter
	overrideLims           map[string]FrontendRateLimiter
	senderLim              FrontendRateLimiter
	allowedChainIds        []*big.Int
	limExemptOrigins       []*regexp.Regexp
	limExemptUserAgents    []*regexp.Regexp
	globallyLimitedMethods map[string]bool
	rpcServer              *http.Server
	wsServer               *http.Server
	cache                  RPCCache
	srvMu                  sync.Mutex
	rateLimitHeader        string
	sanctionedAddresses    map[common.Address]struct{}
	watchedAddresses       map[common.Address]struct{}
}

type limiterFunc func(method string) bool

func NewServer(
	backendGroups map[string]*BackendGroup,
	wsBackendGroup *BackendGroup,
	wsMethodWhitelist *StringSet,
	rpcMethodMappings map[string]string,
	maxBodySize int64,
	authenticatedPaths map[string]string,
	timeout time.Duration,
	maxUpstreamBatchSize int,
	enableServedByHeader bool,
	cache RPCCache,
	rateLimitConfig RateLimitConfig,
	senderRateLimitConfig SenderRateLimitConfig,
	enableRequestLog bool,
	maxRequestBodyLogLen int,
	maxBatchSize int,
	redisClient *redis.Client,
	sanctionedAddresses map[common.Address]struct{},
	watchedAddresses map[common.Address]struct{},
) (*Server, error) {
	if cache == nil {
		cache = &NoopRPCCache{}
	}

	if maxBodySize == 0 {
		maxBodySize = defaultBodySizeLimit
	}

	if timeout == 0 {
		timeout = defaultRPCTimeout
	}

	if maxUpstreamBatchSize == 0 {
		maxUpstreamBatchSize = defaultMaxUpstreamBatchSize
	}

	if maxBatchSize == 0 {
		maxBatchSize = DefaultMaxBatchRPCCallsLimit
	}

	if maxBatchSize > MaxBatchRPCCallsHardLimit {
		maxBatchSize = MaxBatchRPCCallsHardLimit
	}

	limiterFactory := func(dur time.Duration, max int, prefix string) FrontendRateLimiter {
		if rateLimitConfig.UseRedis {
			return NewRedisFrontendRateLimiter(redisClient, dur, max, prefix)
		}

		return NewMemoryFrontendRateLimit(dur, max)
	}

	var mainLim FrontendRateLimiter
	limExemptOrigins := make([]*regexp.Regexp, 0)
	limExemptUserAgents := make([]*regexp.Regexp, 0)
	if rateLimitConfig.BaseRate > 0 {
		mainLim = limiterFactory(time.Duration(rateLimitConfig.BaseInterval), rateLimitConfig.BaseRate, "main")
		for _, origin := range rateLimitConfig.ExemptOrigins {
			pattern, err := regexp.Compile(origin)
			if err != nil {
				return nil, err
			}
			limExemptOrigins = append(limExemptOrigins, pattern)
		}
		for _, agent := range rateLimitConfig.ExemptUserAgents {
			pattern, err := regexp.Compile(agent)
			if err != nil {
				return nil, err
			}
			limExemptUserAgents = append(limExemptUserAgents, pattern)
		}
	} else {
		mainLim = NoopFrontendRateLimiter
	}

	overrideLims := make(map[string]FrontendRateLimiter)
	globalMethodLims := make(map[string]bool)
	for method, override := range rateLimitConfig.MethodOverrides {
		overrideLims[method] = limiterFactory(time.Duration(override.Interval), override.Limit, method)

		if override.Global {
			globalMethodLims[method] = true
		}
	}
	var senderLim FrontendRateLimiter
	if senderRateLimitConfig.Enabled {
		senderLim = limiterFactory(time.Duration(senderRateLimitConfig.Interval), senderRateLimitConfig.Limit, "senders")
	}

	rateLimitHeader := defaultRateLimitHeader
	if rateLimitConfig.IPHeaderOverride != "" {
		rateLimitHeader = rateLimitConfig.IPHeaderOverride
	}

	return &Server{
		BackendGroups:        backendGroups,
		wsBackendGroup:       wsBackendGroup,
		wsMethodWhitelist:    wsMethodWhitelist,
		rpcMethodMappings:    rpcMethodMappings,
		maxBodySize:          maxBodySize,
		authenticatedPaths:   authenticatedPaths,
		timeout:              timeout,
		maxUpstreamBatchSize: maxUpstreamBatchSize,
		enableServedByHeader: enableServedByHeader,
		cache:                cache,
		enableRequestLog:     enableRequestLog,
		maxRequestBodyLogLen: maxRequestBodyLogLen,
		maxBatchSize:         maxBatchSize,
		upgrader: &websocket.Upgrader{
			HandshakeTimeout: defaultWSHandshakeTimeout,
		},
		mainLim:                mainLim,
		overrideLims:           overrideLims,
		globallyLimitedMethods: globalMethodLims,
		senderLim:              senderLim,
		allowedChainIds:        senderRateLimitConfig.AllowedChainIds,
		limExemptOrigins:       limExemptOrigins,
		limExemptUserAgents:    limExemptUserAgents,
		rateLimitHeader:        rateLimitHeader,
		sanctionedAddresses:    sanctionedAddresses,
		watchedAddresses:       watchedAddresses,
	}, nil
}

func (s *Server) RPCListenAndServe(host string, port int) error {
	s.srvMu.Lock()
	hdlr := mux.NewRouter()

	// Non-instrumented methods
	hdlr.HandleFunc("/healthz", s.HandleHealthz).Methods("GET")
	// hdlr.HandleFunc("/", s.HandleRPC).Methods("POST")
	// hdlr.HandleFunc("/{authorization}", s.HandleRPC).Methods("POST")

	// Instrumented methods
	// hdlr.Handle("/healthz", otelhttp.NewHandler(http.HandlerFunc(s.HandleHealthz), "HealthzHandler")).Methods("GET")
	hdlr.Handle("/", otelhttp.NewHandler(http.HandlerFunc(s.HandleRPC),
		"RPCHandler",
		otelhttp.WithSpanOptions(trace.WithAttributes(
			attribute.Bool("force_sample", true),
		)),
	)).Methods("POST")
	hdlr.Handle("/{authorization}", otelhttp.NewHandler(http.HandlerFunc(s.HandleRPC), "RPCHandler")).Methods("POST")

	c := cors.New(cors.Options{
		AllowedOrigins: []string{"*"},
	})
	addr := fmt.Sprintf("%s:%d", host, port)
	s.rpcServer = &http.Server{
		Handler: instrumentedHdlr(c.Handler(hdlr)),
		Addr:    addr,
	}
	log.Info("starting HTTP server", "addr", addr)
	s.srvMu.Unlock()
	return s.rpcServer.ListenAndServe()
}

func (s *Server) WSListenAndServe(host string, port int) error {
	s.srvMu.Lock()
	hdlr := mux.NewRouter()
	hdlr.HandleFunc("/", s.HandleWS)
	hdlr.HandleFunc("/{authorization}", s.HandleWS)
	c := cors.New(cors.Options{
		AllowedOrigins: []string{"*"},
	})
	addr := fmt.Sprintf("%s:%d", host, port)
	s.wsServer = &http.Server{
		Handler: instrumentedHdlr(c.Handler(hdlr)),
		Addr:    addr,
	}
	log.Info("starting WS server", "addr", addr)
	s.srvMu.Unlock()
	return s.wsServer.ListenAndServe()
}

func (s *Server) Shutdown() {
	s.srvMu.Lock()
	defer s.srvMu.Unlock()
	if s.rpcServer != nil {
		_ = s.rpcServer.Shutdown(context.Background())
	}
	if s.wsServer != nil {
		_ = s.wsServer.Shutdown(context.Background())
	}
	for _, bg := range s.BackendGroups {
		bg.Shutdown()
	}
}

func (s *Server) HandleHealthz(w http.ResponseWriter, r *http.Request) {
	// _, span := tracer.Start(r.Context(), "HealthzFunction")
	// defer span.End()

	_, _ = w.Write([]byte("OK"))
}

func (s *Server) HandleRPC(w http.ResponseWriter, r *http.Request) {

	_, span := tracer.Start(r.Context(), "RPCFunction")
	defer span.End()

	ctx := s.populateContext(w, r)
	if ctx == nil {
		return
	}
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(ctx, s.timeout)
	defer cancel()

	origin := r.Header.Get("Origin")
	userAgent := r.Header.Get("User-Agent")
	// Use XFF in context since it will automatically be replaced by the remote IP
	xff := stripXFF(GetXForwardedFor(ctx))
	isUnlimitedOrigin := s.isUnlimitedOrigin(origin)
	isUnlimitedUserAgent := s.isUnlimitedUserAgent(userAgent)

	if xff == "" {
		writeRPCError(ctx, w, nil, ErrInvalidRequest("request does not include a remote IP"))
		return
	}

	isLimited := func(method string) bool {
		isGloballyLimitedMethod := s.isGlobalLimit(method)
		if !isGloballyLimitedMethod && (isUnlimitedOrigin || isUnlimitedUserAgent) {
			return false
		}

		var lim FrontendRateLimiter
		if method == "" {
			lim = s.mainLim
		} else {
			lim = s.overrideLims[method]
		}

		if lim == nil {
			return false
		}

		ok, err := lim.Take(ctx, xff)
		if err != nil {
			log.Warn("error taking rate limit", "err", err)
			return true
		}
		return !ok
	}

	log.Debug(
		"received RPC request",
		"req_id", GetReqID(ctx),
		"auth", GetAuthCtx(ctx),
		"user_agent", userAgent,
		"origin", origin,
		"remote_ip", xff,
	)

	body, err := io.ReadAll(LimitReader(r.Body, s.maxBodySize))
	if errors.Is(err, ErrLimitReaderOverLimit) {
		log.Error("request body too large", "req_id", GetReqID(ctx))
		RecordRPCError(ctx, BackendProxyd, MethodUnknown, ErrRequestBodyTooLarge)
		writeRPCError(ctx, w, nil, ErrRequestBodyTooLarge)
		return
	}
	if err != nil {
		log.Error("error reading request body", "err", err)
		writeRPCError(ctx, w, nil, ErrInternal)
		return
	}
	RecordRequestPayloadSize(ctx, len(body))

	if IsBatch(body) {
		reqs, err := ParseBatchRPCReq(body)
		if err != nil {
			log.Error("error parsing batch RPC request", "err", err)
			RecordRPCError(ctx, BackendProxyd, MethodUnknown, err)
			writeRPCError(ctx, w, nil, ErrParseErr)
			return
		}

		RecordBatchSize(len(reqs))

		if len(reqs) > s.maxBatchSize {
			RecordRPCError(ctx, BackendProxyd, MethodUnknown, ErrTooManyBatchRequests)
			writeRPCError(ctx, w, nil, ErrTooManyBatchRequests)
			return
		}

		if len(reqs) == 0 {
			writeRPCError(ctx, w, nil, ErrInvalidRequest("must specify at least one batch call"))
			return
		}

		batchRes, batchContainsCached, servedBy, err := s.handleBatchRPC(ctx, reqs, isLimited, true, body)
		if err == context.DeadlineExceeded {
			writeRPCError(ctx, w, nil, ErrGatewayTimeout)
			return
		}
		if errors.Is(err, ErrConsensusGetReceiptsCantBeBatched) ||
			errors.Is(err, ErrConsensusGetReceiptsInvalidTarget) {
			writeRPCError(ctx, w, nil, ErrInvalidRequest(err.Error()))
			return
		}
		if err != nil {
			writeRPCError(ctx, w, nil, ErrInternal)
			return
		}
		if s.enableServedByHeader {
			w.Header().Set("x-served-by", servedBy)
		}
		setCacheHeader(w, batchContainsCached)
		writeBatchRPCRes(ctx, w, batchRes)
		return
	}

	rawBody := json.RawMessage(body)
	backendRes, cached, servedBy, err := s.handleBatchRPC(ctx, []json.RawMessage{rawBody}, isLimited, false, body)
	if err != nil {
		if errors.Is(err, ErrConsensusGetReceiptsCantBeBatched) ||
			errors.Is(err, ErrConsensusGetReceiptsInvalidTarget) {
			writeRPCError(ctx, w, nil, ErrInvalidRequest(err.Error()))
			return
		}
		writeRPCError(ctx, w, nil, ErrInternal)
		return
	}
	if s.enableServedByHeader {
		w.Header().Set("x-served-by", servedBy)
	}
	setCacheHeader(w, cached)
	writeRPCRes(ctx, w, backendRes[0])
}

func (s *Server) handleBatchRPC(ctx context.Context, reqs []json.RawMessage, isLimited limiterFunc, isBatch bool, rawBody []byte) ([]*RPCRes, bool, string, error) {

	_, span := tracer.Start(ctx, "BatchRPCFunction")
	defer span.End()

	// A request set is transformed into groups of batches.
	// Each batch group maps to a forwarded JSON-RPC batch request (subject to maxUpstreamBatchSize constraints)
	// A groupID is used to decouple Requests that have duplicate ID so they're not part of the same batch that's
	// forwarded to the backend. This is done to ensure that the order of JSON-RPC Responses match the Request order
	// as the backend MAY return Responses out of order.
	// NOTE: Duplicate request ids induces 1-sized JSON-RPC batches
	type batchGroup struct {
		groupID      int
		backendGroup string
	}

	responses := make([]*RPCRes, len(reqs))
	batches := make(map[batchGroup][]batchElem)
	ids := make(map[string]int, len(reqs))

	// Check if request is from Valora
	isValora, _ := ctx.Value("is_valora").(bool)

	for i := range reqs {
		parsedReq, err := ParseRPCReq(reqs[i])
		if err != nil {
			log.Info("error parsing RPC call", "source", "rpc", "err", err)
			responses[i] = NewRPCErrorRes(nil, err)
			continue
		}

		// Log request information
		s.LogRequestInfo(ctx, parsedReq, "rpc", rawBody)

		// Log transaction details if from/to matches a watched address
		s.LogWatchedAddressTransaction(ctx, parsedReq)

		// Check for Valora's specific eth_call request
		if isValora && parsedReq.Method == "eth_call" {
			var params []json.RawMessage
			if err := json.Unmarshal(parsedReq.Params, &params); err != nil {
				log.Debug("error unmarshalling eth_call params", "err", err, "req_id", GetReqID(ctx))
				responses[i] = NewRPCErrorRes(parsedReq.ID, ErrInvalidParams("invalid params"))
				continue
			}
			if len(params) < 1 {
				log.Debug("eth_call missing params", "req_id", GetReqID(ctx))
				responses[i] = NewRPCErrorRes(parsedReq.ID, ErrInvalidParams("missing required params"))
				continue
			}
			var callObj map[string]json.RawMessage
			if err := json.Unmarshal(params[0], &callObj); err != nil {
				log.Debug("error unmarshalling call object", "err", err, "req_id", GetReqID(ctx))
				responses[i] = NewRPCErrorRes(parsedReq.ID, ErrInvalidParams("invalid call object"))
				continue
			}
			var toAddr string
			if err := json.Unmarshal(callObj["to"], &toAddr); err != nil {
				log.Debug("error unmarshalling to address", "err", err, "req_id", GetReqID(ctx))
				responses[i] = NewRPCErrorRes(parsedReq.ID, ErrInvalidParams("invalid to address"))
				continue
			}

			// Map of data values to responses
			responseMap := map[string]string{
				// CELO
				"a54b7fc0000000000000000000000000471ece3750da237f93b8e339c536989b8978a438": "00000000000000000000000000000000000000000000000000000005d21dba00",
				// cUSD
				"a54b7fc0000000000000000000000000765de816845861e75a25fca122bb6898b8b1282a": "000000000000000000000000000000000000000000000000000000024ab9f46a",
				// USDC
				"a54b7fc00000000000000000000000002f25deb3848c207fc8e0c34035b3ba7fc157602b": "000000000000000000000000000000000000000000000000000000024ab9f46a",
				// cCOP
				"a54b7fc00000000000000000000000008a567e2ae79ca692bd748ab832081c45de4041ea": "000000000000000000000000000000000000000000000000000024c3929fa684",
				// USDT
				"a54b7fc00000000000000000000000000e2a3e05bc9a16f5292a6170456a710cb89c6f72": "000000000000000000000000000000000000000000000000000000024ab9f46a",
				// cEUR
				"a54b7fc0000000000000000000000000d8763cba276a3738e6de85b4b3bf5fded6d6ca73": "000000000000000000000000000000000000000000000000000000021fb97d29",
				// cREAL
				"a54b7fc0000000000000000000000000e8537a3d056da446677b9e9d6c5db704eaab4787": "0000000000000000000000000000000000000000000000000000000d1b210737",
				// eXOF
				"a54b7fc000000000000000000000000073f93dcc49cb8a239e2032663e9475dd5ef29a08": "0000000000000000000000000000000000000000000000000000057076e4459b",
				// cKES
				"a54b7fc0000000000000000000000000456a3d042c0dbd3db53d5489e98dfb038553b0d0": "0000000000000000000000000000000000000000000000000000012908aa4e00",
			}

			// Check for the specific contract address
			isCallToGasPriceMinimum := strings.EqualFold(toAddr, "0xdfca3a8d7699d8bafe656823ad60c17cb8270ecc")
			isMulticall := strings.EqualFold(toAddr, "0xcA11bde05977b3631167028862bE2a173976CA11")
			if isCallToGasPriceMinimum || isMulticall {
				// Check if data field exists
				if callObj["data"] == nil {
					log.Debug("missing data field in call object", "req_id", GetReqID(ctx))
					responses[i] = NewRPCErrorRes(parsedReq.ID, ErrInvalidParams("missing data field"))
					continue
				}

				var dataStr string
				if err := json.Unmarshal(callObj["data"], &dataStr); err != nil {
					log.Debug("error unmarshalling data field", "err", err, "req_id", GetReqID(ctx))
					responses[i] = NewRPCErrorRes(parsedReq.ID, ErrInvalidParams("invalid data field"))
					continue
				}

				var fixedResult string

				// Check for partial matches
				for pattern, response := range responseMap {
					if strings.Contains(dataStr, pattern) {
						if isMulticall {
							fixedResult = "0x000000000000000000000000000000000000000000000000000000000000002000000000000000000000000000000000000000000000000000000000000000010000000000000000000000000000000000000000000000000000000000000020000000000000000000000000000000000000000000000000000000000000000100000000000000000000000000000000000000000000000000000000000000400000000000000000000000000000000000000000000000000000000000000020" + response
						} else {
							fixedResult = "0x" + response
						}
						break
					}
				}

				if fixedResult != "" {
					responses[i] = NewRPCRes(parsedReq.ID, fixedResult)
					RecordRPCForward(ctx, BackendProxyd, "eth_call", RPCRequestSourceHTTP)
					log.Info("served fixed response for Valora eth_call",
						"req_id", GetReqID(ctx),
						"data", dataStr,
						"response_length", len(fixedResult))
					continue
				}
			}
		}

		// Simple health check
		if len(reqs) == 1 && parsedReq.Method == proxydHealthzMethod {
			res := &RPCRes{
				ID:      parsedReq.ID,
				JSONRPC: JSONRPCVersion,
				Result:  "OK",
			}
			return []*RPCRes{res}, false, "", nil
		}

		if err := ValidateRPCReq(parsedReq); err != nil {
			RecordRPCError(ctx, BackendProxyd, MethodUnknown, err)
			responses[i] = NewRPCErrorRes(nil, err)
			continue
		}

		if parsedReq.Method == "eth_accounts" {
			RecordRPCForward(ctx, BackendProxyd, "eth_accounts", RPCRequestSourceHTTP)
			responses[i] = NewRPCRes(parsedReq.ID, emptyArrayResponse)
			continue
		}

		group := s.rpcMethodMappings[parsedReq.Method]
		if group == "" {
			// use unknown below to prevent DOS vector that fills up memory
			// with arbitrary method names.
			log.Info(
				"blocked request for non-whitelisted method",
				"source", "rpc",
				"req_id", GetReqID(ctx),
				"method", parsedReq.Method,
			)
			RecordRPCError(ctx, BackendProxyd, MethodUnknown, ErrMethodNotWhitelisted)
			responses[i] = NewRPCErrorRes(parsedReq.ID, ErrMethodNotWhitelisted)
			continue
		}

		// Take base rate limit first
		if isLimited("") {
			log.Debug(
				"rate limited individual RPC in a batch request",
				"source", "rpc",
				"req_id", parsedReq.ID,
				"method", parsedReq.Method,
			)
			RecordRPCError(ctx, BackendProxyd, parsedReq.Method, ErrOverRateLimit)
			responses[i] = NewRPCErrorRes(parsedReq.ID, ErrOverRateLimit)
			continue
		}

		// Take rate limit for specific methods.
		if _, ok := s.overrideLims[parsedReq.Method]; ok && isLimited(parsedReq.Method) {
			log.Debug(
				"rate limited specific RPC",
				"source", "rpc",
				"req_id", GetReqID(ctx),
				"method", parsedReq.Method,
			)
			RecordRPCError(ctx, BackendProxyd, parsedReq.Method, ErrOverRateLimit)
			responses[i] = NewRPCErrorRes(parsedReq.ID, ErrOverRateLimit)
			continue
		}

		// Check if the sender is sanctioned.
		if parsedReq.Method == "eth_sendRawTransaction" && s.sanctionedAddresses != nil {
			if err := s.filterSanctionedAddresses(ctx, parsedReq); err != nil {
				RecordRPCError(ctx, BackendProxyd, parsedReq.Method, err)
				responses[i] = NewRPCErrorRes(parsedReq.ID, err)
				continue
			}
		}

		// Apply a sender-based rate limit if it is enabled. Note that sender-based rate
		// limits apply regardless of origin or user-agent. As such, they don't use the
		// isLimited method.
		if parsedReq.Method == "eth_sendRawTransaction" && s.senderLim != nil {
			if err := s.rateLimitSender(ctx, parsedReq); err != nil {
				RecordRPCError(ctx, BackendProxyd, parsedReq.Method, err)
				responses[i] = NewRPCErrorRes(parsedReq.ID, err)
				continue
			}
		}

		id := string(parsedReq.ID)
		// If this is a duplicate Request ID, move the Request to a new batchGroup
		ids[id]++
		batchGroupID := ids[id]
		batchGroup := batchGroup{groupID: batchGroupID, backendGroup: group}
		batches[batchGroup] = append(batches[batchGroup], batchElem{parsedReq, i})
	}

	servedBy := make(map[string]bool, 0)
	var cached bool
	for group, batch := range batches {
		var cacheMisses []batchElem

		for _, req := range batch {
			backendRes, _ := s.cache.GetRPC(ctx, req.Req)
			if backendRes != nil {
				responses[req.Index] = backendRes
				cached = true
			} else {
				cacheMisses = append(cacheMisses, req)
			}
		}

		// Create minibatches - each minibatch must be no larger than the maxUpstreamBatchSize
		numBatches := int(math.Ceil(float64(len(cacheMisses)) / float64(s.maxUpstreamBatchSize)))
		for i := 0; i < numBatches; i++ {
			if ctx.Err() == context.DeadlineExceeded {
				log.Info("short-circuiting batch RPC",
					"req_id", GetReqID(ctx),
					"auth", GetAuthCtx(ctx),
					"batch_index", i,
				)
				batchRPCShortCircuitsTotal.Inc()
				return nil, false, "", context.DeadlineExceeded
			}

			start := i * s.maxUpstreamBatchSize
			end := int(math.Min(float64(start+s.maxUpstreamBatchSize), float64(len(cacheMisses))))
			elems := cacheMisses[start:end]
			res, sb, err := s.BackendGroups[group.backendGroup].Forward(ctx, createBatchRequest(elems), isBatch)
			servedBy[sb] = true
			if err != nil {
				if errors.Is(err, ErrConsensusGetReceiptsCantBeBatched) ||
					errors.Is(err, ErrConsensusGetReceiptsInvalidTarget) {
					return nil, false, "", err
				}
				log.Error(
					"error forwarding RPC batch",
					"batch_size", len(elems),
					"backend_group", group,
					"req_id", GetReqID(ctx),
					"err", err,
				)
				res = nil
				for _, elem := range elems {
					res = append(res, NewRPCErrorRes(elem.Req.ID, err))
				}
			}

			for i := range elems {
				responses[elems[i].Index] = res[i]

				// TODO(inphi): batch put these
				if res[i].Error == nil && res[i].Result != nil {
					if err := s.cache.PutRPC(ctx, elems[i].Req, res[i]); err != nil {
						log.Warn(
							"cache put error",
							"req_id", GetReqID(ctx),
							"err", err,
						)
					}
				}
			}
		}
	}

	servedByString := ""
	for sb := range servedBy {
		if servedByString != "" {
			servedByString += ", "
		}
		servedByString += sb
	}

	return responses, cached, servedByString, nil
}

func (s *Server) HandleWS(w http.ResponseWriter, r *http.Request) {
	ctx := s.populateContext(w, r)
	if ctx == nil {
		return
	}

	log.Info("received WS connection", "req_id", GetReqID(ctx))

	clientConn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Error("error upgrading client conn", "auth", GetAuthCtx(ctx), "req_id", GetReqID(ctx), "err", err)
		return
	}
	clientConn.SetReadLimit(s.maxBodySize)

	proxier, err := s.wsBackendGroup.ProxyWS(ctx, clientConn, s.wsMethodWhitelist, s)
	if err != nil {
		if errors.Is(err, ErrNoBackends) {
			RecordUnserviceableRequest(ctx, RPCRequestSourceWS)
		}
		log.Error("error dialing ws backend", "auth", GetAuthCtx(ctx), "req_id", GetReqID(ctx), "err", err)
		clientConn.Close()
		return
	}

	activeClientWsConnsGauge.WithLabelValues(GetAuthCtx(ctx)).Inc()
	go func() {
		// Below call blocks so run it in a goroutine.
		if err := proxier.Proxy(ctx); err != nil {
			log.Error("error proxying websocket", "auth", GetAuthCtx(ctx), "req_id", GetReqID(ctx), "err", err)
		}
		activeClientWsConnsGauge.WithLabelValues(GetAuthCtx(ctx)).Dec()
	}()

	log.Info("accepted WS connection", "auth", GetAuthCtx(ctx), "req_id", GetReqID(ctx))
}

func (s *Server) populateContext(w http.ResponseWriter, r *http.Request) context.Context {
	vars := mux.Vars(r)
	authorization := vars["authorization"]
	xff := r.Header.Get(s.rateLimitHeader)
	if xff == "" {
		ipPort := strings.Split(r.RemoteAddr, ":")
		if len(ipPort) == 2 {
			xff = ipPort[0]
		}
	}
	ctx := context.WithValue(r.Context(), ContextKeyXForwardedFor, xff) // nolint:staticcheck

	// Capture HTTP headers for logging
	referer := r.Header.Get("Referer")
	userAgent := r.Header.Get("User-Agent")
	ctx = context.WithValue(ctx, ContextKeyReferer, referer)     // nolint:staticcheck
	ctx = context.WithValue(ctx, ContextKeyUserAgent, userAgent) // nolint:staticcheck

	// Check for Valora user agent
	isValora := strings.Contains(userAgent, "Valora") || strings.Contains(userAgent, "SheFi") || strings.Contains(userAgent, "cKash")
	ctx = context.WithValue(ctx, "is_valora", isValora) // nolint:staticcheck

	if len(s.authenticatedPaths) > 0 {
		if authorization == "" || s.authenticatedPaths[authorization] == "" {
			log.Info("blocked unauthorized request", "authorization", authorization)
			httpResponseCodesTotal.WithLabelValues("401").Inc()
			w.WriteHeader(401)
			return nil
		}

		ctx = context.WithValue(ctx, ContextKeyAuth, s.authenticatedPaths[authorization]) // nolint:staticcheck
	}

	return context.WithValue(
		ctx,
		ContextKeyReqID, // nolint:staticcheck
		randStr(10),
	)
}

func randStr(l int) string {
	b := make([]byte, l)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func (s *Server) isUnlimitedOrigin(origin string) bool {
	for _, pat := range s.limExemptOrigins {
		if pat.MatchString(origin) {
			return true
		}
	}

	return false
}

func (s *Server) isUnlimitedUserAgent(origin string) bool {
	for _, pat := range s.limExemptUserAgents {
		if pat.MatchString(origin) {
			return true
		}
	}
	return false
}

func (s *Server) isGlobalLimit(method string) bool {
	return s.globallyLimitedMethods[method]
}

func (s *Server) processTransaction(ctx context.Context, req *RPCReq) (*types.Transaction, *common.Address, error) {
	var params []string
	if err := json.Unmarshal(req.Params, &params); err != nil {
		log.Debug("error unmarshalling raw transaction params", "err", err, "req_Id", GetReqID(ctx))
		return nil, nil, ErrParseErr
	}

	if len(params) != 1 {
		log.Debug("raw transaction request has invalid number of params", "req_id", GetReqID(ctx))
		return nil, nil, ErrInvalidParams("missing value for required argument 0")
	}

	var data hexutil.Bytes
	if err := data.UnmarshalText([]byte(params[0])); err != nil {
		log.Debug("error decoding raw tx data", "err", err, "req_id", GetReqID(ctx))
		return nil, nil, ErrInvalidParams(err.Error())
	}

	tx := new(types.Transaction)
	if err := tx.UnmarshalBinary(data); err != nil {
		log.Debug("could not unmarshal transaction", "err", err, "req_id", GetReqID(ctx))
		return nil, nil, ErrInvalidParams(err.Error())
	}

	signer := types.LatestSignerForChainID(tx.ChainId())
	from, err := types.Sender(signer, tx)
	if err != nil {
		log.Debug("could not get sender from transaction with LatestSignerForChainID", "err", err, "req_id", GetReqID(ctx))
		return nil, nil, ErrInvalidParams(err.Error())
	}

	return tx, &from, nil
}

// CheckSanctionedAddresses checks if a request involves any sanctioned addresses
func (s *Server) CheckSanctionedAddresses(ctx context.Context, req *RPCReq) error {
	if req.Method != "eth_sendRawTransaction" || s.sanctionedAddresses == nil {
		return nil
	}

	tx, from, err := s.processTransaction(ctx, req)
	if err != nil {
		return err
	}

	if _, ok := s.sanctionedAddresses[*from]; ok {
		log.Info("sender is sanctioned", "sender", from, "req_id", GetReqID(ctx))
		return ErrNoBackends
	}
	to := tx.To()
	// Create transactions do not have a "to" address so in this case "to" can be nil.
	if to != nil {
		if _, ok := s.sanctionedAddresses[*to]; ok {
			log.Info("recipient is sanctioned", "recipient", to, "req_id", GetReqID(ctx))
			return ErrNoBackends
		}
	}

	return nil
}

func (s *Server) filterSanctionedAddresses(ctx context.Context, req *RPCReq) error {
	return s.CheckSanctionedAddresses(ctx, req)
}

func (s *Server) rateLimitSender(ctx context.Context, req *RPCReq) error {
	tx, from, err := s.processTransaction(ctx, req)
	if err != nil {
		return err
	}

	if !s.isAllowedChainId(tx.ChainId()) {
		log.Debug("chain id is not allowed", "req_id", GetReqID(ctx))
		return txpool.ErrInvalidSender
	}

	ok, err := s.senderLim.Take(ctx, fmt.Sprintf("%s:%d", from.Hex(), tx.Nonce()))
	if err != nil {
		log.Error("error taking from sender limiter", "err", err, "req_id", GetReqID(ctx))
		return ErrInternal
	}
	if !ok {
		log.Debug("sender rate limit exceeded", "sender", from.Hex(), "req_id", GetReqID(ctx))
		return ErrOverSenderRateLimit
	}

	return nil
}

func (s *Server) isAllowedChainId(chainId *big.Int) bool {
	if len(s.allowedChainIds) == 0 {
		return true
	}
	for _, id := range s.allowedChainIds {
		if chainId.Cmp(id) == 0 {
			return true
		}
	}
	return false
}

func setCacheHeader(w http.ResponseWriter, cached bool) {
	if cached {
		w.Header().Set(cacheStatusHdr, "HIT")
	} else {
		w.Header().Set(cacheStatusHdr, "MISS")
	}
}

func writeRPCError(ctx context.Context, w http.ResponseWriter, id json.RawMessage, err error) {
	var res *RPCRes
	if r, ok := err.(*RPCErr); ok {
		res = NewRPCErrorRes(id, r)
	} else {
		res = NewRPCErrorRes(id, ErrInternal)
	}
	writeRPCRes(ctx, w, res)
}

func writeRPCRes(ctx context.Context, w http.ResponseWriter, res *RPCRes) {
	statusCode := 200
	if res.IsError() && res.Error.HTTPErrorCode != 0 {
		statusCode = res.Error.HTTPErrorCode
	}

	w.Header().Set("content-type", "application/json")
	w.WriteHeader(statusCode)
	ww := &recordLenWriter{Writer: w}
	enc := json.NewEncoder(ww)
	if err := enc.Encode(res); err != nil {
		log.Error("error writing rpc response", "err", err)
		RecordRPCError(ctx, BackendProxyd, MethodUnknown, err)
		return
	}
	httpResponseCodesTotal.WithLabelValues(strconv.Itoa(statusCode)).Inc()
	RecordResponsePayloadSize(ctx, ww.Len)
}

func writeBatchRPCRes(ctx context.Context, w http.ResponseWriter, res []*RPCRes) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(200)
	ww := &recordLenWriter{Writer: w}
	enc := json.NewEncoder(ww)
	if err := enc.Encode(res); err != nil {
		log.Error("error writing batch rpc response", "err", err)
		RecordRPCError(ctx, BackendProxyd, MethodUnknown, err)
		return
	}
	RecordResponsePayloadSize(ctx, ww.Len)
}

func instrumentedHdlr(h http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		respTimer := prometheus.NewTimer(httpRequestDurationSumm)
		h.ServeHTTP(w, r)
		respTimer.ObserveDuration()
	}
}

func GetAuthCtx(ctx context.Context) string {
	authUser, ok := ctx.Value(ContextKeyAuth).(string)
	if !ok {
		return "none"
	}

	return authUser
}

func GetReqID(ctx context.Context) string {
	reqId, ok := ctx.Value(ContextKeyReqID).(string)
	if !ok {
		return ""
	}
	return reqId
}

func GetXForwardedFor(ctx context.Context) string {
	xff, ok := ctx.Value(ContextKeyXForwardedFor).(string)
	if !ok {
		return ""
	}
	return xff
}

func GetReferer(ctx context.Context) string {
	referer, ok := ctx.Value(ContextKeyReferer).(string)
	if !ok {
		return ""
	}
	return referer
}

func GetUserAgent(ctx context.Context) string {
	userAgent, ok := ctx.Value(ContextKeyUserAgent).(string)
	if !ok {
		return ""
	}
	return userAgent
}

type recordLenWriter struct {
	io.Writer
	Len int
}

func (w *recordLenWriter) Write(p []byte) (n int, err error) {
	n, err = w.Writer.Write(p)
	w.Len += n
	return
}

type NoopRPCCache struct{}

func (n *NoopRPCCache) GetRPC(context.Context, *RPCReq) (*RPCRes, error) {
	return nil, nil
}

func (n *NoopRPCCache) PutRPC(context.Context, *RPCReq, *RPCRes) error {
	return nil
}

func truncate(str string, maxLen int) string {
	if maxLen == 0 {
		maxLen = maxRequestBodyLogLen
	}

	if len(str) > maxLen {
		return str[:maxLen] + "..."
	} else {
		return str
	}
}

type batchElem struct {
	Req   *RPCReq
	Index int
}

func createBatchRequest(elems []batchElem) []*RPCReq {
	batch := make([]*RPCReq, len(elems))
	for i := range elems {
		batch[i] = elems[i].Req
	}
	return batch
}

// RequestInfo contains extracted information from an RPC request for logging
type RequestInfo struct {
	RemoteIP        string `json:"remote_ip"`
	XForwardedFor   string `json:"x_forwarded_for"`
	RPCMethod       string `json:"rpc_method"`
	FromAddress     string `json:"from_address,omitempty"`
	ToAddress       string `json:"to_address,omitempty"`
	TransactionHash string `json:"transaction_hash,omitempty"`
	Referer         string `json:"referer,omitempty"`
	UserAgent       string `json:"user_agent,omitempty"`
}

// LogRequestInfo logs comprehensive information about RPC and WebSocket requests
func (s *Server) LogRequestInfo(ctx context.Context, req *RPCReq, source string, rawBody ...[]byte) {
	// Only log if enabled in configuration
	if !s.enableRequestLog {
		return
	}

	info := s.extractRequestInfo(ctx, req)

	// Build dynamic log fields, only including non-empty values
	logFields := []interface{}{
		"source", source,
		"remote_ip", info.RemoteIP,
		"rpc_method", info.RPCMethod,
		"req_id", GetReqID(ctx),
		"auth", GetAuthCtx(ctx),
	}

	// Add raw body if provided (for RPC requests)
	if len(rawBody) > 0 && len(rawBody[0]) > 0 {
		logFields = append(logFields, "body", truncate(string(rawBody[0]), s.maxRequestBodyLogLen))
	}

	// Add optional fields only if they have values
	if info.XForwardedFor != "" {
		logFields = append(logFields, "x_forwarded_for", info.XForwardedFor)
	}
	if info.FromAddress != "" {
		logFields = append(logFields, "from_address", info.FromAddress)
	}
	if info.ToAddress != "" {
		logFields = append(logFields, "to_address", info.ToAddress)
	}
	if info.TransactionHash != "" {
		logFields = append(logFields, "transaction_hash", info.TransactionHash)
	}
	if info.Referer != "" {
		logFields = append(logFields, "referer", info.Referer)
	}
	if info.UserAgent != "" {
		logFields = append(logFields, "user_agent", info.UserAgent)
	}

	log.Info("request received", logFields...)
}

// extractRequestInfo extracts detailed information from an RPC request
func (s *Server) extractRequestInfo(ctx context.Context, req *RPCReq) *RequestInfo {
	info := &RequestInfo{
		RemoteIP:      stripXFF(GetXForwardedFor(ctx)),
		XForwardedFor: GetXForwardedFor(ctx),
		RPCMethod:     req.Method,
		Referer:       GetReferer(ctx),
		UserAgent:     GetUserAgent(ctx),
	}

	// Extract transaction details based on method
	switch req.Method {
	case "eth_sendRawTransaction":
		s.extractSendRawTransactionInfo(req, info)
	case "eth_sendTransaction":
		s.extractSendTransactionInfo(req, info)
	case "eth_getTransactionByHash":
		s.extractTransactionHashFromParams(req, info)
	case "eth_getTransactionReceipt":
		s.extractTransactionHashFromParams(req, info)
	case "eth_call":
		s.extractCallInfo(req, info)
	case "eth_estimateGas":
		s.extractEstimateGasInfo(req, info)
	case "eth_getBalance", "eth_getCode", "eth_getTransactionCount", "eth_getStorageAt":
		s.extractAddressFromParams(req, info)
	}

	return info
}

// extractSendRawTransactionInfo extracts transaction details from eth_sendRawTransaction
func (s *Server) extractSendRawTransactionInfo(req *RPCReq, info *RequestInfo) {
	tx, from, err := s.processTransaction(context.Background(), req)
	if err != nil {
		return
	}

	info.FromAddress = from.Hex()
	info.TransactionHash = tx.Hash().Hex()

	if to := tx.To(); to != nil {
		info.ToAddress = to.Hex()
	}
}

// extractSendTransactionInfo extracts transaction details from eth_sendTransaction
func (s *Server) extractSendTransactionInfo(req *RPCReq, info *RequestInfo) {
	var params []map[string]interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return
	}

	if len(params) == 0 {
		return
	}

	txParam := params[0]

	if from, ok := txParam["from"].(string); ok {
		info.FromAddress = from
	}

	if to, ok := txParam["to"].(string); ok {
		info.ToAddress = to
	}
}

// extractTransactionHashFromParams extracts transaction hash from first parameter
func (s *Server) extractTransactionHashFromParams(req *RPCReq, info *RequestInfo) {
	var params []string
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return
	}

	if len(params) > 0 {
		info.TransactionHash = params[0]
	}
}

// extractCallInfo extracts call information from eth_call
func (s *Server) extractCallInfo(req *RPCReq, info *RequestInfo) {
	var params []interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return
	}

	if len(params) == 0 {
		return
	}

	if callParam, ok := params[0].(map[string]interface{}); ok {
		if from, ok := callParam["from"].(string); ok {
			info.FromAddress = from
		}

		if to, ok := callParam["to"].(string); ok {
			info.ToAddress = to
		}
	}
}

// extractEstimateGasInfo extracts gas estimation information
func (s *Server) extractEstimateGasInfo(req *RPCReq, info *RequestInfo) {
	s.extractCallInfo(req, info) // Same structure as eth_call
}

// LogWatchedAddressTransaction checks if a transaction involves any watched address
// and if so, logs all available transaction details.
func (s *Server) LogWatchedAddressTransaction(ctx context.Context, req *RPCReq) {
	if len(s.watchedAddresses) == 0 {
		return
	}

	switch req.Method {
	case "eth_sendRawTransaction":
		s.logWatchedRawTransaction(ctx, req)
	case "eth_sendTransaction", "eth_call", "eth_estimateGas":
		s.logWatchedCallTransaction(ctx, req)
	}
}

// logWatchedRawTransaction decodes a raw transaction and logs it if from/to matches a watched address
func (s *Server) logWatchedRawTransaction(ctx context.Context, req *RPCReq) {
	tx, from, err := s.processTransaction(ctx, req)
	if err != nil {
		return
	}

	to := tx.To()
	fromWatched := s.isWatchedAddress(from)
	toWatched := to != nil && s.isWatchedAddress(to)

	if !fromWatched && !toWatched {
		return
	}

	logFields := []interface{}{
		"req_id", GetReqID(ctx),
		"auth", GetAuthCtx(ctx),
		"method", req.Method,
		"tx_hash", tx.Hash().Hex(),
		"from", from.Hex(),
		"nonce", tx.Nonce(),
		"value", tx.Value().String(),
		"gas", tx.Gas(),
		"chain_id", tx.ChainId().String(),
		"tx_type", tx.Type(),
	}

	if to != nil {
		logFields = append(logFields, "to", to.Hex())
	} else {
		logFields = append(logFields, "to", "contract_creation")
	}

	if tx.GasPrice() != nil && tx.GasPrice().Sign() > 0 {
		logFields = append(logFields, "gas_price", tx.GasPrice().String())
	}
	if tx.GasTipCap() != nil && tx.GasTipCap().Sign() > 0 {
		logFields = append(logFields, "gas_tip_cap", tx.GasTipCap().String())
	}
	if tx.GasFeeCap() != nil && tx.GasFeeCap().Sign() > 0 {
		logFields = append(logFields, "gas_fee_cap", tx.GasFeeCap().String())
	}
	if len(tx.Data()) > 0 {
		dataHex := fmt.Sprintf("0x%x", tx.Data())
		logFields = append(logFields, "data", truncate(dataHex, 256))
		logFields = append(logFields, "data_len", len(tx.Data()))
	}
	if tx.BlobGasFeeCap() != nil && tx.BlobGasFeeCap().Sign() > 0 {
		logFields = append(logFields, "blob_gas_fee_cap", tx.BlobGasFeeCap().String())
	}
	if tx.BlobGas() > 0 {
		logFields = append(logFields, "blob_gas", tx.BlobGas())
	}

	logFields = append(logFields, "from_watched", fromWatched, "to_watched", toWatched)

	log.Info("watched address transaction detected", logFields...)
}

// logWatchedCallTransaction checks eth_sendTransaction / eth_call / eth_estimateGas params
// for watched addresses and logs details if found
func (s *Server) logWatchedCallTransaction(ctx context.Context, req *RPCReq) {
	var params []json.RawMessage
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params) == 0 {
		return
	}

	var callObj map[string]json.RawMessage
	if err := json.Unmarshal(params[0], &callObj); err != nil {
		return
	}

	var fromStr, toStr string
	if raw, ok := callObj["from"]; ok {
		json.Unmarshal(raw, &fromStr) // nolint:errcheck
	}
	if raw, ok := callObj["to"]; ok {
		json.Unmarshal(raw, &toStr) // nolint:errcheck
	}

	var fromAddr, toAddr *common.Address
	if common.IsHexAddress(fromStr) {
		a := common.HexToAddress(fromStr)
		fromAddr = &a
	}
	if common.IsHexAddress(toStr) {
		a := common.HexToAddress(toStr)
		toAddr = &a
	}

	fromWatched := fromAddr != nil && s.isWatchedAddress(fromAddr)
	toWatched := toAddr != nil && s.isWatchedAddress(toAddr)

	if !fromWatched && !toWatched {
		return
	}

	logFields := []interface{}{
		"req_id", GetReqID(ctx),
		"auth", GetAuthCtx(ctx),
		"method", req.Method,
	}

	if fromAddr != nil {
		logFields = append(logFields, "from", fromAddr.Hex())
	}
	if toAddr != nil {
		logFields = append(logFields, "to", toAddr.Hex())
	}

	// Extract optional fields from the call object
	for _, field := range []string{"value", "gas", "gasPrice", "maxFeePerGas", "maxPriorityFeePerGas", "nonce"} {
		if raw, ok := callObj[field]; ok {
			var val string
			if json.Unmarshal(raw, &val) == nil && val != "" {
				logFields = append(logFields, field, val)
			}
		}
	}

	if raw, ok := callObj["data"]; ok {
		var dataStr string
		if json.Unmarshal(raw, &dataStr) == nil && dataStr != "" {
			logFields = append(logFields, "data", truncate(dataStr, 256))
			logFields = append(logFields, "data_len", len(dataStr))
		}
	}
	if raw, ok := callObj["input"]; ok {
		var inputStr string
		if json.Unmarshal(raw, &inputStr) == nil && inputStr != "" {
			logFields = append(logFields, "input", truncate(inputStr, 256))
		}
	}

	logFields = append(logFields, "from_watched", fromWatched, "to_watched", toWatched)

	log.Info("watched address transaction detected", logFields...)
}

// isWatchedAddress checks if an address is in the watched addresses set
func (s *Server) isWatchedAddress(addr *common.Address) bool {
	if addr == nil || len(s.watchedAddresses) == 0 {
		return false
	}
	_, ok := s.watchedAddresses[*addr]
	return ok
}

// extractAddressFromParams extracts address from first parameter (for balance, code, etc.)
func (s *Server) extractAddressFromParams(req *RPCReq, info *RequestInfo) {
	var params []string
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return
	}

	if len(params) > 0 {
		info.ToAddress = params[0] // Using ToAddress field for the queried address
	}
}
