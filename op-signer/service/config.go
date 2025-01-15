package service

import (
	"errors"
	"fmt"
	"math/big"
	"os"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"gopkg.in/yaml.v3"
)

// KeyProvider represents the cloud provider for the key management service
type KeyProvider string

const (
	KeyProviderAWS KeyProvider = "AWS"
	KeyProviderGCP KeyProvider = "GCP"
)

// IsValid checks if the KeyProvider value is valid
func (k KeyProvider) IsValid() bool {
	switch k {
	case KeyProviderAWS, KeyProviderGCP:
		return true
	default:
		return false
	}
}

type AuthConfig struct {
	// ClientName DNS name of the client connecting to op-signer.
	ClientName string `yaml:"name"`
	// KeyName key resource name of the Cloud KMS
	KeyName string `yaml:"key"`
	// ChainID chain id of the op-signer to sign for
	ChainID uint64 `yaml:"chainID"`
	// FromAddress sender address that is sending the rpc request
	FromAddress common.Address `yaml:"fromAddress"`
	ToAddresses []string       `yaml:"toAddresses"`
	MaxValue    string         `yaml:"maxValue"`
}

func (c AuthConfig) MaxValueToInt() *big.Int {
	return hexutil.MustDecodeBig(c.MaxValue)
}

type SignerServiceConfig struct {
	ProviderType KeyProvider  `yaml:"provider"`
	Auth         []AuthConfig `yaml:"auth"`
}

func ReadConfig(path string) (SignerServiceConfig, error) {
	config := SignerServiceConfig{}
	data, err := os.ReadFile(path)
	if err != nil {
		return config, err
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		return config, err
	}

	// Default to GCP if Provider is empty to avoid breaking changes
	if config.ProviderType == "" {
		config.ProviderType = KeyProviderGCP
	}

	if !config.ProviderType.IsValid() {
		return config, fmt.Errorf("invalid provider '%s' in config. Must be 'AWS' or 'GCP'", config.ProviderType)
	}

	for _, authConfig := range config.Auth {
		for _, toAddress := range authConfig.ToAddresses {
			if _, err := hexutil.Decode(toAddress); err != nil {
				return config, fmt.Errorf("invalid toAddress '%s' in auth config: %w", toAddress, err)
			}
			if authConfig.MaxValue != "" {
				if _, err := hexutil.DecodeBig(authConfig.MaxValue); err != nil {
					return config, fmt.Errorf("invalid maxValue '%s' in auth config: %w", toAddress, err)
				}
			}
		}
	}
	return config, err
}

func (s SignerServiceConfig) GetAuthConfigForClient(clientName string, fromAddress *common.Address) (*AuthConfig, error) {
	if clientName == "" {
		return nil, errors.New("client name is empty")
	}
	for _, ac := range s.Auth {
		if ac.ClientName == clientName {
			// If fromAddress is specified, it must match the address in the authConfig
			if fromAddress != nil && *fromAddress != ac.FromAddress {
				continue
			}

			return &ac, nil
		}
	}
	return nil, fmt.Errorf("client '%s' is not authorized to use any keys", clientName)
}
