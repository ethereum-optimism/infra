package provider

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"io"
	"testing"

	gomock "github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"cloud.google.com/go/kms/apiv1/kmspb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/crypto/secp256k1"
	"github.com/ethereum/go-ethereum/log"
)

const (
	GCPKMSPemPublicKey = `-----BEGIN PUBLIC KEY-----
MFYwEAYHKoZIzj0CAQYFK4EEAAoDQgAEQpdToIk9lwjBdl0VcqXl7AwqhB9NwRf+
IHRNqIUNa8vAH/5l5MGXO/qVT5D/4sOTfpd29BQAkDVOgTAneA2Vrg==
-----END PUBLIC KEY-----`
)

func generateKey() *ecdsa.PrivateKey {
	key, err := ecdsa.GenerateKey(secp256k1.S256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	return key
}

// pulled from go-ethereum/crypto/secp256k1/secp256_test.go
func csprngEntropy(n int) []byte {
	buf := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		panic("reading from crypto/rand failed: " + err.Error())
	}
	return buf
}

func TestGCPKMS_SignDigest(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := NewMockGCPKMSClient(ctrl)
	provider := NewGCPKMSSignatureProviderWithClient(log.Root(), mockClient)

	keyName := "keyName"
	digest, _ := hexutil.Decode("0x8dabbae6d856bb7ab93bc35b74c1303975a3f70f942d033e8591a9f8c897ae42")
	derSignature, _ := hexutil.Decode("0x30450221008680faa49fd6653d273fb34393a47efac44b8f4a4de62bbe11a65ee53739e9bb0220350897677c32d67dc1e520d7458c5cca4a7fe49a3e9d74bdef1ec96836148661")
	pemPublicKey := []byte(GCPKMSPemPublicKey)

	var tests = []struct {
		testName                 string
		keyName                  string
		digest                   []byte
		respError                error
		respVerifiedDigestCrc32C bool
		respSignatureCrc32       uint32
		wantErr                  bool
	}{
		{"happy path", keyName, digest, nil, true, crc32c(derSignature), false},
		{"req failure", keyName, digest, assert.AnError, true, crc32c(derSignature), true},
		{"invalid req keyName", "wrongKeyName", digest, nil, true, crc32c(derSignature), true},
		{"invalid req crc32", keyName, digest, nil, false, crc32c(derSignature), true},
		{"invalid resp crc32", keyName, digest, nil, true, crc32c([]byte("")), true},
	}

	for _, tt := range tests {
		t.Run(tt.testName, func(t *testing.T) {
			signRequest := createSignRequestFromDigest(tt.keyName, tt.digest)
			mockClient.EXPECT().AsymmetricSign(gomock.Any(), signRequest).Return(
				&kmspb.AsymmetricSignResponse{
					Name:                 keyName,
					Signature:            derSignature,
					VerifiedDigestCrc32C: tt.respVerifiedDigestCrc32C,
					SignatureCrc32C:      wrapperspb.Int64(int64(tt.respSignatureCrc32)),
				},
				tt.respError,
			)

			getPublicKeyRequest := &kmspb.GetPublicKeyRequest{Name: tt.keyName}
			mockClient.EXPECT().GetPublicKey(gomock.Any(), getPublicKeyRequest).Return(
				&kmspb.PublicKey{
					Pem:       string(pemPublicKey),
					PemCrc32C: wrapperspb.Int64(int64(crc32c(pemPublicKey))),
				},
				nil,
			)
			publicKey, _ := decodePublicKeyPEM(pemPublicKey)
			wantSignature, _ := convertToCompactRecoverableSignature(derSignature, tt.digest, publicKey)

			signature, err := provider.SignDigest(context.TODO(), tt.keyName, tt.digest)
			if !tt.wantErr {
				assert.Nil(t, err)
				assert.Equal(t, wantSignature, signature)
				// make sure recoverable pubkey is as expected
				recoveredPublicKey, err := crypto.Ecrecover(tt.digest, signature)
				assert.Nil(t, err)
				assert.Equal(t, publicKey, recoveredPublicKey)
			} else {
				assert.Error(t, err)
				assert.Nil(t, signature)
			}
		})
	}
}

func TestGCPKMS_GetPublicKey(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := NewMockGCPKMSClient(ctrl)
	provider := NewGCPKMSSignatureProviderWithClient(log.Root(), mockClient)

	keyName := "keyName"
	pemPublicKey := []byte(GCPKMSPemPublicKey)
	wantPublicKey, _ := hexutil.Decode("0x04429753a0893d9708c1765d1572a5e5ec0c2a841f4dc117fe20744da8850d6bcbc01ffe65e4c1973bfa954f90ffe2c3937e9776f4140090354e813027780d95ae")

	var tests = []struct {
		testName     string
		keyName      string
		respError    error
		respPemCrc32 uint32
		wantErr      bool
	}{
		{"happy path", keyName, nil, crc32c(pemPublicKey), false},
		{"req failure", keyName, assert.AnError, crc32c(pemPublicKey), false},
		{"invalid resp crc32", keyName, assert.AnError, crc32c([]byte("")), true},
	}
	for _, tt := range tests {
		t.Run(tt.testName, func(t *testing.T) {
			request := &kmspb.GetPublicKeyRequest{Name: tt.keyName}
			mockClient.EXPECT().GetPublicKey(gomock.Any(), request).Return(
				&kmspb.PublicKey{
					Pem:       string(pemPublicKey),
					PemCrc32C: wrapperspb.Int64(int64(tt.respPemCrc32)),
				},
				nil,
			)
			publicKey, err := provider.GetPublicKey(context.TODO(), tt.keyName)
			if !tt.wantErr {
				assert.Nil(t, err)
				assert.Equal(t, wantPublicKey, publicKey)
			} else {
				assert.Error(t, err)
				assert.Nil(t, publicKey)
			}
		})
	}
}

// TestVerifySignatureFromRecoveredPublicKey tests that the compact signature can
// recover a publicKey that can be used to verify the original DER signature.
// Since all other reference implementations produce compact, recoverable signatures already,
// this serves as the test that converToCompactSignture and calculateRecoveryID produce expected results,
func TestVerifySignatureFromRecoveredPublicKey(t *testing.T) {
	key := generateKey()
	pubKey := crypto.FromECDSAPub(&key.PublicKey)

	const TestCount = 1000
	for i := 0; i < TestCount; i++ {
		digest := csprngEntropy(32)
		derSig, err := ecdsa.SignASN1(rand.Reader, key, digest)
		if err != nil {
			panic(err)
		}

		sig, err := convertToCompactSignature(derSig)
		assert.Nil(t, err)
		assert.Len(t, sig, 64)
		assert.Nil(t, compactSignatureMalleabilityCheck(sig))

		recId, err := calculateRecoveryID(sig, digest, pubKey)
		assert.Nil(t, err)
		assert.GreaterOrEqual(t, recId, 0)
		assert.Less(t, recId, 4)

		sig = append(sig, byte(recId))
		recoveredRawPubKey, err := secp256k1.RecoverPubkey(digest, sig)
		assert.Nil(t, err)

		recoveredPubKey, err := crypto.UnmarshalPubkey(recoveredRawPubKey)
		require.NoError(t, err)

		assert.True(t, ecdsa.VerifyASN1(recoveredPubKey, digest, derSig))
	}
}
