package op_txproxy

import (
	"context"
	"fmt"

	"github.com/ethereum-optimism/optimism/op-service/client"
	"github.com/ethereum-optimism/optimism/op-service/metrics"
	"github.com/ethereum-optimism/optimism/op-service/predeploys"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rpc"

	"github.com/prometheus/client_golang/prometheus"

	"golang.org/x/time/rate"
)

var (
	// errs
	rateLimitErr             = &rpc.JsonError{Message: "rate limited", Code: params.TransactionConditionalCostExceededMaxErrCode}
	endpointDisabledErr      = &rpc.JsonError{Message: "endpoint disabled", Code: params.TransactionConditionalRejectedErrCode}
	entrypointSupportErr     = &rpc.JsonError{Message: "only 4337 Entrypoint contract support", Code: params.TransactionConditionalRejectedErrCode}
	failedValidationErr      = &rpc.JsonError{Message: "failed conditional validation", Code: params.TransactionConditionalRejectedErrCode}
	maxCostExceededErr       = &rpc.JsonError{Message: "max cost exceeded", Code: params.TransactionConditionalRejectedErrCode}
	missingAuthenticationErr = &rpc.JsonError{Message: "missing authentication", Code: params.TransactionConditionalRejectedErrCode}
	invalidAuthenticationErr = &rpc.JsonError{Message: "invalid authentication", Code: params.TransactionConditionalRejectedErrCode}
)

type ConditionalTxService struct {
	log log.Logger
	cfg *CLIConfig

	limiter             *rate.Limiter
	backend             client.RPC
	entrypointAddresses map[common.Address]bool

	costSummary prometheus.Summary
	requests    prometheus.Counter
	failures    *prometheus.CounterVec
}

func NewConditionalTxService(ctx context.Context, log log.Logger, m metrics.Factory, cfg *CLIConfig) (*ConditionalTxService, error) {
	rpc, err := client.NewRPC(ctx, log, cfg.SendRawTransactionConditionalBackend)
	if err != nil {
		return nil, fmt.Errorf("failed to dial backend %s: %w", cfg.SendRawTransactionConditionalBackend, err)
	}

	rpcMetrics := metrics.MakeRPCClientMetrics("backend", m)
	backend := client.NewInstrumentedRPC(rpc, &rpcMetrics)

	limiter := rate.NewLimiter(rate.Limit(cfg.SendRawTransactionConditionalRateLimit), params.TransactionConditionalMaxCost)
	entrypointAddresses := map[common.Address]bool{predeploys.EntryPoint_v060Addr: true, predeploys.EntryPoint_v070Addr: true}

	return &ConditionalTxService{
		log: log,
		cfg: cfg,

		limiter:             limiter,
		backend:             backend,
		entrypointAddresses: entrypointAddresses,

		costSummary: m.NewSummary(prometheus.SummaryOpts{
			Namespace: MetricsNameSpace,
			Name:      "txconditional_cost",
			Help:      "summary of cost observed by *accepted* conditional txs",
		}),
		requests: m.NewCounter(prometheus.CounterOpts{
			Namespace: MetricsNameSpace,
			Name:      "txconditional_requests",
			Help:      "number of conditional transaction requests",
		}),
		failures: m.NewCounterVec(prometheus.CounterOpts{
			Namespace: MetricsNameSpace,
			Name:      "txconditional_failures",
			Help:      "number of conditional transaction failures",
		}, []string{"err"}),
	}, nil
}

func (s *ConditionalTxService) SendRawTransactionConditional(ctx context.Context, txBytes hexutil.Bytes, cond types.TransactionConditional) (common.Hash, error) {
	s.requests.Inc()
	if !s.cfg.SendRawTransactionConditionalEnabled {
		s.failures.WithLabelValues("disabled").Inc()
		return common.Hash{}, endpointDisabledErr
	}

	// Ensure the request is authenticated
	authInfo := AuthFromContext(ctx)
	if authInfo == nil {
		s.failures.WithLabelValues("missing auth").Inc()
		return common.Hash{}, missingAuthenticationErr
	}
	if authInfo.Err != nil {
		s.failures.WithLabelValues("invalid auth").Inc()
		return common.Hash{}, &rpc.JsonError{
			Message: fmt.Sprintf("invalid authentication: %s", authInfo.Err),
			Code:    params.TransactionConditionalRejectedErrCode,
		}
	}

	// Handle the request. For now, we do nothing with the authenticated signer
	hash, err := s.sendCondTx(ctx, authInfo.Caller, txBytes, &cond)
	if err != nil {
		s.failures.WithLabelValues(err.Error()).Inc()
		return common.Hash{}, err
	}

	return hash, err
}

func (s *ConditionalTxService) sendCondTx(ctx context.Context, caller common.Address, txBytes hexutil.Bytes, cond *types.TransactionConditional) (common.Hash, error) {
	tx := new(types.Transaction)
	if err := tx.UnmarshalBinary(txBytes); err != nil {
		return common.Hash{}, fmt.Errorf("failed to unmarshal tx: %w", err)
	}

	txHash, cost := tx.Hash(), cond.Cost()

	// external checks (tx target, conditional cost & validation)
	if tx.To() == nil || !s.entrypointAddresses[*tx.To()] {
		return txHash, entrypointSupportErr
	}
	if err := cond.Validate(); err != nil {
		s.log.Info("failed conditional validation", "err", err, "caller", caller.String())
		return txHash, failedValidationErr
	}
	if cost > params.TransactionConditionalMaxCost {
		s.log.Info("conditional max cost exceeded", "cost", cost, "max", params.TransactionConditionalMaxCost, "caller", caller.String())
		return txHash, maxCostExceededErr
	}

	// enforce rate limit on the cost to be observed
	if err := s.limiter.WaitN(ctx, cost); err != nil {
		return txHash, rateLimitErr
	}

	s.costSummary.Observe(float64(cost))
	s.log.Info("broadcasting conditional transaction", "caller", caller.String(), "hash", txHash.String())
	return txHash, s.backend.CallContext(ctx, nil, "eth_sendRawTransactionConditional", txBytes, cond)
}
