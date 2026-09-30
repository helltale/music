package audio

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/helltale/music/backend/internal/platform"
)

type db interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Store reads and writes audio_assets. recording_id is not a foreign key.
type Store struct {
	db db
}

// NewStore uses a pool or a transaction.
func NewStore(db db) *Store {
	return &Store{db: db}
}

const assetColumns = `
	id, recording_id, source_provider, source_reference, generation,
	original_object_key, playable_object_key, codec, container, bitrate,
	sample_rate, channels, duration_ms, size_bytes, checksum, status, last_error,
	created_at, updated_at
`

// GetByRecording loads the asset for one recording.
func (s *Store) GetByRecording(ctx context.Context, recordingID string) (Asset, error) {
	row := s.db.QueryRow(ctx, `SELECT `+assetColumns+` FROM audio_assets WHERE recording_id = $1`, recordingID)
	asset, err := scanAsset(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Asset{}, ErrNotFound
	}
	if err != nil {
		return Asset{}, fmt.Errorf("get audio asset: %w", err)
	}
	return asset, nil
}

// Ensure inserts a PENDING asset or returns the one that already exists.
func (s *Store) Ensure(ctx context.Context, recordingID, sourceProvider, sourceReference string) (Asset, error) {
	id, err := platform.NewUUID()
	if err != nil {
		return Asset{}, err
	}
	var reference any
	if sourceReference != "" {
		reference = sourceReference
	}
	row := s.db.QueryRow(ctx, `
		INSERT INTO audio_assets (id, recording_id, source_provider, source_reference, status, updated_at)
		VALUES ($1, $2, $3, $4, 'PENDING', now())
		ON CONFLICT (recording_id) DO UPDATE
		SET source_provider = EXCLUDED.source_provider,
		    source_reference = COALESCE(EXCLUDED.source_reference, audio_assets.source_reference)
		RETURNING `+assetColumns, id, recordingID, sourceProvider, reference)
	asset, err := scanAsset(row)
	if err != nil {
		return Asset{}, fmt.Errorf("ensure audio asset: %w", err)
	}
	return asset, nil
}

// BeginAttempt starts a new generation and marks the asset ACQUIRING.
func (s *Store) BeginAttempt(ctx context.Context, recordingID string) (int, error) {
	var generation int
	err := s.db.QueryRow(ctx, `
		UPDATE audio_assets
		SET generation = generation + 1,
		    status = 'ACQUIRING',
		    last_error = NULL,
		    updated_at = now()
		WHERE recording_id = $1
		RETURNING generation
	`, recordingID).Scan(&generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("begin audio attempt: %w", err)
	}
	return generation, nil
}

// MarkProcessing moves the current generation from ACQUIRING to PROCESSING.
func (s *Store) MarkProcessing(ctx context.Context, recordingID string, generation int) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE audio_assets
		SET status = 'PROCESSING', updated_at = now()
		WHERE recording_id = $1 AND generation = $2 AND status = 'ACQUIRING'
	`, recordingID, generation)
	if err != nil {
		return fmt.Errorf("mark audio processing: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrSuperseded
	}
	return nil
}

// PublishReady stores the playable metadata when this generation is still current.
func (s *Store) PublishReady(ctx context.Context, asset Asset) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE audio_assets
		SET status = 'READY',
		    original_object_key = $3,
		    playable_object_key = $4,
		    codec = $5,
		    container = $6,
		    bitrate = $7,
		    sample_rate = $8,
		    channels = $9,
		    duration_ms = $10,
		    size_bytes = $11,
		    checksum = $12,
		    last_error = NULL,
		    updated_at = now()
		WHERE recording_id = $1 AND generation = $2
	`, asset.RecordingID, asset.Generation, asset.OriginalObjectKey, asset.PlayableObjectKey,
		asset.Codec, asset.Container, asset.Bitrate, asset.SampleRate, asset.Channels,
		asset.DurationMS, asset.SizeBytes, asset.Checksum)
	if err != nil {
		return fmt.Errorf("publish audio asset: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrSuperseded
	}
	return nil
}

// MarkFailed records a short error for the current generation.
// generation 0 marks a failure that never started an attempt.
func (s *Store) MarkFailed(ctx context.Context, recordingID string, generation int, message string) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE audio_assets
		SET status = 'FAILED', last_error = $3, updated_at = now()
		WHERE recording_id = $1 AND generation = $2
	`, recordingID, generation, message)
	if err != nil {
		return fmt.Errorf("fail audio asset: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrSuperseded
	}
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanAsset(row scanner) (Asset, error) {
	var asset Asset
	err := row.Scan(
		&asset.ID, &asset.RecordingID, &asset.SourceProvider, &asset.SourceReference, &asset.Generation,
		&asset.OriginalObjectKey, &asset.PlayableObjectKey, &asset.Codec, &asset.Container, &asset.Bitrate,
		&asset.SampleRate, &asset.Channels, &asset.DurationMS, &asset.SizeBytes, &asset.Checksum, &asset.Status, &asset.LastError,
		&asset.CreatedAt, &asset.UpdatedAt,
	)
	if err != nil {
		return Asset{}, err
	}
	return asset, nil
}
