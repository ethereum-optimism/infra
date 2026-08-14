-- Baseline. This reproduces the schema that was applied by hand up to this
-- point, so it is written to be a no-op against a database that already has it.
--
-- +goose Up
CREATE TABLE IF NOT EXISTS pipelines
(
    id         VARCHAR PRIMARY KEY,
    number     INT       NOT NULL,
    commit     VARCHAR   NOT NULL,
    branch     VARCHAR   NOT NULL,
    created_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS pipelines_number_idx ON pipelines (number);
CREATE INDEX IF NOT EXISTS pipelines_branch_idx ON pipelines (branch);

CREATE TABLE IF NOT EXISTS jobs
(
    id            VARCHAR PRIMARY KEY,
    pipeline_id   VARCHAR   NOT NULL REFERENCES pipelines (id),
    workflow_id   VARCHAR   NOT NULL,
    workflow_name VARCHAR   NOT NULL,
    number        INT       NOT NULL,
    name          VARCHAR   NOT NULL,
    status        VARCHAR   NOT NULL,
    started_at    TIMESTAMP NOT NULL,
    stopped_at    TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS jobs_pipeline_id_idx ON jobs (pipeline_id);
CREATE INDEX IF NOT EXISTS jobs_workflow_id_idx ON jobs (workflow_id);
CREATE INDEX IF NOT EXISTS jobs_name_idx ON jobs (name);
CREATE INDEX IF NOT EXISTS jobs_status_idx ON jobs (status);

CREATE TABLE IF NOT EXISTS test_results
(
    id      SERIAL PRIMARY KEY,
    job_id  VARCHAR NOT NULL REFERENCES jobs (id),
    name    VARCHAR NOT NULL,
    runtime float8  NOT NULL,
    status  VARCHAR NOT NULL,
    message TEXT
);

CREATE INDEX IF NOT EXISTS test_results_job_id_idx ON test_results (job_id);
CREATE INDEX IF NOT EXISTS test_results_name_idx ON test_results (name);
CREATE INDEX IF NOT EXISTS test_results_status_idx ON test_results (status);

-- Superseded by goose_db_version, and kept only so that a database built from
-- these migrations matches the one they were derived from.
CREATE TABLE IF NOT EXISTS schema_version
(
    version INT PRIMARY KEY
);

INSERT INTO schema_version (version)
VALUES (1)
ON CONFLICT DO NOTHING;

-- +goose Down
DROP TABLE IF EXISTS schema_version;
DROP TABLE IF EXISTS test_results;
DROP TABLE IF EXISTS jobs;
DROP TABLE IF EXISTS pipelines;
