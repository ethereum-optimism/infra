-- +goose Up
CREATE INDEX test_results_job_id_status_idx ON test_results (job_id, status);

DROP INDEX test_results_job_id_idx;
DROP INDEX test_results_status_idx;

-- (job_id, name) is not unique, because CircleCI reports Solidity tests without
-- the contract that owns them, so there is no natural key to promote here.
ALTER TABLE test_results
    DROP CONSTRAINT test_results_pkey;

ALTER TABLE test_results
    DROP COLUMN id;

-- +goose Down
-- Restoring id rewrites the table.
ALTER TABLE test_results
    ADD COLUMN id SERIAL PRIMARY KEY;

CREATE INDEX test_results_job_id_idx ON test_results (job_id);
CREATE INDEX test_results_status_idx ON test_results (status);

DROP INDEX test_results_job_id_status_idx;
