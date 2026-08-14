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

## Indexing model

Each run indexes pipelines created after the high water mark, `MAX(created_at)` over `pipelines`,
along with any already recorded as `complete = FALSE` within `FETCH_LIMIT_DAYS`. A pipeline that is
still running is recorded as incomplete rather than skipped, because pipelines do not finish in
creation order and the mark would otherwise move past it for good. Reindexing is idempotent.

`pkg/service/service.go` decides when a pipeline is complete and which workflows are worth
recording.

## Schema

The schema is the migration chain in `pkg/db/migrations`, applied at startup. Nothing is applied by
hand. To change it, add a numbered file with `-- +goose Up` and `-- +goose Down` sections.

`pkg/db` tests need a real Postgres. CI provides one; locally, point `TEST_DATABASE_URI` at one:

```
TEST_DATABASE_URI=postgres://user@localhost:5432/postgres?sslmode=disable go test ./pkg/db
```
