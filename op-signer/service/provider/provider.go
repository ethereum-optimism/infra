//go:generate mockgen -destination=mock_provider.go -package=provider github.com/ethereum-optimism/infra/op-signer/service/provider SignatureProvider
package provider

import "context"

type SignatureProvider interface {
	SignDigest(ctx context.Context, keyName string, digest []byte) ([]byte, error)
	GetPublicKey(ctx context.Context, keyName string) ([]byte, error)
}
