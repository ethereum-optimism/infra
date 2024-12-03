package proxyd

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"golang.org/x/sync/semaphore"
)

func TestStripXFF(t *testing.T) {
	tests := []struct {
		in, out string
	}{
		{"1.2.3, 4.5.6, 7.8.9", "1.2.3"},
		{"1.2.3,4.5.6", "1.2.3"},
		{" 1.2.3 , 4.5.6 ", "1.2.3"},
	}

	for _, test := range tests {
		actual := stripXFF(test.in)
		assert.Equal(t, test.out, actual)
	}
}

func TestForwardContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sem := semaphore.NewWeighted(100)
	backend := NewBackend(
		"test",
		"http://localhost:8545",
		"ws://localhost:8545",
		sem,
	)
	_, err := backend.Forward(ctx, []*RPCReq{
		{JSONRPC: "2.0", Method: "eth_blockNumber", ID: json.RawMessage("1")},
	}, true)
	assert.ErrorIs(t, context.Canceled, err)
	assert.Equal(t, uint(1), backend.networkRequestsSlidingWindow.Count())
	assert.Equal(t, uint(0), backend.intermittentErrorsSlidingWindow.Count())
	assert.Zero(t, backend.ErrorRate())
}
