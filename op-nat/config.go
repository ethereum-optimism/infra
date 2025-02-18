package nat

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strconv"

	"github.com/urfave/cli/v2"

	"github.com/ethereum-optimism/infra/op-nat/flags"
	"github.com/ethereum-optimism/infra/op-nat/network"
	"github.com/ethereum/go-ethereum/log"
)

type Config struct {
	// Network config
	SC  SuperchainManifest
	L1  *network.Network
	L2A *network.Network

	// Test config
	TestDir         string
	ValidatorConfig string
	TargetGate      string

	Log log.Logger
}

// NewConfig creates a new Config instance
func NewConfig(ctx *cli.Context, log log.Logger, testDir string, validatorConfig string, gate string) (*Config, error) {
	// Parse flags
	if err := flags.CheckRequired(ctx); err != nil {
		return nil, fmt.Errorf("missing required flags: %w", err)
	}
	if testDir == "" {
		return nil, errors.New("test directory is required")
	}
	if validatorConfig == "" {
		return nil, errors.New("validator config path is required")
	}
	if gate == "" {
		return nil, errors.New("gate is required")
	}

	// Get absolute paths
	absTestDir, err := filepath.Abs(testDir)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for test dir: %w", err)
	}
	absValidatorConfig, err := filepath.Abs(validatorConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for validator config: %w", err)
	}

	// Parse kurtosis-devnet manifest
	manifest, err := parseManifest(ctx.String(flags.Manifest.Name))
	if err != nil {
		return nil, fmt.Errorf("failed to parse kurtosis-devnet manifest: %w", err)
	}

	l1ID, err := strconv.Atoi(manifest.L1.ID)
	if err != nil {
		log.Warn("L1 Chain ID was not supplied, will skip l1 chain-id test")
		l1ID = -1
	}

	l1, err := network.NewNetwork(
		ctx.Context,
		log,
		manifest.L1.Name,
		manifest.L1.Nodes[0].Services.EL.Endpoints["rpc"].Host,
		manifest.L1.Nodes[0].Services.EL.Endpoints["rpc"].Secure,
		big.NewInt(int64(l1ID)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to setup l1 network: %w", err)
	}

	l2AID, err := strconv.Atoi(manifest.L2[0].ID)
	if err != nil {
		log.Warn("L2A Chain ID was not supplied, will skip l2A chain-id test")
		l2AID = -1
	}

	l2A, err := network.NewNetwork(
		ctx.Context,
		log,
		manifest.L2[0].Name,
		manifest.L2[0].Nodes[0].Services.EL.Endpoints["rpc"].Host,
		manifest.L2[0].Nodes[0].Services.EL.Endpoints["rpc"].Secure,
		big.NewInt(int64(l2AID)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to setup l2A network: %w", err)
	}

	return &Config{
		SC:              *manifest,
		TestDir:         absTestDir,
		ValidatorConfig: absValidatorConfig,
		TargetGate:      gate,
		L1:              l1,
		L2A:             l2A,
		Log:             log,
	}, nil
}

func parseManifest(manifestPath string) (*SuperchainManifest, error) {
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest file: %w", err)
	}

	var superchainManifest SuperchainManifest
	if err := json.Unmarshal(manifest, &superchainManifest); err != nil {
		return nil, fmt.Errorf("failed to unmarshal manifest: %w", err)
	}
	return &superchainManifest, nil

}
