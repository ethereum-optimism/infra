package proxyd

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRPCResJSON(t *testing.T) {
	tests := []struct {
		name string
		in   *RPCRes
		out  string
	}{
		{
			"string result",
			&RPCRes{
				JSONRPC: JSONRPCVersion,
				Result:  "foobar",
				ID:      []byte("123"),
			},
			`{"jsonrpc":"2.0","result":"foobar","id":123}`,
		},
		{
			"object result",
			&RPCRes{
				JSONRPC: JSONRPCVersion,
				Result: struct {
					Str string `json:"str"`
				}{
					"test",
				},
				ID: []byte("123"),
			},
			`{"jsonrpc":"2.0","result":{"str":"test"},"id":123}`,
		},
		{
			"nil result",
			&RPCRes{
				JSONRPC: JSONRPCVersion,
				Result:  nil,
				ID:      []byte("123"),
			},
			`{"jsonrpc":"2.0","result":null,"id":123}`,
		},
		{
			"error result without data",
			&RPCRes{
				JSONRPC: JSONRPCVersion,
				Error: &RPCErr{
					Code:    1234,
					Message: "test err",
				},
				ID: []byte("123"),
			},
			`{"jsonrpc":"2.0","error":{"code":1234,"message":"test err"},"id":123}`,
		},
		{
			"error result with data",
			&RPCRes{
				JSONRPC: JSONRPCVersion,
				Error: &RPCErr{
					Code:    1234,
					Message: "test err",
					Data:    []byte(`"revert"`),
				},
				ID: []byte("123"),
			},
			`{"jsonrpc":"2.0","error":{"code":1234,"message":"test err","data":"revert"},"id":123}`,
		},
		{
			"string ID",
			&RPCRes{
				JSONRPC: JSONRPCVersion,
				Result:  "foobar",
				ID:      []byte("\"123\""),
			},
			`{"jsonrpc":"2.0","result":"foobar","id":"123"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := json.Marshal(tt.in)
			require.NoError(t, err)
			require.Equal(t, tt.out, string(out))
		})
	}
}

func TestIsPendingRequest(t *testing.T) {
	tests := []struct {
		name     string
		in       *RPCReq
		expected bool
	}{
		{
			"eth_getTransactionReceipt",
			&RPCReq{
				Method: "eth_getTransactionReceipt",
				Params: mustMarshalJSON([]string{"0x00"}),
			},
			true,
		},
		{
			"eth_getBlockByNumber with pending block number and detail flag set",
			&RPCReq{
				Method: "eth_getBlockByNumber",
				Params: mustMarshalJSON([]any{"pending", "true"}),
			},
			true,
		},
		{
			"eth_getBlockByNumber with pending block number and detail flag unset",
			&RPCReq{
				Method: "eth_getBlockByNumber",
				Params: mustMarshalJSON([]any{"pending", false}),
			},
			false,
		},
		{
			"eth_getBlockByNumber with latest block number and detail flag set",
			&RPCReq{
				Method: "eth_getBlockByNumber",
				Params: mustMarshalJSON([]any{"latest", false}),
			},
			false,
		},
		{
			"eth_getBalance with pending block number",
			&RPCReq{
				Method: "eth_getBalance",
				Params: mustMarshalJSON([]string{"0x01", "pending"}),
			},
			true,
		},
		{
			"eth_getBalance with latest block number",
			&RPCReq{
				Method: "eth_getBalance",
				Params: mustMarshalJSON([]string{"0x01", "latest"}),
			},
			false,
		},
		{
			"eth_getTransactionCount with pending block number",
			&RPCReq{
				Method: "eth_getTransactionCount",
				Params: mustMarshalJSON([]string{"0x01", "pending"}),
			},
			true,
		},
		{
			"eth_getTransactionCount with latest block number",
			&RPCReq{
				Method: "eth_getTransactionCount",
				Params: mustMarshalJSON([]string{"0x01", "latest"}),
			},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, IsPendingRequest(tt.in), tt.expected)
		})
	}
}
