package service

import (
	"context"
	"strings"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/ethereum-optimism/infra/op-signer/service/provider"
	oprpc "github.com/ethereum-optimism/optimism/op-service/rpc"
	"github.com/ethereum-optimism/optimism/op-service/signer"
)

type SignerService struct {
	logger   log.Logger
	config   SignerServiceConfig
	provider provider.SignatureProvider
}

func NewSignerService(logger log.Logger, config SignerServiceConfig) *SignerService {
	return NewSignerServiceWithProvider(logger, config, provider.NewCloudKMSSignatureProvider(logger))
}

func NewSignerServiceWithProvider(
	logger log.Logger,
	config SignerServiceConfig,
	provider provider.SignatureProvider,
) *SignerService {
	return &SignerService{logger, config, provider}
}

func (s *SignerService) RegisterAPIs(server *oprpc.Server) {
	server.AddAPI(rpc.API{
		Namespace: "eth",
		Service:   s,
	})
}

func containsNormalized(s []string, e string) bool {
	for _, a := range s {
		if strings.EqualFold(a, e) {
			return true
		}
	}
	return false
}

// SignTransaction will sign the given transaction with the key configured for the authenticated client
func (s *SignerService) SignTransaction(ctx context.Context, args signer.TransactionArgs) (hexutil.Bytes, error) {
	clientInfo := ClientInfoFromContext(ctx)
	authConfig, err := s.config.GetAuthConfigForClient(clientInfo.ClientName)
	if err != nil {
		return nil, rpc.HTTPError{StatusCode: 403, Status: "Forbidden", Body: []byte(err.Error())}
	}

	labels := prometheus.Labels{"client": clientInfo.ClientName, "status": "error", "error": ""}
	defer func() {
		MetricSignTransactionTotal.With(labels).Inc()
	}()

	if err := args.Check(); err != nil {
		s.logger.Warn("invalid signing arguments", "err", err)
		labels["error"] = "invalid_transaction"
		return nil, &InvalidTransactionError{message: err.Error()}
	}

	if len(authConfig.ToAddresses) > 0 && !containsNormalized(authConfig.ToAddresses, args.To.Hex()) {
		return nil, &UnauthorizedTransactionError{"to address not authorized"}
	}
	if len(authConfig.MaxValue) > 0 && args.Value.ToInt().Cmp(authConfig.MaxValueToInt()) > 0 {
		return nil, &UnauthorizedTransactionError{"value exceeds maximum"}
	}

	txData, err := args.ToTransactionData()
	if err != nil {
		labels["error"] = "transaction_args_error"
		return nil, &InvalidTransactionError{err.Error()}
	}
	tx := types.NewTx(txData)

	txSigner := types.LatestSignerForChainID(tx.ChainId())
	digest := txSigner.Hash(tx)

	signature, err := s.provider.SignDigest(ctx, authConfig.KeyName, digest.Bytes())
	if err != nil {
		labels["error"] = "sign_error"
		return nil, &InvalidTransactionError{err.Error()}
	}

	signed, err := tx.WithSignature(txSigner, signature)
	if err != nil {
		labels["error"] = "invalid_transaction_error"
		return nil, &InvalidTransactionError{err.Error()}
	}
	signerFrom, err := txSigner.Sender(signed)
	if err != nil {
		labels["error"] = "sign_error"
		return nil, &InvalidTransactionError{err.Error()}
	}

	// sanity check that we used the right account
	if args.From != nil && *args.From != signerFrom {
		s.logger.Warn("user is trying to sign with different account than actual signer-provider",
			"provider", signerFrom, "request", *args.From)
		labels["error"] = "sign_error"
		return nil, &InvalidTransactionError{"unexpected from address"}
	}

	txraw, err := signed.MarshalBinary()
	if err != nil {
		labels["error"] = "transaction_marshal_error"
		return nil, &InvalidTransactionError{err.Error()}
	}

	labels["status"] = "success"
	txTo := ""
	if tx.To() != nil {
		txTo = tx.To().Hex()
	}

	s.logger.Info(
		"Signed transaction",
		"digest", hexutil.Encode(digest.Bytes()),
		"client.name", clientInfo.ClientName,
		"client.keyname", authConfig.KeyName,
		"tx.type", tx.Type(),
		"tx.raw", hexutil.Encode(txraw),
		"tx.value", tx.Value(),
		"tx.to", txTo,
		"tx.nonce", tx.Nonce(),
		"tx.gas", tx.Gas(),
		"tx.gasprice", tx.GasPrice(),
		"tx.gastipcap", tx.GasTipCap(),
		"tx.gasfeecap", tx.GasFeeCap(),
		"tx.type", tx.Type(),
		"tx.hash", tx.Hash().Hex(),
		"tx.chainid", tx.ChainId(),
		"tx.blobhashes", tx.BlobHashes(),
		"tx.blobfeecap", tx.BlobGasFeeCap(),
		"signature", hexutil.Encode(signature),
	)

	return hexutil.Bytes(txraw), nil
}
