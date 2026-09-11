package service

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

// SignMessageArgs identifies an authorized signer and the message to sign using EIP-191.
type SignMessageArgs struct {
	Message       hexutil.Bytes   `json:"message"`
	SenderAddress *common.Address `json:"senderAddress"`
}
