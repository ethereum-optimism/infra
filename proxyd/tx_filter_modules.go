package proxyd

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/txpool"
	"github.com/ethereum/go-ethereum/log"
)

// takeSenderLimit applies a per-sender:nonce rate limit, mapping a limiter
// error to ErrInternal and an exhausted limit to ErrOverSenderRateLimit. Shared
// by every sender limiter so their semantics stay in lockstep.
func takeSenderLimit(ctx context.Context, lim FrontendRateLimiter, from common.Address, nonce uint64) error {
	ok, err := lim.Take(ctx, fmt.Sprintf("%s:%d", from.Hex(), nonce))
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

// chainIDModule rejects any transaction whose chain ID is not in the allowed
// set. It is wired whenever allowedChainIds is non-empty, independent of either
// rate limiter (decoupling chain-ID/replay protection from the rate limiter).
type chainIDModule struct {
	allowedChainIds []*big.Int
}

func (m *chainIDModule) Name() string { return "chain_id" }

func (m *chainIDModule) Apply(ctx context.Context, sub *TxSubmission) error {
	for _, tx := range sub.Txs {
		if !isAllowedChainId(m.allowedChainIds, tx.ChainId()) {
			log.Debug("chain id is not allowed", "req_id", GetReqID(ctx))
			return txpool.ErrInvalidSender
		}
	}
	return nil
}

// senderRateLimitModule applies the per-sender:nonce rate limit. It is skipped
// for API-key-bypassed submissions.
type senderRateLimitModule struct {
	lim FrontendRateLimiter
}

func (m *senderRateLimitModule) Name() string { return "sender_rate_limit" }

func (m *senderRateLimitModule) Apply(ctx context.Context, sub *TxSubmission) error {
	if sub.BypassRateLimit {
		return nil
	}
	for i, tx := range sub.Txs {
		from, err := sub.Sender(i)
		if err != nil {
			log.Debug("could not get sender from transaction", "err", err, "req_id", GetReqID(ctx))
			return ErrInvalidParams(err.Error())
		}
		if err := takeSenderLimit(ctx, m.lim, from, tx.Nonce()); err != nil {
			return err
		}
	}
	return nil
}

// interopModule validates every transaction in a submission against the
// op-interop-filter. Each tx carrying an interop access list is rate-limited
// (via the interop sender limiter; chain-ID enforcement is the chainIDModule's
// responsibility), size-checked, and validated by the strategy. Fail-closed
// behavior lives in the strategy and the no-URL preflight check.
type interopModule struct {
	strategy         InteropStrategy
	interopSenderLim FrontendRateLimiter
	validatingCfg    InteropValidationConfig
}

func (m *interopModule) Name() string { return "interop" }

func (m *interopModule) Apply(ctx context.Context, sub *TxSubmission) error {
	for i, tx := range sub.Txs {
		interopAccessList, isInterop, err := checkInteropAndReturnAccessList(ctx, tx)
		if err != nil {
			return err
		}
		if !isInterop {
			continue
		}

		log.Info(
			"validating interop access list",
			"source", "rpc",
			"req_id", GetReqID(ctx),
			"strategy", m.validatingCfg.Strategy,
			"tx_hash", tx.Hash(),
		)

		// The interop sender limit is not bypassed by API key, unlike
		// senderRateLimitModule.
		if m.interopSenderLim != nil {
			from, err := sub.Sender(i)
			if err != nil {
				log.Debug("could not get sender from transaction", "err", err, "req_id", GetReqID(ctx))
				return ErrInvalidParams(err.Error())
			}
			if err := takeSenderLimit(ctx, m.interopSenderLim, from, tx.Nonce()); err != nil {
				return err
			}
		} else {
			log.Warn("interop sender rate limiter is not enabled, skipping", "req_id", GetReqID(ctx))
		}

		if err := reqSizeLimitCheck(ctx, tx, m.validatingCfg.ReqSizeLimit); err != nil {
			return err
		}

		finalErr := m.strategy.ValidateAccessList(ctx, interopAccessList)

		rpcInteropValidationsTotal.WithLabelValues(
			interopValidationResult(finalErr),
			interopValidationReason(finalErr),
			string(m.validatingCfg.Strategy),
		).Inc()

		if finalErr == nil {
			log.Info("interop access list validated successfully", "req_id", GetReqID(ctx), "tx_hash", tx.Hash())
		} else {
			log.Info("interop access list validation failed", "req_id", GetReqID(ctx), "tx_hash", tx.Hash(), "error", finalErr)
			return finalErr
		}
	}
	return nil
}

// txMiddlewareModule applies the configurable transaction-validation
// middleware. It honors the configurable per-method opt-out and the
// fail-open policy, delegating to validateTransactions.
type txMiddlewareModule struct {
	endpoint string
	fn       TxValidationFunc
	failOpen bool
	methods  TxValidationMethodSet
}

func (m *txMiddlewareModule) Name() string { return "tx_middleware" }

func (m *txMiddlewareModule) Apply(ctx context.Context, sub *TxSubmission) error {
	if !m.methods.Contains(sub.Method) {
		return nil
	}
	return validateTransactions(ctx, sub.Txs, m.endpoint, m.fn, m.failOpen)
}
