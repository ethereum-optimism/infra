//go:generate mockgen -destination=mock_kms.go -package=provider github.com/ethereum-optimism/infra/op-signer/provider GCPKMSClient
package provider

import (
	"bytes"
	"context"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"fmt"
	"hash/crc32"
	"math/big"

	kms "cloud.google.com/go/kms/apiv1"
	"cloud.google.com/go/kms/apiv1/kmspb"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto/secp256k1"
	"github.com/ethereum/go-ethereum/log"
	gax "github.com/googleapis/gax-go"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

var (
	oidPublicKeyECDSA      = asn1.ObjectIdentifier{1, 2, 840, 10045, 2, 1}
	oidNamedCurveSECP256K1 = asn1.ObjectIdentifier{1, 3, 132, 0, 10}
)

type publicKeyInfo struct {
	Raw       asn1.RawContent
	Algorithm pkix.AlgorithmIdentifier
	PublicKey asn1.BitString
}

type GCPKMSClient interface {
	GetPublicKey(ctx context.Context, req *kmspb.GetPublicKeyRequest, opts ...gax.CallOption) (*kmspb.PublicKey, error)
	AsymmetricSign(context context.Context, req *kmspb.AsymmetricSignRequest, opts ...gax.CallOption) (*kmspb.AsymmetricSignResponse, error)
}

type GCPKMSSignatureProvider struct {
	logger log.Logger
	client GCPKMSClient
}

func NewGCPKMSSignatureProvider(logger log.Logger) (SignatureProvider, error) {
	ctx := context.Background()
	client, err := kms.NewKeyManagementClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize GCP KMS client: %w", err)
	}
	return &GCPKMSSignatureProvider{logger, client}, nil
}

func NewGCPKMSSignatureProviderWithClient(logger log.Logger, client GCPKMSClient) SignatureProvider {
	return &GCPKMSSignatureProvider{logger, client}
}

func crc32c(data []byte) uint32 {
	t := crc32.MakeTable(crc32.Castagnoli)
	return crc32.Checksum(data, t)
}

func createSignRequestFromDigest(keyName string, digest []byte) *kmspb.AsymmetricSignRequest {
	digestCRC32C := crc32c(digest)
	return &kmspb.AsymmetricSignRequest{
		Name: keyName,
		Digest: &kmspb.Digest{
			Digest: &kmspb.Digest_Sha256{
				Sha256: digest,
			},
		},
		DigestCrc32C: wrapperspb.Int64(int64(digestCRC32C)),
	}
}

// SignDigest signs the digest with a given GCP KMS keyname and returns a compact recoverable signature.
// If the keyName provided is not a EC_SIGN_SECP256K1_SHA256 key, the result will be an error.
func (c *GCPKMSSignatureProvider) SignDigest(
	ctx context.Context,
	keyName string,
	digest []byte,
) ([]byte, error) {
	publicKey, err := c.GetPublicKey(ctx, keyName)
	if err != nil {
		return nil, fmt.Errorf("failed to get public key: %w", err)
	}

	request := createSignRequestFromDigest(keyName, digest)
	result, err := c.client.AsymmetricSign(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("GCP KMS sign request failed: %w", err)
	}
	if result.Name != request.Name {
		return nil, errors.New("GCP KMS sign request corrupted in transit")
	}
	if !result.VerifiedDigestCrc32C {
		return nil, errors.New("GCP KMS sign request corrupted in transit")
	}
	if int64(crc32c(result.Signature)) != result.SignatureCrc32C.Value {
		return nil, errors.New("GCP KMS sign response corrupted in transit")
	}

	c.logger.Debug(fmt.Sprintf("der signature: %s", hexutil.Encode(result.Signature)))

	return convertToCompactRecoverableSignature(result.Signature, digest, publicKey)
}

func convertToCompactRecoverableSignature(derSignature, digest, publicKey []byte) ([]byte, error) {
	signature, err := convertToCompactSignature(derSignature)
	if err != nil {
		// should never happen
		return nil, fmt.Errorf("failed to convert to compact signature: %w", err)
	}

	// NOTE: so far I haven't seen GCP KMS produce a malleable signature
	// but if it does happen, this can be handled as a retryable error by the client
	if err := compactSignatureMalleabilityCheck(signature); err != nil {
		// should never happen
		return nil, fmt.Errorf("signature failed malleability check: %w", err)
	}

	if !secp256k1.VerifySignature(publicKey, digest, signature) {
		// should never happen
		return nil, errors.New("signature could not be verified with public key")
	}

	recId, err := calculateRecoveryID(signature, digest, publicKey)
	if err != nil {
		// should never happen
		return nil, fmt.Errorf("failed to calculate recovery id: %w", err)
	}

	signature = append(signature, byte(recId))

	return signature, nil
}

// convertToCompactSignature compacts a DER signature output from KMS (>70 bytes) into 64 bytes
func convertToCompactSignature(derSignature []byte) ([]byte, error) {
	var parsedSig struct{ R, S *big.Int }
	if _, err := asn1.Unmarshal(derSignature, &parsedSig); err != nil {
		return nil, fmt.Errorf("asn1.Unmarshal error: %w", err)
	}

	curveOrderLen := 32
	signature := make([]byte, 2*curveOrderLen)

	// if S is non-canonical, lower it
	curveOrder := secp256k1.S256().Params().Params().N
	if parsedSig.S.Cmp(new(big.Int).Div(curveOrder, big.NewInt(2))) > 0 {
		parsedSig.S = new(big.Int).Sub(curveOrder, parsedSig.S)
	}

	// left pad R and S with zeroes
	rBytes := parsedSig.R.Bytes()
	sBytes := parsedSig.S.Bytes()
	copy(signature[curveOrderLen-len(rBytes):], rBytes)
	copy(signature[len(signature)-len(sBytes):], sBytes)

	return signature, nil
}

// calculateRecoveryID calculates the signature recovery id (65th byte, [0-3])
func calculateRecoveryID(signature, digest, pubKey []byte) (int, error) {
	recId := -1
	var errorRes error

	for i := 0; i < 4; i++ {
		recSig := append(signature, byte(i))
		publicKey, err := secp256k1.RecoverPubkey(digest, recSig)
		if err != nil {
			errorRes = err
			continue
		}
		if bytes.Equal(publicKey, pubKey) {
			recId = i
			break
		}
	}

	if recId == -1 {
		return recId, fmt.Errorf("failed to calculate recovery id, should never happen: %w", errorRes)
	}
	return recId, nil
}

// compactSignatureMalleabilityCheck checks if signature can be used to produce a new valid signature
// pulled from go-ethereum/crypto/secp256k1/secp256_test.go
// see: http://coders-errand.com/malleability-ecdsa-signatures/
func compactSignatureMalleabilityCheck(sig []byte) error {
	b := int(sig[32])
	if b < 0 {
		return fmt.Errorf("highest bit is negative: %d", b)
	}
	if ((b >> 7) == 1) != ((b & 0x80) == 0x80) {
		return fmt.Errorf("highest bit: %d bit >> 7: %d", b, b>>7)
	}
	if (b & 0x80) == 0x80 {
		return fmt.Errorf("highest bit: %d bit & 0x80: %d", b, b&0x80)
	}
	return nil
}

// GetPublicKey returns a decoded secp256k1 public key.
func (c *GCPKMSSignatureProvider) GetPublicKey(
	ctx context.Context,
	keyName string,
) ([]byte, error) {
	request := kmspb.GetPublicKeyRequest{
		Name: keyName,
	}

	result, err := c.client.GetPublicKey(ctx, &request)
	if err != nil {
		return nil, fmt.Errorf("GCP KMS get public key request failed: %w", err)
	}

	key := []byte(result.Pem)
	if int64(crc32c(key)) != result.PemCrc32C.Value {
		return nil, errors.New("GCP KMS public key response corrupted in transit")
	}

	return decodePublicKeyPEM(key)
}

// decodePublicKeyPEM decodes a PEM ECDSA public key with secp256k1 curve
func decodePublicKeyPEM(key []byte) ([]byte, error) {
	block, rest := pem.Decode([]byte(key))
	if len(rest) > 0 {
		return nil, fmt.Errorf("crypto: failed to parse PEM string, not all bytes in PEM key were decoded: %x", rest)
	}

	pkBytes, err := x509ParseECDSAPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("crypto: failed to parse PEM string: %w", err)
	}

	return pkBytes, err
}

// x509ParseECDSAPublicKey parses a DER-encoded public key and ensures secp256k1 curve
func x509ParseECDSAPublicKey(derBytes []byte) ([]byte, error) {
	var pki publicKeyInfo
	if rest, err := asn1.Unmarshal(derBytes, &pki); err != nil {
		return nil, err
	} else if len(rest) != 0 {
		return nil, errors.New("x509: trailing data after ASN.1 of public-key")
	}

	if !pki.Algorithm.Algorithm.Equal(oidPublicKeyECDSA) {
		return nil, errors.New("x509: unknown public key algorithm")
	}

	asn1Data := pki.PublicKey.RightAlign()
	paramsData := pki.Algorithm.Parameters.FullBytes
	namedCurveOID := new(asn1.ObjectIdentifier)
	rest, err := asn1.Unmarshal(paramsData, namedCurveOID)
	if err != nil {
		return nil, fmt.Errorf("x509: failed to parse ECDSA parameters as named curve: %w", err)
	}
	if len(rest) != 0 {
		return nil, errors.New("x509: trailing data after ECDSA parameters")
	}

	if !namedCurveOID.Equal(oidNamedCurveSECP256K1) {
		return nil, errors.New("x509: unsupported elliptic curve")
	}

	if asn1Data[0] != 4 { // uncompressed form
		return nil, errors.New("x509: only uncompressed keys are supported")
	}

	return asn1Data, nil
}
