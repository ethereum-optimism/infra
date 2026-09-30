package provider

import (
	"errors"
	"fmt"
	"math/big"
	"os"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"gopkg.in/yaml.v3"
)

type AuthConfig struct {
	// ClientName DNS name of the client connecting to op-signer.
	ClientName string `yaml:"name"`
	// KeyName key locator for the KMS (resource name in cloud provider, or path to private key file for local provider)
	KeyName string `yaml:"key"`
	// ChainID chain id of the op-signer to sign for
	ChainID uint64 `yaml:"chainID"`
	// FromAddress sender address that is sending the rpc request
	FromAddress common.Address `yaml:"fromAddress"`
	// MessageSigningOnly permits EIP-191 message signing and denies transaction and block signing.
	MessageSigningOnly bool     `yaml:"messageSigningOnly,omitempty"`
	ToAddresses        []string `yaml:"toAddresses"`
	MaxValue           string   `yaml:"maxValue"`
}

func (c AuthConfig) MaxValueToInt() *big.Int {
	return hexutil.MustDecodeBig(c.MaxValue)
}

type ProviderConfig struct {
	ProviderType ProviderType `yaml:"provider"`
	Auth         []AuthConfig `yaml:"auth"`
}

func ReadConfig(path string) (ProviderConfig, error) {
	config := ProviderConfig{}
	data, err := os.ReadFile(path)
	if err != nil {
		return config, err
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		return config, err
	}

	// Default to GCP if Provider is empty
	if config.ProviderType == "" {
		config.ProviderType = KeyProviderGCP
	}

	if !config.ProviderType.IsValid() {
		return config, fmt.Errorf("invalid provider '%s' in config. Must be 'AWS', 'GCP', or 'LOCAL'", config.ProviderType)
	}

	for _, authConfig := range config.Auth {
		if authConfig.MessageSigningOnly && authConfig.FromAddress == (common.Address{}) {
			return config, fmt.Errorf("auth config for client '%s' has messageSigningOnly set but no fromAddress", authConfig.ClientName)
		}
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

// GetAuthConfigForClient returns the first transaction and block payload signing auth config for
// clientName. If fromAddress is specified, it must match the config's FromAddress. Message-signing-only
// configs are skipped.
func (s ProviderConfig) GetAuthConfigForClient(clientName string, fromAddress *common.Address) (*AuthConfig, error) {
	return s.getAuthConfigForClient(clientName, fromAddress, false)
}

// GetMessageSigningAuthConfigForClient returns the message-signing-only auth config for clientName
// and fromAddress.
func (s ProviderConfig) GetMessageSigningAuthConfigForClient(clientName string, fromAddress common.Address) (*AuthConfig, error) {
	return s.getAuthConfigForClient(clientName, &fromAddress, true)
}

func (s ProviderConfig) getAuthConfigForClient(clientName string, fromAddress *common.Address, messageSigningOnly bool) (*AuthConfig, error) {
	if clientName == "" {
		return nil, errors.New("client name is empty")
	}
	for _, ac := range s.Auth {
		if ac.ClientName == clientName && ac.MessageSigningOnly == messageSigningOnly {
			// If fromAddress is specified, it must match the address in the authConfig
			if fromAddress != nil && *fromAddress != ac.FromAddress {
				continue
			}

			return &ac, nil
		}
	}
	return nil, fmt.Errorf("client '%s' is not authorized to use any keys", clientName)
}
