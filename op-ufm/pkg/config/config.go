package config

import (
	"math/big"

	"github.com/BurntSushi/toml"
	"github.com/pkg/errors"
)

type Config struct {
	LogLevel string `toml:"log_level"`

	Signer  SignerServiceConfig `toml:"signer_service"`
	Metrics MetricsConfig       `toml:"metrics"`
	Healthz HealthzConfig       `toml:"healthz"`

	Wallets   map[string]*WalletConfig   `toml:"wallets"`
	Providers map[string]*ProviderConfig `toml:"providers"`
}

type SignerServiceConfig struct {
	URL       string `toml:"url"`
	TLSCaCert string `toml:"tls_ca_cert"`
	TLSCert   string `toml:"tls_cert"`
	TLSKey    string `toml:"tls_key"`
}

type MetricsConfig struct {
	Enabled bool   `toml:"enabled"`
	Debug   bool   `toml:"debug"`
	Host    string `toml:"host"`
	Port    string `toml:"port"`
}

type HealthzConfig struct {
	Enabled bool   `toml:"enabled"`
	Host    string `toml:"host"`
	Port    string `toml:"port"`
}

type WalletConfig struct {
	ChainID big.Int `toml:"chain_id"`

	// signer | static
	SignerMethod string `toml:"signer_method"`
	Address      string `toml:"address"`
	// private key is used for static signing
	PrivateKey string `toml:"private_key"`

	// transaction parameters
	TxValue big.Int `toml:"tx_value"`
}

type ProviderConfig struct {
	Network string `toml:"network"`
	URL     string `toml:"url"`

	ReadOnly     bool         `toml:"read_only"`
	ReadInterval TOMLDuration `toml:"read_interval"`

	SendInterval                 TOMLDuration `toml:"send_interval"`
	SendTransactionRetryInterval TOMLDuration `toml:"send_transaction_retry_interval"`
	SendTransactionRetryTimeout  TOMLDuration `toml:"send_transaction_retry_timeout"`
	SendTransactionCoolDown      TOMLDuration `toml:"send_transaction_cool_down"`
	ReceiptRetrievalInterval     TOMLDuration `toml:"receipt_retrieval_interval"`
	ReceiptRetrievalTimeout      TOMLDuration `toml:"receipt_retrieval_timeout"`

	Wallet string `toml:"wallet"`
}

func New(file string) (*Config, error) {
	cfg := &Config{}
	if _, err := toml.DecodeFile(file, cfg); err != nil {
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

	if len(c.Wallets) == 0 {
		return errors.New("at least one wallet must be set")
	}

	if len(c.Providers) == 0 {
		return errors.New("at least one provider must be set")
	}

	for name, wallet := range c.Wallets {
		if wallet.ChainID.BitLen() == 0 {
			return errors.Errorf("wallet [%s] chain_id is missing", name)
		}
		if wallet.SignerMethod != "signer" && wallet.SignerMethod != "static" {
			return errors.Errorf("wallet [%s] signer_method is invalid", name)
		}
		if wallet.SignerMethod == "signer" {
			if c.Signer.URL == "" {
				return errors.New("signer url is missing")
			}
			if c.Signer.TLSCaCert == "" {
				return errors.New("signer tls_ca_cert is missing")
			}
			if c.Signer.TLSCert == "" {
				return errors.New("signer tls_cert is missing")
			}
			if c.Signer.TLSKey == "" {
				return errors.New("signer tls_key is missing")
			}
		}
		if wallet.SignerMethod == "static" {
			if wallet.PrivateKey == "" {
				return errors.Errorf("wallet [%s] private_key is missing", name)
			}
		}
		if wallet.Address == "" {
			return errors.Errorf("wallet [%s] address is missing", name)
		}
		if wallet.TxValue.BitLen() == 0 {
			return errors.Errorf("wallet [%s] tx_value is missing", name)
		}
	}

	for name, provider := range c.Providers {
		if provider.URL == "" {
			return errors.Errorf("provider [%s] url is missing", name)
		}
		if provider.ReadInterval == 0 {
			return errors.Errorf("provider [%s] read_interval is missing", name)
		}
		if provider.SendInterval == 0 {
			return errors.Errorf("provider [%s] send_interval is missing", name)
		}
		if provider.SendTransactionRetryInterval == 0 {
			return errors.Errorf("provider [%s] send_transaction_retry_interval is missing", name)
		}
		if provider.SendTransactionRetryTimeout == 0 {
			return errors.Errorf("provider [%s] send_transaction_retry_timeout is missing", name)
		}
		if provider.SendTransactionCoolDown == 0 {
			return errors.Errorf("provider [%s] send_transaction_cool_down is missing", name)
		}
		if provider.ReceiptRetrievalInterval == 0 {
			return errors.Errorf("provider [%s] receipt_retrieval_interval is missing", name)
		}
		if provider.ReceiptRetrievalTimeout == 0 {
			return errors.Errorf("provider [%s] receipt_retrieval_timeout is missing", name)
		}
		if provider.Wallet == "" {
			return errors.Errorf("provider [%s] wallet is missing", name)
		}
		if _, ok := c.Wallets[provider.Wallet]; !ok {
			return errors.Errorf("provider [%s] has an invalid wallet [%s]", name, provider.Wallet)
		}
	}

	if c.LogLevel == "" {
		c.LogLevel = "debug"
	}

	return nil
}
