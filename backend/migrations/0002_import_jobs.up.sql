CREATE TABLE import_jobs (
    id uuid PRIMARY KEY,
    type text NOT NULL,
    status text NOT NULL,
    provider text NOT NULL,
    subject_external_id text NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    progress jsonb NOT NULL DEFAULT '{}'::jsonb,
    attempt integer NOT NULL DEFAULT 0,
    max_attempts integer NOT NULL DEFAULT 5,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    locked_at timestamptz,
    locked_by uuid,
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    finished_at timestamptz,
    CONSTRAINT import_jobs_type_known CHECK (type IN ('artist_import')),
    CONSTRAINT import_jobs_status_known CHECK (status IN ('QUEUED', 'RUNNING', 'COMPLETED', 'PARTIALLY_COMPLETED', 'FAILED')),
    CONSTRAINT import_jobs_attempt_non_negative CHECK (attempt >= 0),
    CONSTRAINT import_jobs_max_attempts_positive CHECK (max_attempts >= 1),
    CONSTRAINT import_jobs_attempt_within_max CHECK (attempt <= max_attempts),
    CONSTRAINT import_jobs_provider_not_empty CHECK (char_length(provider) > 0),
    CONSTRAINT import_jobs_subject_not_empty CHECK (char_length(subject_external_id) > 0),
    CONSTRAINT import_jobs_last_error_not_empty CHECK (last_error IS NULL OR char_length(last_error) > 0)
);

CREATE UNIQUE INDEX import_jobs_one_active_subject
    ON import_jobs (type, provider, subject_external_id)
    WHERE status IN ('QUEUED', 'RUNNING');

CREATE INDEX import_jobs_claim_idx
    ON import_jobs (next_attempt_at, created_at)
    WHERE status = 'QUEUED';

CREATE INDEX import_jobs_running_lease_idx
    ON import_jobs (locked_at)
    WHERE status = 'RUNNING';
