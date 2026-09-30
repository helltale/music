package catalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/helltale/music/backend/internal/platform"
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	adminURL := getenv("TEST_ADMIN_DATABASE_URL", "postgres://music:music@127.0.0.1:5432/music?sslmode=disable")
	testURL := getenv("TEST_DATABASE_URL", "postgres://music:music@127.0.0.1:5432/music_test?sslmode=disable")

	admin, err := platform.OpenPostgres(ctx, adminURL)
	if err != nil {
		os.Stderr.WriteString("catalog tests: " + err.Error() + "\n")
		os.Exit(1)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE music_test`); err != nil && !duplicateDatabase(err) {
		os.Stderr.WriteString("create music_test: " + err.Error() + "\n")
		os.Exit(1)
	}
	admin.Close()

	pool, err := platform.OpenPostgres(ctx, testURL)
	if err != nil {
		os.Stderr.WriteString("catalog tests: " + err.Error() + "\n")
		os.Exit(1)
	}
	if _, err := platform.Apply(ctx, pool, migrationsDir()); err != nil {
		os.Stderr.WriteString("apply migrations: " + err.Error() + "\n")
		os.Exit(1)
	}
	testPool = pool
	code := m.Run()
	pool.Close()
	os.Exit(code)
}

var testPool *pgxpool.Pool

func TestMigrationsApplyOnce(t *testing.T) {
	ctx := context.Background()
	applied, err := platform.Apply(ctx, testPool, migrationsDir())
	if err != nil {
		t.Fatal(err)
	}
	if applied != 0 {
		t.Fatalf("second apply = %d", applied)
	}
	var version string
	if err := testPool.QueryRow(ctx, `SELECT version FROM schema_migrations WHERE version = '0001_catalog.up.sql'`).Scan(&version); err != nil {
		t.Fatal(err)
	}
}

func TestArtistPersistence(t *testing.T) {
	ctx := context.Background()
	store, rollback := newStore(t)
	defer rollback()

	description := "a test artist"
	created, err := store.CreateArtist(ctx, Artist{ID: newID(t), Name: "Test Artist", Description: &description})
	if err != nil {
		t.Fatal(err)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatal("timestamps were not set")
	}
	got, err := store.GetArtist(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Test Artist" || got.Description == nil || *got.Description != description {
		t.Fatalf("artist = %+v", got)
	}

	sameName := Artist{ID: newID(t), Name: "Test Artist"}
	if _, err := store.CreateArtist(ctx, sameName); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateArtist(ctx, Artist{ID: newID(t), Name: "Other"}); err != nil {
		t.Fatal(err)
	}
	percent := Artist{ID: newID(t), Name: "100%"}
	if _, err := store.CreateArtist(ctx, percent); err != nil {
		t.Fatal(err)
	}

	items, total, err := store.ListArtists(ctx, "test", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("search total=%d items=%d", total, len(items))
	}
	matched, _, err := store.ListArtists(ctx, "100%", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(matched) != 1 || matched[0].Name != "100%" {
		t.Fatalf("percent match = %+v", matched)
	}
	if _, err := store.GetArtist(ctx, "00000000-0000-4000-8000-000000000000"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing artist error = %v", err)
	}
}

func TestArtistRejectsEmptyName(t *testing.T) {
	store, rollback := newStore(t)
	defer rollback()
	_, err := store.CreateArtist(context.Background(), Artist{ID: newID(t), Name: ""})
	if !isCheck(err) {
		t.Fatalf("error = %v", err)
	}
}

func TestRecordingSharedByTwoReleases(t *testing.T) {
	ctx := context.Background()
	store, rollback := newStore(t)
	defer rollback()

	artist, err := store.CreateArtist(ctx, Artist{ID: newID(t), Name: "Test Artist"})
	if err != nil {
		t.Fatal(err)
	}
	isrc := "USTST2600001"
	duration := 180000
	recording, err := store.CreateRecording(ctx, Recording{ID: newID(t), Title: "Track One", ISRC: &isrc, DurationMS: &duration})
	if err != nil {
		t.Fatal(err)
	}
	otherDuration := 200000
	second, err := store.CreateRecording(ctx, Recording{ID: newID(t), Title: "Track One Again", ISRC: &isrc, DurationMS: &otherDuration})
	if err != nil {
		t.Fatal(err)
	}
	found, err := store.ListRecordingsByISRC(ctx, isrc)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 {
		t.Fatalf("isrc matches = %d", len(found))
	}
	if err := store.AddRecordingArtist(ctx, recording.ID, artist.ID, "primary"); err != nil {
		t.Fatal(err)
	}

	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	album, err := store.CreateRelease(ctx, Release{ID: newID(t), Title: "First Album", ReleaseType: "album", ReleaseDate: &day})
	if err != nil {
		t.Fatal(err)
	}
	deluxe, err := store.CreateRelease(ctx, Release{ID: newID(t), Title: "First Album Deluxe", ReleaseType: "album"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddReleaseArtist(ctx, album.ID, artist.ID, "primary"); err != nil {
		t.Fatal(err)
	}
	if err := store.AddReleaseArtist(ctx, deluxe.ID, artist.ID, "primary"); err != nil {
		t.Fatal(err)
	}
	if err := store.AddReleaseArtist(ctx, deluxe.ID, artist.ID, "featured"); err != nil {
		t.Fatal(err)
	}
	override := "Track One (Remaster)"
	if err := store.AddReleaseTrack(ctx, ReleaseTrack{ReleaseID: album.ID, RecordingID: recording.ID, DiscNumber: 1, TrackNumber: 1}); err != nil {
		t.Fatal(err)
	}
	if err := store.AddReleaseTrack(ctx, ReleaseTrack{ReleaseID: deluxe.ID, RecordingID: recording.ID, DiscNumber: 1, TrackNumber: 1, TitleOverride: &override}); err != nil {
		t.Fatal(err)
	}

	releases, total, err := store.ListArtistReleases(ctx, artist.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(releases) != 2 {
		t.Fatalf("releases total=%d len=%d", total, len(releases))
	}
	if releases[0].Title != "First Album" || releases[1].Title != "First Album Deluxe" {
		t.Fatalf("order = %s, %s", releases[0].Title, releases[1].Title)
	}
	if releases[0].ReleaseDate == nil || releases[0].ReleaseDate.Format("2006-01-02") != "2024-01-15" {
		t.Fatalf("date = %v", releases[0].ReleaseDate)
	}
	if releases[1].ReleaseDate != nil {
		t.Fatal("deluxe date should be empty")
	}

	roles, err := store.ListReleaseArtists(ctx, deluxe.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(roles) != 2 {
		t.Fatalf("roles = %+v", roles)
	}
	tracks, err := store.ListReleaseTracks(ctx, deluxe.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 1 || tracks[0].RecordingID != recording.ID || tracks[0].Title != override {
		t.Fatalf("tracks = %+v", tracks)
	}
	if second.ID == recording.ID {
		t.Fatal("recordings collapsed")
	}
}

func TestReleaseTrackDuplicatesRejected(t *testing.T) {
	ctx := context.Background()
	store, rollback := newStore(t)
	defer rollback()

	recording := mustRecording(t, store, "Track")
	other := mustRecording(t, store, "Other")
	release := mustRelease(t, store, "Album")
	if err := store.AddReleaseTrack(ctx, ReleaseTrack{ReleaseID: release.ID, RecordingID: recording.ID, DiscNumber: 1, TrackNumber: 1}); err != nil {
		t.Fatal(err)
	}
	err := store.AddReleaseTrack(ctx, ReleaseTrack{ReleaseID: release.ID, RecordingID: other.ID, DiscNumber: 1, TrackNumber: 1})
	if !isUnique(err) {
		t.Fatalf("position error = %v", err)
	}
}

func TestSameRecordingTwiceOnOneReleaseRejected(t *testing.T) {
	ctx := context.Background()
	store, rollback := newStore(t)
	defer rollback()

	recording := mustRecording(t, store, "Track")
	release := mustRelease(t, store, "Album")
	if err := store.AddReleaseTrack(ctx, ReleaseTrack{ReleaseID: release.ID, RecordingID: recording.ID, DiscNumber: 1, TrackNumber: 1}); err != nil {
		t.Fatal(err)
	}
	err := store.AddReleaseTrack(ctx, ReleaseTrack{ReleaseID: release.ID, RecordingID: recording.ID, DiscNumber: 1, TrackNumber: 2})
	if !isUnique(err) {
		t.Fatalf("recording error = %v", err)
	}
}

func TestInvalidISRCRejected(t *testing.T) {
	store, rollback := newStore(t)
	defer rollback()
	bad := "US-TST-26-00001"
	_, err := store.CreateRecording(context.Background(), Recording{ID: newID(t), Title: "Track", ISRC: &bad})
	if !isCheck(err) {
		t.Fatalf("error = %v", err)
	}
}

func TestExternalIDIdentity(t *testing.T) {
	ctx := context.Background()
	store, rollback := newStore(t)
	defer rollback()

	artist := mustArtist(t, store, "Artist")
	synced := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	row := ExternalID{EntityID: artist.ID, Provider: "fake", ExternalID: "test-artist", LastSyncedAt: synced}
	if err := store.AddArtistExternalID(ctx, row); err != nil {
		t.Fatal(err)
	}
	id, err := store.FindArtistByExternalID(ctx, "fake", "test-artist")
	if err != nil {
		t.Fatal(err)
	}
	if id != artist.ID {
		t.Fatalf("id = %s", id)
	}
	other := mustArtist(t, store, "Other")
	err = store.AddArtistExternalID(ctx, ExternalID{EntityID: other.ID, Provider: "fake", ExternalID: "test-artist", LastSyncedAt: synced})
	if !isUnique(err) {
		t.Fatalf("duplicate external id error = %v", err)
	}
}

func TestSecondProviderIDForSameArtistRejected(t *testing.T) {
	ctx := context.Background()
	store, rollback := newStore(t)
	defer rollback()
	artist := mustArtist(t, store, "Artist")
	synced := time.Now().UTC()
	if err := store.AddArtistExternalID(ctx, ExternalID{EntityID: artist.ID, Provider: "fake", ExternalID: "one", LastSyncedAt: synced}); err != nil {
		t.Fatal(err)
	}
	err := store.AddArtistExternalID(ctx, ExternalID{EntityID: artist.ID, Provider: "fake", ExternalID: "two", LastSyncedAt: synced})
	if !isUnique(err) {
		t.Fatalf("error = %v", err)
	}
}

func TestMissingArtistForeignKey(t *testing.T) {
	store, rollback := newStore(t)
	defer rollback()
	release := mustRelease(t, store, "Album")
	err := store.AddReleaseArtist(context.Background(), release.ID, newID(t), "primary")
	if !isForeignKey(err) {
		t.Fatalf("error = %v", err)
	}
}

func TestUnknownReleaseTypeRejected(t *testing.T) {
	store, rollback := newStore(t)
	defer rollback()
	_, err := store.CreateRelease(context.Background(), Release{ID: newID(t), Title: "Album", ReleaseType: "lp"})
	if !isCheck(err) {
		t.Fatalf("error = %v", err)
	}
}

func newStore(t *testing.T) (*Store, func()) {
	t.Helper()
	tx, err := testPool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return NewStore(tx), func() {
		_ = tx.Rollback(context.Background())
	}
}

func mustArtist(t *testing.T, store *Store, name string) Artist {
	t.Helper()
	artist, err := store.CreateArtist(context.Background(), Artist{ID: newID(t), Name: name})
	if err != nil {
		t.Fatal(err)
	}
	return artist
}

func mustRelease(t *testing.T, store *Store, title string) Release {
	t.Helper()
	release, err := store.CreateRelease(context.Background(), Release{ID: newID(t), Title: title, ReleaseType: "album"})
	if err != nil {
		t.Fatal(err)
	}
	return release
}

func mustRecording(t *testing.T, store *Store, title string) Recording {
	t.Helper()
	recording, err := store.CreateRecording(context.Background(), Recording{ID: newID(t), Title: title})
	if err != nil {
		t.Fatal(err)
	}
	return recording
}

func newID(t *testing.T) string {
	t.Helper()
	id, err := platform.NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func isCheck(err error) bool {
	return pgCode(err) == "23514"
}

func isUnique(err error) bool {
	return pgCode(err) == "23505"
}

func isForeignKey(err error) bool {
	return pgCode(err) == "23503"
}

func pgCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

func duplicateDatabase(err error) bool {
	return pgCode(err) == "42P04"
}

func migrationsDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "migrations"
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "migrations")
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
