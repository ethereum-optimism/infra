package proxyd

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/ethereum/go-ethereum/log"
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
	buf := bytes.NewBuffer(nil)
	log.SetDefault(log.NewLogger(slog.NewTextHandler(buf, &slog.HandlerOptions{
		Level: slog.LevelDebug.Level(),
	})))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sem := semaphore.NewWeighted(100)
	backend := NewBackend(
		"test",
		"http://localhost:8545",
		"ws://localhost:8545",
		sem,
	)
	backendGroup := &BackendGroup{
		Name:            "testgroup",
		Backends:        []*Backend{backend},
		routingStrategy: "fallback",
	}
	_, _, err := backendGroup.Forward(ctx, []*RPCReq{
		{JSONRPC: "2.0", Method: "eth_blockNumber", ID: json.RawMessage("1")},
	}, true)
	assert.ErrorIs(t, context.Canceled, err)
	assert.Equalf(t, uint(1), backend.networkRequestsSlidingWindow.Count(), "exact 1 network request should be counted")
	assert.Equalf(t, uint(0), backend.intermittentErrorsSlidingWindow.Count(), "no intermittent errors should be counted")
	assert.Zerof(t, backend.ErrorRate(), "error rate should be zero")
	logs := buf.String()
	assert.NotZero(t, logs)
	assert.NotContainsf(t, logs, "level=ERROR", "context canceled error should not be logged as a ERROR")
}
