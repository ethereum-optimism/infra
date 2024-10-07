package service

import (
	"errors"
	"fmt"
	"math/big"
	"os"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"gopkg.in/yaml.v3"
)

type AuthConfig struct {
	// ClientName DNS name of the client connecting to op-signer.
	ClientName string `yaml:"name"`
	// KeyName key resource name of the Cloud KMS
	KeyName     string   `yaml:"key"`
	ToAddresses []string `yaml:"toAddresses"`
	MaxValue    string   `yaml:"maxValue"`
}

func (c AuthConfig) MaxValueToInt() *big.Int {
	return hexutil.MustDecodeBig(c.MaxValue)
}

type SignerServiceConfig struct {
	Auth []AuthConfig `yaml:"auth"`
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

func (s SignerServiceConfig) GetAuthConfigForClient(clientName string) (*AuthConfig, error) {
	if clientName == "" {
		return nil, errors.New("client name is empty")
	}
	for _, ac := range s.Auth {
		if ac.ClientName == clientName {
			return &ac, nil
		}
	}
	return nil, fmt.Errorf("client '%s' is not authorized to use any keys", clientName)
}
