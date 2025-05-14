package proxyd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

type MockBlockNumberTracker struct {
	Latest   uint64
	Safe     uint64
	Final    uint64
	LatestOk bool
	SafeOk   bool
	FinalOk  bool
}

func (m *MockBlockNumberTracker) GetLatestBlockNumber() (uint64, bool) {
	return m.Latest, m.LatestOk
}

func (m *MockBlockNumberTracker) GetSafeBlockNumber() (uint64, bool) {
	return m.Safe, m.SafeOk
}

func (m *MockBlockNumberTracker) GetFinalizedBlockNumber() (uint64, bool) {
	return m.Final, m.FinalOk
}

func TestExtractBlockRange(t *testing.T) {
	testCases := []struct {
		name     string
		req      *RPCReq
		tracker  *MockBlockNumberTracker
		expected *BlockRange
	}{
		{
			name: "latest blocks",
			req: &RPCReq{
				Method: "eth_getLogs",
				Params: []byte(`[{"fromBlock": "latest", "toBlock": "latest"}]`),
			},
			tracker: &MockBlockNumberTracker{
				Latest:   100,
				LatestOk: true,
			},
			expected: &BlockRange{
				FromBlock: 100,
				ToBlock:   100,
			},
		},
		{
			name: "finalized blocks",
			req: &RPCReq{
				Method: "eth_getLogs",
				Params: []byte(`[{"fromBlock": "finalized", "toBlock": "finalized"}]`),
			},
			tracker: &MockBlockNumberTracker{
				Final:   80,
				FinalOk: true,
			},
			expected: &BlockRange{
				FromBlock: 80,
				ToBlock:   80,
			},
		},
		{
			name: "safe blocks",
			req: &RPCReq{
				Method: "eth_getLogs",
				Params: []byte(`[{"fromBlock": "safe", "toBlock": "safe"}]`),
			},
			tracker: &MockBlockNumberTracker{
				Safe:   90,
				SafeOk: true,
			},
			expected: &BlockRange{
				FromBlock: 90,
				ToBlock:   90,
			},
		},
		{
			name: "earliest blocks",
			req: &RPCReq{
				Method: "eth_getLogs",
				Params: []byte(`[{"fromBlock": "earliest", "toBlock": "earliest"}]`),
			},
			tracker: &MockBlockNumberTracker{},
			expected: &BlockRange{
				FromBlock: 0,
				ToBlock:   0,
			},
		},
		{
			name: "hex block numbers",
			req: &RPCReq{
				Method: "eth_getLogs",
				Params: []byte(`[{"fromBlock": "0x1", "toBlock": "0xa"}]`),
			},
			tracker: &MockBlockNumberTracker{},
			expected: &BlockRange{
				FromBlock: 1,
				ToBlock:   10,
			},
		},
		{
			name: "unset fromBlock defaults to latest",
			req: &RPCReq{
				Method: "eth_getLogs",
				Params: []byte(`[{"toBlock": "0xa"}]`),
			},
			tracker: &MockBlockNumberTracker{
				Latest:   100,
				LatestOk: true,
			},
			expected: &BlockRange{
				FromBlock: 100,
				ToBlock:   10,
			},
		},
		{
			name: "unset toBlock defaults to latest",
			req: &RPCReq{
				Method: "eth_getLogs",
				Params: []byte(`[{"fromBlock": "0x1"}]`),
			},
			tracker: &MockBlockNumberTracker{
				Latest:   100,
				LatestOk: true,
			},
			expected: &BlockRange{
				FromBlock: 1,
				ToBlock:   100,
			},
		},
		{
			name: "both blocks unset returns nil",
			req: &RPCReq{
				Method: "eth_getLogs",
				Params: []byte(`[{}]`),
			},
			tracker:  &MockBlockNumberTracker{},
			expected: nil,
		},
		{
			name: "non-logs method returns nil",
			req: &RPCReq{
				Method: "eth_getBalance",
				Params: []byte(`[{"fromBlock": "latest", "toBlock": "latest"}]`),
			},
			tracker:  &MockBlockNumberTracker{},
			expected: nil,
		},
		{
			name: "latest block not available returns nil",
			req: &RPCReq{
				Method: "eth_getLogs",
				Params: []byte(`[{"fromBlock": "latest", "toBlock": "latest"}]`),
			},
			tracker: &MockBlockNumberTracker{
				Latest:   100,
				LatestOk: false,
			},
			expected: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := ExtractBlockRange(tc.req, tc.tracker)
			assert.Equal(t, tc.expected, result)
		})
	}
}
