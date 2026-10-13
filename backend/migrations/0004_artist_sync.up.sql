ALTER TABLE artists
    ADD COLUMN sync_enabled boolean NOT NULL DEFAULT false,
    ADD COLUMN last_synced_at timestamptz,
    ADD COLUMN next_sync_at timestamptz;

CREATE INDEX artists_sync_due_idx
    ON artists (next_sync_at, id)
    WHERE sync_enabled AND next_sync_at IS NOT NULL;
