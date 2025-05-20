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
	Validate(ctx context.Context, req *RPCReq) error
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

func (s *commonInteropStrategy) preflightChecksToAccessList(ctx context.Context, req *RPCReq) ([]common.Hash, bool, error) {
	tx, err := convertSendReqToSendTx(ctx, req)
	if err != nil {
		return nil, false, err
	}

	if err := reqSizeLimitCheck(ctx, req, s.reqSizeLimit); err != nil {
		return nil, false, err
	}

	interopAccessList := interoptypes.TxToInteropAccessList(tx)
	if len(interopAccessList) == 0 {
		log.Debug(
			"no interop access list found, inferring the absence of executing messages and skipping interop validation",
			"source", "rpc",
			"req_id", GetReqID(ctx),
			"method", "eth_sendRawTransaction",
		)
		return nil, false, nil
	}

	// at this point, we know it's an interop transaction worthy of being validated
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

func (s *firstSupervisorStrategyImpl) Validate(ctx context.Context, req *RPCReq) error {
	accessListToValidate, proceedFurther, err := s.preflightChecksToAccessList(ctx, req)
	if err != nil {
		return err
	}

	if !proceedFurther {
		return nil
	}

	firstSupervisorUrl := s.urls[0]

	httpCode, rpcErrorCode, err := performCheckAccessListOp(ctx, accessListToValidate, firstSupervisorUrl)

	log.Debug(
		"an interop validating backend has responded",
		"supervisor_url", firstSupervisorUrl,
		"req_id", GetReqID(ctx),
		"method", "eth_sendRawTransaction",
		"error", err,
	)

	rpcSupervisorChecksTotal.WithLabelValues(
		firstSupervisorUrl,
		httpCode,
		rpcErrorCode,
		string(FirstSupervisorStrategy),
	).Inc()

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

func (s *multicallStrategyImpl) Validate(ctx context.Context, req *RPCReq) error {
	accessListToValidate, proceedFurther, err := s.preflightChecksToAccessList(ctx, req)
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
			httpCode, rpcErrorCode, err := performCheckAccessListOp(ctx, accessListToValidate, url)

			log.Debug(
				"an interop validating backend has responded",
				"supervisor_url", url,
				"req_id", GetReqID(ctx),
				"method", "eth_sendRawTransaction",
				"error", err,
			)

			rpcSupervisorChecksTotal.WithLabelValues(
				url,
				httpCode,
				rpcErrorCode,
				string(MulticallStrategy),
			).Inc()

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
			"method", req.Method,
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

func NewHealthAwareLoadBalancingStrategy(urls []string, opts ...commonStrategyOpt) *healthAwareLoadBalancingStrategyImpl {
	s := &healthAwareLoadBalancingStrategyImpl{
		commonInteropStrategy: NewCommonInteropStrategy(urls, opts...),
		backends:              NewLoadBalancingBuffer(urls),
	}
	return s
}

func (s *healthAwareLoadBalancingStrategyImpl) Validate(ctx context.Context, req *RPCReq) error {
	defer s.backends.NextBackend() // move to the next backend after the request is done

	accessListToValidate, proceedFurther, err := s.preflightChecksToAccessList(ctx, req)
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

		httpCode, err := backend.Validate(ctx, accessListToValidate, req)
		if err == nil {
			return nil
		}

		failedValidation := httpCode < 500
		if failedValidation {
			return err
		}

		// just the backend is unhealthy, so we mark it as such and try the next backend
		backend.MarkUnhealthy()
		backend = s.backends.NextBackend()
	}

	// retries exhausted
	return ParseInteropError(fmt.Errorf("no healthy supervisor backends found"))
}

type healthAwareBackend struct {
	url           string
	lastUnhealthy time.Time
}

func NewHealthAwareBackend(url string) *healthAwareBackend {
	return &healthAwareBackend{
		url:           url,
		lastUnhealthy: time.Time{},
	}
}

func (b *healthAwareBackend) IsHealthy() bool {
	if b.lastUnhealthy.IsZero() {
		return true
	}

	timeTenSecondsAgo := time.Now().Add(-10 * time.Second)
	return b.lastUnhealthy.Before(timeTenSecondsAgo)
}

func (b *healthAwareBackend) MarkUnhealthy() {
	b.lastUnhealthy = time.Now()
}

func (b *healthAwareBackend) Validate(ctx context.Context, accessList []common.Hash, req *RPCReq) (int, error) {
	var outputHttpCode int
	var outputErr error

	httpCode, rpcErrorCode, err := performCheckAccessListOp(ctx, accessList, b.url)
	if err != nil {
		outputErr = ParseInteropError(err)
	}

	log.Debug(
		"an interop validating backend has responded",
		"supervisor_url", b.url,
		"req_id", GetReqID(ctx),
		"method", "eth_sendRawTransaction",
		"error", err,
	)

	rpcSupervisorChecksTotal.WithLabelValues(
		b.url,
		httpCode,
		rpcErrorCode,
		string(HealthAwareLoadBalancingStrategy),
	).Inc()

	outputHttpCode, parseErr := strconv.Atoi(httpCode)
	if parseErr != nil {
		outputHttpCode = 0
	}

	return outputHttpCode, outputErr
}

func performCheckAccessListOp(ctx context.Context, accessList []common.Hash, url string) (string, string, error) {
	validatingBackend := interop.NewInteropClient(url)
	err := validatingBackend.CheckAccessList(ctx, accessList, interoptypes.CrossUnsafe, interoptypes.ExecutingDescriptor{
		Timestamp: getInteropExecutingDescriptorTimestamp(),
	})

	var httpCode, rpcErrorCode string
	if err == nil {
		httpCode = "200"
		rpcErrorCode = "-"
	} else {
		interopErr := ParseInteropError(err)
		httpCode = strconv.Itoa(interopErr.HTTPErrorCode)
		rpcErrorCode = strconv.Itoa(interopErr.Code)

		err = interopErr
	}

	return httpCode, rpcErrorCode, err
}

type loadBalancingBuffer struct {
	mu                  *sync.RWMutex
	backends            []*healthAwareBackend
	currentBackendIndex int
}

func NewLoadBalancingBuffer(urls []string) *loadBalancingBuffer {
	b := &loadBalancingBuffer{
		mu:                  &sync.RWMutex{},
		backends:            make([]*healthAwareBackend, len(urls)),
		currentBackendIndex: 0,
	}

	for i, url := range urls {
		b.backends[i] = NewHealthAwareBackend(url)
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
