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

// TxFilterModule is a single check in the submission filter pipeline. Apply
// returns nil to accept (allowing the pipeline to continue) or an error to
// reject the whole submission. Each module owns its own failure policy.
type TxFilterModule interface {
	Name() string
	Apply(ctx context.Context, sub *TxSubmission) error
}

// TxFilter is the single chokepoint for transaction-submission filtering. It
// owns an ordered list of modules and runs them in order, short-circuiting on
// the first rejection (logical AND).
type TxFilter struct {
	srv     *Server
	modules []TxFilterModule
}

func NewTxFilter(srv *Server, modules ...TxFilterModule) *TxFilter {
	return &TxFilter{srv: srv, modules: modules}
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
//   - single-tx methods reuse convertSendReqToSendTx
//   - eth_sendBundle reuses transactionsFromBundleReq
//
// It returns the same decode errors those return today and enforces
// maxBundleTransactions here so the cap applies to every bundle regardless of
// middleware enablement.
func (f *TxFilter) Build(ctx context.Context, req *RPCReq, bypassRateLimit bool) (*TxSubmission, error) {
	var txs []*types.Transaction
	switch req.Method {
	case "eth_sendRawTransaction", "eth_sendRawTransactionConditional":
		tx, err := f.srv.convertSendReqToSendTx(ctx, req)
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
