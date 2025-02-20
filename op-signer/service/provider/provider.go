//go:generate mockgen -destination=mock_provider.go -package=provider github.com/ethereum-optimism/infra/op-signer/service/provider SignatureProvider
package provider

import (
	"context"
	"fmt"

	"github.com/ethereum/go-ethereum/log"
)

type SignatureProvider interface {
	SignDigest(ctx context.Context, keyName string, digest []byte) ([]byte, error)
	GetPublicKey(ctx context.Context, keyName string) ([]byte, error)
}

// ProviderType represents the cloud provider for the key management service
type ProviderType string

const (
	KeyProviderAWS ProviderType = "AWS"
	KeyProviderGCP ProviderType = "GCP"
)

// IsValid checks if the KeyProvider value is valid
func (k ProviderType) IsValid() bool {
	switch k {
	case KeyProviderAWS, KeyProviderGCP:
		return true
	default:
		return false
	}
}

// NewSignatureProvider creates a new SignatureProvider based on the provider type
func NewSignatureProvider(logger log.Logger, providerType ProviderType) (SignatureProvider, error) {
	switch providerType {
	case KeyProviderGCP:
		return NewCloudKMSSignatureProvider(logger)
	case KeyProviderAWS:
		return NewAWSKMSSignatureProvider(logger)
	default:
		return nil, fmt.Errorf("unsupported provider type: %s", providerType)
	}
}
