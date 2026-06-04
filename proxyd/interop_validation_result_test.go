package proxyd

import (
	"errors"
	"testing"

	interopErrors "github.com/ethereum-optimism/optimism/op-core/interop"
	"github.com/stretchr/testify/require"
)

func TestInteropValidationResult(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "nil error is passed",
			err:  nil,
			want: "passed",
		},
		{
			name: "interop filter rejection (4xx RPCErr) is filtered",
			err:  interopRPCErrorMap[interopErrors.ErrSkipped], // 422
			want: "filtered",
		},
		{
			name: "access list out of bounds (413 RPCErr) is filtered",
			err:  ErrInteropAccessListOutOfBounds, // 413
			want: "filtered",
		},
		{
			name: "failsafe enabled (503 RPCErr) is errored",
			err:  interopRPCErrorMap[interopErrors.ErrFailsafeEnabled], // 503
			want: "errored",
		},
		{
			name: "internal RPCErr (500) is errored",
			err:  &RPCErr{Code: JSONRPCErrorInternal, HTTPErrorCode: 500},
			want: "errored",
		},
		{
			name: "non-RPCErr error is errored",
			err:  errors.New("interop filter backend unavailable"),
			want: "errored",
		},
		{
			name: "no interop filter backend (non-RPCErr) is errored",
			err:  interopErrors.ErrNoRPCSource,
			want: "errored",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, interopValidationResult(tt.err))
		})
	}
}

func TestInteropValidationReason(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "nil error is none",
			err:  nil,
			want: "none",
		},
		{
			name: "skipped maps to its rpc error code",
			err:  interopRPCErrorMap[interopErrors.ErrSkipped],
			want: "-320500",
		},
		{
			name: "conflict maps to its rpc error code",
			err:  interopRPCErrorMap[interopErrors.ErrConflict],
			want: "-320600",
		},
		{
			name: "access list out of bounds maps to its rpc error code",
			err:  ErrInteropAccessListOutOfBounds,
			want: "-32022",
		},
		{
			name: "failsafe maps to its rpc error code",
			err:  interopRPCErrorMap[interopErrors.ErrFailsafeEnabled],
			want: "-32602",
		},
		{
			name: "non-RPCErr is internal",
			err:  errors.New("interop filter backend unavailable"),
			want: "internal",
		},
		{
			name: "arbitrary RPCErr maps to its code",
			err:  &RPCErr{Code: -39999, HTTPErrorCode: 400},
			want: "-39999",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, interopValidationReason(tt.err))
		})
	}
}
