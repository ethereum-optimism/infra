package proxyd

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	// Driver for psql
	"github.com/lib/pq"

	// Driver for in memory DB
	_ "github.com/proullon/ramsql/driver"
)

const (
	UniqueViolationErr = pq.ErrorCode("23505")
)

var (
	ErrSecretDuplicated = errors.New("secret already exists")
)

type DynamicAuthenticator interface {
	// Initialize is responsible for the storage backend initialization
	// The method must be idempotent. It is called multiple times and
	// the method should not produce error if backend is already
	// initialized.
	Initialize() error

	// IsSecretValid returns an error when the secret is invalid.
	// When the secret is valid the method must not return any error
	IsSecretValid(secret string) error

	// New Secret should add secret if it does not exist otherwise
	// it must return the `ErrSecretDuplicated` error.
	NewSecret(secret string) error

	// Delete secret deletes secret from the storage backend.
	// It may return error if secret does not exist but it is not mandatory
	DeleteSecret(secret string) error
}

// todo: We may want to add in memory caching, cache can be refreshed only
// when new item is added(or periodically every 30 sec?)
type psqlAuthenticator struct {
	db *sql.DB
}

func IsErrorCode(err error, errcode pq.ErrorCode) bool {
	if pgerr, ok := err.(*pq.Error); ok {
		return pgerr.Code == errcode
	}
	return false
}

func (pa *psqlAuthenticator) IsSecretValid(secret string) error {
	rows, err := pa.db.Query("SELECT secret FROM secrets WHERE secret=$1", secret)
	if err != nil {
		return fmt.Errorf("failed to check if secret is valid: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		return nil
	}

	return fmt.Errorf("secret not found")
}

func (pa *psqlAuthenticator) Initialize() error {
	const initSQL string = `
CREATE TABLE IF NOT EXISTS secrets(
	secret varchar(255) PRIMARY KEY NOT NULL,
	add_date date
);`

	if pa.db == nil {
		return fmt.Errorf("not connected to db")
	}

	if _, err := pa.db.Exec(initSQL); err != nil {
		return fmt.Errorf("failed to initialize secrets store in postgresql: %w", err)
	}

	return nil
}

func (pa *psqlAuthenticator) NewSecret(secret string) error {
	const insertSQL string = `INSERT INTO secrets (secret, add_date) VALUES($1, NOW())`

	_, err := pa.db.Exec(insertSQL, secret)
	if err != nil {
		if IsErrorCode(err, UniqueViolationErr) {
			return ErrSecretDuplicated
		}

		return fmt.Errorf("failed to insert secret into postgresql: %w", err)
	}

	return nil
}

func (pa *psqlAuthenticator) DeleteSecret(secret string) error {
	const deleteSQL string = `DELETE FROM secrets WHERE secret=$1`

	_, err := pa.db.Exec(deleteSQL, secret)
	if err != nil {
		return fmt.Errorf("failed to delete secret from postgresql: %w", err)
	}

	return nil
}

func NewPSQLAuthenticator(ctx context.Context, connString string) (DynamicAuthenticator, error) {
	db, err := sql.Open("postgres", connString)
	if err != nil {
		return nil, fmt.Errorf("failed to open psql connection: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &psqlAuthenticator{
		db: db,
	}, nil
}

// This function is used when we want to spawn the postgresql compatible backed
// authenticator but with in memory database.
// Remember this will be erased every time when the program restarts
func NewInMemoryAuthenticator() (DynamicAuthenticator, error) {
	ramdb, err := sql.Open("ramsql", "InMemoryProxyD")
	if err != nil {
		return nil, fmt.Errorf("failed to start ramsql db: %w", err)
	}

	return &psqlAuthenticator{
		db: ramdb,
	}, nil
}
