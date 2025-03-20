package provider

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/asn1"
	"encoding/pem"
	"fmt"
	"os"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/log"
)

// LocalKMSSignatureProvider implements SignatureProvider using local private keys
type LocalKMSSignatureProvider struct {
	logger     log.Logger
	config     ProviderConfig
	keyMap     map[string]*ecdsa.PrivateKey
}

// NewLocalKMSSignatureProvider creates a new LocalKMSSignatureProvider and loads all configured keys
func NewLocalKMSSignatureProvider(logger log.Logger, config ProviderConfig) (SignatureProvider, error) {
	provider := &LocalKMSSignatureProvider{
		logger: logger,
		config: config,
		keyMap: make(map[string]*ecdsa.PrivateKey),
	}

	// Load all keys during construction
	for _, auth := range config.Auth {
		if err := provider.loadKey(auth.KeyName); err != nil {
			return nil, fmt.Errorf("failed to load key from path '%s': %w", auth.KeyName, err)
		}
	}

	return provider, nil
}

// parsePrivateKey parses a private key from a PEM-formatted file
func (l *LocalKMSSignatureProvider) parsePrivateKey(keyPath string) (*ecdsa.PrivateKey, error) {
	// Read the private key file
	keyBytes, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read private key from path '%s': %w", keyPath, err)
	}

	block, _ := pem.Decode(keyBytes)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block from key path '%s'", keyPath)
	}

	if block.Type != "EC PRIVATE KEY" {
		return nil, fmt.Errorf("invalid PEM block type '%s' in key path '%s' (expected 'EC PRIVATE KEY')", block.Type, keyPath)
	}

	// Parse SEC1 format
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		// Try to extract raw private key from ASN.1 structure
		// SEC1 ASN.1 structure for EC private keys:
		// ECPrivateKey ::= SEQUENCE {
		//   version INTEGER { ecPrivkeyVer1(1) },
		//   privateKey OCTET STRING,
		//   parameters [0] ECParameters {{ NamedCurve }} OPTIONAL,
		//   publicKey [1] BIT STRING OPTIONAL
		// }
		var asn1Key struct {
			Version       int
			PrivateKey   []byte
			Parameters   asn1.ObjectIdentifier `asn1:"optional,explicit,tag:0"`
			PublicKey    asn1.BitString       `asn1:"optional,explicit,tag:1"`
		}
		if _, err := asn1.Unmarshal(block.Bytes, &asn1Key); err != nil {
			return nil, fmt.Errorf("failed to parse SEC1 key from path '%s': %w", keyPath, err)
		}
		if len(asn1Key.PrivateKey) != 32 {
			return nil, fmt.Errorf("invalid private key length from path '%s': got %d bytes, expected 32", keyPath, len(asn1Key.PrivateKey))
		}
		key, err = crypto.ToECDSA(asn1Key.PrivateKey)
		if err != nil {
			return nil, fmt.Errorf("failed to convert private key from path '%s': %w", keyPath, err)
		}
	}

	// Verify it's using secp256k1 curve
	if key.Curve != crypto.S256() {
		return nil, fmt.Errorf("key from path '%s' must use secp256k1 curve (got %s)", keyPath, key.Curve.Params().Name)
	}

	return key, nil
}

// loadKey loads a private key from a file path and stores it in the key map
func (l *LocalKMSSignatureProvider) loadKey(keyPath string) error {
	key, err := l.parsePrivateKey(keyPath)
	if err != nil {
		return fmt.Errorf("failed to load key from path '%s': %w", keyPath, err)
	}
	l.keyMap[keyPath] = key
	l.logger.Info("loaded private key", 
		"keyPath", keyPath,
		"address", crypto.PubkeyToAddress(key.PublicKey).Hex())
	return nil
}

// SignDigest signs the digest using the local private key and returns a compact recoverable signature
func (l *LocalKMSSignatureProvider) SignDigest(
	ctx context.Context,
	keyName string,
	digest []byte,
) ([]byte, error) {
	privateKey, ok := l.keyMap[keyName]
	if !ok {
		return nil, fmt.Errorf("key '%s' not found in key map", keyName)
	}

	signature, err := crypto.Sign(digest, privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign digest")
	}

	return signature, nil
}

// GetPublicKey returns the public key in uncompressed format
func (l *LocalKMSSignatureProvider) GetPublicKey(
	ctx context.Context,
	keyName string,
) ([]byte, error) {
	privateKey, ok := l.keyMap[keyName]
	if !ok {
		return nil, fmt.Errorf("key '%s' not found in key map", keyName)
	}

	return crypto.FromECDSAPub(&privateKey.PublicKey), nil
} 
