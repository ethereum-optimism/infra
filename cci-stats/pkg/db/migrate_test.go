package db

import (
	"context"
	_ "embed"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// legacyV1Schema is the schema as it was applied by hand, before these
// migrations existed. It is the state of the deployed database, and the
// baseline migration has to be a no-op against it.
//
//go:embed testdata/legacy_v1_schema.sql
var legacyV1Schema string

// defaultTestDatabaseURI matches the Postgres service container attached to the
// go-test job in .circleci/continue_config.yml.
const defaultTestDatabaseURI = "postgres://opc@localhost:5432/postgres?sslmode=disable"

// TestMigrateFromEmpty covers a database built from nothing.
func TestMigrateFromEmpty(t *testing.T) {
	ctx := context.Background()
	uri, schema := newTestSchema(ctx, t, "empty")

	if err := Migrate(ctx, uri); err != nil {
		t.Fatalf("migrating an empty database: %v", err)
	}
	assertVersion(ctx, t, uri, 1)
	for _, table := range []string{"pipelines", "jobs", "test_results"} {
		assertTableExists(ctx, t, uri, schema, table)
	}
}

// TestMigrateIsIdempotent covers the runner starting twice against a database
// that is already current.
func TestMigrateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	uri, _ := newTestSchema(ctx, t, "idempotent")

	if err := Migrate(ctx, uri); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if err := Migrate(ctx, uri); err != nil {
		t.Fatalf("second migrate against an already current database: %v", err)
	}
	assertVersion(ctx, t, uri, 1)
}

// TestMigrateOverLegacySchema is the one that matters for the first deploy: the
// baseline must adopt the hand-applied database without touching it, rather
// than failing on objects that already exist.
func TestMigrateOverLegacySchema(t *testing.T) {
	ctx := context.Background()
	uri, schema := newTestSchema(ctx, t, "legacy")
	exec(ctx, t, uri, legacyV1Schema)

	if err := Migrate(ctx, uri); err != nil {
		t.Fatalf("migrating a database that was set up by hand: %v", err)
	}
	assertVersion(ctx, t, uri, 1)

	// The point of a migration chain: a database built from scratch and one
	// carried over from the hand-applied schema must end up identical.
	freshURI, freshSchema := newTestSchema(ctx, t, "legacy_fresh")
	if err := Migrate(ctx, freshURI); err != nil {
		t.Fatalf("migrating an empty database: %v", err)
	}
	got := fingerprint(ctx, t, uri, schema)
	want := fingerprint(ctx, t, freshURI, freshSchema)
	if got != want {
		t.Errorf("adopted and freshly built schemas differ:\n--- adopted ---\n%s\n--- fresh ---\n%s", got, want)
	}
}

// TestMigrateRejectsNewerSchema covers a rollback to a binary that predates the
// schema in the database. Up applies only what is missing, so without the
// version check the old binary would run against a schema it does not know.
func TestMigrateRejectsNewerSchema(t *testing.T) {
	ctx := context.Background()
	uri, _ := newTestSchema(ctx, t, "newer")

	if err := Migrate(ctx, uri); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	exec(ctx, t, uri, "INSERT INTO goose_db_version (version_id, is_applied) VALUES (99, TRUE)")

	err := Migrate(ctx, uri)
	if err == nil {
		t.Fatal("migrating against a newer schema was allowed")
	}
	if !strings.Contains(err.Error(), "expects") {
		t.Errorf("error does not explain the version mismatch: %v", err)
	}
}

// newTestSchema gives a test its own Postgres schema, and a URI scoped to it so
// that the unqualified names in the migrations resolve there.
func newTestSchema(ctx context.Context, t *testing.T, name string) (uri string, schema string) {
	t.Helper()

	base := os.Getenv("TEST_DATABASE_URI")
	if base == "" {
		base = defaultTestDatabaseURI
	}

	conn, err := pgx.Connect(ctx, base)
	if err != nil {
		// A missing database must not quietly pass in CI, where the go-test job
		// runs one alongside.
		if os.Getenv("CI") != "" {
			t.Fatalf("no database at %s: %v", base, err)
		}
		t.Skipf("no database at %s, set TEST_DATABASE_URI to run: %v", base, err)
	}
	defer conn.Close(ctx)

	schema = "cci_stats_test_" + name
	if _, err := conn.Exec(ctx, fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schema)); err != nil {
		t.Fatalf("dropping stale schema: %v", err)
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE SCHEMA %s", schema)); err != nil {
		t.Fatalf("creating schema: %v", err)
	}
	t.Cleanup(func() {
		cleanup, err := pgx.Connect(context.Background(), base)
		if err != nil {
			t.Logf("could not reconnect to drop schema %s: %v", schema, err)
			return
		}
		defer cleanup.Close(context.Background())
		if _, err := cleanup.Exec(context.Background(), fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schema)); err != nil {
			t.Logf("could not drop schema %s: %v", schema, err)
		}
	})

	parsed, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parsing %s: %v", base, err)
	}
	q := parsed.Query()
	q.Set("search_path", schema)
	parsed.RawQuery = q.Encode()
	return parsed.String(), schema
}

func exec(ctx context.Context, t *testing.T, uri, sql string) {
	t.Helper()
	conn, err := pgx.Connect(ctx, uri)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, sql); err != nil {
		t.Fatalf("executing sql: %v", err)
	}
}

func assertVersion(ctx context.Context, t *testing.T, uri string, want int64) {
	t.Helper()
	conn, err := pgx.Connect(ctx, uri)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	defer conn.Close(ctx)

	var got int64
	row := conn.QueryRow(ctx, "SELECT MAX(version_id) FROM goose_db_version WHERE is_applied")
	if err := row.Scan(&got); err != nil {
		t.Fatalf("reading schema version: %v", err)
	}
	if got != want {
		t.Errorf("schema version = %d, want %d", got, want)
	}
}

func assertTableExists(ctx context.Context, t *testing.T, uri, schema, table string) {
	t.Helper()
	conn, err := pgx.Connect(ctx, uri)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	defer conn.Close(ctx)

	var exists bool
	row := conn.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = $1 AND table_name = $2)",
		schema, table)
	if err := row.Scan(&exists); err != nil {
		t.Fatalf("checking for table %s: %v", table, err)
	}
	if !exists {
		t.Errorf("table %s was not created", table)
	}
}

// fingerprint renders the columns and indexes of a schema as comparable text,
// with the schema name normalised out so two schemas can be compared.
func fingerprint(ctx context.Context, t *testing.T, uri, schema string) string {
	t.Helper()
	conn, err := pgx.Connect(ctx, uri)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	defer conn.Close(ctx)

	var sb strings.Builder
	rows, err := conn.Query(ctx, `
SELECT table_name, column_name, data_type, is_nullable, COALESCE(column_default, '')
FROM information_schema.columns
WHERE table_schema = $1
ORDER BY table_name, ordinal_position`, schema)
	if err != nil {
		t.Fatalf("reading columns: %v", err)
	}
	for rows.Next() {
		var table, column, dataType, nullable, def string
		if err := rows.Scan(&table, &column, &dataType, &nullable, &def); err != nil {
			t.Fatalf("scanning column: %v", err)
		}
		fmt.Fprintf(&sb, "column %s.%s %s null=%s default=%s\n", table, column, dataType, nullable, normalise(def, schema))
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("reading columns: %v", err)
	}

	idxRows, err := conn.Query(ctx,
		"SELECT indexname, indexdef FROM pg_indexes WHERE schemaname = $1 ORDER BY indexname", schema)
	if err != nil {
		t.Fatalf("reading indexes: %v", err)
	}
	defer idxRows.Close()
	for idxRows.Next() {
		var name, def string
		if err := idxRows.Scan(&name, &def); err != nil {
			t.Fatalf("scanning index: %v", err)
		}
		fmt.Fprintf(&sb, "index %s %s\n", name, normalise(def, schema))
	}
	if err := idxRows.Err(); err != nil {
		t.Fatalf("reading indexes: %v", err)
	}
	return sb.String()
}

func normalise(s, schema string) string {
	return strings.ReplaceAll(s, schema, "<schema>")
}
