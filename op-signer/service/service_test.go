package service

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/holiman/uint256"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rpc"

	"github.com/ethereum-optimism/infra/op-signer/service/provider"
	clientSigner "github.com/ethereum-optimism/optimism/op-service/signer"
)

func createEIP1559Tx() *types.Transaction {
	aa := common.HexToAddress("0x000000000000000000000000000000000000aaaa")
	accesses := types.AccessList{types.AccessTuple{
		Address:     aa,
		StorageKeys: []common.Hash{{0}},
	}}
	txdata := &types.DynamicFeeTx{
		ChainID:    params.AllEthashProtocolChanges.ChainID,
		Nonce:      0,
		To:         &aa,
		Gas:        30000,
		GasFeeCap:  big.NewInt(1),
		GasTipCap:  big.NewInt(1),
		AccessList: accesses,
		Data:       []byte{},
		Value:      big.NewInt(1),
	}
	tx := types.NewTx(txdata)
	return tx
}

func createBlobTx() *types.Transaction {
	aa := common.HexToAddress("0x000000000000000000000000000000000000aaaa")
	accesses := types.AccessList{types.AccessTuple{
		Address:     aa,
		StorageKeys: []common.Hash{{0}},
	}}

	txdata := &types.BlobTx{
		ChainID:    uint256.MustFromBig(params.AllEthashProtocolChanges.ChainID),
		Nonce:      0,
		To:         aa,
		Gas:        30000,
		GasFeeCap:  uint256.NewInt(1),
		GasTipCap:  uint256.NewInt(1),
		AccessList: accesses,
		Data:       []byte{},
		Value:      uint256.NewInt(1),
		BlobFeeCap: uint256.NewInt(1),
		BlobHashes: []common.Hash{common.HexToHash("c0ffee")},
	}
	tx := types.NewTx(txdata)
	return tx
}

var config = SignerServiceConfig{
	Auth: []AuthConfig{
		{ClientName: "client.oplabs.co", KeyName: "keyName"},
		{ClientName: "alt-client.oplabs.co", KeyName: "altKeyName"},
		{ClientName: "authorized-to.oplabs.co", KeyName: "keyName", ToAddresses: []string{"0x000000000000000000000000000000000000Aaaa"}},
		{ClientName: "unauthorized-to.oplabs.co", KeyName: "keyName", ToAddresses: []string{"0x000000000000000000000000000000000000bbbb"}},
		{ClientName: "within-max-value.oplabs.co", KeyName: "keyName", MaxValue: hexutil.EncodeBig(big.NewInt(2))},
		{ClientName: "exceeds-max-value.oplabs.co", KeyName: "keyName", MaxValue: hexutil.EncodeBig(big.NewInt(0))},
	},
}

type testCase struct {
	name     string
	template func() *types.Transaction
}

var testTxs = []testCase{
	{"regular", createEIP1559Tx},
	{"blob-tx", createBlobTx},
}

func TestSignTransaction(t *testing.T) {
	for _, tc := range testTxs {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			testSignTransaction(t, tc.template())
		})
	}
}

func testSignTransaction(t *testing.T, tx *types.Transaction) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	signer := types.LatestSignerForChainID(tx.ChainId())
	digest := signer.Hash(tx).Bytes()

	priv, err := crypto.GenerateKey()
	require.NoError(t, err)

	sender := crypto.PubkeyToAddress(priv.PublicKey)

	signature, err := crypto.Sign(digest, priv)
	require.NoError(t, err)

	args := clientSigner.NewTransactionArgsFromTransaction(tx.ChainId(), nil, tx)
	missingNonce := clientSigner.NewTransactionArgsFromTransaction(tx.ChainId(), nil, tx)
	missingNonce.Nonce = nil

	validFrom := clientSigner.NewTransactionArgsFromTransaction(tx.ChainId(), nil, tx)
	validFrom.From = &sender

	invalidFrom := clientSigner.NewTransactionArgsFromTransaction(tx.ChainId(), nil, tx)
	random := common.HexToAddress("1234")
	invalidFrom.From = &random

	// signature, _ := hexutil.Decode("0x5392c93b50eb9e3412ab43d378048d4f7d644f3cea02acb529f07e2babba1d3a332377f4abe24a40030b3ff6bff3413a44364aad4665f4e24117466328ce8d3600")

	tests := []struct {
		testName    string
		args        clientSigner.TransactionArgs
		digest      []byte
		clientName  string
		wantKeyName string
		wantErrCode int
	}{
		{"happy path", *args, digest, "client.oplabs.co", "keyName", 0},
		{"nonce not specified", *missingNonce, digest, "client.oplabs.co", "keyName", -32010},
		{"happy path - different client and key", *args, digest, "alt-client.oplabs.co", "altKeyName", 0},
		{"client not authorized", *args, digest, "forbidden-client.oplabs.co", "keyName", 403},
		{"client empty", *args, digest, "", "", 403},
		{"authorized to address", *args, digest, "authorized-to.oplabs.co", "keyName", 0},
		{"unauthorized to address", *args, digest, "unauthorized-to.oplabs.co", "keyName", -32011},
		{"within max value", *args, digest, "within-max-value.oplabs.co", "keyName", 0},
		{"exceeds max value", *args, digest, "exceeds-max-value.oplabs.co", "keyName", -32011},
		{"valid from", *validFrom, digest, "client.oplabs.co", "keyName", 0},
		{"invalid from", *invalidFrom, digest, "client.oplabs.co", "keyName", -32010},
	}
	for _, tt := range tests {
		t.Run(tt.testName, func(t *testing.T) {
			mockSignatureProvider := provider.NewMockSignatureProvider(ctrl)
			service := NewSignerServiceWithProvider(log.Root(), config, mockSignatureProvider)

			ctx := context.WithValue(context.TODO(), clientInfoContextKey{}, ClientInfo{ClientName: tt.clientName})
			if tt.wantErrCode == 0 || tt.testName == "invalid from" {
				mockSignatureProvider.EXPECT().
					SignDigest(ctx, tt.wantKeyName, tt.digest).
					Return(signature, nil)
			}
			resp, err := service.eth.SignTransaction(ctx, tt.args)
			if tt.wantErrCode == 0 {
				assert.Nil(t, err)
				if assert.NotNil(t, resp) {
					assert.NotEmpty(t, resp)
				}
			} else {
				assert.NotNil(t, err)
				assert.Nil(t, resp)
				var rpcErr rpc.Error
				var httpErr rpc.HTTPError
				if errors.As(err, &rpcErr) {
					assert.Equal(t, tt.wantErrCode, rpcErr.ErrorCode())
				} else if errors.As(err, &httpErr) {
					assert.Equal(t, tt.wantErrCode, httpErr.StatusCode)
				} else {
					assert.Fail(t, "returned error is not an rpc.Error or rpc.HTTPError")
				}
			}
		})
	}
}

func TestSignBlockPayload(t *testing.T) {
	priv, err := crypto.GenerateKey()
	require.NoError(t, err)

	sender := crypto.PubkeyToAddress(priv.PublicKey)

	var blockPayloadConfig = SignerServiceConfig{
		Auth: []AuthConfig{
			{ClientName: "client.oplabs.co", KeyName: "keyName", ChainID: 1, FromAddress: sender},
			{ClientName: "invalid-chainId-client.oplabs.co", KeyName: "keyName", ChainID: 2, FromAddress: sender},
			{ClientName: "alt-client.oplabs.co", KeyName: "altKeyName", ChainID: 1, FromAddress: sender},
			{ClientName: "unspecified-sender-client.oplabs.co", KeyName: "keyName", ChainID: 1},
		},
	}

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	blockPayloadArgs := clientSigner.NewBlockPayloadArgs([32]byte{}, big.NewInt(1), []byte("c0ffee"), &sender)
	signingHash, err := blockPayloadArgs.ToSigningHash()
	require.NoError(t, err)

	signature, err := crypto.Sign(signingHash.Bytes(), priv)
	require.NoError(t, err)

	missingChainId := clientSigner.NewBlockPayloadArgs([32]byte{}, nil, []byte("c0ffee"), &sender)

	missingPayloadHash := clientSigner.NewBlockPayloadArgs([32]byte{}, big.NewInt(1), []byte{}, &sender)
	missingPayloadHash.PayloadHash = nil

	random := common.HexToAddress("1234")
	invalidSender := clientSigner.NewBlockPayloadArgs([32]byte{}, big.NewInt(1), []byte("c0ffee"), &random)

	tests := []struct {
		testName    string
		args        clientSigner.BlockPayloadArgs
		signingHash []byte
		clientName  string
		wantKeyName string
		wantErrCode int
	}{
		{"happy path", *blockPayloadArgs, signingHash.Bytes(), "client.oplabs.co", "keyName", 0},
		{"happy path - different client and key", *blockPayloadArgs, signingHash.Bytes(), "alt-client.oplabs.co", "altKeyName", 0},

		{"chainId not specified", *missingChainId, signingHash.Bytes(), "client.oplabs.co", "keyName", -32012},
		{"invalid chainId", *blockPayloadArgs, signingHash.Bytes(), "invalid-chainId-client.oplabs.co", "keyName", -32013},
		{"payload hash not specified", *missingPayloadHash, signingHash.Bytes(), "client.oplabs.co", "keyName", -32012},

		{"unspecified sender", *blockPayloadArgs, signingHash.Bytes(), "unspecified-sender-client.oplabs.co", "keyName", 403},
		{"invalid sender", *invalidSender, signingHash.Bytes(), "client.oplabs.co", "keyName", 403},
		{"client not authorized", *blockPayloadArgs, signingHash.Bytes(), "forbidden-client.oplabs.co", "keyName", 403},
		{"client empty", *blockPayloadArgs, signingHash.Bytes(), "", "", 403},
	}
	for _, tt := range tests {
		t.Run(tt.testName, func(t *testing.T) {
			mockSignatureProvider := provider.NewMockSignatureProvider(ctrl)
			service := NewSignerServiceWithProvider(log.Root(), blockPayloadConfig, mockSignatureProvider)

			ctx := context.WithValue(context.TODO(), clientInfoContextKey{}, ClientInfo{ClientName: tt.clientName})
			if tt.wantErrCode == 0 || tt.testName == "invalid from" {
				mockSignatureProvider.EXPECT().
					SignDigest(ctx, tt.wantKeyName, tt.signingHash).
					Return(signature, nil)
			}
			resp, err := service.opsigner.SignBlockPayload(ctx, tt.args)
			if tt.wantErrCode == 0 {
				assert.Nil(t, err)
				if assert.NotNil(t, resp) {
					assert.NotEmpty(t, resp)
					assert.Equal(t, resp.String(), hexutil.Encode(signature))
				}
			} else {
				assert.NotNil(t, err)
				assert.Nil(t, resp)
				var rpcErr rpc.Error
				var httpErr rpc.HTTPError
				if errors.As(err, &rpcErr) {
					assert.Equal(t, tt.wantErrCode, rpcErr.ErrorCode())
				} else if errors.As(err, &httpErr) {
					assert.Equal(t, tt.wantErrCode, httpErr.StatusCode)
				} else {
					assert.Fail(t, "returned error is not an rpc.Error or rpc.HTTPError")
				}
			}
		})
	}
}
