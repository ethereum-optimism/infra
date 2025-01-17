package integration_tests

import (
	"crypto/rand"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/ethereum-optimism/infra/proxyd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func generateSecret() string {
	b := make([]byte, 16)
	_, err := rand.Read(b)

	if err != nil {
		panic(fmt.Sprintf("failed read random bytes: %s", err.Error()))
	}

	return fmt.Sprintf("%x", b)
}

func TestDynamicAuthenticationWhenStaticAuthenticationIsEnabled(t *testing.T) {
	goodBackend := NewMockBackend(SingleResponseHandler(200, dummyRes))
	defer goodBackend.Close()

	require.NoError(t, os.Setenv("GOOD_BACKEND_RPC_URL", goodBackend.URL()))

	config := ReadConfig("dynamic_authentication")
	_, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)
	defer shutdown()

	adminClient, err := NewDynamicAuthClient("http://127.0.0.1:8545", "0xdeadbeef")
	require.NoError(t, err)

	secret := generateSecret()
	require.Len(t, secret, 32)
	secret2 := generateSecret()
	require.Len(t, secret2, 32)

	// Define clients
	clientValidSecret := NewProxydClient(fmt.Sprintf("http://127.0.0.1:8545/%s", secret))
	clientInvalidSecret := NewProxydClient(fmt.Sprintf("http://127.0.0.1:8545/%s", secret2))
	clientNoAuth := NewProxydClient("http://127.0.0.1:8545")

	require.NoError(t, adminClient.PutKey(secret))

	// Authentication enabled, but no token provided
	_, code1, err := clientNoAuth.SendRequest(makeSendRawTransaction(txHex1))
	require.NoError(t, err)
	require.Equal(t, 401, code1)

	// Authentication enabled, invalid token provided
	_, code1, err = clientInvalidSecret.SendRequest(makeSendRawTransaction(txHex1))
	require.NoError(t, err)
	require.Equal(t, 401, code1)

	// Authentication enabled, valid token provided
	_, code1, err = clientValidSecret.SendRequest(makeSendRawTransaction(txHex1))
	require.NoError(t, err)
	require.Equal(t, 200, code1)
}

func TestDynamicAuthenticationFeature(t *testing.T) {
	goodBackend := NewMockBackend(SingleResponseHandler(200, dummyRes))
	defer goodBackend.Close()

	require.NoError(t, os.Setenv("GOOD_BACKEND_RPC_URL", goodBackend.URL()))

	config := ReadConfig("dynamic_authentication")
	client := NewProxydClient("http://127.0.0.1:8545")
	_, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)
	defer shutdown()

	adminClient, err := NewDynamicAuthClient("http://127.0.0.1:8545", "0xdeadbeef")
	require.NoError(t, err)

	// This request will fail because no authentication is provided
	_, code1, err := client.SendRequest(makeSendRawTransaction(txHex1))
	require.NoError(t, err)
	require.Equal(t, 401, code1)

	secret := generateSecret()
	require.Len(t, secret, 32)
	secret2 := generateSecret()
	require.Len(t, secret2, 32)

	// Define clients
	client2 := NewProxydClient(fmt.Sprintf("http://127.0.0.1:8545/%s", secret))
	client3 := NewProxydClient(fmt.Sprintf("http://127.0.0.1:8545/%s", secret2))

	// Authentication enabled, but no token added via api
	_, code1, err = client2.SendRequest(makeSendRawTransaction(txHex1))
	require.NoError(t, err)
	require.Equal(t, 401, code1)

	// Add token
	require.NoError(t, adminClient.PutKey(secret))

	// Token already added, request must not fail
	_, code1, err = client2.SendRequest(makeSendRawTransaction(txHex1))
	require.NoError(t, err)
	require.Equal(t, 200, code1)

	// This request will fail because second token not added
	_, code1, err = client3.SendRequest(makeSendRawTransaction(txHex1))
	require.NoError(t, err)
	require.Equal(t, 401, code1)

	// Add second token and send request
	require.NoError(t, adminClient.PutKey(secret2))
	_, code1, err = client3.SendRequest(makeSendRawTransaction(txHex1))
	require.NoError(t, err)
	require.Equal(t, 200, code1)

	// Cannot add the same ticket second time(Maybe we should change it?)
	require.Error(t, adminClient.PutKey(secret2))

	// Delete second token
	require.NoError(t, adminClient.DeleteKey(secret2))
	// And make sure request will fail
	_, code1, err = client3.SendRequest(makeSendRawTransaction(txHex1))
	require.NoError(t, err)
	require.Equal(t, 401, code1)
	// But first token should be still valid
	_, code1, err = client2.SendRequest(makeSendRawTransaction(txHex1))
	require.NoError(t, err)
	require.Equal(t, 200, code1)
}

func TestPostgreSQLAuthentication(t *testing.T) {
	psql, err := proxyd.NewInMemoryAuthenticator()
	assert.NotNil(t, psql)
	assert.NoError(t, err)

	err = psql.Initialize()
	assert.NoError(t, err, "initialize database should be successful")

	err = psql.Initialize()
	assert.NoError(t, err, "double initialize should not fail")

	t.Run("non existing secret", func(t *testing.T) {
		t.Parallel()
		assert.Error(t, psql.IsSecretValid("secret123"))
	})

	t.Run("add secret", func(t *testing.T) {
		secret := generateSecret()
		assert.Len(t, secret, 32)
		assert.NoError(t, psql.NewSecret(secret), "secret should be added without errors")
		assert.NoErrorf(t, psql.IsSecretValid(secret), "secret should be valid")
	})

	t.Run("delete secret", func(t *testing.T) {
		secret := generateSecret()
		assert.Len(t, secret, 32)
		assert.NoError(t, psql.NewSecret(secret), "secret should be added without errors")
		assert.NoErrorf(t, psql.IsSecretValid(secret), "secret should be valid")
		assert.NoError(t, psql.DeleteSecret(secret), "secret should be deleted without error")
		assert.Errorf(t, psql.IsSecretValid(secret), "secret should be now invalid")
		assert.NoError(t, psql.DeleteSecret(secret), "secret should be deleted without error, even if it does not exist")
	})

	t.Run("test simultaneous read", func(t *testing.T) {
		var wg sync.WaitGroup

		for i := 0; i < 64; i++ {
			wg.Add(1)

			go func() {
				defer wg.Done()

				secret := generateSecret()
				require.Len(t, secret, 32)
				require.NoError(t, psql.NewSecret(secret))
				require.NoError(t, psql.IsSecretValid(secret))
			}()
		}

		wg.Wait()
	})
}
