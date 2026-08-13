# cci-stats

`cci-stats` is a program that reads stats from the CircleCI API, and writes them to a Postgres database. It is used
internally at OP Labs to keep track of CI pass rate, merge throughput, and other engineering health metrics.

To run the program, specify the following env vars:

- `CCI_KEY`: CircleCI API Key
- `DATABASE_URI`: Postgres database URI. The role needs DDL rights, because the runner migrates the
  schema at startup
- `PROJECT_SLUG`: Slug of the CCI project you want to grab stats for
- `BRANCH_PATTERN`: Regex pattern to filter branches by
- `WORKFLOW_PATTERN`: Regex pattern to filter workflows by
- `FETCH_LIMIT_DAYS`: Maximum number of days to look into the past for new build data
- `MAX_CONCURRENT_FETCH_JOBS`: How many concurrent requests to CCI to make at once. Used to tune rate limits
- `SLOW_TEST_THRESHOLD_SECONDS`: Tests slower than this threshold are written to the database as "slow tests" for
  further debugging

Then run `go run cmd/runner/main.go`.

## Schema

The schema is the migration chain in `pkg/db/migrations`, applied at startup. There is no separate
file to apply by hand, so a database built from scratch and one carried forward from an earlier
version cannot drift apart.

To change the schema, add a numbered file next to the others:

```sql
-- +goose Up
ALTER TABLE pipelines ADD COLUMN ...;

-- +goose Down
ALTER TABLE pipelines DROP COLUMN ...;
```

The runner holds a Postgres advisory lock while it migrates, so two runners starting at once cannot
apply the same migration twice. It also refuses to start when the database is ahead of the binary,
which stops a rolled-back deploy from reading a schema it was not built for.

`00001_initial_schema.sql` is a baseline: it reproduces the schema that was applied by hand up to
this point and is written to be a no-op against a database that already has it. The `schema_version`
table is from that era and is no longer read. It is kept only so a database built from the
migrations matches the deployed one, and a later migration can drop it.

### Testing the migrations

`pkg/db` tests run against a real Postgres, which CI provides. They skip locally unless you point
`TEST_DATABASE_URI` at one, and they fail rather than skip when `CI` is set, so a missing database
cannot pass silently:

```
TEST_DATABASE_URI=postgres://user@localhost:5432/postgres?sslmode=disable go test ./pkg/db
```

Each test works in its own schema and drops it afterwards.
