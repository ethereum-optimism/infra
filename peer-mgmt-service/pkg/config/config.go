package config

import (
	"fmt"
	"os"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/pkg/errors"
	"gopkg.in/yaml.v3"

	_ "gopkg.in/yaml.v3"
)

type Config struct {
	LogLevel string `yaml:"log_level"`
	DryRun   bool   `yaml:"dry_run"`

	Metrics MetricsConfig `yaml:"metrics"`
	Healthz HealthzConfig `yaml:"healthz"`

	PollInterval        time.Duration `yaml:"poll_interval"`
	NodeStateExpiration time.Duration `yaml:"node_state_expiration"`
	RPCTimeout          time.Duration `yaml:"rpc_timeout"`

	Nodes map[string]*NodeConfig `yaml:"nodes"`

	Networks map[string]*NetworkConfig `yaml:"networks"`
}

type MetricsConfig struct {
	Enabled bool   `yaml:"enabled"`
	Debug   bool   `yaml:"debug"`
	Host    string `yaml:"host"`
	Port    string `yaml:"port"`
}

type HealthzConfig struct {
	Enabled bool   `yaml:"enabled"`
	Host    string `yaml:"host"`
	Port    string `yaml:"port"`
}

type NodeConfig struct {
	RPCAddress       string `yaml:"rpc_address"`
	PeerID           string `yaml:"peer_id"`            // libp2p 54-char PeerID
	PeerAddress      string `yaml:"peer_address"`       // libp2p PeerAddress, supports {peer_id} as a placeholder
	PeerAddressLocal string `yaml:"peer_address_local"` // same as PeerAddress, but used for connecting in the same cluster
	Cluster          string `yaml:"cluster"`
	PreventInbound   bool   `yaml:"prevent_inbound"`
	PreventOutbound  bool   `yaml:"prevent_outbound"`
}

type NetworkConfig struct {
	Members []string `yaml:"members"`
}

// IsExternal reports whether this node is an external peer.
// External peers have no rpc_address; PMS only dials them from internal nodes
// using the static peer_address.
func (n *NodeConfig) IsExternal() bool {
	return n.RPCAddress == ""
}

func New(file string) (*Config, error) {
	cfg := &Config{}
	contents, err := os.ReadFile(file)
	if err != nil {
		fmt.Printf("error reading config file: %v\n", err)
	}
	if err := yaml.Unmarshal(contents, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) Validate() error {
	if c.Metrics.Enabled {
		if c.Metrics.Host == "" || c.Metrics.Port == "" {
			return errors.New("metrics is enabled but host or port are missing")
		}
	}
	if c.Healthz.Enabled {
		if c.Healthz.Host == "" || c.Healthz.Port == "" {
			return errors.New("healthz is enabled but host or port are missing")
		}
	}

	if len(c.Nodes) == 0 {
		return errors.New("no nodes configured")
	}

	if len(c.Networks) == 0 {
		return errors.New("no networks configured")
	}

	for name, node := range c.Nodes {
		if node.IsExternal() {
			if node.PeerID == "" {
				return errors.Errorf("node [%s] is external (no rpc_address) but peer_id is missing", name)
			}
			if node.PeerAddress == "" {
				return errors.Errorf("node [%s] is external (no rpc_address) but peer_address is missing", name)
			}
			if _, err := peer.Decode(node.PeerID); err != nil {
				return errors.Errorf("node [%s] has invalid peer_id [%s]: %v", name, node.PeerID, err)
			}
		}
	}

	for name, network := range c.Networks {
		if len(network.Members) < 2 {
			return errors.Errorf("network [%s] has less than 2 members", name)
		}
		internalCount := 0
		for _, member := range network.Members {
			nodeCfg, ok := c.Nodes[member]
			if !ok {
				return errors.Errorf("network [%s] member [%s] is not configured", name, member)
			}
			if !nodeCfg.IsExternal() {
				internalCount++
			}
		}
		if internalCount == 0 {
			return errors.Errorf("network [%s] has no internal members (all members are external; nothing to dial from)", name)
		}
	}

	if c.LogLevel == "" {
		c.LogLevel = "debug"
	}

	return nil
}
