package proxyd

import (
	"context"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
)

// TxSubmission is the decoded form of a single transaction-submission request
// (eth_sendRawTransaction, eth_sendRawTransactionConditional, eth_sendBundle).
// The submission is decoded once and the recovered sender for each tx is
// memoized so that the chain-ID, sender-rate-limit, and interop modules can all
// share a single ecrecover per transaction.
type TxSubmission struct {
	Method          string
	Req             *RPCReq
	Txs             []*types.Transaction
	BypassRateLimit bool

	senders   []common.Address
	senderErr []error
	recovered []bool
}

// Sender returns the recovered sender for Txs[i], computing it at most once.
// Recovery uses the same signer selection as getSender (HomesteadSigner for
// chainID 0, else LatestSignerForChainID) and memoizes both value and error. A
// recovery error is returned to the caller; modules treat it as a hard reject.
func (s *TxSubmission) Sender(i int) (common.Address, error) {
	if s.recovered == nil {
		s.senders = make([]common.Address, len(s.Txs))
		s.senderErr = make([]error, len(s.Txs))
		s.recovered = make([]bool, len(s.Txs))
	}
	if !s.recovered[i] {
		s.senders[i], s.senderErr[i] = getSender(s.Txs[i])
		s.recovered[i] = true
	}
	return s.senders[i], s.senderErr[i]
}

// txDecodeFunc decodes a single-tx submission request into a transaction. It is
// injected into TxFilter so the filter does not hold a back-reference to the
// Server (Server.convertSendReqToSendTx is passed in).
type txDecodeFunc func(ctx context.Context, req *RPCReq) (*types.Transaction, error)

// TxFilterModule is a single check in the submission filter pipeline.
//
// Apply returns nil to accept (allowing the pipeline to continue) or an error
// to reject the whole submission. The contract:
//
//   - Apply runs for EVERY submission method the filter handles. Per-method
//     scoping is the module's own responsibility — see txMiddlewareModule, which
//     opts out of methods not in its configured set.
//   - Modules MUST treat sub.Txs as immutable. TxSubmission.Sender memoizes
//     ecrecover per index keyed by position in sub.Txs; mutating or reordering
//     the slice would corrupt that memo.
//   - The default failure policy is fail-CLOSED: returning an error rejects the
//     submission. Fail-open (returning nil despite an internal failure) must be
//     an explicit, operator-gated decision, as txMiddlewareModule does via its
//     failOpen flag — never a silent default.
type TxFilterModule interface {
	Name() string
	Apply(ctx context.Context, sub *TxSubmission) error
}

// TxFilter is the single chokepoint for transaction-submission filtering. It
// owns an ordered list of modules and runs them in order, short-circuiting on
// the first rejection (logical AND).
type TxFilter struct {
	decode  txDecodeFunc
	modules []TxFilterModule
}

func NewTxFilter(decode txDecodeFunc, modules ...TxFilterModule) *TxFilter {
	return &TxFilter{decode: decode, modules: modules}
}

// IsSubmissionMethod reports the static set of submission methods the filter
// handles. Per-method middleware opt-out is enforced inside the middleware
// module, not here — chain-ID/rate-limit/interop always apply.
func (f *TxFilter) IsSubmissionMethod(method string) bool {
	switch method {
	case "eth_sendRawTransaction", "eth_sendRawTransactionConditional", "eth_sendBundle":
		return true
	default:
		return false
	}
}

// Build decodes a submission request into a TxSubmission, method-aware:
//   - single-tx methods use convertSendReqToSendTx
//   - eth_sendBundle uses transactionsFromBundleReq
//
// It propagates those decoders' errors and enforces maxBundleTransactions here
// so the cap applies to every bundle regardless of middleware enablement.
func (f *TxFilter) Build(ctx context.Context, req *RPCReq, bypassRateLimit bool) (*TxSubmission, error) {
	var txs []*types.Transaction
	switch req.Method {
	case "eth_sendRawTransaction", "eth_sendRawTransactionConditional":
		tx, err := f.decode(ctx, req)
		if err != nil {
			return nil, err
		}
		txs = []*types.Transaction{tx}
	case "eth_sendBundle":
		bundleTxs, err := transactionsFromBundleReq(ctx, req)
		if err != nil {
			return nil, err
		}
		txs = bundleTxs
	default:
		return nil, fmt.Errorf("not a submission method: %s", req.Method)
	}

	if len(txs) > maxBundleTransactions {
		log.Warn("bundle contains too many transactions",
			"req_id", GetReqID(ctx),
			"tx_count", len(txs),
			"max_allowed", maxBundleTransactions)
		return nil, ErrInvalidParams(fmt.Sprintf("bundle contains %d transactions, maximum allowed is %d", len(txs), maxBundleTransactions))
	}

	return &TxSubmission{
		Method:          req.Method,
		Req:             req,
		Txs:             txs,
		BypassRateLimit: bypassRateLimit,
	}, nil
}

// Apply runs every module in order, returning the first module's rejection.
func (f *TxFilter) Apply(ctx context.Context, sub *TxSubmission) error {
	for _, m := range f.modules {
		if err := m.Apply(ctx, sub); err != nil {
			return err
		}
	}
	return nil
}
