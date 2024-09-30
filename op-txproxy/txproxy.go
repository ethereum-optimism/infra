package op_txproxy

import (
	"context"
	"fmt"

	"github.com/ethereum-optimism/optimism/op-service/metrics"

	"github.com/ethereum/go-ethereum/log"
	gethrpc "github.com/ethereum/go-ethereum/rpc"
)

var (
	MetricsNameSpace = "op_txproxy"
)

type TxProxy struct {
	conditionalTxService *ConditionalTxService
}

func NewTxProxy(ctx context.Context, log log.Logger, m metrics.Factory, cfg *CLIConfig) (*TxProxy, error) {
	conditionalTxService, err := NewConditionalTxService(ctx, log, m, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create conditional tx service: %w", err)
	}

	return &TxProxy{conditionalTxService}, nil
}

func (txp *TxProxy) GetAPIs() []gethrpc.API {
	return []gethrpc.API{{Namespace: "eth", Service: txp.conditionalTxService}}
}
