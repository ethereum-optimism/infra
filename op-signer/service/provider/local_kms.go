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
	logger log.Logger
}

// NewLocalKMSSignatureProvider creates a new LocalKMSSignatureProvider
func NewLocalKMSSignatureProvider(logger log.Logger) (SignatureProvider, error) {
	return &LocalKMSSignatureProvider{
		logger: logger,
	}, nil
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

	// Log detailed key information
	l.logger.Debug("parsing private key", 
		"keyPath", keyPath,
		"blockType", block.Type,
		"blockHeaders", block.Headers,
		"blockBytesLen", len(block.Bytes),
		"keyDataPrefix", fmt.Sprintf("%x", block.Bytes[:min(32, len(block.Bytes))]))

	// Support both SEC1 ("EC PRIVATE KEY") and PKCS8 ("PRIVATE KEY") formats
	var key *ecdsa.PrivateKey
	if block.Type == "EC PRIVATE KEY" {
		// Try to parse as SEC1 format
		l.logger.Debug("attempting to parse SEC1 key",
			"keyPath", keyPath,
			"keyDataLength", len(block.Bytes),
			"keyDataHex", fmt.Sprintf("%x", block.Bytes))

		// Try to decode the ASN.1 structure
		var asn1Key struct {
			Version       int
			PrivateKey    []byte
			NamedCurveOID asn1.ObjectIdentifier
			PublicKey     asn1.BitString
		}
		rest, err := asn1.Unmarshal(block.Bytes, &asn1Key)
		if err != nil {
			l.logger.Debug("failed to decode ASN.1 structure",
				"keyPath", keyPath,
				"error", err.Error(),
				"errorType", fmt.Sprintf("%T", err))
		} else {
			l.logger.Debug("decoded ASN.1 structure",
				"keyPath", keyPath,
				"version", asn1Key.Version,
				"namedCurveOID", asn1Key.NamedCurveOID,
				"privateKeyLen", len(asn1Key.PrivateKey),
				"publicKeyLen", asn1Key.PublicKey.BitLength,
				"restLen", len(rest))
		}

		// Try to parse the key using x509.ParseECPrivateKey
		key, err = x509.ParseECPrivateKey(block.Bytes)
		if err != nil {
			l.logger.Debug("failed to parse EC private key as SEC1",
				"keyPath", keyPath,
				"error", err.Error(),
				"errorType", fmt.Sprintf("%T", err),
				"keyDataHex", fmt.Sprintf("%x", block.Bytes))

			// Try to extract the private key bytes from the ASN.1 structure
			if len(asn1Key.PrivateKey) == 32 {
				l.logger.Debug("extracting private key bytes from ASN.1 structure",
					"keyPath", keyPath,
					"privateKeyLen", len(asn1Key.PrivateKey),
					"privateKeyHex", fmt.Sprintf("%x", asn1Key.PrivateKey))
				key, err = crypto.ToECDSA(asn1Key.PrivateKey)
				if err != nil {
					l.logger.Debug("failed to parse extracted private key bytes",
						"keyPath", keyPath,
						"error", err.Error(),
						"errorType", fmt.Sprintf("%T", err))
				}
			} else {
				l.logger.Debug("private key length is not 32 bytes",
					"keyPath", keyPath,
					"privateKeyLen", len(asn1Key.PrivateKey))
			}
		}
	} else if block.Type == "PRIVATE KEY" {
		// Try to parse as PKCS8 format
		l.logger.Debug("attempting to parse PKCS8 key",
			"keyPath", keyPath,
			"keyDataLength", len(block.Bytes))

		privKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			l.logger.Debug("failed to parse private key as PKCS8",
				"keyPath", keyPath,
				"error", err.Error(),
				"errorType", fmt.Sprintf("%T", err),
				"keyDataPrefix", fmt.Sprintf("%x", block.Bytes[:min(32, len(block.Bytes))]))

			// Try to parse the raw bytes as a secp256k1 key
			rawKey, err := crypto.ToECDSA(block.Bytes)
			if err != nil {
				l.logger.Debug("failed to parse raw key bytes",
					"keyPath", keyPath,
					"error", err.Error(),
					"errorType", fmt.Sprintf("%T", err))
			} else {
				key = rawKey
			}
		} else {
			var ok bool
			key, ok = privKey.(*ecdsa.PrivateKey)
			if !ok {
				l.logger.Debug("parsed key is not an EC key",
					"keyPath", keyPath,
					"keyType", fmt.Sprintf("%T", privKey))
				return nil, fmt.Errorf("key from path '%s' is not an EC key", keyPath)
			}
		}
	} else {
		return nil, fmt.Errorf("invalid PEM block type '%s' in key path '%s' (expected 'EC PRIVATE KEY' or 'PRIVATE KEY')", block.Type, keyPath)
	}

	if key == nil {
		return nil, fmt.Errorf("failed to parse private key from path '%s'", keyPath)
	}

	// Log curve information for debugging
	l.logger.Debug("parsed EC key", 
		"keyPath", keyPath,
		"curve", key.Curve.Params().Name,
		"curveParams", fmt.Sprintf("%+v", key.Curve.Params()),
		"publicKey", fmt.Sprintf("%x", crypto.FromECDSAPub(&key.PublicKey)))

	// Verify it's using secp256k1 curve
	if key.Curve != crypto.S256() {
		return nil, fmt.Errorf("key from path '%s' must use secp256k1 curve (got %s)", keyPath, key.Curve.Params().Name)
	}

	return key, nil
}

// min returns the minimum of a and b
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// SignDigest signs the digest using the local private key and returns a compact recoverable signature
func (l *LocalKMSSignatureProvider) SignDigest(
	ctx context.Context,
	keyName string,
	digest []byte,
) ([]byte, error) {
	// Parse the private key for this request
	privateKey, err := l.parsePrivateKey(keyName)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	// Sign the digest
	signature, err := crypto.Sign(digest, privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign digest")
	}

	l.logger.Debug("local signature generated", 
		"signature", fmt.Sprintf("%x", signature),
		"keyAddress", crypto.PubkeyToAddress(privateKey.PublicKey).Hex())

	return signature, nil
}

// GetPublicKey returns the public key in uncompressed format
func (l *LocalKMSSignatureProvider) GetPublicKey(
	ctx context.Context,
	keyName string,
) ([]byte, error) {
	// Parse the private key for this request
	privateKey, err := l.parsePrivateKey(keyName)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	return crypto.FromECDSAPub(&privateKey.PublicKey), nil
} 
