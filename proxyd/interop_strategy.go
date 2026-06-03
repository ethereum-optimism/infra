package proxyd

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	interopErrors "github.com/ethereum-optimism/optimism/op-core/interop"
	"github.com/ethereum-optimism/optimism/op-core/interop/messages"
	"github.com/ethereum-optimism/optimism/op-service/client"
	"github.com/ethereum-optimism/optimism/op-service/eth"
	"github.com/ethereum-optimism/optimism/op-service/eth/safety"
	"github.com/ethereum-optimism/optimism/op-service/sources"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rpc"
)

type InteropStrategy interface {
	ValidateAccessList(ctx context.Context, interopAccessList []common.Hash) error
}

type commonInteropStrategy struct {
	urls                                    []string
	chainID                                 eth.ChainID
	accessListSizeLimit                     int
	reqSizeLimit                            int
	validateAndDeduplicateInteropAccessList bool
	skipOnNoSupervisorBackend               bool
}

func NewCommonInteropStrategy(urls []string, opts ...commonStrategyOpt) *commonInteropStrategy {
	c := &commonInteropStrategy{
		urls:                                    urls,
		accessListSizeLimit:                     0,
		reqSizeLimit:                            0,
		validateAndDeduplicateInteropAccessList: true,
		skipOnNoSupervisorBackend:               false,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

func CommonStrategyOpts(opts ...commonStrategyOpt) []commonStrategyOpt {
	return opts
}

type commonStrategyOpt func(*commonInteropStrategy)

var WithReqSizeLimit = func(reqSizeLimit int) commonStrategyOpt {
	return func(s *commonInteropStrategy) {
		s.reqSizeLimit = reqSizeLimit
	}
}

var WithAccessListSizeLimit = func(accessListSizeLimit int) commonStrategyOpt {
	return func(s *commonInteropStrategy) {
		s.accessListSizeLimit = accessListSizeLimit
	}
}

var WithValidateAndDeduplicateInteropAccessList = func(validateAndDeduplicateInteropAccessList bool) commonStrategyOpt {
	return func(s *commonInteropStrategy) {
		s.validateAndDeduplicateInteropAccessList = validateAndDeduplicateInteropAccessList
	}
}

var WithSkipOnNoSupervisorBackend = func(skipOnNoSupervisorBackend bool) commonStrategyOpt {
	return func(s *commonInteropStrategy) {
		s.skipOnNoSupervisorBackend = skipOnNoSupervisorBackend
	}
}

var WithChainID = func(chainID uint64) commonStrategyOpt {
	return func(s *commonInteropStrategy) {
		s.chainID = eth.ChainIDFromUInt64(chainID)
	}
}

func (s *commonInteropStrategy) preflightChecksAndCleanupAccessList(ctx context.Context, interopAccessList []common.Hash) ([]common.Hash, bool, error) {
	if len(s.urls) == 0 {
		if s.skipOnNoSupervisorBackend {
			log.Info(
				"no validating backends found for an interop transaction, skipping",
				"req_id", GetReqID(ctx),
				"method", "eth_sendRawTransaction",
			)
			return nil, false, nil
		}
		log.Error(
			"no validating backends found for an interop transaction",
			"req_id", GetReqID(ctx),
			"method", "eth_sendRawTransaction",
		)
		return nil, false, interopErrors.ErrNoRPCSource
	}
	var err error
	if s.validateAndDeduplicateInteropAccessList {
		interopAccessList, err = validateAndDeduplicateInteropAccessList(interopAccessList)
		if err != nil {
			log.Error("error validating and deduplicating interop access list", "req_id", GetReqID(ctx), "error", err)
			return nil, false, ParseInteropError(fmt.Errorf("failed to read data: %w", err))
		}
	}

	if s.accessListSizeLimit > 0 {
		if len(interopAccessList) > s.accessListSizeLimit {
			log.Error(
				"interop access list exceeds maximum size limit",
				"req_id", GetReqID(ctx),
				"size", len(interopAccessList),
				"max_size", s.accessListSizeLimit,
			)
			return nil, false, ErrInteropAccessListOutOfBounds
		}
	}

	return interopAccessList, true, nil
}

type firstSupervisorStrategyImpl struct {
	*commonInteropStrategy
}

func NewFirstSupervisorStrategy(urls []string, opts ...commonStrategyOpt) *firstSupervisorStrategyImpl {
	return &firstSupervisorStrategyImpl{
		commonInteropStrategy: NewCommonInteropStrategy(urls, opts...),
	}
}

func (s *firstSupervisorStrategyImpl) ValidateAccessList(ctx context.Context, interopAccessList []common.Hash) error {
	accessListToValidate, proceedFurther, err := s.preflightChecksAndCleanupAccessList(ctx, interopAccessList)
	if err != nil {
		return err
	}

	if !proceedFurther {
		return nil
	}

	firstSupervisorUrl := s.urls[0]

	ctx = context.WithValue(ctx, ContextKeyInteropValidationStrategy, FirstSupervisorStrategy) // nolint:staticcheck
	_, _, err = performCheckAccessListOp(ctx, accessListToValidate, firstSupervisorUrl, s.chainID)
	return err
}

type multicallStrategyImpl struct {
	*commonInteropStrategy
}

func NewMulticallStrategy(urls []string, opts ...commonStrategyOpt) *multicallStrategyImpl {
	return &multicallStrategyImpl{
		commonInteropStrategy: NewCommonInteropStrategy(urls, opts...),
	}
}

func (s *multicallStrategyImpl) ValidateAccessList(ctx context.Context, interopAccessList []common.Hash) error {
	accessListToValidate, proceedFurther, err := s.preflightChecksAndCleanupAccessList(ctx, interopAccessList)
	if err != nil {
		return err
	}

	if !proceedFurther {
		return nil
	}

	resultChan := make(chan error, len(s.urls))
	// concurrently broadcast the checkAccessList operation to all the validating backends
	var wg sync.WaitGroup
	for _, url := range s.urls {
		wg.Add(1)
		go func(ctx context.Context, url string) {
			defer wg.Done()
			_, _, err := performCheckAccessListOp(ctx, accessListToValidate, url, s.chainID)
			resultChan <- err
		}(ctx, url)
	}

	// goroutine which waits for all the sender goroutines created above to be done, and drain and close the resultChan
	go func() {
		wg.Wait()
		log.Debug(
			"all interop validating backends have responded",
			"source", "rpc",
			"req_id", GetReqID(ctx),
			"method", "eth_sendRawTransaction",
		)
		for range resultChan {
		} // drain the channel
		close(resultChan)
	}()

	// Success: if at least one backend responds successfully
	// Failure: the first error response if all the backends respond with an error
	var firstErr error
	for range len(s.urls) {
		err := <-resultChan
		if err == nil { // at least one success observed
			return nil
		} else if firstErr == nil { // record the first error for returning it if no validating backend succeeds
			firstErr = err
		}
	}
	return ParseInteropError(firstErr)
}

// agreementStrategyImpl fans every check out to all configured urls
// concurrently and combines the verdicts by quorum agreement. A "definitive
// verdict" is either a successful validation (valid) or a known supervisor
// validation rejection (invalid); transport errors, timeouts, 5xx and
// cancellations are non-responses and never count.
//
// Failsafe is a hard, short-circuit rejection: if ANY received response reports
// failsafe enabled, the message is rejected, even when the minimum number of
// accepts is already in hand. A single endpoint asserting failsafe is enough to
// refuse the message even if every other endpoint would have accepted it.
//
// The strategy fast-accepts at the minimum: it decides as soon as minResponses
// definitive verdicts arrive and does not await slow or unresponded endpoints. A
// failsafe verdict, whenever received, short-circuits to a reject. Before
// returning an accept it performs a non-blocking drain of any responses already
// buffered, so a failsafe that has already arrived beats a met quorum; it never
// blocks on an endpoint that has not yet responded.
//
// Decision once the quorum is met: all-valid accepts, all-invalid rejects with
// the real rejection error, a mix logs an error and rejects. Fewer than
// minResponses definitive verdicts fails closed (quorum-not-reached).
//
// Disagreement detection is best-effort: breaking at minResponses means a slow
// dissenter past the quorum is never observed. Guaranteed detection would need
// an all-wait audit mode, which is out of scope.
type agreementStrategyImpl struct {
	*commonInteropStrategy
	minResponses int
}

var _ InteropStrategy = (*agreementStrategyImpl)(nil)

func NewAgreementStrategy(urls []string, minResponses int, opts ...commonStrategyOpt) *agreementStrategyImpl {
	return &agreementStrategyImpl{
		commonInteropStrategy: NewCommonInteropStrategy(urls, opts...),
		minResponses:          minResponses,
	}
}

func (s *agreementStrategyImpl) ValidateAccessList(ctx context.Context, interopAccessList []common.Hash) error {
	accessListToValidate, proceedFurther, err := s.preflightChecksAndCleanupAccessList(ctx, interopAccessList)
	if err != nil {
		return err
	}

	if !proceedFurther {
		return nil
	}

	ctx = context.WithValue(ctx, ContextKeyInteropValidationStrategy, AgreementStrategy) // nolint:staticcheck

	type verdict struct {
		valid    bool
		failsafe bool
		err      error
	}

	// Buffered to len(urls) so every goroutine can always write its verdict and
	// exit even after we break early, avoiding goroutine leaks.
	results := make(chan verdict, len(s.urls))

	// Cancelled on break to abort in-flight requests to endpoints that have not
	// yet responded once the quorum is reached or failsafe short-circuits.
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()

	for _, url := range s.urls {
		go func(url string) {
			_, _, e := performCheckAccessListOp(cctx, accessListToValidate, url, s.chainID)
			switch {
			case e == nil:
				results <- verdict{valid: true}
			case isFailsafeError(e):
				results <- verdict{failsafe: true, err: e}
			case isDefinitiveInteropRejection(e):
				results <- verdict{valid: false, err: e}
			default:
				results <- verdict{} // non-response: transport/timeout/5xx/cancel
			}
		}(url)
	}

	rejectFailsafe := func(valid, invalid int, err error) error {
		cancel()
		log.Error(
			"interop endpoint reported failsafe enabled; rejecting",
			"req_id", GetReqID(ctx),
		)
		recordAgreementOutcome(ctx, valid, invalid, s.minResponses, true)
		return ParseInteropError(err)
	}

	// Read verdicts as they arrive, breaking once minResponses definitive
	// verdicts are in. A failsafe verdict short-circuits to a reject. Slow or
	// unresponded endpoints are never awaited past the quorum.
	var valid, invalid int
	var firstInvalid error
	for range s.urls {
		v := <-results
		switch {
		case v.failsafe:
			return rejectFailsafe(valid, invalid, v.err)
		case v.valid:
			valid++
		case v.err != nil:
			invalid++
			if firstInvalid == nil {
				firstInvalid = v.err
			}
		default:
			continue // non-response; keep waiting for definitive verdicts
		}
		if valid+invalid >= s.minResponses {
			break
		}
	}

	if valid+invalid < s.minResponses {
		recordAgreementOutcome(ctx, valid, invalid, s.minResponses, false)
		return ParseInteropError(fmt.Errorf("interop quorum not reached: %d definitive responses, %d required", valid+invalid, s.minResponses))
	}

	switch {
	case invalid == 0:
		// About to accept: non-blocking drain of any responses already buffered so
		// a failsafe that has already arrived beats the met quorum. This consumes
		// only what is already buffered and never blocks on an endpoint that has
		// not yet responded.
		for {
			select {
			case v := <-results:
				if v.failsafe {
					return rejectFailsafe(valid, invalid, v.err)
				}
			default:
				recordAgreementOutcome(ctx, valid, invalid, s.minResponses, false)
				return nil
			}
		}
	case valid == 0:
		recordAgreementOutcome(ctx, valid, invalid, s.minResponses, false)
		return firstInvalid // all reachable endpoints agree the tx is invalid
	default:
		log.Error(
			"interop endpoints disagreed; rejecting",
			"req_id", GetReqID(ctx),
			"valid", valid,
			"invalid", invalid,
		)
		recordAgreementOutcome(ctx, valid, invalid, s.minResponses, false)
		return firstInvalid
	}
}

// isFailsafeError reports whether err is a supervisor failsafe rejection.
// op-interop-filter emits the dedicated code -320602 for failsafe, so detection
// keys on the code and is immune to message wording or HTTP status. Failsafe on
// any endpoint is a hard rejection handled before the quorum logic.
func isFailsafeError(err error) bool {
	var e *RPCErr
	return errors.As(err, &e) && e.Code == failsafeInteropRejectionCode
}

// failsafeInteropRejectionCode is the dedicated supervisor failsafe code. It is
// handled as a hard short-circuit rejection (see isFailsafeError) and is never a
// definitive verdict.
const failsafeInteropRejectionCode = -320602

// definitiveInteropRejectionCodes is the set of supervisor verdict codes that
// count as a definitive INVALID verdict for the agreement strategy. It is the
// interopRPCErrorMap codes minus the generic params fallbacks (-32602) and the
// failsafe code (-320602). Failsafe is handled separately as a hard
// short-circuit rejection (see isFailsafeError), so it never reaches this set.
// This mirrors the codes op-reth accepts as SuperchainDAError so both sides
// agree on what counts as a rejection.
var definitiveInteropRejectionCodes = buildDefinitiveRejectionSet()

func buildDefinitiveRejectionSet() map[int]struct{} {
	codes := make(map[int]struct{})
	for _, rpcErr := range interopRPCErrorMap {
		if rpcErr.Code == -32602 || rpcErr.Code == failsafeInteropRejectionCode {
			continue
		}
		codes[rpcErr.Code] = struct{}{}
	}
	return codes
}

// isDefinitiveInteropRejection reports whether err is a definitive INVALID
// verdict from a supervisor. A cancelled request yields context.Canceled / HTTP
// 499 and must never count (it is the agreement strategy's own cancellation, or
// an upstream client disconnect), so it is explicitly excluded even though it
// lives in the 4xx band.
func isDefinitiveInteropRejection(err error) bool {
	var e *RPCErr
	if !errors.As(err, &e) {
		return false
	}
	if errors.Is(err, context.Canceled) || e.HTTPErrorCode == 499 {
		return false
	}
	_, ok := definitiveInteropRejectionCodes[e.Code]
	return ok
}

type healthAwareLoadBalancingStrategyImpl struct {
	*commonInteropStrategy
	backends *loadBalancingBuffer
}

func NewHealthAwareLoadBalancingStrategy(urls []string, unhealthinessTimeout time.Duration, opts ...commonStrategyOpt) *healthAwareLoadBalancingStrategyImpl {
	s := &healthAwareLoadBalancingStrategyImpl{
		commonInteropStrategy: NewCommonInteropStrategy(urls, opts...),
		backends:              NewLoadBalancingBuffer(urls, unhealthinessTimeout),
	}
	return s
}

func (s *healthAwareLoadBalancingStrategyImpl) ValidateAccessList(ctx context.Context, interopAccessList []common.Hash) error {
	defer s.backends.NextBackend() // move to the next backend after the request is done

	accessListToValidate, proceedFurther, err := s.preflightChecksAndCleanupAccessList(ctx, interopAccessList)
	if err != nil {
		return err
	}

	if !proceedFurther {
		return nil
	}

	// retry only in case of encountering unhealthy backends
	// retries prevents an infinite loop in case all backends are unhealthy
	maxRetries := s.backends.Size()

	// NOTE: races can still happen between the backend selection and the validation, yet they're are harmless
	// The harmeless races are just a trade-off against avoiding lock contention for the duration of the entire request
	backend := s.backends.GetBackend()
	for retryCount := 0; retryCount < maxRetries; retryCount++ {
		if !backend.IsHealthy() {
			backend = s.backends.NextBackend()
			continue
		}

		httpCode, err := backend.Validate(ctx, accessListToValidate, s.chainID)
		if err == nil {
			return nil
		}

		if backendFoundUnhealthy := httpCode >= 500; backendFoundUnhealthy {
			backend.MarkUnhealthy()
			backend = s.backends.NextBackend()
			continue
		}

		// genuine validation error
		return err
	}

	// retries exhausted
	return ParseInteropError(fmt.Errorf("no healthy supervisor backends found"))
}

type healthAwareBackend struct {
	url                  string
	lastUnhealthy        time.Time
	unhealthinessTimeout time.Duration
}

func NewHealthAwareBackend(url string, unhealthinessTimeout time.Duration) *healthAwareBackend {
	b := &healthAwareBackend{
		url:                  url,
		lastUnhealthy:        time.Time{},
		unhealthinessTimeout: unhealthinessTimeout,
	}

	return b
}

func (b *healthAwareBackend) IsHealthy() bool {
	if b.lastUnhealthy.IsZero() {
		return true
	}
	if b.unhealthinessTimeout <= 0 {
		log.Warn("unhealthiness timeout is corrupted for a health-aware backend. Defaulting it to be healthy",
			"url", b.url, "unhealthiness_timeout", b.unhealthinessTimeout)
		return true
	}

	return time.Since(b.lastUnhealthy) > b.unhealthinessTimeout
}

func (b *healthAwareBackend) MarkUnhealthy() {
	b.lastUnhealthy = time.Now()
}

func (b *healthAwareBackend) Validate(ctx context.Context, accessList []common.Hash, chainID eth.ChainID) (int, error) {
	httpCode, _, err := performCheckAccessListOp(ctx, accessList, b.url, chainID)
	if err != nil {
		return httpCode, ParseInteropError(err)
	}
	return httpCode, nil
}

func performCheckAccessListOp(ctx context.Context, accessList []common.Hash, url string, chainID eth.ChainID) (int, string, error) {
	rpcClient, err := rpc.DialContext(ctx, url)
	if err != nil {
		log.Error("failed to dial interop filter backend", "url", url, "req_id", GetReqID(ctx), "err", err)
		return 0, "", fmt.Errorf("interop filter backend unavailable")
	}
	defer rpcClient.Close()

	validatingBackend := sources.NewInteropFilterClient(client.NewBaseRPCClient(rpcClient))
	err = validatingBackend.CheckAccessList(ctx, accessList, safety.CrossUnsafe, messages.ExecutingDescriptor{
		ChainID:   chainID,
		Timestamp: getInteropExecutingDescriptorTimestamp(),
	})

	var httpCode int
	var rpcErrorCode string
	if err == nil {
		httpCode = 200
		rpcErrorCode = "-"
	} else {
		interopErr := ParseInteropError(err)
		httpCode = interopErr.HTTPErrorCode
		rpcErrorCode = strconv.Itoa(interopErr.Code)

		err = interopErr
	}

	strategy, ok := ctx.Value(ContextKeyInteropValidationStrategy).(InteropValidationStrategy)
	if !ok {
		strategy = EmptyStrategy
	}

	log.Debug(
		"an interop validating backend has responded",
		"supervisor_url", url,
		"strategy", strategy,
		"req_id", GetReqID(ctx),
		"method", "eth_sendRawTransaction",
		"error", err,
	)

	rpcSupervisorChecksTotal.WithLabelValues(
		url,
		strconv.Itoa(httpCode),
		rpcErrorCode,
		string(strategy),
	).Inc()

	return httpCode, rpcErrorCode, err
}

type loadBalancingBuffer struct {
	mu                  *sync.RWMutex
	backends            []*healthAwareBackend
	currentBackendIndex int
}

func NewLoadBalancingBuffer(urls []string, unhealthinessTimeout time.Duration) *loadBalancingBuffer {
	b := &loadBalancingBuffer{
		mu:                  &sync.RWMutex{},
		backends:            make([]*healthAwareBackend, len(urls)),
		currentBackendIndex: 0,
	}

	for i, url := range urls {
		b.backends[i] = NewHealthAwareBackend(url, unhealthinessTimeout)
	}

	return b
}

func (b *loadBalancingBuffer) Size() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.backends)
}

func (b *loadBalancingBuffer) GetBackend() *healthAwareBackend {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.backends[b.currentBackendIndex]
}

func (b *loadBalancingBuffer) NextBackend() *healthAwareBackend {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.currentBackendIndex = (b.currentBackendIndex + 1) % len(b.backends)
	return b.backends[b.currentBackendIndex]
}
