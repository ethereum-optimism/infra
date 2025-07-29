package integration_tests

import (
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/ethereum-optimism/infra/proxyd"
	"github.com/stretchr/testify/require"
)

const (
	backendTimeoutResponse = `{"jsonrpc": "2.0", "result": "hello", "id": 999}`
)

func TestBackendSpecificTimeout(t *testing.T) {
	// Create mock backends with different response times
	fastBackend := NewMockBackend(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Fast backend takes 2 seconds (exceeds its 400ms timeout)
		time.Sleep(2 * time.Second)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(backendTimeoutResponse))
	}))
	defer fastBackend.Close()

	slowBackend := NewMockBackend(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Slow backend takes 1.6 seconds (within its 5 second timeout)
		time.Sleep(1600 * time.Millisecond)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(backendTimeoutResponse))
	}))
	defer slowBackend.Close()

	// Set environment variables for backend URLs
	require.NoError(t, os.Setenv("FAST_BACKEND_RPC_URL", fastBackend.URL()))
	require.NoError(t, os.Setenv("SLOW_BACKEND_RPC_URL", slowBackend.URL()))

	// Start proxyd with the test configuration
	config := ReadConfig("backend_timeout")
	client := NewProxydClient("http://127.0.0.1:8546")
	_, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)
	defer shutdown()

	// Test scenario:
	// 1. Fast backend (configured with 400ms timeout) is primary but takes 2s (will timeout)
	// 2. Slow backend (configured with5s timeout) is fallback and takes 1.6s (will succeed)
	// 3. Request should succeed via the slow backend after fast backend times out

	t.Run("backend timeout fallback behavior", func(t *testing.T) {
		start := time.Now()

		res, statusCode, err := client.SendRPC("eth_chainId", nil)

		elapsed := time.Since(start)

		// Should succeed (via fallback backend)
		require.NoError(t, err)
		require.Equal(t, 200, statusCode)
		RequireEqualJSON(t, []byte(backendTimeoutResponse), res)

		// Should take at least 1.8 seconds (time to hit both backends minus a threshold)
		require.GreaterOrEqual(t, elapsed, 1800*time.Millisecond)

		// Verify that both backends were called
		// Fast backend should have been called first and timed out
		require.Equal(t, 1, len(fastBackend.Requests()))

		// Slow backend should have been called as fallback
		require.Equal(t, 1, len(slowBackend.Requests()))

		// Reset for next test
		fastBackend.Reset()
		slowBackend.Reset()
	})

	t.Run("fast backend timeout + slow backend error => 503 Service Unavailable", func(t *testing.T) {
		// Test that fast backend alone times out
		fastBackend.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Take longer than the 400ms timeout
			time.Sleep(500 * time.Millisecond)
			w.WriteHeader(200)
			_, _ = w.Write([]byte(backendTimeoutResponse))
		}))

		// Disable slow backend temporarily
		slowBackend.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(500) // Return error
		}))

		_, statusCode, err := client.SendRPC("eth_chainId", nil)

		// Should fail due to timeout - expect 503 Service Unavailable
		require.NoError(t, err)
		require.Equal(t, 503, statusCode)

		// Reset handlers
		fastBackend.Reset()
		slowBackend.Reset()
	})

	t.Run("both backends timeout => 503 Service Unavailable", func(t *testing.T) {
		// Test that both backends timeout
		fastBackend.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Take longer than the 400ms timeout
			time.Sleep(500 * time.Millisecond)
			w.WriteHeader(200)
			_, _ = w.Write([]byte(backendTimeoutResponse))
		}))

		slowBackend.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Take longer than the 5 second timeout
			time.Sleep(6 * time.Second)
			w.WriteHeader(200)
			_, _ = w.Write([]byte(backendTimeoutResponse))
		}))

		start := time.Now()

		_, statusCode, err := client.SendRPC("eth_chainId", nil)

		elapsed := time.Since(start)

		// Should fail due to both backends timing out - expect 503 Service Unavailable
		require.NoError(t, err)
		require.Equal(t, 503, statusCode)

		// Should timeout after trying both backends
		// Fast backend: ~400ms timeout
		// Slow backend: ~5s timeout
		// Total: should be at least 5.4 seconds
		require.GreaterOrEqual(t, elapsed, 5400*time.Millisecond)

		// Verify that both backends were called
		require.Equal(t, 1, len(fastBackend.Requests()))
		require.Equal(t, 1, len(slowBackend.Requests()))

		// Reset handlers
		fastBackend.Reset()
		slowBackend.Reset()
	})
}
