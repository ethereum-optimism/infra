CREATE TABLE pipelines
(
    id         VARCHAR PRIMARY KEY,
    number     INT       NOT NULL,
    commit     VARCHAR   NOT NULL,
    branch     VARCHAR   NOT NULL,
    created_at TIMESTAMP NOT NULL
);

CREATE INDEX pipelines_number_idx ON pipelines (number);
CREATE INDEX pipelines_branch_idx ON pipelines (branch);

CREATE TABLE jobs
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

CREATE INDEX jobs_pipeline_id_idx ON jobs (pipeline_id);
CREATE INDEX jobs_workflow_id_idx ON jobs (workflow_id);
CREATE INDEX jobs_name_idx ON jobs (name);
CREATE INDEX jobs_status_idx ON jobs (status);

CREATE TABLE test_results
(
    id      SERIAL PRIMARY KEY,
    job_id  VARCHAR NOT NULL REFERENCES jobs (id),
    name    VARCHAR NOT NULL,
    runtime float8  NOT NULL,
    status  VARCHAR NOT NULL,
    message TEXT
);

CREATE INDEX test_results_job_id_idx ON test_results (job_id);
CREATE INDEX test_results_name_idx ON test_results (name);
CREATE INDEX test_results_status_idx ON test_results (status);

CREATE TABLE schema_version
(
    version INT PRIMARY KEY
);

INSERT INTO schema_version (version)
VALUES (1);