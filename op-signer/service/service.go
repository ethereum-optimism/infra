package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/holiman/uint256"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/ethereum-optimism/infra/op-signer/provider"
	"github.com/ethereum-optimism/optimism/op-service/eth"
	oprpc "github.com/ethereum-optimism/optimism/op-service/rpc"
	"github.com/ethereum-optimism/optimism/op-service/signer"
)

type SignerService struct {
	eth      *EthService
	opsigner *OpsignerService
}

type EthService struct {
	logger   log.Logger
	config   provider.ProviderConfig
	provider provider.SignatureProvider
}

type OpsignerService struct {
	logger   log.Logger
	config   provider.ProviderConfig
	provider provider.SignatureProvider
}

func NewSignerService(logger log.Logger, config provider.ProviderConfig) (*SignerService, error) {
	provider, err := provider.NewSignatureProvider(logger, config.ProviderType, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create signature provider: %w", err)
	}
	return NewSignerServiceWithProvider(logger, config, provider), nil
}

func NewSignerServiceWithProvider(
	logger log.Logger,
	config provider.ProviderConfig,
	provider provider.SignatureProvider,
) *SignerService {
	ethService := EthService{logger, config, provider}
	opsignerService := OpsignerService{logger, config, provider}
	return &SignerService{&ethService, &opsignerService}
}

func (s *SignerService) RegisterAPIs(server *oprpc.Server) {
	server.AddAPI(rpc.API{
		Namespace: "eth",
		Service:   s.eth,
	})
	server.AddAPI(rpc.API{
		Namespace: "opsigner",
		Service:   s.opsigner,
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
func (s *EthService) SignTransaction(ctx context.Context, args signer.TransactionArgs) (hexutil.Bytes, error) {
	clientInfo := ClientInfoFromContext(ctx)
	authConfig, err := s.config.GetAuthConfigForClient(clientInfo.ClientName, nil)
	if err != nil {
		return nil, rpc.HTTPError{StatusCode: 403, Status: "Forbidden", Body: []byte(err.Error())}
	}

	labels := prometheus.Labels{"client": clientInfo.ClientName, "status": "error", "error": ""}
	defer func() {
		MetricSignTransactionTotal.With(labels).Inc()
	}()
	if authConfig.MessageSigningOnly {
		labels["error"] = "unauthorized_method"
		return nil, &UnauthorizedTransactionError{"client is only authorized for message signing"}
	}

	if err := args.Check(); err != nil {
		s.logger.Warn("invalid signing arguments", "err", err)
		labels["error"] = "invalid_transaction"
		return nil, &InvalidTransactionError{message: err.Error()}
	}

	if len(authConfig.ToAddresses) > 0 && !containsNormalized(authConfig.ToAddresses, args.To.Hex()) {
		return nil, &UnauthorizedTransactionError{"to address not authorized"}
	}
	if len(authConfig.MaxValue) > 0 && ((*uint256.Int)(args.Value)).ToBig().Cmp(authConfig.MaxValueToInt()) > 0 {
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
		"tx.blobhashes", fmt.Sprint(tx.BlobHashes()),
		"tx.blobfeecap", fmt.Sprint(tx.BlobGasFeeCap()),
		"signature", hexutil.Encode(signature),
	)

	return hexutil.Bytes(txraw), nil
}

func (s *OpsignerService) SignBlockPayload(ctx context.Context, args signer.BlockPayloadArgs) (*eth.Bytes65, error) {
	return s.signBlockPayload(ctx, args.Message, args.SenderAddress)
}

func (s *OpsignerService) SignBlockPayloadV2(ctx context.Context, args signer.BlockPayloadArgsV2) (*eth.Bytes65, error) {
	return s.signBlockPayload(ctx, args.Message, args.SenderAddress)
}

// SignMessage signs the EIP-191 hash of an arbitrary message with the key authorized for the
// authenticated client and requested sender address.
func (s *OpsignerService) SignMessage(ctx context.Context, args SignMessageArgs) (*eth.Bytes65, error) {
	clientInfo := ClientInfoFromContext(ctx)
	labels := prometheus.Labels{"client": clientInfo.ClientName, "status": "error", "error": ""}
	defer func() {
		MetricSignMessageTotal.With(labels).Inc()
	}()

	if args.SenderAddress == nil {
		labels["error"] = "unauthorized_message"
		return nil, &UnauthorizedMessageError{"sender address is required"}
	}
	authConfig, err := s.config.GetAuthConfigForClient(
		clientInfo.ClientName,
		args.SenderAddress,
	)
	if err != nil {
		labels["error"] = "unauthorized_client"
		return nil, &UnauthorizedMessageError{err.Error()}
	}
	if !authConfig.MessageSigningOnly {
		labels["error"] = "unauthorized_method"
		return nil, &UnauthorizedMessageError{"client is not authorized for message signing"}
	}
	if len(args.Message) == 0 {
		labels["error"] = "invalid_message"
		return nil, &InvalidMessageError{"message must not be empty"}
	}

	digest := accounts.TextHash(args.Message)
	signature, err := s.provider.SignDigest(ctx, authConfig.KeyName, digest)
	if err != nil {
		labels["error"] = "sign_error"
		return nil, &InvalidMessageError{err.Error()}
	}
	if len(signature) != 65 {
		labels["error"] = "sign_error"
		return nil, &InvalidMessageError{"signature has invalid length"}
	}
	publicKey, err := crypto.SigToPub(digest, signature)
	if err != nil {
		labels["error"] = "sign_error"
		return nil, &InvalidMessageError{fmt.Sprintf("failed to recover signature: %v", err)}
	}
	signerAddress := crypto.PubkeyToAddress(*publicKey)
	if signerAddress != authConfig.FromAddress {
		s.logger.Error(
			"message signature does not match authorized sender",
			"authorized", authConfig.FromAddress,
			"recovered", signerAddress,
		)
		labels["error"] = "sign_error"
		return nil, &InvalidMessageError{"signature does not match authorized sender"}
	}

	result := eth.Bytes65(signature)
	labels["status"] = "success"
	s.logger.Info(
		"Signed message",
		"client.name", clientInfo.ClientName,
		"client.keyname", authConfig.KeyName,
		"digest", hexutil.Encode(digest),
		"signature", hexutil.Encode(signature),
	)
	return &result, nil
}

func (s *OpsignerService) signBlockPayload(
	ctx context.Context,
	getMsg func() (*signer.BlockSigningMessage, error),
	fromAddress *common.Address,
) (*eth.Bytes65, error) {
	clientInfo := ClientInfoFromContext(ctx)
	authConfig, err := s.config.GetAuthConfigForClient(clientInfo.ClientName, fromAddress)
	if err != nil {
		return nil, rpc.HTTPError{StatusCode: 403, Status: "Forbidden", Body: []byte(err.Error())}
	}

	labels := prometheus.Labels{"client": clientInfo.ClientName, "status": "error", "error": ""}
	defer func() {
		MetricSignBlockPayloadTotal.With(labels).Inc()
	}()
	if authConfig.MessageSigningOnly {
		labels["error"] = "unauthorized_method"
		return nil, &UnauthorizedBlockPayloadError{"client is only authorized for message signing"}
	}

	msg, err := getMsg()
	if err != nil {
		s.logger.Warn("invalid signing arguments", "err", err)
		labels["error"] = "invalid_blockPayload"
		return nil, &InvalidBlockPayloadError{message: err.Error()}
	}

	if fromAddress != nil && *fromAddress != authConfig.FromAddress {
		s.logger.Warn("user is trying to sign with different sender account than actual signer-provider",
			"provider", authConfig.FromAddress, "request", fromAddress)
		labels["error"] = "sign_error"
		return nil, &UnauthorizedBlockPayloadError{"unexpected from address"}
	}

	if msg.ChainID != eth.ChainIDFromUInt64(authConfig.ChainID) {
		s.logger.Warn("user is trying to sign a block payload for a different chainID than the actual signer's chainID",
			"provider", authConfig.ChainID, "request", msg.ChainID)
		labels["error"] = "sign_error"
		return nil, &UnauthorizedBlockPayloadError{"unexpected chainId"}
	}

	signingHash := msg.ToSigningHash()

	signature, err := s.provider.SignDigest(ctx, authConfig.KeyName, signingHash[:])
	if err != nil {
		labels["error"] = "sign_error"
		return nil, &InvalidBlockPayloadError{err.Error()}
	}
	if len(signature) != 65 {
		labels["error"] = "sign_error"
		return nil, &InvalidBlockPayloadError{"signature has invalid length"}
	}
	result := eth.Bytes65(signature)

	labels["status"] = "success"

	s.logger.Info(
		"Signed block payload",
		"signingHash", hexutil.Encode(signingHash.Bytes()),
		"signature", hexutil.Encode(signature),
	)

	return &result, nil
}
