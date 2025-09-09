package provider

import (
	"crypto/ecdsa"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/crypto/secp256k1"
)

// marshalECDSAPublicKey marshals a secp256k1 public key into DER format. This
// is needed because the Golang standard crypto/esdsa and crypto/elliptic libs
// do not support secp256k1.
func marshalECDSAPublicKey(pub *ecdsa.PublicKey) ([]byte, error) {
	algoFullBytes, _ := asn1.Marshal(oidNamedCurveSECP256K1)

	// Encode AlgorithmIdentifier with OID for ECDSA and named curve
	publicKeyAlgorithm := pkix.AlgorithmIdentifier{
		Algorithm: oidPublicKeyECDSA,
		Parameters: asn1.RawValue{
			Tag:        asn1.TagOID,
			FullBytes:  algoFullBytes,
			Class:      asn1.ClassUniversal,
			IsCompound: false,
		},
	}

	// Marshal the public key point (X, Y)
	publicKeyBytes := secp256k1.S256().Marshal(pub.X, pub.Y)

	// Construct the publicKeyInfo structure
	pkix := publicKeyInfo{
		Algorithm: publicKeyAlgorithm,
		PublicKey: asn1.BitString{
			Bytes:     publicKeyBytes,
			BitLength: len(publicKeyBytes) * 8,
		},
	}

	// Encode the structure into DER
	return asn1.Marshal(pkix)
}

// unmarshalECDSAPublicKey parses a secp256k1 public key from DER format.
func unmarshalECDSAPublicKey(derBytes []byte) (*ecdsa.PublicKey, error) {
	var pkInfo publicKeyInfo

	// Decode DER bytes into publicKeyInfo structure
	_, err := asn1.Unmarshal(derBytes, &pkInfo)
	if err != nil {
		return nil, err
	}

	namedCurveOID := new(asn1.ObjectIdentifier)
	_, err = asn1.Unmarshal(pkInfo.Algorithm.Parameters.FullBytes, namedCurveOID)
	if err != nil {
		return nil, fmt.Errorf("x509: failed to parse ECDSA parameters as named curve: %w", err)
	}

	if !namedCurveOID.Equal(oidNamedCurveSECP256K1) {
		return nil, errors.New("x509: unsupported elliptic curve")
	}

	asn1Data := pkInfo.PublicKey.RightAlign()
	if asn1Data[0] != 4 { // uncompressed form
		return nil, errors.New("x509: only uncompressed keys are supported")
	}

	// Decode the public key point
	curve := secp256k1.S256()
	x, y := curve.Unmarshal(pkInfo.PublicKey.Bytes)
	if x == nil || y == nil {
		return nil, fmt.Errorf("invalid public key")
	}

	return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil
}

// ConvertCompactRecoverableSignatureToDER converts an Ethereum signature in
// [R || S || V] format to DER format.
func convertCompactRecoverableSignatureToDER(sig []byte) ([]byte, error) {
	// Ensure the signature is the correct length (65 bytes: R=32, S=32, V=1)
	if len(sig) != 65 {
		return nil, fmt.Errorf("invalid signature length: expected 65 bytes, got %d", len(sig))
	}

	// Extract R and S components
	r := new(big.Int).SetBytes(sig[:32])   // First 32 bytes
	s := new(big.Int).SetBytes(sig[32:64]) // Next 32 bytes

	// Create a struct representing the DER sequence
	derSignature := struct {
		R *big.Int
		S *big.Int
	}{
		R: r,
		S: s,
	}

	// Marshal the struct into DER
	derBytes, err := asn1.Marshal(derSignature)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal DER: %w", err)
	}

	return derBytes, nil
}
