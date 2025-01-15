package provider

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/crypto/secp256k1"
	"github.com/ethereum/go-ethereum/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockAWSKMSClient implements AWSKMSClient interface for testing
type mockAWSKMSClient struct {
	signOutput         *kms.SignOutput
	getPublicKeyOutput *kms.GetPublicKeyOutput
}

func (m *mockAWSKMSClient) Sign(ctx context.Context, params *kms.SignInput, optFns ...func(*kms.Options)) (*kms.SignOutput, error) {
	return m.signOutput, nil
}

func (m *mockAWSKMSClient) GetPublicKey(ctx context.Context, params *kms.GetPublicKeyInput, optFns ...func(*kms.Options)) (*kms.GetPublicKeyOutput, error) {
	return m.getPublicKeyOutput, nil
}

func TestAWSKMSSignatureProvider_GetPublicKey(t *testing.T) {
	// Generate a secp256k1 key pair
	privateKey, err := crypto.GenerateKey()
	require.NoError(t, err)

	publicKey := privateKey.PublicKey
	publicKeyDER, err := marshalECDSAPublicKey(&publicKey)
	require.NoError(t, err)

	mockClient := &mockAWSKMSClient{
		getPublicKeyOutput: &kms.GetPublicKeyOutput{
			PublicKey: publicKeyDER,
		},
	}

	provider := NewAWSKMSSignatureProviderWithClient(log.New(), mockClient)

	_, err = provider.GetPublicKey(context.Background(), "test-key")
	require.NoError(t, err)
}

func TestAWSKMSSignatureProvider_SignDigest(t *testing.T) {
	// Generate a secp256k1 key pair
	privateKey, err := crypto.GenerateKey()
	require.NoError(t, err)

	publicKey := privateKey.PublicKey
	publicKeyDER, err := marshalECDSAPublicKey(&publicKey)
	if err != nil {
		t.Fatalf("failed to marshal public key: %v", err)
	}

	// Sign a message using the private key so we can later verify the signature
	message := []byte("test message to sign")
	digest := crypto.Keccak256Hash(message).Bytes()

	compactRecoverableSig, err := crypto.Sign(digest, privateKey)
	require.NoError(t, err)

	derSignature, err := convertCompactRecoverableSignatureToDER(compactRecoverableSig)
	require.NoError(t, err)

	// Create mock AWS KMS client that returns our test signature
	mockClient := &mockAWSKMSClient{
		getPublicKeyOutput: &kms.GetPublicKeyOutput{
			PublicKey: publicKeyDER,
		},
		signOutput: &kms.SignOutput{
			Signature: derSignature,
		},
	}

	// Create provider with mock client
	provider := NewAWSKMSSignatureProviderWithClient(log.New(), mockClient)

	// Test signing
	signature, err := provider.SignDigest(context.Background(), "test-key", digest)
	require.NoError(t, err)

	// Verify signature length (65 bytes: r[32] || s[32] || v[1])
	assert.Equal(t, 65, len(signature))

	// Verify signature is recoverable
	recoveredPub, err := secp256k1.RecoverPubkey(digest, signature)
	require.NoError(t, err)

	// Verify recovered public key matches original
	publicKeyBytes, err := x509ParseECDSAPublicKey(publicKeyDER)
	require.NoError(t, err)

	assert.Equal(t, publicKeyBytes, recoveredPub)

	// Verify signature using go-ethereum's crypto implementation
	assert.True(t, crypto.VerifySignature(publicKeyBytes, digest, signature[:64]))
}
