package provider

import (
	"context"
	"encoding/asn1"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/crypto/secp256k1"
	"github.com/ethereum/go-ethereum/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeKeyToTempFile(t *testing.T, pemData []byte) string {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "test_key.pem")
	err := os.WriteFile(keyPath, pemData, 0600)
	require.NoError(t, err)
	return keyPath
}

func generateTestPrivateKeyPEM(t *testing.T) ([]byte, []byte) {
	// Generate a test private key
	privateKey, err := crypto.GenerateKey()
	require.NoError(t, err)

	// Get the raw private key bytes
	privateKeyBytes := crypto.FromECDSA(privateKey)
	publicKeyBytes := crypto.FromECDSAPub(&privateKey.PublicKey)

	// Create ASN.1 structure for SEC1 EC private key
	asn1Bytes, err := asn1.Marshal(struct {
		Version    int
		PrivateKey []byte
		Parameters asn1.ObjectIdentifier `asn1:"optional,explicit,tag:0"`
		PublicKey  asn1.BitString       `asn1:"optional,explicit,tag:1"`
	}{
		Version:    1,
		PrivateKey: privateKeyBytes,
		Parameters: oidNamedCurveSECP256K1,
		PublicKey:  asn1.BitString{Bytes: publicKeyBytes, BitLength: len(publicKeyBytes) * 8},
	})
	require.NoError(t, err)

	// Encode as PEM
	pemData := pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: asn1Bytes,
	})

	return pemData, publicKeyBytes
}

func generateTestASN1PrivateKeyPEM(t *testing.T) []byte {
	// Generate a test private key
	privateKey, err := crypto.GenerateKey()
	require.NoError(t, err)

	// Create ASN.1 structure manually
	asn1Bytes, err := asn1.Marshal(struct {
		Version    int
		PrivateKey []byte
	}{
		Version:    1,
		PrivateKey: crypto.FromECDSA(privateKey),
	})
	require.NoError(t, err)

	// Encode as PEM
	return pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: asn1Bytes,
	})
}

func TestLocalKMSSignatureProvider_GetPublicKey(t *testing.T) {
	// Generate and write test key
	keyPEM, expectedPublicKey := generateTestPrivateKeyPEM(t)
	keyPath := writeKeyToTempFile(t, keyPEM)

	// Create provider config
	config := ProviderConfig{
		Auth: []AuthConfig{{KeyName: keyPath}},
	}

	// Create provider
	provider, err := NewLocalKMSSignatureProvider(log.New(), config)
	require.NoError(t, err)

	// Get public key
	publicKey, err := provider.GetPublicKey(context.Background(), keyPath)
	require.NoError(t, err)

	// Verify public key matches
	assert.Equal(t, expectedPublicKey, publicKey)
}

func TestLocalKMSSignatureProvider_SignDigest(t *testing.T) {
	// Generate and write test key
	keyPEM, expectedPublicKey := generateTestPrivateKeyPEM(t)
	keyPath := writeKeyToTempFile(t, keyPEM)

	// Create provider config
	config := ProviderConfig{
		Auth: []AuthConfig{{KeyName: keyPath}},
	}

	// Create provider
	provider, err := NewLocalKMSSignatureProvider(log.New(), config)
	require.NoError(t, err)

	// Create a test message and digest
	message := []byte("test message to sign")
	digest := crypto.Keccak256Hash(message).Bytes()

	// Sign the digest
	signature, err := provider.SignDigest(context.Background(), keyPath, digest)
	require.NoError(t, err)

	// Verify signature length (65 bytes: r[32] || s[32] || v[1])
	assert.Equal(t, 65, len(signature))

	// Verify signature is recoverable
	recoveredPub, err := secp256k1.RecoverPubkey(digest, signature)
	require.NoError(t, err)

	// Verify recovered public key matches original
	assert.Equal(t, expectedPublicKey, recoveredPub)

	// Verify signature using go-ethereum's crypto implementation
	assert.True(t, crypto.VerifySignature(expectedPublicKey, digest, signature[:64]))
}

func TestLocalKMSSignatureProvider_ASN1FallbackPath(t *testing.T) {
	// Generate and write test key with ASN.1 structure
	keyPEM := generateTestASN1PrivateKeyPEM(t)
	keyPath := writeKeyToTempFile(t, keyPEM)

	// Create provider config
	config := ProviderConfig{
		Auth: []AuthConfig{{KeyName: keyPath}},
	}

	// Create provider
	provider, err := NewLocalKMSSignatureProvider(log.New(), config)
	require.NoError(t, err)

	// Verify we can sign with the key
	digest := crypto.Keccak256Hash([]byte("test")).Bytes()
	signature, err := provider.SignDigest(context.Background(), keyPath, digest)
	require.NoError(t, err)
	assert.Equal(t, 65, len(signature))
}

func TestLocalKMSSignatureProvider_ErrorCases(t *testing.T) {
	tests := []struct {
		name     string
		setupKey func(t *testing.T) string
		wantErr  string
	}{
		{
			name: "non-existent file",
			setupKey: func(t *testing.T) string {
				return "/nonexistent/key.pem"
			},
			wantErr: "failed to read private key",
		},
		{
			name: "invalid PEM",
			setupKey: func(t *testing.T) string {
				return writeKeyToTempFile(t, []byte("invalid pem data"))
			},
			wantErr: "failed to decode PEM block",
		},
		{
			name: "wrong PEM type",
			setupKey: func(t *testing.T) string {
				pemData := pem.EncodeToMemory(&pem.Block{
					Type:  "RSA PRIVATE KEY",
					Bytes: []byte{1, 2, 3},
				})
				return writeKeyToTempFile(t, pemData)
			},
			wantErr: "invalid PEM block type",
		},
		{
			name: "invalid ASN.1 structure",
			setupKey: func(t *testing.T) string {
				pemData := pem.EncodeToMemory(&pem.Block{
					Type:  "EC PRIVATE KEY",
					Bytes: []byte{1, 2, 3},
				})
				return writeKeyToTempFile(t, pemData)
			},
			wantErr: "failed to parse SEC1 key",
		},
		{
			name: "wrong key length",
			setupKey: func(t *testing.T) string {
				asn1Bytes, _ := asn1.Marshal(struct {
					Version    int
					PrivateKey []byte
				}{
					Version:    1,
					PrivateKey: make([]byte, 16), // Wrong length
				})
				pemData := pem.EncodeToMemory(&pem.Block{
					Type:  "EC PRIVATE KEY",
					Bytes: asn1Bytes,
				})
				return writeKeyToTempFile(t, pemData)
			},
			wantErr: "invalid private key length",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keyPath := tt.setupKey(t)
			config := ProviderConfig{
				Auth: []AuthConfig{{KeyName: keyPath}},
			}
			_, err := NewLocalKMSSignatureProvider(log.New(), config)
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestLocalKMSSignatureProvider_KeyNotFound(t *testing.T) {
	// Generate and write test key
	keyPEM, _ := generateTestPrivateKeyPEM(t)
	keyPath := writeKeyToTempFile(t, keyPEM)

	// Create provider config
	config := ProviderConfig{
		Auth: []AuthConfig{{KeyName: keyPath}},
	}

	// Create provider
	provider, err := NewLocalKMSSignatureProvider(log.New(), config)
	require.NoError(t, err)

	// Try to use non-existent key
	_, err = provider.GetPublicKey(context.Background(), "nonexistent")
	assert.ErrorContains(t, err, "not found in key map")

	_, err = provider.SignDigest(context.Background(), "nonexistent", []byte{1, 2, 3})
	assert.ErrorContains(t, err, "not found in key map")
} 
