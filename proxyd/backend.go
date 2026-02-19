package proxyd

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"math/rand"
	"net/http"
	"net/http/httptrace"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	sw "github.com/ethereum-optimism/infra/proxyd/pkg/avg-sliding-window"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/gorilla/websocket"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/xaionaro-go/weightedshuffle"
	"go.opentelemetry.io/contrib/instrumentation/net/http/httptrace/otelhttptrace"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"golang.org/x/sync/semaphore"
)

const (
	JSONRPCVersion         = "2.0"
	JSONRPCErrorInternal   = -32000
	notFoundRpcError       = -32601
	sanctionedAddressError = -32801
	blocksInStateFullNode  = 128
)

var (
	blockParamIndex = map[string]int{
		"eth_getBalance":          1,
		"eth_getCode":             1,
		"eth_getTransactionCount": 1,
		"eth_call":                1,
		"eth_getStorageAt":        2,
		"eth_getProof":            2,
		"eth_getBlockReceipts":    0,
	}
)

var (
	ErrParseErr = &RPCErr{
		Code:          -32700,
		Message:       "parse error",
		HTTPErrorCode: 400,
	}
	ErrInternal = &RPCErr{
		Code:          JSONRPCErrorInternal,
		Message:       "internal error",
		HTTPErrorCode: 500,
	}
	ErrMethodNotWhitelisted = &RPCErr{
		Code:          notFoundRpcError,
		Message:       "rpc method is not whitelisted",
		HTTPErrorCode: 403,
	}
	ErrBackendOffline = &RPCErr{
		Code:          JSONRPCErrorInternal - 10,
		Message:       "backend offline",
		HTTPErrorCode: 503,
	}
	ErrNoBackends = &RPCErr{
		Code:          JSONRPCErrorInternal - 11,
		Message:       "no backend is currently healthy to serve traffic",
		HTTPErrorCode: 503,
	}
	ErrBackendOverCapacity = &RPCErr{
		Code:          JSONRPCErrorInternal - 12,
		Message:       "backend is over capacity",
		HTTPErrorCode: 429,
	}
	ErrBackendBadResponse = &RPCErr{
		Code:          JSONRPCErrorInternal - 13,
		Message:       "backend returned an invalid response",
		HTTPErrorCode: 500,
	}
	ErrTooManyBatchRequests = &RPCErr{
		Code:    JSONRPCErrorInternal - 14,
		Message: "too many RPC calls in batch request",
	}
	ErrGatewayTimeout = &RPCErr{
		Code:          JSONRPCErrorInternal - 15,
		Message:       "gateway timeout",
		HTTPErrorCode: 504,
	}
	ErrOverRateLimit = &RPCErr{
		Code:          JSONRPCErrorInternal - 16,
		Message:       "over rate limit",
		HTTPErrorCode: 429,
	}
	ErrOverSenderRateLimit = &RPCErr{
		Code:          JSONRPCErrorInternal - 17,
		Message:       "sender is over rate limit",
		HTTPErrorCode: 429,
	}
	ErrNotHealthy = &RPCErr{
		Code:          JSONRPCErrorInternal - 18,
		Message:       "backend is currently not healthy to serve traffic",
		HTTPErrorCode: 503,
	}
	ErrBlockOutOfRange = &RPCErr{
		Code:          JSONRPCErrorInternal - 19,
		Message:       "block is out of range",
		HTTPErrorCode: 400,
	}

	ErrRequestBodyTooLarge = &RPCErr{
		Code:          JSONRPCErrorInternal - 21,
		Message:       "request body too large",
		HTTPErrorCode: 413,
	}

	ErrBackendResponseTooLarge = &RPCErr{
		Code:          JSONRPCErrorInternal - 20,
		Message:       "backend response too large",
		HTTPErrorCode: 500,
	}

	ErrSanctionedAddress = &RPCErr{
		Code:          sanctionedAddressError,
		Message:       "address is sanctioned",
		HTTPErrorCode: 403,
	}

	ErrBackendUnexpectedJSONRPC = errors.New("backend returned an unexpected JSON-RPC response")

	ErrConsensusGetReceiptsCantBeBatched = errors.New("consensus_getReceipts cannot be batched")
	ErrConsensusGetReceiptsInvalidTarget = errors.New("unsupported consensus_receipts_target")
)

func ErrInvalidRequest(msg string) *RPCErr {
	return &RPCErr{
		Code:          -32600,
		Message:       msg,
		HTTPErrorCode: 400,
	}
}

func ErrInvalidParams(msg string) *RPCErr {
	return &RPCErr{
		Code:          -32602,
		Message:       msg,
		HTTPErrorCode: 400,
	}
}

type Backend struct {
	Name                 string
	rpcURL               string
	receiptsTarget       string
	wsURL                string
	authUsername         string
	authPassword         string
	headers              map[string]string
	client               *LimitedHTTPClient
	dialer               *websocket.Dialer
	maxRetries           int
	maxResponseSize      int64
	maxRPS               int
	maxWSConns           int
	outOfServiceInterval time.Duration
	stripTrailingXFF     bool
	proxydIP             string

	skipPeerCountCheck bool
	forcedCandidate    bool
	archive            bool

	maxDegradedLatencyThreshold time.Duration
	maxLatencyThreshold         time.Duration
	maxErrorRateThreshold       float64

	latencySlidingWindow            *sw.AvgSlidingWindow
	networkRequestsSlidingWindow    *sw.AvgSlidingWindow
	intermittentErrorsSlidingWindow *sw.AvgSlidingWindow

	weight int
}

type BackendOpt func(b *Backend)

func WithBasicAuth(username, password string) BackendOpt {
	return func(b *Backend) {
		b.authUsername = username
		b.authPassword = password
	}
}

func WithHeaders(headers map[string]string) BackendOpt {
	return func(b *Backend) {
		b.headers = headers
	}
}

func WithTimeout(timeout time.Duration) BackendOpt {
	return func(b *Backend) {
		b.client.Timeout = timeout
	}
}

func WithMaxRetries(retries int) BackendOpt {
	return func(b *Backend) {
		b.maxRetries = retries
	}
}

func WithMaxResponseSize(size int64) BackendOpt {
	return func(b *Backend) {
		b.maxResponseSize = size
	}
}

func WithOutOfServiceDuration(interval time.Duration) BackendOpt {
	return func(b *Backend) {
		b.outOfServiceInterval = interval
	}
}

func WithMaxRPS(maxRPS int) BackendOpt {
	return func(b *Backend) {
		b.maxRPS = maxRPS
	}
}

func WithMaxWSConns(maxConns int) BackendOpt {
	return func(b *Backend) {
		b.maxWSConns = maxConns
	}
}

func WithTLSConfig(tlsConfig *tls.Config) BackendOpt {
	return func(b *Backend) {
		if b.client.Transport == nil {
			b.client.Transport = &http.Transport{}
		}
		b.client.Transport.(*http.Transport).TLSClientConfig = tlsConfig
	}
}

func WithStrippedTrailingXFF() BackendOpt {
	return func(b *Backend) {
		b.stripTrailingXFF = true
	}
}

func WithProxydIP(ip string) BackendOpt {
	return func(b *Backend) {
		b.proxydIP = ip
	}
}

func WithConsensusSkipPeerCountCheck(skipPeerCountCheck bool) BackendOpt {
	return func(b *Backend) {
		b.skipPeerCountCheck = skipPeerCountCheck
	}
}

func WithConsensusForcedCandidate(forcedCandidate bool) BackendOpt {
	return func(b *Backend) {
		b.forcedCandidate = forcedCandidate
	}
}

func WithArchive(archive bool) BackendOpt {
	return func(b *Backend) {
		b.archive = archive
	}
}

func WithWeight(weight int) BackendOpt {
	return func(b *Backend) {
		b.weight = weight
	}
}

func WithMaxDegradedLatencyThreshold(maxDegradedLatencyThreshold time.Duration) BackendOpt {
	return func(b *Backend) {
		b.maxDegradedLatencyThreshold = maxDegradedLatencyThreshold
	}
}

func WithMaxLatencyThreshold(maxLatencyThreshold time.Duration) BackendOpt {
	return func(b *Backend) {
		b.maxLatencyThreshold = maxLatencyThreshold
	}
}

func WithMaxErrorRateThreshold(maxErrorRateThreshold float64) BackendOpt {
	return func(b *Backend) {
		b.maxErrorRateThreshold = maxErrorRateThreshold
	}
}

func WithConsensusReceiptTarget(receiptsTarget string) BackendOpt {
	return func(b *Backend) {
		b.receiptsTarget = receiptsTarget
	}
}

func WithIntermittentNetworkErrorSlidingWindow(sw *sw.AvgSlidingWindow) BackendOpt {
	return func(b *Backend) {
		b.intermittentErrorsSlidingWindow = sw
	}
}

type indexedReqRes struct {
	index int
	req   *RPCReq
	res   *RPCRes
}

const proxydHealthzMethod = "proxyd_healthz"

const ConsensusGetReceiptsMethod = "consensus_getReceipts"

const ReceiptsTargetDebugGetRawReceipts = "debug_getRawReceipts"
const ReceiptsTargetAlchemyGetTransactionReceipts = "alchemy_getTransactionReceipts"
const ReceiptsTargetParityGetTransactionReceipts = "parity_getBlockReceipts"
const ReceiptsTargetEthGetTransactionReceipts = "eth_getBlockReceipts"

type ConsensusGetReceiptsResult struct {
	Method string      `json:"method"`
	Result interface{} `json:"result"`
}

// BlockHashOrNumberParameter is a non-conventional wrapper used by alchemy_getTransactionReceipts
type BlockHashOrNumberParameter struct {
	BlockHash   *common.Hash     `json:"blockHash"`
	BlockNumber *rpc.BlockNumber `json:"blockNumber"`
}

func NewBackend(
	name string,
	rpcURL string,
	wsURL string,
	rpcSemaphore *semaphore.Weighted,
	opts ...BackendOpt,
) *Backend {
	backend := &Backend{
		Name:            name,
		rpcURL:          rpcURL,
		wsURL:           wsURL,
		maxResponseSize: math.MaxInt64,
		client: &LimitedHTTPClient{
			Client: http.Client{
				Transport: otelhttp.NewTransport(http.DefaultTransport),
				Timeout:   5 * time.Second},
			sem:         rpcSemaphore,
			backendName: name,
		},
		dialer: &websocket.Dialer{},

		maxLatencyThreshold:         10 * time.Second,
		maxDegradedLatencyThreshold: 5 * time.Second,
		maxErrorRateThreshold:       0.5,

		latencySlidingWindow:            sw.NewSlidingWindow(),
		networkRequestsSlidingWindow:    sw.NewSlidingWindow(),
		intermittentErrorsSlidingWindow: sw.NewSlidingWindow(),
	}

	backend.Override(opts...)

	if !backend.stripTrailingXFF && backend.proxydIP == "" {
		log.Warn("proxied requests' XFF header will not contain the proxyd ip address")
	}

	return backend
}

func (b *Backend) Override(opts ...BackendOpt) {
	for _, opt := range opts {
		opt(b)
	}
}

func (b *Backend) Forward(ctx context.Context, reqs []*RPCReq, isBatch bool) ([]*RPCRes, error) {
	_, span := tracer.Start(ctx, "ForwardFunction")
	defer span.End()

	var lastError error
	// <= to account for the first attempt not technically being
	// a retry
	for i := 0; i <= b.maxRetries; i++ {
		RecordBatchRPCForward(ctx, b.Name, reqs, RPCRequestSourceHTTP)
		metricLabelMethod := reqs[0].Method
		if isBatch {
			metricLabelMethod = "<batch>"
		}
		timer := prometheus.NewTimer(
			rpcBackendRequestDurationSumm.WithLabelValues(
				b.Name,
				metricLabelMethod,
				strconv.FormatBool(isBatch),
			),
		)

		log.Trace(
			"forwarding request to backend",
			"name", b.Name,
			"req_id", GetReqID(ctx),
			"attempt_count", i+1,
			"max_attempts", b.maxRetries+1,
			"method", metricLabelMethod,
		)
		res, err := b.doForward(ctx, reqs, isBatch)
		switch err {
		case nil: // do nothing
		case ErrBackendResponseTooLarge:
			log.Warn(
				"backend response too large",
				"name", b.Name,
				"req_id", GetReqID(ctx),
				"max", b.maxResponseSize,
				"method", metricLabelMethod,
			)
			RecordBatchRPCError(ctx, b.Name, reqs, err)
		case ErrBackendOverCapacity:
			log.Warn(
				"backend over capacity",
				"name", b.Name,
				"req_id", GetReqID(ctx),
				"method", metricLabelMethod,
			)
			RecordBatchRPCError(ctx, b.Name, reqs, err)
		case ErrConsensusGetReceiptsCantBeBatched:
			log.Warn(
				"Received unsupported batch request for consensus_getReceipts",
				"name", b.Name,
				"req_id", GetReqID(ctx),
				"err", err,
			)
		case ErrConsensusGetReceiptsInvalidTarget:
			log.Error(
				"Unsupported consensus_receipts_target for consensus_getReceipts",
				"name", b.Name,
				"req_id", GetReqID(ctx),
				"err", err,
			)
		// ErrBackendUnexpectedJSONRPC occurs because infura responds with a single JSON-RPC object
		// to a batch request whenever any Request Object in the batch would induce a partial error.
		// We don't label the backend offline in this case. But the error is still returned to
		// callers so failover can occur if needed.
		case ErrBackendUnexpectedJSONRPC:
			log.Debug(
				"Received unexpected JSON-RPC response",
				"name", b.Name,
				"req_id", GetReqID(ctx),
				"err", err,
			)
		default:
			lastError = err
			log.Warn(
				"backend request failed, trying again",
				"name", b.Name,
				"req_id", GetReqID(ctx),
				"err", err,
				"method", metricLabelMethod,
				"attempt_count", i+1,
				"max_retries", b.maxRetries+1,
			)
			timer.ObserveDuration()
			RecordBatchRPCError(ctx, b.Name, reqs, err)
			sleepContext(ctx, calcBackoff(i))
			continue
		}
		timer.ObserveDuration()

		MaybeRecordErrorsInRPCRes(ctx, b.Name, reqs, res)
		return res, err
	}

	return nil, wrapErr(lastError, "permanent error forwarding request")
}

func (b *Backend) ProxyWS(clientConn *websocket.Conn, methodWhitelist *StringSet, server *Server) (*WSProxier, error) {
	backendConn, _, err := b.dialer.Dial(b.wsURL, nil) // nolint:bodyclose
	if err != nil {
		return nil, wrapErr(err, "error dialing backend")
	}

	activeBackendWsConnsGauge.WithLabelValues(b.Name).Inc()
	return NewWSProxier(b, clientConn, backendConn, methodWhitelist, server), nil
}

// ForwardRPC makes a call directly to a backend and populate the response into `res`
func (b *Backend) ForwardRPC(ctx context.Context, res *RPCRes, id string, method string, params ...any) error {
	_, span := tracer.Start(ctx, "ForwardRPCFunction")
	defer span.End()

	jsonParams, err := json.Marshal(params)
	if err != nil {
		return err
	}

	rpcReq := RPCReq{
		JSONRPC: JSONRPCVersion,
		Method:  method,
		Params:  jsonParams,
		ID:      []byte(id),
	}

	slicedRes, err := b.doForward(ctx, []*RPCReq{&rpcReq}, false)
	if err != nil {
		return err
	}

	if len(slicedRes) != 1 {
		return fmt.Errorf("unexpected response len for non-batched request (len != 1)")
	}
	if slicedRes[0].IsError() {
		return fmt.Errorf("backend error: %s", slicedRes[0].Error.Error())
	}

	*res = *(slicedRes[0])
	return nil
}

func (b *Backend) doForward(ctx context.Context, rpcReqs []*RPCReq, isBatch bool) ([]*RPCRes, error) {
	ctx, span := tracer.Start(ctx, "doForwardFunction")
	defer span.End()

	// we are concerned about network error rates, so we record 1 request independently of how many are in the batch
	b.networkRequestsSlidingWindow.Incr()

	translatedReqs := make(map[string]*RPCReq, len(rpcReqs))
	// translate consensus_getReceipts to receipts target
	// right now we only support non-batched
	if isBatch {
		for _, rpcReq := range rpcReqs {
			if rpcReq.Method == ConsensusGetReceiptsMethod {
				return nil, ErrConsensusGetReceiptsCantBeBatched
			}
		}
	} else {
		for _, rpcReq := range rpcReqs {
			if rpcReq.Method == ConsensusGetReceiptsMethod {
				translatedReqs[string(rpcReq.ID)] = rpcReq
				rpcReq.Method = b.receiptsTarget
				var reqParams []rpc.BlockNumberOrHash
				err := json.Unmarshal(rpcReq.Params, &reqParams)
				if err != nil {
					return nil, ErrInvalidRequest("invalid request")
				}

				var translatedParams []byte
				switch rpcReq.Method {
				case ReceiptsTargetDebugGetRawReceipts,
					ReceiptsTargetEthGetTransactionReceipts,
					ReceiptsTargetParityGetTransactionReceipts:
					// conventional methods use an array of strings having either block number or block hash
					// i.e. ["0xc6ef2fc5426d6ad6fd9e2a26abeab0aa2411b7ab17f30a99d3cb96aed1d1055b"]
					params := make([]string, 1)
					if reqParams[0].BlockNumber != nil {
						params[0] = reqParams[0].BlockNumber.String()
					} else {
						params[0] = reqParams[0].BlockHash.Hex()
					}
					translatedParams = mustMarshalJSON(params)
				case ReceiptsTargetAlchemyGetTransactionReceipts:
					// alchemy uses an array of object with either block number or block hash
					// i.e. [{ blockHash: "0xc6ef2fc5426d6ad6fd9e2a26abeab0aa2411b7ab17f30a99d3cb96aed1d1055b" }]
					params := make([]BlockHashOrNumberParameter, 1)
					if reqParams[0].BlockNumber != nil {
						params[0].BlockNumber = reqParams[0].BlockNumber
					} else {
						params[0].BlockHash = reqParams[0].BlockHash
					}
					translatedParams = mustMarshalJSON(params)
				default:
					return nil, ErrConsensusGetReceiptsInvalidTarget
				}

				rpcReq.Params = translatedParams
			}
		}
	}

	isSingleElementBatch := len(rpcReqs) == 1

	// Single element batches are unwrapped before being sent
	// since Alchemy handles single requests better than batches.
	var body []byte
	if isSingleElementBatch {
		body = mustMarshalJSON(rpcReqs[0])
	} else {
		body = mustMarshalJSON(rpcReqs)
	}

	// Inject DNS/TLS hooks
	clientTrace := otelhttptrace.NewClientTrace(ctx)
	ctx = httptrace.WithClientTrace(ctx, clientTrace)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", b.rpcURL, bytes.NewReader(body))
	if err != nil {
		b.intermittentErrorsSlidingWindow.Incr()
		RecordBackendNetworkErrorRateSlidingWindow(b, b.ErrorRate())
		return nil, wrapErr(err, "error creating backend request")
	}

	if b.authPassword != "" {
		httpReq.SetBasicAuth(b.authUsername, b.authPassword)
	}

	xForwardedFor := GetXForwardedFor(ctx)
	if b.stripTrailingXFF {
		xForwardedFor = stripXFF(xForwardedFor)
	} else if b.proxydIP != "" {
		xForwardedFor = fmt.Sprintf("%s, %s", xForwardedFor, b.proxydIP)
	}

	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("X-Forwarded-For", xForwardedFor)

	for name, value := range b.headers {
		httpReq.Header.Set(name, value)
	}

	start := time.Now()
	httpRes, err := b.client.DoLimited(httpReq)
	if err != nil {
		b.intermittentErrorsSlidingWindow.Incr()
		RecordBackendNetworkErrorRateSlidingWindow(b, b.ErrorRate())
		// Check if this is a "too many requests" error from our own rate limiter
		// to enable failover to other backends instead of retrying the same one
		if strings.Contains(err.Error(), "too many requests") {
			return nil, ErrBackendOverCapacity
		}
		return nil, wrapErr(err, "error in backend request")
	}

	metricLabelMethod := rpcReqs[0].Method
	if isBatch {
		metricLabelMethod = "<batch>"
	}
	rpcBackendHTTPResponseCodesTotal.WithLabelValues(
		GetAuthCtx(ctx),
		b.Name,
		metricLabelMethod,
		strconv.Itoa(httpRes.StatusCode),
		strconv.FormatBool(isBatch),
	).Inc()

	// Alchemy returns a 400 on bad JSONs, so handle that case
	if httpRes.StatusCode != 200 && httpRes.StatusCode != 400 {
		b.intermittentErrorsSlidingWindow.Incr()
		RecordBackendNetworkErrorRateSlidingWindow(b, b.ErrorRate())
		// Return ErrBackendOverCapacity for 429 responses to enable failover to other backends
		if httpRes.StatusCode == 429 {
			return nil, ErrBackendOverCapacity
		}
		return nil, fmt.Errorf("response code %d", httpRes.StatusCode)
	}

	defer httpRes.Body.Close()
	resB, err := io.ReadAll(LimitReader(httpRes.Body, b.maxResponseSize))
	if errors.Is(err, ErrLimitReaderOverLimit) {
		return nil, ErrBackendResponseTooLarge
	}
	if err != nil {
		b.intermittentErrorsSlidingWindow.Incr()
		RecordBackendNetworkErrorRateSlidingWindow(b, b.ErrorRate())
		return nil, wrapErr(err, "error reading response body")
	}

	var rpcRes []*RPCRes
	if isSingleElementBatch {
		var singleRes RPCRes
		if err := json.Unmarshal(resB, &singleRes); err != nil {
			return nil, ErrBackendBadResponse
		}
		rpcRes = []*RPCRes{
			&singleRes,
		}
	} else {
		if err := json.Unmarshal(resB, &rpcRes); err != nil {
			// Infura may return a single JSON-RPC response if, for example, the batch contains a request for an unsupported method
			if responseIsNotBatched(resB) {
				b.intermittentErrorsSlidingWindow.Incr()
				RecordBackendNetworkErrorRateSlidingWindow(b, b.ErrorRate())
				return nil, ErrBackendUnexpectedJSONRPC
			}
			b.intermittentErrorsSlidingWindow.Incr()
			RecordBackendNetworkErrorRateSlidingWindow(b, b.ErrorRate())
			return nil, ErrBackendBadResponse
		}
	}

	if len(rpcReqs) != len(rpcRes) {
		b.intermittentErrorsSlidingWindow.Incr()
		RecordBackendNetworkErrorRateSlidingWindow(b, b.ErrorRate())
		return nil, ErrBackendUnexpectedJSONRPC
	}

	// capture the HTTP status code in the response. this will only
	// ever be 400 given the status check on line 318 above.
	if httpRes.StatusCode != 200 {
		for _, res := range rpcRes {
			res.Error.HTTPErrorCode = httpRes.StatusCode
		}
	}
	duration := time.Since(start)
	b.latencySlidingWindow.Add(float64(duration))
	RecordBackendNetworkLatencyAverageSlidingWindow(b, time.Duration(b.latencySlidingWindow.Avg()))
	RecordBackendNetworkErrorRateSlidingWindow(b, b.ErrorRate())

	// enrich the response with the actual request method
	for _, res := range rpcRes {
		translatedReq, exist := translatedReqs[string(res.ID)]
		if exist {
			res.Result = ConsensusGetReceiptsResult{
				Method: translatedReq.Method,
				Result: res.Result,
			}
		}
	}

	sortBatchRPCResponse(rpcReqs, rpcRes)

	return rpcRes, nil
}

// IsHealthy checks if the backend is able to serve traffic, based on dynamic parameters
func (b *Backend) IsHealthy() bool {
	errorRate := b.ErrorRate()
	avgLatency := time.Duration(b.latencySlidingWindow.Avg())
	if errorRate >= b.maxErrorRateThreshold {
		return false
	}
	if avgLatency >= b.maxLatencyThreshold {
		return false
	}
	return true
}

// ErrorRate returns the instant error rate of the backend
func (b *Backend) ErrorRate() (errorRate float64) {
	// we only really start counting the error rate after a minimum of 10 requests
	// this is to avoid false positives when the backend is just starting up
	if b.networkRequestsSlidingWindow.Sum() >= 10 {
		errorRate = b.intermittentErrorsSlidingWindow.Sum() / b.networkRequestsSlidingWindow.Sum()
	}
	return errorRate
}

// IsDegraded checks if the backend is serving traffic in a degraded state (i.e. used as a last resource)
func (b *Backend) IsDegraded() bool {
	avgLatency := time.Duration(b.latencySlidingWindow.Avg())
	return avgLatency >= b.maxDegradedLatencyThreshold
}

func responseIsNotBatched(b []byte) bool {
	var r RPCRes
	return json.Unmarshal(b, &r) == nil
}

// sortBatchRPCResponse sorts the RPCRes slice according to the position of its corresponding ID in the RPCReq slice
func sortBatchRPCResponse(req []*RPCReq, res []*RPCRes) {
	pos := make(map[string]int, len(req))
	for i, r := range req {
		key := string(r.ID)
		if _, ok := pos[key]; ok {
			panic("bug! detected requests with duplicate IDs")
		}
		pos[key] = i
	}

	sort.Slice(res, func(i, j int) bool {
		l := res[i].ID
		r := res[j].ID
		return pos[string(l)] < pos[string(r)]
	})
}

type BackendGroup struct {
	Name                       string
	Backends                   []*Backend
	WeightedRouting            bool
	SkipEIP1898                bool
	Consensus                  *ConsensusPoller
	FallbackBackends           map[string]bool
	routingStrategy            RoutingStrategy
	RestrictArchiveNodeTraffic bool
	ArchiveBlockThreshold      uint64
}

func (bg *BackendGroup) GetRoutingStrategy() RoutingStrategy {
	return bg.routingStrategy
}

func (bg *BackendGroup) Fallbacks() []*Backend {
	fallbacks := []*Backend{}
	for _, a := range bg.Backends {
		if fallback, ok := bg.FallbackBackends[a.Name]; ok && fallback {
			fallbacks = append(fallbacks, a)
		}
	}
	return fallbacks
}

func (bg *BackendGroup) Primaries() []*Backend {
	primaries := []*Backend{}
	for _, a := range bg.Backends {
		fallback, ok := bg.FallbackBackends[a.Name]
		if ok && !fallback {
			primaries = append(primaries, a)
		}
	}
	return primaries
}

// NOTE: BackendGroup Forward contains the log for balancing with consensus aware
func (bg *BackendGroup) Forward(ctx context.Context, rpcReqs []*RPCReq, isBatch bool) ([]*RPCRes, string, error) {
	if len(rpcReqs) == 0 {
		return nil, "", nil
	}

	backends := bg.orderedBackendsForRequest()

	overriddenResponses := make([]*indexedReqRes, 0)
	rewrittenReqs := make([]*RPCReq, 0, len(rpcReqs))

	// When routing_strategy is set to `consensus_aware` the backend group acts as a load balancer
	// serving traffic from any backend that agrees in the consensus group
	// We also rewrite block tags to enforce compliance with consensus
	if bg.Consensus != nil {
		rpcReqs, overriddenResponses = bg.OverwriteConsensusResponses(rpcReqs, overriddenResponses, rewrittenReqs)
	}

	// When routing_strategy is set to 'multicall' the request will be forward to all backends
	// and return the first successful response
	if bg.GetRoutingStrategy() == MulticallRoutingStrategy && isValidMulticallTx(rpcReqs) && !isBatch {
		backendResp := bg.ExecuteMulticall(ctx, rpcReqs)
		return backendResp.RPCRes, backendResp.ServedBy, backendResp.error
	}

	// Determine if archive is required
	archiveRequired := false
	for _, req := range rpcReqs {
		// All debug_* methods require archive nodes
		if strings.HasPrefix(req.Method, "debug_") {
			archiveRequired = true
			break
		}

		idx, ok := blockParamIndex[req.Method]
		if !ok {
			continue
		}
		var params []interface{}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			log.Error("error unmarshalling params for archive-aware methods",
				"req_id", GetReqID(ctx),
				"error", err)
			return nil, "", ErrInvalidRequest("invalid request")
		}
		if len(params) <= idx {
			continue // no block param, skip
		}

		blockParam := extractBlockParameter(params[idx])
		if blockParam == "" {
			// Special case: if it's a map with blockHash, assume archive is required
			if blockParamMap, ok := params[idx].(map[string]interface{}); ok {
				if _, exists := blockParamMap["blockHash"]; exists {
					archiveRequired = true
				}
			}
			continue
		}

		if bg.Consensus != nil && requiresArchiveForBlock(blockParam, bg.Consensus.GetLatestBlockNumber(), bg.ArchiveBlockThreshold) {
			archiveRequired = true
		}
	}

	// Filter backends based on archive requirement
	if archiveRequired {
		var archiveBackends []*Backend
		for _, backend := range backends {
			if backend.archive {
				archiveBackends = append(archiveBackends, backend)
			}
		}
		if len(archiveBackends) > 0 {
			backends = archiveBackends
		}
		rpcArchiveRequestsTotal.Inc()
	} else if bg.RestrictArchiveNodeTraffic {
		// When restrict_archive_node_traffic is enabled and request doesn't require archive,
		// prefer non-archive backends but fall back to archive backends if none available
		var nonArchiveBackends []*Backend
		for _, backend := range backends {
			if !backend.archive {
				nonArchiveBackends = append(nonArchiveBackends, backend)
			}
		}
		if len(nonArchiveBackends) > 0 {
			backends = nonArchiveBackends
		}
		// If no non-archive backends available, use archive backends as fallback (keep original backends list)
	}

	rpcRequestsTotal.Inc()

	ch := make(chan BackendGroupRPCResponse)
	go func() {
		defer close(ch)
		backendResp := bg.ForwardRequestToBackendGroup(rpcReqs, backends, ctx, isBatch)
		ch <- *backendResp
	}()
	backendResp := <-ch

	if backendResp.error != nil {
		log.Error("error serving requests",
			"req_id", GetReqID(ctx),
			"auth", GetAuthCtx(ctx),
			"err", backendResp.error,
		)
		return backendResp.RPCRes, backendResp.ServedBy, backendResp.error
	}

	// re-apply overridden responses
	log.Trace("successfully served request overriding responses",
		"req_id", GetReqID(ctx),
		"auth", GetAuthCtx(ctx),
	)
	res := OverrideResponses(backendResp.RPCRes, overriddenResponses)
	return res, backendResp.ServedBy, backendResp.error
}

func isValidMulticallTx(rpcReqs []*RPCReq) bool {
	if len(rpcReqs) == 1 {
		if rpcReqs[0].Method == "eth_sendRawTransaction" {
			return true
		}
	}
	return false
}

// Using special struct since servedBy may not be populated if error occurs
type multicallTuple struct {
	response    *BackendGroupRPCResponse
	backendName string
}

func (bg *BackendGroup) ExecuteMulticall(ctx context.Context, rpcReqs []*RPCReq) *BackendGroupRPCResponse {

	// Create ctx without cancel so background tasks process
	// after original request returns
	bgCtx := context.WithoutCancel(ctx)

	log.Info("executing multicall routing strategy",
		"req_id", GetReqID(bgCtx),
		"auth", GetAuthCtx(bgCtx),
	)
	var wg sync.WaitGroup
	ch := make(chan *multicallTuple, len(bg.Backends))
	for _, backend := range bg.Backends {
		wg.Add(1)
		go bg.MulticallRequest(backend, rpcReqs, &wg, bgCtx, ch)
	}

	go func() {
		wg.Wait()
		log.Debug("closing multicall channel",
			"req_id", GetReqID(bgCtx),
			"auth", GetAuthCtx(bgCtx),
		)
		close(ch)
	}()

	return bg.ProcessMulticallResponses(ch, bgCtx)
}

func (bg *BackendGroup) MulticallRequest(backend *Backend, rpcReqs []*RPCReq, wg *sync.WaitGroup, ctx context.Context, ch chan *multicallTuple) {
	defer wg.Done()
	log.Debug("forwarding multicall request to backend",
		"req_id", GetReqID(ctx),
		"auth", GetAuthCtx(ctx),
		"backend", backend.Name,
	)

	RecordBackendGroupMulticallRequest(bg, backend.Name)
	backendResp := bg.ForwardRequestToBackendGroup(rpcReqs, []*Backend{backend}, ctx, false)

	multicallResp := &multicallTuple{
		response:    backendResp,
		backendName: backend.Name,
	}

	log.Debug("placing multicall response into channel",
		"req_id", GetReqID(ctx),
		"auth", GetAuthCtx(ctx),
		"backend", backend.Name,
	)

	ch <- multicallResp

	log.Trace("placed multicall response into channel",
		"req_id", GetReqID(ctx),
		"auth", GetAuthCtx(ctx),
		"backend", backend.Name,
	)

	if backendResp.error != nil {
		log.Error("received multicall error response from backend",
			"req_id", GetReqID(ctx),
			"auth", GetAuthCtx(ctx),
			"backend", backend.Name,
			"error", backendResp.error.Error(),
		)
		RecordBackendGroupMulticallCompletion(bg, backend.Name, backendResp.error.Error())
	} else {
		RecordBackendGroupMulticallCompletion(bg, backend.Name, "nil")
	}
}

func (bg *BackendGroup) ProcessMulticallResponses(ch chan *multicallTuple, ctx context.Context) *BackendGroupRPCResponse {
	var finalResp *BackendGroupRPCResponse
	i := 0
	for {
		multicallResp, ok := <-ch
		if !ok {
			log.Trace("multicall response channel closed",
				"req_id", GetReqID(ctx),
				"auth", GetAuthCtx(ctx),
				"response_count", i,
			)
			if i > 0 {
				return finalResp
			}
			return &BackendGroupRPCResponse{
				RPCRes:   nil,
				ServedBy: "",
				error:    errors.New("no multicall response received"),
			}
		}

		i++
		resp := multicallResp.response
		backendName := multicallResp.backendName

		if resp.error != nil {
			log.Error("received error response from multicall channel",
				"req_id", GetReqID(ctx),
				"auth", GetAuthCtx(ctx),
				"err", resp.error,
				"backend", backendName,
			)
			finalResp = resp
			continue
		}
		log.Info("received successful response from multicall channel",
			"req_id", GetReqID(ctx),
			"auth", GetAuthCtx(ctx),
			"served_by", resp.ServedBy,
			"backend", backendName,
		)
		return resp
	}
}

func (bg *BackendGroup) ProxyWS(ctx context.Context, clientConn *websocket.Conn, methodWhitelist *StringSet, server *Server) (*WSProxier, error) {
	for _, back := range bg.Backends {
		if back.archive {
			log.Debug(
				"skipping archive backend for WS proxy",
				"name", back.Name,
				"req_id", GetReqID(ctx),
				"auth", GetAuthCtx(ctx),
			)
			continue
		}
		proxier, err := back.ProxyWS(clientConn, methodWhitelist, server)
		if errors.Is(err, ErrBackendOffline) {
			log.Warn(
				"skipping offline backend",
				"name", back.Name,
				"req_id", GetReqID(ctx),
				"auth", GetAuthCtx(ctx),
			)
			continue
		}
		if errors.Is(err, ErrBackendOverCapacity) {
			log.Warn(
				"skipping over-capacity backend",
				"name", back.Name,
				"req_id", GetReqID(ctx),
				"auth", GetAuthCtx(ctx),
			)
			continue
		}
		if err != nil {
			log.Warn(
				"error dialing ws backend",
				"name", back.Name,
				"req_id", GetReqID(ctx),
				"auth", GetAuthCtx(ctx),
				"err", err,
			)
			continue
		}
		return proxier, nil
	}

	return nil, ErrNoBackends
}

func weightedShuffle(backends []*Backend) {
	weight := func(i int) float64 {
		return float64(backends[i].weight)
	}

	weightedshuffle.ShuffleInplace(backends, weight, nil)
}

func (bg *BackendGroup) orderedBackendsForRequest() []*Backend {
	if bg.Consensus != nil {
		return bg.loadBalancedConsensusGroup()
	} else {
		healthy := make([]*Backend, 0, len(bg.Backends))
		unhealthy := make([]*Backend, 0, len(bg.Backends))
		for _, be := range bg.Backends {
			if be.IsHealthy() {
				healthy = append(healthy, be)
			} else {
				unhealthy = append(unhealthy, be)
			}
		}
		if bg.WeightedRouting {
			weightedShuffle(healthy)
			weightedShuffle(unhealthy)
		}
		return append(healthy, unhealthy...)
	}
}

func (bg *BackendGroup) loadBalancedConsensusGroup() []*Backend {
	cg := bg.Consensus.GetConsensusGroup()

	backendsHealthy := make([]*Backend, 0, len(cg))
	backendsDegraded := make([]*Backend, 0, len(cg))
	// separate into healthy, degraded and unhealthy backends
	for _, be := range cg {
		// unhealthy are filtered out and not attempted
		if !be.IsHealthy() {
			continue
		}
		if be.IsDegraded() {
			backendsDegraded = append(backendsDegraded, be)
			continue
		}
		backendsHealthy = append(backendsHealthy, be)
	}

	// shuffle both slices
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	r.Shuffle(len(backendsHealthy), func(i, j int) {
		backendsHealthy[i], backendsHealthy[j] = backendsHealthy[j], backendsHealthy[i]
	})
	r.Shuffle(len(backendsDegraded), func(i, j int) {
		backendsDegraded[i], backendsDegraded[j] = backendsDegraded[j], backendsDegraded[i]
	})

	if bg.WeightedRouting {
		weightedShuffle(backendsHealthy)
	}

	// healthy are put into a priority position
	// degraded backends are used as fallback
	backendsHealthy = append(backendsHealthy, backendsDegraded...)

	return backendsHealthy
}

func (bg *BackendGroup) Shutdown() {
	if bg.Consensus != nil {
		bg.Consensus.Shutdown()
	}
}

func calcBackoff(i int) time.Duration {
	jitter := float64(rand.Int63n(250))
	ms := math.Min(math.Pow(2, float64(i))*1000+jitter, 3000)
	return time.Duration(ms) * time.Millisecond
}

type WSProxier struct {
	backend         *Backend
	clientConn      *websocket.Conn
	clientConnMu    sync.Mutex
	backendConn     *websocket.Conn
	backendConnMu   sync.Mutex
	methodWhitelist *StringSet
	readTimeout     time.Duration
	writeTimeout    time.Duration
	server          *Server
}

func NewWSProxier(backend *Backend, clientConn, backendConn *websocket.Conn, methodWhitelist *StringSet, server *Server) *WSProxier {
	return &WSProxier{
		backend:         backend,
		clientConn:      clientConn,
		backendConn:     backendConn,
		methodWhitelist: methodWhitelist,
		readTimeout:     defaultWSReadTimeout,
		writeTimeout:    defaultWSWriteTimeout,
		server:          server,
	}
}

func (w *WSProxier) Proxy(ctx context.Context) error {
	errC := make(chan error, 2)
	go w.clientPump(ctx, errC)
	go w.backendPump(ctx, errC)
	err := <-errC
	w.close()
	return err
}

func (w *WSProxier) clientPump(ctx context.Context, errC chan error) {
	for {
		// Block until we get a message.
		msgType, msg, err := w.clientConn.ReadMessage()
		if err != nil {
			if err := w.writeBackendConn(websocket.CloseMessage, formatWSError(err)); err != nil {
				log.Error("error writing backendConn message", "err", err)
				errC <- err
				return
			}
		}

		RecordWSMessage(ctx, w.backend.Name, SourceClient)

		// Route control messages to the backend. These don't
		// count towards the total RPC requests count.
		if msgType != websocket.TextMessage && msgType != websocket.BinaryMessage {
			err := w.writeBackendConn(msgType, msg)
			if err != nil {
				errC <- err
				return
			}
			continue
		}

		rpcRequestsTotal.Inc()

		// Don't bother sending invalid requests to the backend,
		// just handle them here.
		req, err := w.prepareClientMsg(msg)
		if err != nil {
			var id json.RawMessage
			method := MethodUnknown
			if req != nil {
				id = req.ID
				method = req.Method
			}
			log.Info(
				"error preparing client message",
				"auth", GetAuthCtx(ctx),
				"req_id", GetReqID(ctx),
				"err", err,
			)
			msg = mustMarshalJSON(NewRPCErrorRes(id, err))
			RecordRPCError(ctx, BackendProxyd, method, err)

			// Send error response to client
			err = w.writeClientConn(msgType, msg)
			if err != nil {
				errC <- err
				return
			}
			continue
		}

		// Log WebSocket request information
		w.server.LogRequestInfo(ctx, req, "websocket")

		// Log transaction details if from/to matches a watched address
		w.server.LogWatchedAddressTransaction(ctx, req)

		// Send eth_accounts requests directly to the client
		if req.Method == "eth_accounts" {
			msg = mustMarshalJSON(NewRPCRes(req.ID, emptyArrayResponse))
			RecordRPCForward(ctx, BackendProxyd, "eth_accounts", RPCRequestSourceWS)
			err = w.writeClientConn(msgType, msg)
			if err != nil {
				errC <- err
				return
			}
			continue
		}

		// Check for sanctioned addresses
		if err := w.server.CheckSanctionedAddresses(ctx, req); err != nil {
			log.Info(
				"request involves sanctioned address",
				"auth", GetAuthCtx(ctx),
				"req_id", GetReqID(ctx),
				"err", err,
			)
			msg = mustMarshalJSON(NewRPCErrorRes(req.ID, err))
			RecordRPCError(ctx, BackendProxyd, req.Method, err)
			err = w.writeClientConn(msgType, msg)
			if err != nil {
				errC <- err
				return
			}
			continue
		}

		RecordRPCForward(ctx, w.backend.Name, req.Method, RPCRequestSourceWS)
		log.Info(
			"forwarded WS message to backend",
			"method", req.Method,
			"auth", GetAuthCtx(ctx),
			"req_id", GetReqID(ctx),
		)

		err = w.writeBackendConn(msgType, msg)
		if err != nil {
			errC <- err
			return
		}
	}
}

func (w *WSProxier) backendPump(ctx context.Context, errC chan error) {
	for {
		// Block until we get a message.
		msgType, msg, err := w.backendConn.ReadMessage()
		if err != nil {
			if err := w.writeClientConn(websocket.CloseMessage, formatWSError(err)); err != nil {
				log.Error("error writing clientConn message", "err", err)
				errC <- err
				return
			}
		}

		RecordWSMessage(ctx, w.backend.Name, SourceBackend)

		// Route control messages directly to the client.
		if msgType != websocket.TextMessage && msgType != websocket.BinaryMessage {
			err := w.writeClientConn(msgType, msg)
			if err != nil {
				errC <- err
				return
			}
			continue
		}

		res, err := w.parseBackendMsg(msg)
		if err != nil {
			var id json.RawMessage
			if res != nil {
				id = res.ID
			}
			msg = mustMarshalJSON(NewRPCErrorRes(id, err))
			log.Info("backend responded with error", "err", err)
		} else {
			if res.IsError() {
				log.Info(
					"backend responded with RPC error",
					"code", res.Error.Code,
					"msg", res.Error.Message,
					"source", "ws",
					"auth", GetAuthCtx(ctx),
					"req_id", GetReqID(ctx),
				)
				RecordRPCError(ctx, w.backend.Name, MethodUnknown, res.Error)
			} else {
				log.Info(
					"forwarded WS message to client",
					"auth", GetAuthCtx(ctx),
					"req_id", GetReqID(ctx),
				)
			}
		}

		err = w.writeClientConn(msgType, msg)
		if err != nil {
			errC <- err
			return
		}
	}
}

func (w *WSProxier) close() {
	w.clientConn.Close()
	w.backendConn.Close()
	activeBackendWsConnsGauge.WithLabelValues(w.backend.Name).Dec()
}

func (w *WSProxier) prepareClientMsg(msg []byte) (*RPCReq, error) {
	req, err := ParseRPCReq(msg)
	if err != nil {
		return nil, err
	}

	if !w.methodWhitelist.Has(req.Method) {
		return req, ErrMethodNotWhitelisted
	}

	return req, nil
}

func (w *WSProxier) parseBackendMsg(msg []byte) (*RPCRes, error) {
	res, err := ParseRPCRes(bytes.NewReader(msg))
	if err != nil {
		log.Warn("error parsing RPC response", "source", "ws", "err", err)
		return res, ErrBackendBadResponse
	}
	return res, nil
}

func (w *WSProxier) writeClientConn(msgType int, msg []byte) error {
	w.clientConnMu.Lock()
	defer w.clientConnMu.Unlock()
	if err := w.clientConn.SetWriteDeadline(time.Now().Add(w.writeTimeout)); err != nil {
		log.Error("ws client write timeout", "err", err)
		return err
	}
	err := w.clientConn.WriteMessage(msgType, msg)
	return err
}

func (w *WSProxier) writeBackendConn(msgType int, msg []byte) error {
	w.backendConnMu.Lock()
	defer w.backendConnMu.Unlock()
	if err := w.backendConn.SetWriteDeadline(time.Now().Add(w.writeTimeout)); err != nil {
		log.Error("ws backend write timeout", "err", err)
		return err
	}
	err := w.backendConn.WriteMessage(msgType, msg)
	return err
}

func mustMarshalJSON(in interface{}) []byte {
	out, err := json.Marshal(in)
	if err != nil {
		panic(err)
	}
	return out
}

func formatWSError(err error) []byte {
	m := websocket.FormatCloseMessage(websocket.CloseNormalClosure, fmt.Sprintf("%v", err))
	if e, ok := err.(*websocket.CloseError); ok {
		if e.Code != websocket.CloseNoStatusReceived {
			m = websocket.FormatCloseMessage(e.Code, e.Text)
		}
	}
	return m
}

func sleepContext(ctx context.Context, duration time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(duration):
	}
}

type LimitedHTTPClient struct {
	http.Client
	sem         *semaphore.Weighted
	backendName string
}

func (c *LimitedHTTPClient) DoLimited(req *http.Request) (*http.Response, error) {
	_, span := tracer.Start(req.Context(), "DoLimitedFunction")
	defer span.End()

	if err := c.sem.Acquire(req.Context(), 1); err != nil {
		tooManyRequestErrorsTotal.WithLabelValues(c.backendName).Inc()
		return nil, wrapErr(err, "too many requests")
	}
	defer c.sem.Release(1)
	return c.Do(req)
}

func RecordBatchRPCError(ctx context.Context, backendName string, reqs []*RPCReq, err error) {
	for _, req := range reqs {
		RecordRPCError(ctx, backendName, req.Method, err)
	}
}

func MaybeRecordErrorsInRPCRes(ctx context.Context, backendName string, reqs []*RPCReq, resBatch []*RPCRes) {
	log.Debug("forwarded RPC request",
		"backend", backendName,
		"auth", GetAuthCtx(ctx),
		"req_id", GetReqID(ctx),
		"batch_size", len(reqs),
	)

	var lastError *RPCErr
	for i, res := range resBatch {
		if res.IsError() {
			lastError = res.Error
			RecordRPCError(ctx, backendName, reqs[i].Method, res.Error)
		}
	}

	if lastError != nil {
		log.Info(
			"backend responded with RPC error",
			"backend", backendName,
			"last_error_code", lastError.Code,
			"last_error_msg", lastError.Message,
			"req_id", GetReqID(ctx),
			"source", "rpc",
			"auth", GetAuthCtx(ctx),
		)
	}
}

func RecordBatchRPCForward(ctx context.Context, backendName string, reqs []*RPCReq, source string) {
	for _, req := range reqs {
		RecordRPCForward(ctx, backendName, req.Method, source)
	}
}

func (b *Backend) ClearSlidingWindows() {
	b.intermittentErrorsSlidingWindow.Clear()
	b.networkRequestsSlidingWindow.Clear()
}

func stripXFF(xff string) string {
	ipList := strings.Split(xff, ",")
	return strings.TrimSpace(ipList[0])
}

type BackendGroupRPCResponse struct {
	RPCRes   []*RPCRes
	ServedBy string
	error    error
}

func (bg *BackendGroup) ForwardRequestToBackendGroup(
	rpcReqs []*RPCReq,
	backends []*Backend,
	ctx context.Context,
	isBatch bool,
) *BackendGroupRPCResponse {
	var lastArchiveRequiredResponse *BackendGroupRPCResponse

	for _, back := range backends {
		res := make([]*RPCRes, 0)
		var err error

		servedBy := fmt.Sprintf("%s/%s", bg.Name, back.Name)

		if len(rpcReqs) > 0 {

			res, err = back.Forward(ctx, rpcReqs, isBatch)

			if errors.Is(err, ErrConsensusGetReceiptsCantBeBatched) ||
				errors.Is(err, ErrConsensusGetReceiptsInvalidTarget) ||
				errors.Is(err, ErrMethodNotWhitelisted) {
				return &BackendGroupRPCResponse{
					RPCRes:   nil,
					ServedBy: "",
					error:    err,
				}
			}
			if errors.Is(err, ErrBackendResponseTooLarge) {
				return &BackendGroupRPCResponse{
					RPCRes:   nil,
					ServedBy: "",
					error:    err,
				}
			}
			if errors.Is(err, ErrBackendOffline) {
				log.Warn(
					"skipping offline backend",
					"name", back.Name,
					"auth", GetAuthCtx(ctx),
					"req_id", GetReqID(ctx),
				)
				continue
			}
			if errors.Is(err, ErrBackendOverCapacity) {
				log.Warn(
					"skipping over-capacity backend",
					"name", back.Name,
					"auth", GetAuthCtx(ctx),
					"req_id", GetReqID(ctx),
				)
				continue
			}
			if err != nil {
				log.Error(
					"error forwarding request to backend",
					"name", back.Name,
					"req_id", GetReqID(ctx),
					"auth", GetAuthCtx(ctx),
					"err", err,
				)
				continue
			}
		}

		// Check if any response contains an error indicating archive data is required
		if containsArchiveRequiredError(res) {
			log.Warn(
				"backend returned error requiring archive data, will try next backend",
				"name", back.Name,
				"req_id", GetReqID(ctx),
				"auth", GetAuthCtx(ctx),
			)

			// Store this response in case we need to return it later
			lastArchiveRequiredResponse = &BackendGroupRPCResponse{
				RPCRes:   res,
				ServedBy: servedBy,
				error:    nil,
			}

			// Archive-required errors should try other backends
			// Archive nodes may have different retention policies
			continue
		}

		return &BackendGroupRPCResponse{
			RPCRes:   res,
			ServedBy: servedBy,
			error:    nil,
		}
	}

	// If we have an archive-required error response but no archive backend could handle it,
	// try with archive backends only
	if lastArchiveRequiredResponse != nil {
		log.Info(
			"retrying request with archive backends only due to archive-required error",
			"req_id", GetReqID(ctx),
			"auth", GetAuthCtx(ctx),
		)

		// Get archive backends
		var archiveBackends []*Backend
		for _, backend := range bg.Backends {
			if backend.archive {
				archiveBackends = append(archiveBackends, backend)
			}
		}

		// Try archive backends
		for _, back := range archiveBackends {
			res := make([]*RPCRes, 0)
			var err error

			servedBy := fmt.Sprintf("%s/%s", bg.Name, back.Name)

			if len(rpcReqs) > 0 {
				res, err = back.Forward(ctx, rpcReqs, isBatch)

				if errors.Is(err, ErrBackendOffline) {
					log.Warn(
						"skipping offline archive backend",
						"name", back.Name,
						"auth", GetAuthCtx(ctx),
						"req_id", GetReqID(ctx),
					)
					continue
				}
				if errors.Is(err, ErrBackendOverCapacity) {
					log.Warn(
						"skipping over-capacity archive backend",
						"name", back.Name,
						"auth", GetAuthCtx(ctx),
						"req_id", GetReqID(ctx),
					)
					continue
				}
				if err != nil {
					log.Error(
						"error forwarding request to archive backend",
						"name", back.Name,
						"req_id", GetReqID(ctx),
						"auth", GetAuthCtx(ctx),
						"err", err,
					)
					continue
				}
			}

			// Check if this archive backend also returned an archive-required error
			if containsArchiveRequiredError(res) {
				log.Warn(
					"archive backend also returned archive-required error, trying next archive backend",
					"name", back.Name,
					"req_id", GetReqID(ctx),
					"auth", GetAuthCtx(ctx),
				)
				continue
			}

			log.Info(
				"successfully served request with archive backend after archive-required error",
				"name", back.Name,
				"req_id", GetReqID(ctx),
				"auth", GetAuthCtx(ctx),
			)

			// Record successful retry
			RecordMissingTrieNodeRetry(ctx, bg.Name, true)

			return &BackendGroupRPCResponse{
				RPCRes:   res,
				ServedBy: servedBy,
				error:    nil,
			}
		}

		// If no archive backend worked, return the original archive-required error response
		log.Warn(
			"no archive backend available to retry archive-required error",
			"req_id", GetReqID(ctx),
			"auth", GetAuthCtx(ctx),
		)

		// Record failed retry
		RecordMissingTrieNodeRetry(ctx, bg.Name, false)

		return lastArchiveRequiredResponse
	}

	RecordUnserviceableRequest(ctx, RPCRequestSourceHTTP)
	return &BackendGroupRPCResponse{
		RPCRes:   nil,
		ServedBy: "",
		error:    ErrNoBackends,
	}

}

func OverrideResponses(res []*RPCRes, overriddenResponses []*indexedReqRes) []*RPCRes {
	for _, ov := range overriddenResponses {
		if len(res) > 0 {
			// insert ov.res at position ov.index
			res = append(res[:ov.index], append([]*RPCRes{ov.res}, res[ov.index:]...)...)
		} else {
			res = append(res, ov.res)
		}
	}
	return res
}

func (bg *BackendGroup) OverwriteConsensusResponses(rpcReqs []*RPCReq, overriddenResponses []*indexedReqRes, rewrittenReqs []*RPCReq) ([]*RPCReq, []*indexedReqRes) {
	rctx := RewriteContext{
		latest:        bg.Consensus.GetLatestBlockNumber(),
		safe:          bg.Consensus.GetSafeBlockNumber(),
		finalized:     bg.Consensus.GetFinalizedBlockNumber(),
		maxBlockRange: bg.Consensus.maxBlockRange,
	}

	for i, req := range rpcReqs {
		res := RPCRes{JSONRPC: JSONRPCVersion, ID: req.ID}
		result, err := RewriteTags(rctx, req, &res, bg.SkipEIP1898)
		switch result {
		case RewriteOverrideError:
			overriddenResponses = append(overriddenResponses, &indexedReqRes{
				index: i,
				req:   req,
				res:   &res,
			})
			if errors.Is(err, ErrRewriteBlockOutOfRange) {
				res.Error = ErrBlockOutOfRange
			} else if errors.Is(err, ErrRewriteRangeTooLarge) {
				res.Error = ErrInvalidParams(
					fmt.Sprintf("block range greater than %d max", rctx.maxBlockRange),
				)
			} else {
				res.Error = ErrParseErr
			}
		case RewriteOverrideResponse:
			overriddenResponses = append(overriddenResponses, &indexedReqRes{
				index: i,
				req:   req,
				res:   &res,
			})
		case RewriteOverrideRequest, RewriteNone:
			rewrittenReqs = append(rewrittenReqs, req)
		}
	}
	return rewrittenReqs, overriddenResponses
}

// extractBlockParameter extracts a block parameter string from various formats
func extractBlockParameter(param interface{}) string {
	// Direct string parameter
	if blockParamStr, ok := param.(string); ok {
		return blockParamStr
	}

	// Object parameter with blockNumber field
	if blockParamMap, ok := param.(map[string]interface{}); ok {
		if blockNumber, exists := blockParamMap["blockNumber"]; exists {
			if blockNumStr, ok := blockNumber.(string); ok {
				return blockNumStr
			}
		}
	}

	return ""
}

// requiresArchiveForBlock determines if a block parameter requires an archive node.
// archiveBlockThreshold specifies how many blocks from head are considered "old" enough
// to require archive data. If 0, defaults to blocksInStateFullNode (128).
func requiresArchiveForBlock(blockParam string, latestBlockNumber hexutil.Uint64, archiveBlockThreshold uint64) bool {
	if blockParam == "earliest" {
		return true
	}
	if blockParam == "pending" || blockParam == "latest" {
		return false
	}
	if strings.HasPrefix(blockParam, "0x") {
		blockNum, ok := new(big.Int).SetString(blockParam[2:], 16)
		if !ok {
			return false // invalid hex
		}
		threshold := archiveBlockThreshold
		if threshold == 0 {
			threshold = blocksInStateFullNode
		}
		latestBlock := uint64(latestBlockNumber)
		if latestBlock > 0 && blockNum.Uint64() <= latestBlock-threshold {
			return true
		}
	}
	return false
}

// containsArchiveRequiredError checks if any RPC response contains an error that indicates
// archive data is needed. This includes:
// - "missing trie node" - node doesn't have the historical state
// - "old data not available due to pruning" - data has been pruned
// - "root hash mismatch witnessTrieRootHash" - witness trie root hash mismatch
// - "no transactions snapshot file" - backend doesn't have transaction snapshot for the block
func containsArchiveRequiredError(responses []*RPCRes) bool {
	for _, res := range responses {
		if res != nil && res.IsError() && res.Error != nil {
			// Check both the message and data fields for errors requiring archive data
			if strings.Contains(res.Error.Message, "missing trie node") ||
				strings.Contains(res.Error.Data, "missing trie node") ||
				strings.Contains(res.Error.Message, "old data not available due to pruning") ||
				strings.Contains(res.Error.Data, "old data not available due to pruning") ||
				strings.Contains(res.Error.Message, "root hash mismatch witnessTrieRootHash") ||
				strings.Contains(res.Error.Data, "root hash mismatch witnessTrieRootHash") ||
				strings.Contains(res.Error.Message, "distance to target block exceeds maximum proof window") ||
				strings.Contains(res.Error.Data, "distance to target block exceeds maximum proof window") ||
				strings.Contains(res.Error.Message, "No state available for block") ||
				strings.Contains(res.Error.Data, "No state available for block") ||
				strings.Contains(res.Error.Message, "block number is in the future") ||
				strings.Contains(res.Error.Data, "block number is in the future") ||
				strings.Contains(res.Error.Message, "data before") && strings.Contains(res.Error.Message, "not available") ||
				strings.Contains(res.Error.Data, "data before") && strings.Contains(res.Error.Data, "not available") ||
				strings.Contains(res.Error.Message, "state at block") && strings.Contains(res.Error.Message, "is pruned") ||
				strings.Contains(res.Error.Data, "state at block") && strings.Contains(res.Error.Data, "is pruned") ||
				strings.Contains(res.Error.Message, "got 0 receipts") && strings.Contains(res.Error.Message, "expected") ||
				strings.Contains(res.Error.Data, "got 0 receipts") && strings.Contains(res.Error.Data, "expected") ||
				strings.Contains(res.Error.Message, "received 0 receipts") && strings.Contains(res.Error.Message, "expected") ||
				strings.Contains(res.Error.Data, "received 0 receipts") && strings.Contains(res.Error.Data, "expected") ||
				strings.Contains(res.Error.Message, "no transactions snapshot file") ||
				strings.Contains(res.Error.Data, "no transactions snapshot file") {
				return true
			}
		}
	}
	return false
}
