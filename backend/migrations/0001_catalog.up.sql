CREATE TABLE artists (
    id uuid PRIMARY KEY,
    name text NOT NULL,
    description text,
    image_object_key text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL,
    CONSTRAINT artists_name_not_empty CHECK (char_length(name) > 0),
    CONSTRAINT artists_description_not_empty CHECK (description IS NULL OR char_length(description) > 0),
    CONSTRAINT artists_image_key_not_empty CHECK (image_object_key IS NULL OR char_length(image_object_key) > 0)
);

CREATE TABLE releases (
    id uuid PRIMARY KEY,
    title text NOT NULL,
    release_type text NOT NULL,
    release_date date,
    cover_object_key text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL,
    CONSTRAINT releases_title_not_empty CHECK (char_length(title) > 0),
    CONSTRAINT releases_type_known CHECK (release_type IN ('album', 'single', 'ep', 'compilation')),
    CONSTRAINT releases_cover_key_not_empty CHECK (cover_object_key IS NULL OR char_length(cover_object_key) > 0)
);

CREATE TABLE recordings (
    id uuid PRIMARY KEY,
    title text NOT NULL,
    isrc text,
    duration_ms integer,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL,
    CONSTRAINT recordings_title_not_empty CHECK (char_length(title) > 0),
    CONSTRAINT recordings_isrc_format CHECK (isrc IS NULL OR isrc ~ '^[A-Z]{2}[A-Z0-9]{3}[0-9]{7}$'),
    CONSTRAINT recordings_duration_non_negative CHECK (duration_ms IS NULL OR duration_ms >= 0)
);

CREATE INDEX recordings_isrc_idx ON recordings (isrc) WHERE isrc IS NOT NULL;

CREATE TABLE release_artists (
    release_id uuid NOT NULL REFERENCES releases (id),
    artist_id uuid NOT NULL REFERENCES artists (id),
    role text NOT NULL,
    PRIMARY KEY (release_id, artist_id, role),
    CONSTRAINT release_artists_role_known CHECK (role IN ('primary', 'featured'))
);

CREATE INDEX release_artists_artist_id_idx ON release_artists (artist_id);

CREATE TABLE recording_artists (
    recording_id uuid NOT NULL REFERENCES recordings (id),
    artist_id uuid NOT NULL REFERENCES artists (id),
    role text NOT NULL,
    PRIMARY KEY (recording_id, artist_id, role),
    CONSTRAINT recording_artists_role_known CHECK (role IN ('primary', 'featured'))
);

CREATE INDEX recording_artists_artist_id_idx ON recording_artists (artist_id);

CREATE TABLE release_tracks (
    release_id uuid NOT NULL REFERENCES releases (id),
    recording_id uuid NOT NULL REFERENCES recordings (id),
    disc_number integer NOT NULL,
    track_number integer NOT NULL,
    title_override text,
    CONSTRAINT release_tracks_position UNIQUE (release_id, disc_number, track_number),
    CONSTRAINT release_tracks_recording_once UNIQUE (release_id, recording_id),
    CONSTRAINT release_tracks_disc_positive CHECK (disc_number >= 1),
    CONSTRAINT release_tracks_number_positive CHECK (track_number >= 1),
    CONSTRAINT release_tracks_title_override_not_empty CHECK (title_override IS NULL OR char_length(title_override) > 0)
);

CREATE INDEX release_tracks_recording_id_idx ON release_tracks (recording_id);

CREATE TABLE artist_external_ids (
    artist_id uuid NOT NULL REFERENCES artists (id),
    provider text NOT NULL,
    external_id text NOT NULL,
    last_synced_at timestamptz NOT NULL,
    CONSTRAINT artist_external_ids_provider_id UNIQUE (provider, external_id),
    CONSTRAINT artist_external_ids_one_provider UNIQUE (artist_id, provider),
    CONSTRAINT artist_external_ids_provider_not_empty CHECK (char_length(provider) > 0),
    CONSTRAINT artist_external_ids_external_id_not_empty CHECK (char_length(external_id) > 0)
);

CREATE TABLE release_external_ids (
    release_id uuid NOT NULL REFERENCES releases (id),
    provider text NOT NULL,
    external_id text NOT NULL,
    last_synced_at timestamptz NOT NULL,
    CONSTRAINT release_external_ids_provider_id UNIQUE (provider, external_id),
    CONSTRAINT release_external_ids_one_provider UNIQUE (release_id, provider),
    CONSTRAINT release_external_ids_provider_not_empty CHECK (char_length(provider) > 0),
    CONSTRAINT release_external_ids_external_id_not_empty CHECK (char_length(external_id) > 0)
);

CREATE TABLE recording_external_ids (
    recording_id uuid NOT NULL REFERENCES recordings (id),
    provider text NOT NULL,
    external_id text NOT NULL,
    last_synced_at timestamptz NOT NULL,
    CONSTRAINT recording_external_ids_provider_id UNIQUE (provider, external_id),
    CONSTRAINT recording_external_ids_one_provider UNIQUE (recording_id, provider),
    CONSTRAINT recording_external_ids_provider_not_empty CHECK (char_length(provider) > 0),
    CONSTRAINT recording_external_ids_external_id_not_empty CHECK (char_length(external_id) > 0)
);
