package catalog

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrNotFound means the requested catalog row does not exist.
var ErrNotFound = errors.New("not found")

type db interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Store reads and writes catalog tables.
type Store struct {
	db db
}

// NewStore uses a pool or a transaction.
func NewStore(db db) *Store {
	return &Store{db: db}
}

// CreateArtist inserts an artist. The caller supplies the ID.
func (s *Store) CreateArtist(ctx context.Context, artist Artist) (Artist, error) {
	err := s.db.QueryRow(ctx, `
		INSERT INTO artists (id, name, description, image_object_key, updated_at)
		VALUES ($1, $2, $3, $4, now())
		RETURNING created_at, updated_at
	`, artist.ID, artist.Name, artist.Description, artist.ImageObjectKey).Scan(&artist.CreatedAt, &artist.UpdatedAt)
	if err != nil {
		return Artist{}, fmt.Errorf("create artist: %w", err)
	}
	return artist, nil
}

// GetArtist loads one artist.
func (s *Store) GetArtist(ctx context.Context, id string) (Artist, error) {
	var artist Artist
	err := s.db.QueryRow(ctx, `
		SELECT id, name, description, image_object_key, created_at, updated_at
		FROM artists
		WHERE id = $1
	`, id).Scan(&artist.ID, &artist.Name, &artist.Description, &artist.ImageObjectKey, &artist.CreatedAt, &artist.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Artist{}, ErrNotFound
	}
	if err != nil {
		return Artist{}, fmt.Errorf("get artist: %w", err)
	}
	return artist, nil
}

// ListArtists returns artists ordered by name.
// An empty query returns the whole page. A query matches a case-insensitive substring.
func (s *Store) ListArtists(ctx context.Context, query string, limit, offset int) ([]Artist, int, error) {
	pattern, useQuery := containsPattern(query)
	var total int
	if err := s.db.QueryRow(ctx, `
		SELECT count(*)
		FROM artists
		WHERE NOT $1 OR name ILIKE $2 ESCAPE '\'
	`, useQuery, pattern).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count artists: %w", err)
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, name, description, image_object_key, created_at, updated_at
		FROM artists
		WHERE NOT $1 OR name ILIKE $2 ESCAPE '\'
		ORDER BY name, id
		LIMIT $3 OFFSET $4
	`, useQuery, pattern, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list artists: %w", err)
	}
	defer rows.Close()
	var items []Artist
	for rows.Next() {
		var artist Artist
		if err := rows.Scan(&artist.ID, &artist.Name, &artist.Description, &artist.ImageObjectKey, &artist.CreatedAt, &artist.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan artist: %w", err)
		}
		items = append(items, artist)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("list artists: %w", err)
	}
	return items, total, nil
}

// CreateRelease inserts a release. The caller supplies the ID.
func (s *Store) CreateRelease(ctx context.Context, release Release) (Release, error) {
	err := s.db.QueryRow(ctx, `
		INSERT INTO releases (id, title, release_type, release_date, cover_object_key, updated_at)
		VALUES ($1, $2, $3, $4, $5, now())
		RETURNING created_at, updated_at
	`, release.ID, release.Title, release.ReleaseType, dateValue(release.ReleaseDate), release.CoverObjectKey).Scan(&release.CreatedAt, &release.UpdatedAt)
	if err != nil {
		return Release{}, fmt.Errorf("create release: %w", err)
	}
	return release, nil
}

// GetRelease loads one release.
func (s *Store) GetRelease(ctx context.Context, id string) (Release, error) {
	var release Release
	err := s.db.QueryRow(ctx, `
		SELECT id, title, release_type, release_date, cover_object_key, created_at, updated_at
		FROM releases
		WHERE id = $1
	`, id).Scan(&release.ID, &release.Title, &release.ReleaseType, &release.ReleaseDate, &release.CoverObjectKey, &release.CreatedAt, &release.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Release{}, ErrNotFound
	}
	if err != nil {
		return Release{}, fmt.Errorf("get release: %w", err)
	}
	return release, nil
}

// ListArtistReleases returns releases where the artist has any role, newest date first.
func (s *Store) ListArtistReleases(ctx context.Context, artistID string, limit, offset int) ([]Release, int, error) {
	var total int
	if err := s.db.QueryRow(ctx, `
		SELECT count(*)
		FROM releases
		WHERE id IN (SELECT release_id FROM release_artists WHERE artist_id = $1)
	`, artistID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count releases: %w", err)
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, title, release_type, release_date, cover_object_key, created_at, updated_at
		FROM releases
		WHERE id IN (SELECT release_id FROM release_artists WHERE artist_id = $1)
		ORDER BY release_date DESC NULLS LAST, title, id
		LIMIT $2 OFFSET $3
	`, artistID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list releases: %w", err)
	}
	defer rows.Close()
	var items []Release
	for rows.Next() {
		var release Release
		if err := rows.Scan(&release.ID, &release.Title, &release.ReleaseType, &release.ReleaseDate, &release.CoverObjectKey, &release.CreatedAt, &release.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan release: %w", err)
		}
		items = append(items, release)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("list releases: %w", err)
	}
	return items, total, nil
}

// AddReleaseArtist links an artist to a release.
func (s *Store) AddReleaseArtist(ctx context.Context, releaseID, artistID, role string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO release_artists (release_id, artist_id, role)
		VALUES ($1, $2, $3)
	`, releaseID, artistID, role)
	if err != nil {
		return fmt.Errorf("add release artist: %w", err)
	}
	return nil
}

// ListReleaseArtists returns artists of a release ordered by role, then name.
func (s *Store) ListReleaseArtists(ctx context.Context, releaseID string) ([]ArtistRole, error) {
	rows, err := s.db.Query(ctx, `
		SELECT a.id, a.name, ra.role
		FROM release_artists ra
		JOIN artists a ON a.id = ra.artist_id
		WHERE ra.release_id = $1
		ORDER BY ra.role, a.name, a.id
	`, releaseID)
	if err != nil {
		return nil, fmt.Errorf("list release artists: %w", err)
	}
	defer rows.Close()
	return scanRoles(rows)
}

// CreateRecording inserts a recording. The caller supplies the ID.
func (s *Store) CreateRecording(ctx context.Context, recording Recording) (Recording, error) {
	err := s.db.QueryRow(ctx, `
		INSERT INTO recordings (id, title, isrc, duration_ms, updated_at)
		VALUES ($1, $2, $3, $4, now())
		RETURNING created_at, updated_at
	`, recording.ID, recording.Title, recording.ISRC, recording.DurationMS).Scan(&recording.CreatedAt, &recording.UpdatedAt)
	if err != nil {
		return Recording{}, fmt.Errorf("create recording: %w", err)
	}
	return recording, nil
}

// GetRecording loads one recording.
func (s *Store) GetRecording(ctx context.Context, id string) (Recording, error) {
	var recording Recording
	err := s.db.QueryRow(ctx, `
		SELECT id, title, isrc, duration_ms, created_at, updated_at
		FROM recordings
		WHERE id = $1
	`, id).Scan(&recording.ID, &recording.Title, &recording.ISRC, &recording.DurationMS, &recording.CreatedAt, &recording.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Recording{}, ErrNotFound
	}
	if err != nil {
		return Recording{}, fmt.Errorf("get recording: %w", err)
	}
	return recording, nil
}

// ListRecordingsByISRC returns every recording with that ISRC. ISRC is not unique.
func (s *Store) ListRecordingsByISRC(ctx context.Context, isrc string) ([]Recording, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, title, isrc, duration_ms, created_at, updated_at
		FROM recordings
		WHERE isrc = $1
		ORDER BY id
	`, isrc)
	if err != nil {
		return nil, fmt.Errorf("list recordings by isrc: %w", err)
	}
	defer rows.Close()
	var items []Recording
	for rows.Next() {
		var recording Recording
		if err := rows.Scan(&recording.ID, &recording.Title, &recording.ISRC, &recording.DurationMS, &recording.CreatedAt, &recording.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan recording: %w", err)
		}
		items = append(items, recording)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list recordings by isrc: %w", err)
	}
	return items, nil
}

// AddRecordingArtist links an artist to a recording.
func (s *Store) AddRecordingArtist(ctx context.Context, recordingID, artistID, role string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO recording_artists (recording_id, artist_id, role)
		VALUES ($1, $2, $3)
	`, recordingID, artistID, role)
	if err != nil {
		return fmt.Errorf("add recording artist: %w", err)
	}
	return nil
}

// ListRecordingArtists returns artists of a recording ordered by role, then name.
func (s *Store) ListRecordingArtists(ctx context.Context, recordingID string) ([]ArtistRole, error) {
	rows, err := s.db.Query(ctx, `
		SELECT a.id, a.name, ra.role
		FROM recording_artists ra
		JOIN artists a ON a.id = ra.artist_id
		WHERE ra.recording_id = $1
		ORDER BY ra.role, a.name, a.id
	`, recordingID)
	if err != nil {
		return nil, fmt.Errorf("list recording artists: %w", err)
	}
	defer rows.Close()
	return scanRoles(rows)
}

// AddReleaseTrack places a recording on a release.
func (s *Store) AddReleaseTrack(ctx context.Context, track ReleaseTrack) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO release_tracks (release_id, recording_id, disc_number, track_number, title_override)
		VALUES ($1, $2, $3, $4, $5)
	`, track.ReleaseID, track.RecordingID, track.DiscNumber, track.TrackNumber, track.TitleOverride)
	if err != nil {
		return fmt.Errorf("add release track: %w", err)
	}
	return nil
}

// ListReleaseTracks returns positions in disc and track order.
// Title is the override when it is set, otherwise the recording title.
func (s *Store) ListReleaseTracks(ctx context.Context, releaseID string) ([]ReleaseTrack, error) {
	rows, err := s.db.Query(ctx, `
		SELECT rt.release_id, rt.recording_id, rt.disc_number, rt.track_number, rt.title_override,
		       COALESCE(rt.title_override, r.title), r.duration_ms
		FROM release_tracks rt
		JOIN recordings r ON r.id = rt.recording_id
		WHERE rt.release_id = $1
		ORDER BY rt.disc_number, rt.track_number
	`, releaseID)
	if err != nil {
		return nil, fmt.Errorf("list release tracks: %w", err)
	}
	defer rows.Close()
	var items []ReleaseTrack
	for rows.Next() {
		var track ReleaseTrack
		if err := rows.Scan(&track.ReleaseID, &track.RecordingID, &track.DiscNumber, &track.TrackNumber, &track.TitleOverride, &track.Title, &track.DurationMS); err != nil {
			return nil, fmt.Errorf("scan release track: %w", err)
		}
		items = append(items, track)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list release tracks: %w", err)
	}
	return items, nil
}

// AddArtistExternalID stores one provider id for an artist.
func (s *Store) AddArtistExternalID(ctx context.Context, row ExternalID) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO artist_external_ids (artist_id, provider, external_id, last_synced_at)
		VALUES ($1, $2, $3, $4)
	`, row.EntityID, row.Provider, row.ExternalID, row.LastSyncedAt)
	if err != nil {
		return fmt.Errorf("add artist external id: %w", err)
	}
	return nil
}

// FindArtistByExternalID returns the artist id for a provider identifier.
func (s *Store) FindArtistByExternalID(ctx context.Context, provider, externalID string) (string, error) {
	return s.findExternalID(ctx, `SELECT artist_id FROM artist_external_ids WHERE provider = $1 AND external_id = $2`, provider, externalID)
}

// AddReleaseExternalID stores one provider id for a release.
func (s *Store) AddReleaseExternalID(ctx context.Context, row ExternalID) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO release_external_ids (release_id, provider, external_id, last_synced_at)
		VALUES ($1, $2, $3, $4)
	`, row.EntityID, row.Provider, row.ExternalID, row.LastSyncedAt)
	if err != nil {
		return fmt.Errorf("add release external id: %w", err)
	}
	return nil
}

// FindReleaseByExternalID returns the release id for a provider identifier.
func (s *Store) FindReleaseByExternalID(ctx context.Context, provider, externalID string) (string, error) {
	return s.findExternalID(ctx, `SELECT release_id FROM release_external_ids WHERE provider = $1 AND external_id = $2`, provider, externalID)
}

// AddRecordingExternalID stores one provider id for a recording.
func (s *Store) AddRecordingExternalID(ctx context.Context, row ExternalID) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO recording_external_ids (recording_id, provider, external_id, last_synced_at)
		VALUES ($1, $2, $3, $4)
	`, row.EntityID, row.Provider, row.ExternalID, row.LastSyncedAt)
	if err != nil {
		return fmt.Errorf("add recording external id: %w", err)
	}
	return nil
}

// FindRecordingByExternalID returns the recording id for a provider identifier.
func (s *Store) FindRecordingByExternalID(ctx context.Context, provider, externalID string) (string, error) {
	return s.findExternalID(ctx, `SELECT recording_id FROM recording_external_ids WHERE provider = $1 AND external_id = $2`, provider, externalID)
}

func (s *Store) findExternalID(ctx context.Context, query, provider, externalID string) (string, error) {
	var id string
	err := s.db.QueryRow(ctx, query, provider, externalID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("find external id: %w", err)
	}
	return id, nil
}

func scanRoles(rows pgx.Rows) ([]ArtistRole, error) {
	var items []ArtistRole
	for rows.Next() {
		var role ArtistRole
		if err := rows.Scan(&role.ArtistID, &role.Name, &role.Role); err != nil {
			return nil, fmt.Errorf("scan artist role: %w", err)
		}
		items = append(items, role)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list artist roles: %w", err)
	}
	return items, nil
}

func containsPattern(query string) (string, bool) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", false
	}
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(query)
	return "%" + escaped + "%", true
}

// UpdateArtist replaces the name and description of an existing artist.
func (s *Store) UpdateArtist(ctx context.Context, artist Artist) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE artists
		SET name = $2, description = $3, updated_at = now()
		WHERE id = $1
	`, artist.ID, artist.Name, artist.Description)
	if err != nil {
		return fmt.Errorf("update artist: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateArtistName replaces the display name and leaves the description in place.
func (s *Store) UpdateArtistName(ctx context.Context, id, name string) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE artists
		SET name = $2, updated_at = now()
		WHERE id = $1
	`, id, name)
	if err != nil {
		return fmt.Errorf("update artist name: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateRelease replaces the mutable fields of an existing release.
func (s *Store) UpdateRelease(ctx context.Context, release Release) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE releases
		SET title = $2, release_type = $3, release_date = $4, updated_at = now()
		WHERE id = $1
	`, release.ID, release.Title, release.ReleaseType, dateValue(release.ReleaseDate))
	if err != nil {
		return fmt.Errorf("update release: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateRecording replaces the mutable fields of an existing recording.
func (s *Store) UpdateRecording(ctx context.Context, recording Recording) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE recordings
		SET title = $2, isrc = $3, duration_ms = $4, updated_at = now()
		WHERE id = $1
	`, recording.ID, recording.Title, recording.ISRC, recording.DurationMS)
	if err != nil {
		return fmt.Errorf("update recording: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ReplaceRecordingArtists sets the artist roles of a recording to the given list.
func (s *Store) ReplaceRecordingArtists(ctx context.Context, recordingID string, roles []ArtistRole) error {
	if _, err := s.db.Exec(ctx, `DELETE FROM recording_artists WHERE recording_id = $1`, recordingID); err != nil {
		return fmt.Errorf("replace recording artists: %w", err)
	}
	for _, role := range roles {
		if err := s.AddRecordingArtist(ctx, recordingID, role.ArtistID, role.Role); err != nil {
			return err
		}
	}
	return nil
}

// ReplaceReleaseArtists sets the artist roles of a release to the given list.
func (s *Store) ReplaceReleaseArtists(ctx context.Context, releaseID string, roles []ArtistRole) error {
	if _, err := s.db.Exec(ctx, `DELETE FROM release_artists WHERE release_id = $1`, releaseID); err != nil {
		return fmt.Errorf("replace release artists: %w", err)
	}
	for _, role := range roles {
		if err := s.AddReleaseArtist(ctx, releaseID, role.ArtistID, role.Role); err != nil {
			return err
		}
	}
	return nil
}

// ReplaceReleaseTracks sets the track list of a release to the given positions.
func (s *Store) ReplaceReleaseTracks(ctx context.Context, releaseID string, tracks []ReleaseTrack) error {
	if _, err := s.db.Exec(ctx, `DELETE FROM release_tracks WHERE release_id = $1`, releaseID); err != nil {
		return fmt.Errorf("replace release tracks: %w", err)
	}
	for _, track := range tracks {
		track.ReleaseID = releaseID
		if err := s.AddReleaseTrack(ctx, track); err != nil {
			return err
		}
	}
	return nil
}

// BindArtistExternalID inserts or refreshes the provider id of an artist.
func (s *Store) BindArtistExternalID(ctx context.Context, row ExternalID) error {
	return s.bindExternalID(ctx, "artist_external_ids", "artist_id", row)
}

// BindReleaseExternalID inserts or refreshes the provider id of a release.
func (s *Store) BindReleaseExternalID(ctx context.Context, row ExternalID) error {
	return s.bindExternalID(ctx, "release_external_ids", "release_id", row)
}

// BindRecordingExternalID inserts or refreshes the provider id of a recording.
func (s *Store) BindRecordingExternalID(ctx context.Context, row ExternalID) error {
	return s.bindExternalID(ctx, "recording_external_ids", "recording_id", row)
}

func (s *Store) bindExternalID(ctx context.Context, table, idColumn string, row ExternalID) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE `+table+`
		SET external_id = $3, last_synced_at = now()
		WHERE `+idColumn+` = $1 AND provider = $2
	`, row.EntityID, row.Provider, row.ExternalID)
	if err != nil {
		return fmt.Errorf("bind external id: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	_, err = s.db.Exec(ctx, `
		INSERT INTO `+table+` (`+idColumn+`, provider, external_id, last_synced_at)
		VALUES ($1, $2, $3, now())
	`, row.EntityID, row.Provider, row.ExternalID)
	if err != nil {
		return fmt.Errorf("bind external id: %w", err)
	}
	return nil
}

// ListRecordingCandidates returns recordings that have at least one primary artist.
// The matcher compares them in memory. The catalog is small enough for that on MVP.
func (s *Store) ListRecordingCandidates(ctx context.Context) ([]RecordingCandidate, error) {
	rows, err := s.db.Query(ctx, `
		SELECT r.id, r.title, r.isrc, r.duration_ms, a.name
		FROM recordings r
		JOIN recording_artists ra ON ra.recording_id = r.id AND ra.role = 'primary'
		JOIN artists a ON a.id = ra.artist_id
		ORDER BY r.id, a.name, a.id
	`)
	if err != nil {
		return nil, fmt.Errorf("list recording candidates: %w", err)
	}
	defer rows.Close()
	var (
		items []RecordingCandidate
		index = map[string]int{}
	)
	for rows.Next() {
		var (
			id, title, name string
			isrc            *string
			duration        *int
		)
		if err := rows.Scan(&id, &title, &isrc, &duration, &name); err != nil {
			return nil, fmt.Errorf("scan recording candidate: %w", err)
		}
		if pos, ok := index[id]; ok {
			items[pos].PrimaryNames = append(items[pos].PrimaryNames, name)
			continue
		}
		index[id] = len(items)
		items = append(items, RecordingCandidate{
			ID:           id,
			Title:        title,
			ISRC:         isrc,
			DurationMS:   duration,
			PrimaryNames: []string{name},
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list recording candidates: %w", err)
	}
	return items, nil
}

func uniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func dateValue(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format("2006-01-02")
}
