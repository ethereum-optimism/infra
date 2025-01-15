package integration_tests

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ethereum-optimism/infra/proxyd"
	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/stretchr/testify/assert"
)

func generateSecret() string {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		panic(fmt.Sprintf("failed read random bytes: %s", err.Error()))
	}

	return fmt.Sprintf("%x", b)
}

func TestPostgreSQLAuthentication(t *testing.T) {
	logger := &bytes.Buffer{}
	postgres := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Username("user").
		Password("password").
		Database("proxyd").
		Version(embeddedpostgres.V15).
		Port(9876).
		StartTimeout(45 * time.Second).
		StartParameters(map[string]string{"max_connections": "200"}).
		Logger(logger))
	err := postgres.Start()
	assert.NoErrorf(t, err, "postgresql should start without error")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	psql, err := proxyd.NewPSQLAuthenticator(ctx, "postgresql://invalid:user@localhost:9876/non-existing?sslmode=disable")
	assert.Nil(t, psql)
	assert.Error(t, err, "failed to ping database")

	psql, err = proxyd.NewPSQLAuthenticator(ctx, "postgresql://user:password@localhost:9876/proxyd?sslmode=disable")
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
				assert.Len(t, secret, 32)
				psql.NewSecret(secret)
				assert.NoError(t, psql.IsSecretValid(secret))
			}()
		}

		wg.Wait()
	})

	err = postgres.Stop()
	assert.NoErrorf(t, err, "postgresql should stop without error")
}
