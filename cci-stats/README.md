# cci-stats

`cci-stats` is a program that reads stats from the CircleCI API, and writes them to a Postgres database. It is used
internally at OP Labs to keep track of CI pass rate, merge throughput, and other engineering health metrics.

To run the program, specify the following env vars:

- `CCI_KEY`: CircleCI API Key
- `DATABASE_URI`: Postgres database URI
- `PROJECT_SLUG`: Slug of the CCI project you want to grab stats for
- `BRANCH_PATTERN`: Regex pattern to filter branches by
- `WORKFLOW_PATTERN`: Regex pattern to filter workflows by
- `FETCH_LIMIT_DAYS`: Maximum number of days to look into the past for new build data
- `MAX_CONCURRENT_FETCH_JOBS`: How many concurrent requests to CCI to make at once. Used to tune rate limits
- `SLOW_TEST_THRESHOLD_SECONDS`: Tests slower than this threshold are written to the database as "slow tests" for
  further debugging

Then run `go run cmd/runner/main.go`.
