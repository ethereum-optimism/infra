package client

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rpc"

	"github.com/ethereum-optimism/optimism/op-service/signer"
	optls "github.com/ethereum-optimism/optimism/op-service/tls"
)

type SignerClient struct {
	client *rpc.Client
	status string
	logger log.Logger
}

func NewSignerClient(logger log.Logger, endpoint string, tlsConfig optls.CLIConfig) (*SignerClient, error) {

	caCert, err := os.ReadFile(tlsConfig.TLSCaCert)
	if err != nil {
		return nil, fmt.Errorf("failed to read tls.ca: %w", err)
	}
	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCert)

	cert, err := tls.LoadX509KeyPair(tlsConfig.TLSCert, tlsConfig.TLSKey)
	if err != nil {
		return nil, fmt.Errorf("failed to read tls.cert or tls.key: %w", err)
	}

	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:      caCertPool,
				Certificates: []tls.Certificate{cert},
			},
		},
	}

	rpcClient, err := rpc.DialOptions(context.Background(), endpoint, rpc.WithHTTPClient(httpClient))
	if err != nil {
		return nil, err
	}

	signerClient := &SignerClient{logger: logger, client: rpcClient}
	// Check if reachable
	version, err := signerClient.pingVersion()
	if err != nil {
		return nil, err
	}
	signerClient.status = fmt.Sprintf("ok [version=%v]", version)
	return signerClient, nil
}

func (s *SignerClient) pingVersion() (string, error) {
	var v string
	if err := s.client.Call(&v, "health_status"); err != nil {
		return "", err
	}
	return v, nil
}

func (s *SignerClient) SignTransaction(
	ctx context.Context,
	tx *types.Transaction,
) (*types.Transaction, error) {

	args := signer.NewTransactionArgsFromTransaction(tx.ChainId(), nil, tx)

	var result hexutil.Bytes

	if err := s.client.Call(&result, "eth_signTransaction", args); err != nil {
		return nil, fmt.Errorf("eth_signTransaction failed: %w", err)
	}

	signed := &types.Transaction{}
	if err := signed.UnmarshalBinary(result); err != nil {
		return nil, err
	}

	return signed, nil
}

func (s *SignerClient) SignBlockPayload(
	ctx context.Context,
	signingHash common.Hash,
) ([]byte, error) {
	var result []byte

	if err := s.client.Call(&result, "eth_signBlockPayload", signingHash); err != nil {
		return []byte{}, fmt.Errorf("eth_signTransaction failed: %w", err)
	}

	return result, nil
}
