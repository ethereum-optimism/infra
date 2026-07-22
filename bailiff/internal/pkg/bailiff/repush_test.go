package bailiff

import (
	"context"
	"fmt"
	"testing"

	"github.com/ethereum-optimism/optimism/op-service/testlog"
	"github.com/ethereum/go-ethereum/log"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type testifyMockRepusher struct {
	mock.Mock
}

func (m *testifyMockRepusher) Repush(ctx context.Context, forkRepo, srcBranch, upstreamBranch, requestedSHA string) error {
	args := m.Called(ctx, forkRepo, srcBranch, upstreamBranch, requestedSHA)
	return args.Error(0)
}

func TestAsyncRepusher(t *testing.T) {
	innerRepusher := new(testifyMockRepusher)
	asyncRepusher := NewAsyncRepusher(testlog.Logger(t, log.LevelInfo), innerRepusher)

	for i := 0; i < maxAsyncRepushes; i++ {
		iStr := fmt.Sprintf("%d", i)
		innerRepusher.On("Repush", mock.Anything, "forkRepo"+iStr, "srcBranch"+iStr, "upstreamBranch"+iStr, "requestedSHA"+iStr).Return(nil)
		require.NoError(t, asyncRepusher.Repush(
			context.Background(),
			"forkRepo"+iStr,
			"srcBranch"+iStr,
			"upstreamBranch"+iStr,
			"requestedSHA"+iStr,
		))
	}

	asyncRepusher.Close()
	innerRepusher.AssertExpectations(t)

	require.ErrorContains(t, asyncRepusher.Repush(context.Background(), "forkRepo", "srcBranch", "upstreamBranch", "requestedSHA"), "closed")
}
