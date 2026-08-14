package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// migrations is the schema. There is no separate hand-applied file to keep in
// step with it, so a database built from scratch and one migrated from an
// earlier version cannot drift apart.
//
//go:embed migrations/*.sql
var migrations embed.FS

const migrationsDir = "migrations"

// Migrate brings the database up to the version this binary was built against.
//
// It takes a Postgres advisory lock for the duration, so two runners starting
// at once cannot apply the same migration twice.
func Migrate(ctx context.Context, uri string) error {
	sqlDB, err := sql.Open("pgx", uri)
	if err != nil {
		return fmt.Errorf("failed to open database for migration: %w", err)
	}
	defer sqlDB.Close()

	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return fmt.Errorf("failed to create migration locker: %w", err)
	}

	fsys, err := fs.Sub(migrations, migrationsDir)
	if err != nil {
		return fmt.Errorf("failed to read embedded migrations: %w", err)
	}

	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, fsys,
		goose.WithSessionLocker(locker),
		goose.WithDisableGlobalRegistry(true),
	)
	if err != nil {
		return fmt.Errorf("failed to create migration provider: %w", err)
	}

	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("failed to apply migrations: %w", err)
	}
	for _, r := range results {
		slog.Info("applied migration", "version", r.Source.Version, "path", r.Source.Path)
	}

	return checkVersion(ctx, provider)
}

// checkVersion refuses to run against a database ahead of this binary. Up only
// applies what is missing, so a rollback to an older binary would otherwise
// leave it reading a schema it does not know about.
func checkVersion(ctx context.Context, provider *goose.Provider) error {
	sources := provider.ListSources()
	if len(sources) == 0 {
		return fmt.Errorf("no migrations are embedded in this binary")
	}
	var want int64
	for _, s := range sources {
		want = max(want, s.Version)
	}

	got, err := provider.GetDBVersion(ctx)
	if err != nil {
		return fmt.Errorf("failed to read schema version: %w", err)
	}
	if got != want {
		return fmt.Errorf("database is at schema version %d but this binary expects %d", got, want)
	}

	slog.Info("database schema is current", "version", got)
	return nil
}
