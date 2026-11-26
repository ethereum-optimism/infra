package op_txproxy

import (
	"encoding/json"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

// Ported from op-geth's core/types/transaction_conditional_test.go.

func uint64Ptr(num uint64) *uint64 { return &num }

func TestTransactionConditionalCost(t *testing.T) {
	tests := []struct {
		name string
		cond TransactionConditional
		cost int
	}{
		{
			name: "empty conditional",
			cond: TransactionConditional{},
			cost: 0,
		},
		{
			name: "block number lookup counts once",
			cond: TransactionConditional{BlockNumberMin: big.NewInt(1), BlockNumberMax: big.NewInt(2)},
			cost: 1,
		},
		{
			name: "timestamp lookup counts once",
			cond: TransactionConditional{TimestampMin: uint64Ptr(0), TimestampMax: uint64Ptr(5)},
			cost: 1,
		},
		{
			name: "default cost per account",
			cond: TransactionConditional{KnownAccounts: KnownAccounts{
				common.Address{19: 1}: KnownAccount{},
				common.Address{19: 2}: KnownAccount{},
			}},
			cost: 2,
		},
		{
			name: "storage root lookup",
			cond: TransactionConditional{KnownAccounts: KnownAccounts{
				common.Address{19: 1}: KnownAccount{StorageRoot: &types.EmptyRootHash},
			}},
			cost: 2,
		},
		{
			name: "cost per storage slot lookup",
			cond: TransactionConditional{KnownAccounts: KnownAccounts{
				common.Address{19: 1}: KnownAccount{
					StorageSlots: map[common.Hash]common.Hash{
						{}:      {31: 1},
						{31: 1}: {31: 1},
					},
				},
			}},
			cost: 3,
		},
		{
			name: "cost summed together",
			cond: TransactionConditional{
				BlockNumberMin: big.NewInt(1),
				TimestampMin:   uint64Ptr(1),
				KnownAccounts: KnownAccounts{
					common.Address{19: 1}: KnownAccount{StorageRoot: &types.EmptyRootHash},
					common.Address{19: 2}: KnownAccount{
						StorageSlots: map[common.Hash]common.Hash{
							{}:      {31: 1},
							{31: 1}: {31: 1},
						},
					},
				},
			},
			cost: 7,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.cost, test.cond.Cost())
		})
	}
}

func TestTransactionConditionalValidation(t *testing.T) {
	tests := []struct {
		name     string
		cond     TransactionConditional
		mustFail bool
	}{
		{
			name: "empty conditional",
			cond: TransactionConditional{},
		},
		{
			name: "equal block constraint",
			cond: TransactionConditional{BlockNumberMin: big.NewInt(1), BlockNumberMax: big.NewInt(1)},
		},
		{
			name:     "block min greater than max",
			cond:     TransactionConditional{BlockNumberMin: big.NewInt(2), BlockNumberMax: big.NewInt(1)},
			mustFail: true,
		},
		{
			name: "equal timestamp constraint",
			cond: TransactionConditional{TimestampMin: uint64Ptr(1), TimestampMax: uint64Ptr(1)},
		},
		{
			name:     "timestamp min greater than max",
			cond:     TransactionConditional{TimestampMin: uint64Ptr(2), TimestampMax: uint64Ptr(1)},
			mustFail: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.cond.Validate()
			if test.mustFail {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestTransactionConditionalUnmarshal(t *testing.T) {
	hashPtr := func(hash common.Hash) *common.Hash { return &hash }

	tests := []struct {
		name     string
		input    string
		mustFail bool
		expected TransactionConditional
	}{
		{
			name:  "StateRoot",
			input: `{"knownAccounts":{"0x6b3A8798E5Fb9fC5603F3aB5eA2e8136694e55d0":"0x290decd9548b62a8d60345a988386fc84ba6bc95484008f6362f93160ef3e563"}}`,
			expected: TransactionConditional{
				KnownAccounts: KnownAccounts{
					common.HexToAddress("0x6b3A8798E5Fb9fC5603F3aB5eA2e8136694e55d0"): KnownAccount{
						StorageRoot:  hashPtr(common.HexToHash("0x290decd9548b62a8d60345a988386fc84ba6bc95484008f6362f93160ef3e563")),
						StorageSlots: make(map[common.Hash]common.Hash),
					},
				},
			},
		},
		{
			name:  "StorageSlots",
			input: `{"knownAccounts":{"0x6b3A8798E5Fb9fC5603F3aB5eA2e8136694e55d0":{"0xc65a7bb8d6351c1cf70c95a316cc6a92839c986682d98bc35f958f4883f9d2a8":"0x0000000000000000000000000000000000000000000000000000000000000000"}}}`,
			expected: TransactionConditional{
				KnownAccounts: KnownAccounts{
					common.HexToAddress("0x6b3A8798E5Fb9fC5603F3aB5eA2e8136694e55d0"): KnownAccount{
						StorageSlots: map[common.Hash]common.Hash{
							common.HexToHash("0xc65a7bb8d6351c1cf70c95a316cc6a92839c986682d98bc35f958f4883f9d2a8"): common.HexToHash("0x"),
						},
					},
				},
			},
		},
		{
			name:     "EmptyObject",
			input:    `{"knownAccounts":{}}`,
			expected: TransactionConditional{KnownAccounts: make(KnownAccounts)},
		},
		{
			name:     "EmptyStrings",
			input:    `{"knownAccounts":{"":""}}`,
			mustFail: true,
		},
		{
			name:     "BlockNumberMin",
			input:    `{"blockNumberMin":"0x1"}`,
			expected: TransactionConditional{BlockNumberMin: big.NewInt(1)},
		},
		{
			name:     "BlockNumberMax",
			input:    `{"blockNumberMax":"0x2"}`,
			expected: TransactionConditional{BlockNumberMax: big.NewInt(2)},
		},
		{
			name:     "BlockNumber (decimal)",
			input:    `{"blockNumberMin": 0, "blockNumberMax": 1}`,
			expected: TransactionConditional{BlockNumberMin: big.NewInt(0), BlockNumberMax: big.NewInt(1)},
		},
		{
			name:     "TimestampMin",
			input:    `{"timestampMin":"0xffff"}`,
			expected: TransactionConditional{TimestampMin: uint64Ptr(0xffff)},
		},
		{
			name:     "TimestampMax",
			input:    `{"timestampMax":"0xffffff"}`,
			expected: TransactionConditional{TimestampMax: uint64Ptr(0xffffff)},
		},
		{
			name:     "Timestamp (decimal)",
			input:    `{"timestampMin": 0, "timestampMax": 1}`,
			expected: TransactionConditional{TimestampMin: uint64Ptr(0), TimestampMax: uint64Ptr(1)},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var cond TransactionConditional
			err := json.Unmarshal([]byte(test.input), &cond)
			if test.mustFail {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, test.expected, cond)
		})
	}
}

// TestTransactionConditionalMarshal pins the encoding forwarded to the backend's
// eth_sendRawTransactionConditional. The expected strings are what op-geth's type produced.
func TestTransactionConditionalMarshal(t *testing.T) {
	root := common.HexToHash("0x290decd9548b62a8d60345a988386fc84ba6bc95484008f6362f93160ef3e563")
	addr := common.HexToAddress("0x6b3A8798E5Fb9fC5603F3aB5eA2e8136694e55d0")

	tests := []struct {
		name     string
		cond     TransactionConditional
		expected string
	}{
		{
			name:     "empty",
			cond:     TransactionConditional{},
			expected: `{"knownAccounts":null}`,
		},
		{
			name: "storage root and ranges",
			cond: TransactionConditional{
				KnownAccounts:  KnownAccounts{addr: KnownAccount{StorageRoot: &root}},
				BlockNumberMin: big.NewInt(1),
				BlockNumberMax: big.NewInt(300),
				TimestampMin:   uint64Ptr(0),
				TimestampMax:   uint64Ptr(0xffff),
			},
			expected: `{"knownAccounts":{"0x6b3a8798e5fb9fc5603f3ab5ea2e8136694e55d0":"0x290decd9548b62a8d60345a988386fc84ba6bc95484008f6362f93160ef3e563"},"blockNumberMin":"0x1","blockNumberMax":"0x12c","timestampMin":"0x0","timestampMax":"0xffff"}`,
		},
		{
			name: "storage slots",
			cond: TransactionConditional{
				KnownAccounts: KnownAccounts{addr: KnownAccount{StorageSlots: map[common.Hash]common.Hash{root: {31: 1}}}},
			},
			expected: `{"knownAccounts":{"0x6b3a8798e5fb9fc5603f3ab5ea2e8136694e55d0":{"0x290decd9548b62a8d60345a988386fc84ba6bc95484008f6362f93160ef3e563":"0x0000000000000000000000000000000000000000000000000000000000000001"}}}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			out, err := json.Marshal(test.cond)
			require.NoError(t, err)
			require.Equal(t, test.expected, string(out))
		})
	}
}
