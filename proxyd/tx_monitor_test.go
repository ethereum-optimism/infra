package proxyd

import (
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/stretchr/testify/require"
)

func TestTxMonitorConfigDecode(t *testing.T) {
	cfgToml := `
[tx_monitor]
enabled = true
block_poll_url = "http://localhost:8545"
block_poll_backend_group = "replicas"
subblocks_ws_url = "ws://localhost:1112/ws"
inclusion_timeout = "45s"
max_pending = 5000
poll_interval = "500ms"
`
	var cfg Config
	_, err := toml.Decode(cfgToml, &cfg)
	require.NoError(t, err)
	require.True(t, cfg.TxMonitor.Enabled)
	require.Equal(t, "http://localhost:8545", cfg.TxMonitor.BlockPollURL)
	require.Equal(t, "replicas", cfg.TxMonitor.BlockPollBackendGroup)
	require.Equal(t, "ws://localhost:1112/ws", cfg.TxMonitor.SubblocksWSURL)
	require.Equal(t, TOMLDuration(45*time.Second), cfg.TxMonitor.InclusionTimeout)
	require.Equal(t, 5000, cfg.TxMonitor.MaxPending)
	require.Equal(t, TOMLDuration(500*time.Millisecond), cfg.TxMonitor.PollInterval)
}
