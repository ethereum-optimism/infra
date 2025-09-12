package proxyd

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	supervisorTypes "github.com/ethereum-optimism/optimism/op-supervisor/supervisor/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types/interoptypes"
	"github.com/ethereum/go-ethereum/eth/interop"
	"github.com/ethereum/go-ethereum/log"
)

type InteropStrategy interface {
	ValidateAccessList(ctx context.Context, interopAccessList []common.Hash) error
}

type commonInteropStrategy struct {
	urls                                    []string
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
		return nil, false, supervisorTypes.ErrNoRPCSource
	}
	var err error
	if s.validateAndDeduplicateInteropAccessList {
		interopAccessList, err = validateAndDeduplicateInteropAccessList(interopAccessList)
		if err != nil {
			log.Error("error validating and deduplicating interop access list", "req_id", GetReqID(ctx), "error", err)
			rpcErr := ParseInteropError(fmt.Errorf("failed to parse access list entries: %w", err))
			rpcErr.HTTPErrorCode = 400
			rpcErr.Code = JSONRPCErrorInvalidParams
			return nil, false, rpcErr
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
	_, _, err = performCheckAccessListOp(ctx, accessListToValidate, firstSupervisorUrl)
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
			_, _, err := performCheckAccessListOp(ctx, accessListToValidate, url)
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

		httpCode, err := backend.Validate(ctx, accessListToValidate)
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

func (b *healthAwareBackend) Validate(ctx context.Context, accessList []common.Hash) (int, error) {
	httpCode, _, err := performCheckAccessListOp(ctx, accessList, b.url)
	if err != nil {
		return httpCode, ParseInteropError(err)
	}
	return httpCode, nil
}

func performCheckAccessListOp(ctx context.Context, accessList []common.Hash, url string) (int, string, error) {
	validatingBackend := interop.NewInteropClient(url)
	err := validatingBackend.CheckAccessList(ctx, accessList, interoptypes.CrossUnsafe, interoptypes.ExecutingDescriptor{
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
