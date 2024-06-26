package proxyd

import (
	"database/sql"
	"fmt"

	_ "github.com/lib/pq"
)

type DynamicAuthenticator interface {
	Initialize() error
	IsSecretValid(secret string) error
	NewSecret(secret string) error
	DeleteSecret(secret string) error
}

// todo: We may want to add in memory caching, cache can be refreshed only
// when new item is added(or periodically every 30 sec?)
type psqlAuthenticator struct {
	db *sql.DB
}

func (pa *psqlAuthenticator) IsSecretValid(secret string) error {
	rows, err := pa.db.Query("SELECT 1 FROM secrets WHERE secret=$1", secret)
	defer rows.Close()

	if err != nil {
		return fmt.Errorf("failed to check if secret is valid: %w", err)
	}

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

func NewPSQLAuthenticator(connString string) (DynamicAuthenticator, error) {
	db, err := sql.Open("postgres", connString)
	if err != nil {
		return nil, fmt.Errorf("failed to open psql connection: %w", err)
	}

	return &psqlAuthenticator{
		db: db,
	}, nil
}
