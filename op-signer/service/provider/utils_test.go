package provider

import (
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMarshalAndUnmarshalECDSAPublicKey tests that a secp256k1 public key can be
// marshaled and unmarshaled correctly.
func TestMarshalAndUnmarshalECDSAPublicKey(t *testing.T) {
	privateKey, err := crypto.GenerateKey()
	require.NoError(t, err)

	publicKeyDER, err := marshalECDSAPublicKey(&privateKey.PublicKey)
	require.NoError(t, err)

	unmarshalledPublicKey, err := unmarshalECDSAPublicKey(publicKeyDER)
	require.NoError(t, err)

	assert.Equal(t, privateKey.PublicKey, *unmarshalledPublicKey)
}

// TestMarshalAndParseECDSAPublicKey tests that a secp256k1 public key can be
// marshaled and parsed using the existing x509ParseECDSAPublicKey function. This
// ensures that the public key is in the correct format for the AWS KMS client.
func TestMarshalAndParseECDSAPublicKey(t *testing.T) {
	privateKey, err := crypto.GenerateKey()
	require.NoError(t, err)

	publicKeyDER, err := marshalECDSAPublicKey(&privateKey.PublicKey)
	require.NoError(t, err)

	_, err = x509ParseECDSAPublicKey(publicKeyDER)
	require.NoError(t, err)
}

// TestConvertEthereumSignatureToDER tests that an Ethereum signature can be
// converted to DER format. This test also ensures that our DER encoding is
// correct by converting from DER back to a compact sig using the existing
// convertToCompactSignature function.
func TestConvertEthereumSignatureToDER(t *testing.T) {
	privateKey, _ := crypto.GenerateKey()

	// Sign a message using the private key so we can later verify the signature
	message := []byte("test message to sign")
	digest := crypto.Keccak256Hash(message).Bytes()

	signature, err := crypto.Sign(digest, privateKey)
	require.NoError(t, err)

	derSignature, err := convertCompactRecoverableSignatureToDER(signature)
	require.NoError(t, err)

	convertedSignature, err := convertToCompactSignature(derSignature)
	require.NoError(t, err)
	assert.Equal(t, signature[:len(signature)-1], convertedSignature)
}

// TestConvertToEthereumSignatureRecoverable tests that an Ethereum signature
// can be converted to a recoverable signature. This test also ensures that
// our DER encoding is correct by converting from DER back to a compact
// verifiable sig using our convertToCompactRecoverableSignature function.
func TestConvertToEthereumSignatureRecoverable(t *testing.T) {
	privateKey, _ := crypto.GenerateKey()

	// Sign a message using the private key so we can later verify the signature
	message := []byte("test message to sign")
	digest := crypto.Keccak256Hash(message).Bytes()

	signature, err := crypto.Sign(digest, privateKey)
	require.NoError(t, err)

	derSignature, err := convertCompactRecoverableSignatureToDER(signature)
	require.NoError(t, err)

	publicKeyDER, err := marshalECDSAPublicKey(&privateKey.PublicKey)
	require.NoError(t, err)

	publicKeyBytes, err := x509ParseECDSAPublicKey(publicKeyDER)
	require.NoError(t, err)

	convertedSignature, err := convertToCompactRecoverableSignature(derSignature, digest, publicKeyBytes)
	require.NoError(t, err)
	assert.Equal(t, signature, convertedSignature)
}
