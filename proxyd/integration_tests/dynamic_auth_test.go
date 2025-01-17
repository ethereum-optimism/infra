package integration_tests

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"os"
	"sync"
	"testing"

	"github.com/ethereum-optimism/infra/proxyd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const txHexAuth = "0x02f8b28201a406849502f931849502f931830147f9948f3ddd0fbf3e78ca1d6c" +
	"d17379ed88e261249b5280b84447e7ef2400000000000000000000000089c8b1" +
	"b2774201bac50f627403eac1b732459cf7000000000000000000000000000000" +
	"0000000000000000056bc75e2d63100000c080a0473c95566026c312c9664cd6" +
	"1145d2f3e759d49209fe96011ac012884ec5b017a0763b58f6fa6096e6ba28ee" +
	"08bfac58f58fb3b8bcef5af98578bdeaddf40bde42"

func generateSecret() string {
	b := make([]byte, 16)
	_, err := rand.Read(b)

	if err != nil {
		panic(fmt.Sprintf("failed read random bytes: %s", err.Error()))
	}

	return fmt.Sprintf("%x", b)
}

const (
	emptyResponse = "empty-response"
	validResponse = `{"jsonrpc":"2.0","result":"dummy","id":123}`
)

func expectedResponse(
	t *testing.T,
	client *ProxydHTTPClient,
	expectedMessage string,
	expectedResponseCode int,
	expectedError bool,
) {
	res, code1, err := client.SendRequest(makeSendRawTransaction(txHexAuth))

	if !expectedError {
		require.NoError(t, err)
	} else {
		require.Error(t, err)
	}

	require.Equal(t, expectedResponseCode, code1)
	if expectedMessage == emptyResponse {
		require.Empty(t, string(res))
	} else {
		require.Contains(t, string(res), expectedMessage)
	}
}

func TestNewSecretValidation(t *testing.T) {
	goodBackend := NewMockBackend(SingleResponseHandler(200, dummyRes))
	defer goodBackend.Close()

	require.NoError(t, os.Setenv("GOOD_BACKEND_RPC_URL", goodBackend.URL()))

	config := ReadConfig("dynamic_authentication")
	_, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)
	defer shutdown()

	adminClient, err := NewDynamicAuthClient("http://127.0.0.1:8545", "0xdeadbeef")
	require.NoError(t, err)

	secretEmpty := ""
	secret11chars := "12345678901"
	secret12chars := "123456789012"

	err = adminClient.PutKey(secretEmpty)
	require.Error(t, err)
	require.ErrorContains(t, err, `expected 200, received 404`)

	err = adminClient.PutKey(secret11chars)
	require.Error(t, err)
	require.ErrorContains(t, err, `expected 200, received 400`)

	err = adminClient.PutKey(secret12chars)
	require.NoError(t, err)

	// Define clients
	clientValidSecret := NewProxydClient(fmt.Sprintf("http://127.0.0.1:8545/%s", secret12chars))
	clientInvalidSecret := NewProxydClient(fmt.Sprintf("http://127.0.0.1:8545/%s", secret11chars))

	// Authentication enabled, invalid token provided
	expectedResponse(t, clientInvalidSecret, emptyResponse, http.StatusUnauthorized, false)

	// Authentication enabled, valid token provided
	expectedResponse(t, clientValidSecret, validResponse, http.StatusOK, false)
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
	expectedResponse(t, clientNoAuth, emptyResponse, http.StatusUnauthorized, false)

	// Authentication enabled, invalid token provided
	expectedResponse(t, clientInvalidSecret, emptyResponse, http.StatusUnauthorized, false)

	// Authentication enabled, valid token provided
	expectedResponse(t, clientValidSecret, validResponse, http.StatusOK, false)
}

func TestDynamicAuthenticationFeature(t *testing.T) {
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
	noAuthClient := NewProxydClient("http://127.0.0.1:8545")
	clientSecret1 := NewProxydClient(fmt.Sprintf("http://127.0.0.1:8545/%s", secret))
	clientSecret2 := NewProxydClient(fmt.Sprintf("http://127.0.0.1:8545/%s", secret2))

	// This request will fail because no authentication is provided
	expectedResponse(t, noAuthClient, emptyResponse, http.StatusUnauthorized, false)

	// Authentication enabled, but no token added via api
	expectedResponse(t, clientSecret1, emptyResponse, http.StatusUnauthorized, false)
	expectedResponse(t, clientSecret2, emptyResponse, http.StatusUnauthorized, false)

	// Add token
	require.NoError(t, adminClient.PutKey(secret))

	// Token already added, request must not fail
	expectedResponse(t, clientSecret1, validResponse, http.StatusOK, false)

	// This request will fail because second token not added
	expectedResponse(t, clientSecret2, emptyResponse, http.StatusUnauthorized, false)

	// Add second token and send request which must succeed
	require.NoError(t, adminClient.PutKey(secret2))
	expectedResponse(t, clientSecret2, validResponse, http.StatusOK, false)

	// Cannot add the same ticket second time(Maybe we should change it?)
	require.Error(t, adminClient.PutKey(secret2))

	// Delete second token
	require.NoError(t, adminClient.DeleteKey(secret2))
	// And make sure request will fail
	expectedResponse(t, clientSecret2, emptyResponse, http.StatusUnauthorized, false)
	// But first token should be still valid
	expectedResponse(t, clientSecret1, validResponse, http.StatusOK, false)
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
