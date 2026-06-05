package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestConfig(t *testing.T) {
	t.Run("should load an example config file", func(t *testing.T) {
		config, err := New("../../config.example.yaml")
		require.NoError(t, err)
		require.NotNil(t, config)

		require.Equal(t, "info", config.LogLevel)
		require.Equal(t, false, config.DryRun)

		require.Equal(t, false, config.Metrics.Debug)
		require.Equal(t, true, config.Metrics.Enabled)
		require.Equal(t, "0.0.0.0", config.Metrics.Host)
		require.Equal(t, "7300", config.Metrics.Port)

		require.Equal(t, true, config.Healthz.Enabled)
		require.Equal(t, "0.0.0.0", config.Healthz.Host)
		require.Equal(t, "8080", config.Healthz.Port)

		require.Equal(t, mustParseDuration("30s"), config.PollInterval)
		require.Equal(t, mustParseDuration("1h"), config.NodeStateExpiration)
		require.Equal(t, mustParseDuration("15s"), config.RPCTimeout)

		require.Equal(t, 3, len(config.Nodes))
		require.Equal(t, "http://op-node-0:9545", config.Nodes["op-node-0"].RPCAddress)
		require.Equal(t, "http://op-node-1:9545", config.Nodes["op-node-1"].RPCAddress)
		require.True(t, config.Nodes["external-bootnode"].IsExternal())

		require.Equal(t, 1, len(config.Networks))
		require.Equal(t, 3, len(config.Networks["network_name"].Members))
		require.Equal(t, "op-node-0", config.Networks["network_name"].Members[0])
		require.Equal(t, "op-node-1", config.Networks["network_name"].Members[1])
		require.Equal(t, "external-bootnode", config.Networks["network_name"].Members[2])

		require.NoError(t, config.Validate())
	})
}

func mustParseDuration(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		panic(err)
	}
	return d
}

func TestNodeConfig_IsExternal(t *testing.T) {
	require.True(t, (&NodeConfig{}).IsExternal())
	require.True(t, (&NodeConfig{PeerID: "p", PeerAddress: "/dns4/foo/p2p/p"}).IsExternal())
	require.False(t, (&NodeConfig{RPCAddress: "http://x"}).IsExternal())
}

func TestConfig_Validate_ExternalPeer(t *testing.T) {
	base := func() *Config {
		return &Config{
			Nodes: map[string]*NodeConfig{
				"internal": {RPCAddress: "http://internal:9545"},
				"external": {
					PeerID:      "16Uiu2HAmFV3qmRmrnEByXMWFNVbBxbnLuG9PvKsgpvJgZqLF2sB1",
					PeerAddress: "/dns4/ext/tcp/9003/p2p/16Uiu2HAmFV3qmRmrnEByXMWFNVbBxbnLuG9PvKsgpvJgZqLF2sB1",
				},
			},
			Networks: map[string]*NetworkConfig{
				"net": {Members: []string{"internal", "external"}},
			},
		}
	}

	t.Run("valid: external peer with peer_id and peer_address", func(t *testing.T) {
		require.NoError(t, base().Validate())
	})

	t.Run("invalid: external peer missing peer_id", func(t *testing.T) {
		c := base()
		c.Nodes["external"].PeerID = ""
		err := c.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "peer_id")
	})

	t.Run("invalid: external peer missing peer_address", func(t *testing.T) {
		c := base()
		c.Nodes["external"].PeerAddress = ""
		err := c.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "peer_address")
	})

	t.Run("invalid: node missing both rpc_address and peer info", func(t *testing.T) {
		c := base()
		c.Nodes["external"] = &NodeConfig{}
		err := c.Validate()
		require.Error(t, err)
	})

	t.Run("invalid: external peer with malformed peer_id", func(t *testing.T) {
		c := base()
		c.Nodes["external"].PeerID = "not-a-valid-peer-id"
		err := c.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid peer_id")
	})

	t.Run("invalid: network with no internal members", func(t *testing.T) {
		c := base()
		// Make the internal node external too, so the network has only externals.
		c.Nodes["internal"] = &NodeConfig{
			PeerID:      "16Uiu2HAmFV3qmRmrnEByXMWFNVbBxbnLuG9PvKsgpvJgZqLF2sB1",
			PeerAddress: "/dns4/x/tcp/9003/p2p/16Uiu2HAmFV3qmRmrnEByXMWFNVbBxbnLuG9PvKsgpvJgZqLF2sB1",
		}
		c.Nodes["external"].PeerID = "16Uiu2HAmFV3qmRmrnEByXMWFNVbBxbnLuG9PvKsgpvJgZqLF2sB1"
		c.Nodes["external"].PeerAddress = "/dns4/y/tcp/9003/p2p/16Uiu2HAmFV3qmRmrnEByXMWFNVbBxbnLuG9PvKsgpvJgZqLF2sB1"
		err := c.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "no internal members")
	})
}
