package proxyd

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMillisecondsToDuration(t *testing.T) {
	tests := []struct {
		name     string
		ms       int
		expected time.Duration
	}{
		{"zero milliseconds", 0, 0 * time.Millisecond},
		{"one millisecond", 1, 1 * time.Millisecond},
		{"hundred milliseconds", 100, 100 * time.Millisecond},
		{"one second", 1000, 1 * time.Second},
		{"five seconds", 5000, 5 * time.Second},
		{"negative milliseconds", -100, -100 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := millisecondsToDuration(tt.ms)
			if result != tt.expected {
				t.Errorf("millisecondsToDuration(%d) = %v, want %v", tt.ms, result, tt.expected)
			}
		})
	}
}

func TestSecondsToDuration(t *testing.T) {
	tests := []struct {
		name     string
		seconds  int
		expected time.Duration
	}{
		{"zero seconds", 0, 0 * time.Second},
		{"one second", 1, 1 * time.Second},
		{"five seconds", 5, 5 * time.Second},
		{"thirty seconds", 30, 30 * time.Second},
		{"negative seconds", -10, -10 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := secondsToDuration(tt.seconds)
			if result != tt.expected {
				t.Errorf("secondsToDuration(%d) = %v, want %v", tt.seconds, result, tt.expected)
			}
		})
	}
}

func TestMillisecondsToDurationIntegration(t *testing.T) {
	// Test that the duration actually works with time functions
	ms := 50
	duration := millisecondsToDuration(ms)

	start := time.Now()
	time.Sleep(duration)
	elapsed := time.Since(start)

	// Allow some tolerance for timing (within 10ms)
	tolerance := 10 * time.Millisecond
	if elapsed < duration-tolerance || elapsed > duration+tolerance {
		t.Errorf("Sleep duration was %v, expected approximately %v", elapsed, duration)
	}
}

func TestParseCommaSeparatedList(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{"happy path", "key1,key2,key3", []string{"key1", "key2", "key3"}},
		{"trims whitespace", "  key1  ,  key2  ", []string{"key1", "key2"}},
		{"filters empty strings", "key1,,key2", []string{"key1", "key2"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseCommaSeparatedList(tt.input)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("parseCommaSeparatedList(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestConfigureBackendTLS(t *testing.T) {
	t.Run("returns nil without TLS files", func(t *testing.T) {
		tlsConfig, err := configureBackendTLS(&BackendConfig{})
		require.NoError(t, err)
		require.Nil(t, tlsConfig)
	})

	t.Run("supports client cert without CA file", func(t *testing.T) {
		dir := t.TempDir()
		certPath, keyPath := writeSelfSignedClientCert(t, dir)

		tlsConfig, err := configureBackendTLS(&BackendConfig{
			ClientCertFile: certPath,
			ClientKeyFile:  keyPath,
		})
		require.NoError(t, err)
		require.NotNil(t, tlsConfig)
		require.Nil(t, tlsConfig.RootCAs)
		require.Len(t, tlsConfig.Certificates, 1)
	})

	t.Run("supports CA and client cert together", func(t *testing.T) {
		dir := t.TempDir()
		certPath, keyPath := writeSelfSignedClientCert(t, dir)

		tlsConfig, err := configureBackendTLS(&BackendConfig{
			CAFile:         certPath,
			ClientCertFile: certPath,
			ClientKeyFile:  keyPath,
		})
		require.NoError(t, err)
		require.NotNil(t, tlsConfig)
		require.NotNil(t, tlsConfig.RootCAs)
		require.Len(t, tlsConfig.Certificates, 1)
	})
}

func writeSelfSignedClientCert(t *testing.T, dir string) (string, string) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "proxyd-test-client"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})

	certPath := filepath.Join(dir, "client.crt")
	keyPath := filepath.Join(dir, "client.key")

	require.NoError(t, os.WriteFile(certPath, certPEM, 0o600))
	require.NoError(t, os.WriteFile(keyPath, keyPEM, 0o600))

	return certPath, keyPath
}
