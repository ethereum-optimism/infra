//go:generate mockgen -destination=mock_provider.go -package=provider github.com/ethereum-optimism/infra/op-signer/provider SignatureProvider
package provider

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/ethereum/go-ethereum/log"
)

type SignatureProvider interface {
	SignDigest(ctx context.Context, keyName string, digest []byte) ([]byte, error)
	GetPublicKey(ctx context.Context, keyName string) ([]byte, error)
}

// ProviderType represents the provider for the key management service.
type ProviderType string

const (
	KeyProviderAWS   ProviderType = "AWS"
	KeyProviderGCP   ProviderType = "GCP"
	KeyProviderLocal ProviderType = "LOCAL"
)

func GetAllProviderTypes() []ProviderType {
	return []ProviderType{KeyProviderAWS, KeyProviderGCP, KeyProviderLocal}
}

// GetAllProviderTypesString returns a string of all the provider types separated
// by commas and wrapped in single quotes. This is useful for logging the available
// provider types.
func GetAllProviderTypesString() string {
	types := GetAllProviderTypes()
	result := make([]string, len(types))
	for i, t := range types {
		result[i] = string(t)
	}
	if len(result) == 1 {
		return result[0]
	}
	return fmt.Sprintf("'%s' or '%s'", strings.Join(result[:len(result)-1], "', '"), result[len(result)-1])
}

// IsValid checks if the KeyProvider value is valid
func (k ProviderType) IsValid() bool {
	return slices.Contains(GetAllProviderTypes(), k)
}

// NewSignatureProvider creates a new SignatureProvider based on the provider type
func NewSignatureProvider(logger log.Logger, providerType ProviderType, config ProviderConfig) (SignatureProvider, error) {
	switch providerType {
	case KeyProviderGCP:
		return NewGCPKMSSignatureProvider(logger)
	case KeyProviderAWS:
		return NewAWSKMSSignatureProvider(logger)
	case KeyProviderLocal:
		return NewLocalKMSSignatureProvider(logger, config)
	default:
		return nil, fmt.Errorf("unsupported provider type: %s", providerType)
	}
}
