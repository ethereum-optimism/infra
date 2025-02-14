package provider

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/ethereum/go-ethereum/log"
)

// AWSKMSClient is the minimal interface for the AWS KMS client required by the AWSKMSSignatureProvider. These functions
// are already implemented by the AWS SDK, but we define our own type to allow us to mock the client in tests.
type AWSKMSClient interface {
	// https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/kms#Client.Sign
	Sign(ctx context.Context, params *kms.SignInput, optFns ...func(*kms.Options)) (*kms.SignOutput, error)
	// https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/kms#Client.GetPublicKey
	GetPublicKey(ctx context.Context, params *kms.GetPublicKeyInput, optFns ...func(*kms.Options)) (*kms.GetPublicKeyOutput, error)
}

type AWSKMSSignatureProvider struct {
	logger log.Logger
	client AWSKMSClient
}

func NewAWSKMSSignatureProvider(logger log.Logger) (SignatureProvider, error) {
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	client := kms.NewFromConfig(cfg)
	return &AWSKMSSignatureProvider{logger: logger, client: client}, nil
}

func NewAWSKMSSignatureProviderWithClient(logger log.Logger, client AWSKMSClient) SignatureProvider {
	return &AWSKMSSignatureProvider{logger: logger, client: client}
}

// SignDigest signs the digest with a given AWS KMS keyname and returns a compact recoverable signature.
// The key must be an ECC_SECG_P256K1 key type.
func (a *AWSKMSSignatureProvider) SignDigest(
	ctx context.Context,
	keyName string,
	digest []byte,
) ([]byte, error) {
	publicKey, err := a.GetPublicKey(ctx, keyName)
	if err != nil {
		return nil, fmt.Errorf("failed to get public key: %w", err)
	}

	signInput := &kms.SignInput{
		KeyId:            &keyName,
		Message:          digest,
		MessageType:      types.MessageTypeDigest,
		SigningAlgorithm: types.SigningAlgorithmSpecEcdsaSha256,
	}

	result, err := a.client.Sign(ctx, signInput)
	if err != nil {
		return nil, fmt.Errorf("aws kms sign request failed: %w", err)
	}

	a.logger.Debug(fmt.Sprintf("der signature: %x", result.Signature))

	return convertToCompactRecoverableSignature(result.Signature, digest, publicKey)
}

// GetPublicKey returns a decoded secp256k1 public key.
func (a *AWSKMSSignatureProvider) GetPublicKey(
	ctx context.Context,
	keyName string,
) ([]byte, error) {
	input := &kms.GetPublicKeyInput{
		KeyId: &keyName,
	}

	result, err := a.client.GetPublicKey(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("aws kms get public key request failed: %w", err)
	}

	// AWS KMS returns the public key in DER format
	return x509ParseECDSAPublicKey(result.PublicKey)
}
