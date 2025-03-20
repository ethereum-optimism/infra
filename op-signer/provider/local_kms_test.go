package provider

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/crypto/secp256k1"
	"github.com/ethereum/go-ethereum/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func generateTestPrivateKeyPEM(t *testing.T) string {
	// Generate a test private key
	privateKey, err := crypto.GenerateKey()
	require.NoError(t, err)

	// Convert to PKCS8 format
	privateKeyBytes, err := x509.MarshalECPrivateKey(privateKey)
	require.NoError(t, err)

	// Encode as PEM
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: privateKeyBytes,
	})

	return string(privateKeyPEM)
}

func TestLocalKMSSignatureProvider_GetPublicKey(t *testing.T) {
	// Generate a test private key PEM
	privateKeyPEM := generateTestPrivateKeyPEM(t)

	// Create provider
	provider, err := NewLocalKMSSignatureProvider(log.New(), privateKeyPEM)
	require.NoError(t, err)

	// Get public key
	publicKey, err := provider.GetPublicKey(context.Background(), "ignored")
	require.NoError(t, err)

	// Parse the original private key to get its public key
	block, _ := pem.Decode([]byte(privateKeyPEM))
	originalPrivateKey, err := x509.ParseECPrivateKey(block.Bytes)
	require.NoError(t, err)
	expectedPublicKey := crypto.FromECDSAPub(&originalPrivateKey.PublicKey)

	// Verify public key matches
	assert.Equal(t, expectedPublicKey, publicKey)
}

func TestLocalKMSSignatureProvider_SignDigest(t *testing.T) {
	// Generate a test private key PEM
	privateKeyPEM := generateTestPrivateKeyPEM(t)

	// Create provider
	provider, err := NewLocalKMSSignatureProvider(log.New(), privateKeyPEM)
	require.NoError(t, err)

	// Create a test message and digest
	message := []byte("test message to sign")
	digest := crypto.Keccak256Hash(message).Bytes()

	// Sign the digest
	signature, err := provider.SignDigest(context.Background(), "ignored", digest)
	require.NoError(t, err)

	// Verify signature length (65 bytes: r[32] || s[32] || v[1])
	assert.Equal(t, 65, len(signature))

	// Verify signature is recoverable
	recoveredPub, err := secp256k1.RecoverPubkey(digest, signature)
	require.NoError(t, err)

	// Parse the original private key to get its public key
	block, _ := pem.Decode([]byte(privateKeyPEM))
	originalPrivateKey, err := x509.ParseECPrivateKey(block.Bytes)
	require.NoError(t, err)
	expectedPublicKey := crypto.FromECDSAPub(&originalPrivateKey.PublicKey)

	// Verify recovered public key matches original
	assert.Equal(t, expectedPublicKey, recoveredPub)

	// Verify signature using go-ethereum's crypto implementation
	assert.True(t, crypto.VerifySignature(expectedPublicKey, digest, signature[:64]))
}

func TestNewLocalKMSSignatureProvider_InvalidKey(t *testing.T) {
	// Test with invalid PEM string
	_, err := NewLocalKMSSignatureProvider(log.New(), "invalid pem")
	assert.Error(t, err)

	// Test with wrong PEM block type
	invalidPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: []byte("invalid key data"),
	})
	_, err = NewLocalKMSSignatureProvider(log.New(), string(invalidPEM))
	assert.Error(t, err)
} 
