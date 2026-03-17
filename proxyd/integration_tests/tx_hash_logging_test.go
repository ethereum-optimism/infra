package integration_tests

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/ethereum-optimism/infra/proxyd"
	"github.com/ethereum/go-ethereum/log"
	"github.com/stretchr/testify/require"
)

// findJSONLogLine searches JSON log output for a line with the given message
// and returns the parsed fields, or nil if not found.
func findJSONLogLine(t *testing.T, logOutput string, msg string) map[string]any {
	t.Helper()
	for _, line := range strings.Split(logOutput, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry["msg"] == msg {
			return entry
		}
	}
	return nil
}

func TestTxHashLogging(t *testing.T) {
	// Capture log output
	var logBuf bytes.Buffer
	log.SetDefault(log.NewLogger(slog.NewJSONHandler(
		&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))

	goodBackend := NewMockBackend(SingleResponseHandler(200, `{"jsonrpc":"2.0","result":"0x1234567890abcdef","id":1}`))
	defer goodBackend.Close()

	require.NoError(t, os.Setenv("GOOD_BACKEND_RPC_URL", goodBackend.URL()))

	config := ReadConfig("tx_hash_logging")
	config.Server.LogLevel = "debug"
	client := NewProxydClient("http://127.0.0.1:8545")
	_, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)
	defer shutdown()

	t.Run("single sendRawTransaction should log tx hash", func(t *testing.T) {
		logBuf.Reset()

		// Valid transaction from testdata
		body := `{"jsonrpc":"2.0","method":"eth_sendRawTransaction","params":["0x02f8748201a415843b9aca31843b9aca3182520894f80267194936da1e98db10bce06f3147d580a62e880de0b6b3a764000080c001a0b50ee053102360ff5fedf0933b912b7e140c90fe57fa07a0cebe70dbd72339dda072974cb7bfe5c3dc54dde110e2b049408ccab8a879949c3b4d42a3a7555a618b"],"id":1}`

		_, code, err := client.SendRequest([]byte(body))
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, code)

		// Verify log contains transaction hash and required fields
		logOutput := logBuf.String()
		require.Contains(t, logOutput, "processing sendRawTransaction")
		require.Contains(t, logOutput, "tx_hash")
		require.Contains(t, logOutput, "0x0cdd497ce38727606708946cd07b83b101b92a29dea7f090f1f09ae849efd1a3") // Expected tx hash
		require.Contains(t, logOutput, "method")
		require.Contains(t, logOutput, "req_id")
		require.Contains(t, logOutput, "chain_id")
		require.Contains(t, logOutput, "nonce")

		// Verify forwarding log has correct tx_hash and matching req_id
		recvLog := findJSONLogLine(t, logOutput, "processing sendRawTransaction")
		require.NotNil(t, recvLog, "expected processing sendRawTransaction log line")

		fwdLog := findJSONLogLine(t, logOutput, "sendRawTransaction forwarded")
		require.NotNil(t, fwdLog, "expected sendRawTransaction forwarded log line")
		require.Equal(t, "0x0cdd497ce38727606708946cd07b83b101b92a29dea7f090f1f09ae849efd1a3", fwdLog["tx_hash"])
		require.Equal(t, recvLog["req_id"], fwdLog["req_id"], "req_id should match between processing and forwarded logs")
		require.NotEmpty(t, fwdLog["backend"], "backend should be present")
		require.NotNil(t, fwdLog["duration_ms"], "duration_ms should be present")
	})

	t.Run("batch sendRawTransaction should log tx hash", func(t *testing.T) {
		logBuf.Reset()

		// Batch with sendRawTransaction
		body := `[{"jsonrpc":"2.0","method":"eth_sendRawTransaction","params":["0x02f8748201a415843b9aca31843b9aca3182520894f80267194936da1e98db10bce06f3147d580a62e880de0b6b3a764000080c001a0b50ee053102360ff5fedf0933b912b7e140c90fe57fa07a0cebe70dbd72339dda072974cb7bfe5c3dc54dde110e2b049408ccab8a879949c3b4d42a3a7555a618b"],"id":1},{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":2}]`

		_, code, err := client.SendRequest([]byte(body))
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, code)

		// Verify log contains transaction hash and required fields
		logOutput := logBuf.String()
		require.Contains(t, logOutput, "processing sendRawTransaction")
		require.Contains(t, logOutput, "tx_hash")
		require.Contains(t, logOutput, "0x0cdd497ce38727606708946cd07b83b101b92a29dea7f090f1f09ae849efd1a3") // Expected tx hash
		require.Contains(t, logOutput, "method")
		require.Contains(t, logOutput, "req_id")
	})

	t.Run("sendRawTransactionConditional should log tx hash", func(t *testing.T) {
		logBuf.Reset()

		// Conditional transaction from testdata
		body := `{"jsonrpc":"2.0","method":"eth_sendRawTransactionConditional","params":["0x02f8748201a415843b9aca31843b9aca3182520894f80267194936da1e98db10bce06f3147d580a62e880de0b6b3a764000080c001a0b50ee053102360ff5fedf0933b912b7e140c90fe57fa07a0cebe70dbd72339dda072974cb7bfe5c3dc54dde110e2b049408ccab8a879949c3b4d42a3a7555a618b", {}],"id":1}`

		_, code, err := client.SendRequest([]byte(body))
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, code)

		// Verify log contains transaction hash and required fields
		logOutput := logBuf.String()
		require.Contains(t, logOutput, "processing sendRawTransaction")
		require.Contains(t, logOutput, "tx_hash")
		require.Contains(t, logOutput, "0x0cdd497ce38727606708946cd07b83b101b92a29dea7f090f1f09ae849efd1a3") // Expected tx hash
		require.Contains(t, logOutput, "method")
		require.Contains(t, logOutput, "req_id")
	})
}

func TestTxHashLoggingDisabled(t *testing.T) {
	// Capture log output
	var logBuf bytes.Buffer
	log.SetDefault(log.NewLogger(slog.NewJSONHandler(
		&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))

	goodBackend := NewMockBackend(SingleResponseHandler(200, `{"jsonrpc":"2.0","result":"0x1234567890abcdef","id":1}`))
	defer goodBackend.Close()

	require.NoError(t, os.Setenv("GOOD_BACKEND_RPC_URL", goodBackend.URL()))

	config := ReadConfig("interop_validation")
	config.Server.LogLevel = "debug"
	client := NewProxydClient("http://127.0.0.1:8545")
	_, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)
	defer shutdown()

	t.Run("sendRawTransaction should not log tx hash when disabled", func(t *testing.T) {
		logBuf.Reset()

		// Valid transaction from testdata
		body := `{"jsonrpc":"2.0","method":"eth_sendRawTransaction","params":["0x02f8748201a415843b9aca31843b9aca3182520894f80267194936da1e98db10bce06f3147d580a62e880de0b6b3a764000080c001a0b50ee053102360ff5fedf0933b912b7e140c90fe57fa07a0cebe70dbd72339dda072974cb7bfe5c3dc54dde110e2b049408ccab8a879949c3b4d42a3a7555a618b"],"id":1}`

		_, code, err := client.SendRequest([]byte(body))
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, code)

		// Verify log does NOT contain transaction hash logging
		logOutput := logBuf.String()
		require.NotContains(t, logOutput, "processing sendRawTransaction")
		require.NotContains(t, logOutput, "sendRawTransaction forwarded")
		require.NotContains(t, logOutput, "0x0cdd497ce38727606708946cd07b83b101b92a29dea7f090f1f09ae849efd1a3")
	})
}
