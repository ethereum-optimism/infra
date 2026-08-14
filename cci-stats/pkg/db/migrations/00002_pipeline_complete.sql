-- +goose Up
-- DEFAULT TRUE is deliberate: every pre-existing row was only written once all
-- of its workflows had finished, so all of them are complete.
ALTER TABLE pipelines
    ADD COLUMN complete BOOLEAN NOT NULL DEFAULT TRUE;

CREATE INDEX pipelines_incomplete_idx ON pipelines (created_at) WHERE NOT complete;

-- +goose Down
DROP INDEX pipelines_incomplete_idx;

ALTER TABLE pipelines
    DROP COLUMN complete;
