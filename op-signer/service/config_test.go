package service

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadConfig_DefaultKeyProvider(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "config-*.yaml")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	configData := `
auth:
  - name: "test-client"
    key: "test-key"
    chainID: 10
    fromAddress: "0x1234567890123456789012345678901234567890"
    toAddresses: ["0x1234567890123456789012345678901234567890"]
    maxValue: "0x1234"
`
	err = os.WriteFile(tmpFile.Name(), []byte(configData), 0644)
	require.NoError(t, err)

	config, err := ReadConfig(tmpFile.Name())
	require.NoError(t, err)
	assert.Equal(t, KeyProviderGCP, config.ProviderType)
}

func TestReadConfig_ExplicitKeyProvider(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "config-*.yaml")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	configData := `
provider: "AWS"
auth:
  - name: "test-client"
    key: "test-key"
    chainID: 10
    fromAddress: "0x1234567890123456789012345678901234567890"
    toAddresses: ["0x1234567890123456789012345678901234567890"]
    maxValue: "0x1234"
`
	err = os.WriteFile(tmpFile.Name(), []byte(configData), 0644)
	require.NoError(t, err)

	config, err := ReadConfig(tmpFile.Name())
	require.NoError(t, err)
	assert.Equal(t, KeyProviderAWS, config.ProviderType)
}

func TestReadConfig_InvalidKeyProvider(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "config-*.yaml")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	configData := `
provider: "INVALID"
auth:
  - name: "test-client"
    key: "test-key"
    chainID: 10
    fromAddress: "0x1234567890123456789012345678901234567890"
    toAddresses: ["0x1234567890123456789012345678901234567890"]
    maxValue: "0x1234"
`
	err = os.WriteFile(tmpFile.Name(), []byte(configData), 0644)
	require.NoError(t, err)

	_, err = ReadConfig(tmpFile.Name())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid provider")
}
