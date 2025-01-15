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
