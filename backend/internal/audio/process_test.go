package audio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/helltale/music/backend/internal/platform"
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	adminURL := getenv("TEST_ADMIN_DATABASE_URL", "postgres://music:music@127.0.0.1:5432/music?sslmode=disable")
	testURL := getenv("TEST_DATABASE_URL", "postgres://music:music@127.0.0.1:5432/music_audio_test?sslmode=disable")

	admin, err := platform.OpenPostgres(ctx, adminURL)
	if err != nil {
		os.Stderr.WriteString("audio tests: " + err.Error() + "\n")
		os.Exit(1)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE music_audio_test`); err != nil && !duplicateDatabase(err) {
		os.Stderr.WriteString("create music_audio_test: " + err.Error() + "\n")
		os.Exit(1)
	}
	admin.Close()

	pool, err := platform.OpenPostgres(ctx, testURL)
	if err != nil {
		os.Stderr.WriteString("audio tests: " + err.Error() + "\n")
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

func TestMigrationApplied(t *testing.T) {
	ctx := context.Background()
	applied, err := platform.Apply(ctx, testPool, migrationsDir())
	if err != nil {
		t.Fatal(err)
	}
	if applied != 0 {
		t.Fatalf("second apply = %d", applied)
	}
	var constraint string
	err = testPool.QueryRow(ctx, `
		SELECT conname FROM pg_constraint
		WHERE conrelid = 'audio_assets'::regclass AND contype = 'f'
	`).Scan(&constraint)
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign key lookup = %v constraint=%s", err, constraint)
	}
}

func TestPublishRejectsStaleGeneration(t *testing.T) {
	ctx := context.Background()
	store := NewStore(testPool)
	recordingID := newID(t)
	if _, err := store.Ensure(ctx, recordingID, "local", "track-one.wav"); err != nil {
		t.Fatal(err)
	}
	generation, err := store.BeginAttempt(ctx, recordingID)
	if err != nil {
		t.Fatal(err)
	}
	if generation != 1 {
		t.Fatalf("generation = %d", generation)
	}
	key := "recordings/" + recordingID + "/g1/playable.m4a"
	err = store.PublishReady(ctx, Asset{
		RecordingID:       recordingID,
		Generation:        generation + 1,
		PlayableObjectKey: &key,
		Status:            StatusReady,
	})
	if !errors.Is(err, ErrSuperseded) {
		t.Fatalf("stale publish error = %v", err)
	}
	asset, err := store.GetByRecording(ctx, recordingID)
	if err != nil {
		t.Fatal(err)
	}
	if asset.Status != StatusAcquiring || asset.PlayableObjectKey != nil {
		t.Fatalf("asset changed after stale publish: %+v", asset)
	}
}

func TestLocalProviderRejectsPath(t *testing.T) {
	provider := LocalProvider{Dir: t.TempDir()}
	for _, id := range []string{"", ".", "..", "../track-one", "track/one", `track\one`} {
		_, _, err := provider.Open(context.Background(), id)
		if !errors.Is(err, ErrSourceNotFound) {
			t.Fatalf("id %q error = %v", id, err)
		}
	}
}

func TestProcessStoresReadyAsset(t *testing.T) {
	requireFFmpeg(t)
	ctx := context.Background()
	objects := openBucket(t)
	recordingID := newID(t)
	processor := newProcessor(t, objects, LocalProvider{Dir: fixtureDir(t)})

	asset, err := processor.Process(ctx, recordingID, "local", "track-one", newID(t))
	if err != nil {
		t.Fatal(err)
	}
	if asset.Status != StatusReady || asset.Generation != 1 {
		t.Fatalf("status=%s generation=%d", asset.Status, asset.Generation)
	}
	if asset.SourceReference == nil || *asset.SourceReference != "track-one.wav" {
		t.Fatalf("reference = %v", asset.SourceReference)
	}
	if asset.Checksum == nil || *asset.Checksum != fileChecksum(t, filepath.Join(fixtureDir(t), "track-one.wav")) {
		t.Fatalf("checksum = %v", asset.Checksum)
	}
	if asset.Codec == nil || *asset.Codec != "aac" || asset.Container == nil || *asset.Container != "mp4" {
		t.Fatalf("codec=%v container=%v", asset.Codec, asset.Container)
	}
	if asset.Channels == nil || *asset.Channels != 2 || asset.DurationMS == nil || *asset.DurationMS <= 0 {
		t.Fatalf("channels=%v duration=%v", asset.Channels, asset.DurationMS)
	}
	if asset.SizeBytes == nil || *asset.SizeBytes <= 0 || asset.PlayableObjectKey == nil || asset.OriginalObjectKey == nil {
		t.Fatalf("keys=%v %v size=%v", asset.OriginalObjectKey, asset.PlayableObjectKey, asset.SizeBytes)
	}
	if *asset.OriginalObjectKey != "recordings/"+recordingID+"/g1/original.wav" {
		t.Fatalf("original key = %s", *asset.OriginalObjectKey)
	}
	if *asset.PlayableObjectKey != "recordings/"+recordingID+"/g1/playable.m4a" {
		t.Fatalf("playable key = %s", *asset.PlayableObjectKey)
	}
	original := httpGet(t, presign(t, objects, *asset.OriginalObjectKey))
	if string(original[:4]) != "RIFF" {
		t.Fatalf("original header = %q", original[:4])
	}
	playable := httpGet(t, presign(t, objects, *asset.PlayableObjectKey))
	if !bytesContain(playable, []byte("ftyp")) {
		t.Fatal("playable is not an mp4")
	}
	t.Cleanup(func() {
		_ = objects.Delete(context.Background(), *asset.OriginalObjectKey)
		_ = objects.Delete(context.Background(), *asset.PlayableObjectKey)
	})

	again, err := processor.Process(ctx, recordingID, "local", "track-one", newID(t))
	if err != nil {
		t.Fatal(err)
	}
	if again.Generation != 1 || again.Status != StatusReady || *again.Checksum != *asset.Checksum {
		t.Fatalf("repeat changed asset: generation=%d status=%s", again.Generation, again.Status)
	}
	nextOriginal := "recordings/" + recordingID + "/g2/original.wav"
	response, err := http.Get(presign(t, objects, nextOriginal))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("g2 status = %d", response.StatusCode)
	}
	if err := filepath.WalkDir(processor.TempDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			t.Fatalf("temp file remains: %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPublishHookKeepsAssetUnready(t *testing.T) {
	requireFFmpeg(t)
	ctx := context.Background()
	recordingID := newID(t)
	processor := newProcessor(t, discardStorage{}, LocalProvider{Dir: fixtureDir(t)})
	processor.AfterPublish = func(context.Context, pgx.Tx) error {
		return errors.New("job lost")
	}
	_, err := processor.Process(ctx, recordingID, "local", "track-one", newID(t))
	var process *ProcessError
	if !errors.As(err, &process) || process.Permanent {
		t.Fatalf("hook error = %#v", err)
	}
	asset, err := NewStore(testPool).GetByRecording(ctx, recordingID)
	if err != nil {
		t.Fatal(err)
	}
	if asset.Status == StatusReady || asset.PlayableObjectKey != nil {
		t.Fatalf("asset published despite hook failure: %+v", asset)
	}
	if asset.Status != StatusFailed || asset.Generation != 1 {
		t.Fatalf("asset = %+v", asset)
	}
}

func TestProcessMarksMissingAndInvalidFailed(t *testing.T) {
	requireFFmpeg(t)
	ctx := context.Background()
	dir := t.TempDir()
	processor := newProcessor(t, discardStorage{}, LocalProvider{Dir: dir})

	missingID := newID(t)
	_, err := processor.Process(ctx, missingID, "local", "missing-track", newID(t))
	var process *ProcessError
	if !errors.As(err, &process) || !process.Permanent || process.Message != "Audio source not found" {
		t.Fatalf("missing error = %#v", err)
	}
	missing, err := NewStore(testPool).GetByRecording(ctx, missingID)
	if err != nil {
		t.Fatal(err)
	}
	if missing.Status != StatusFailed || missing.Generation != 0 || missing.PlayableObjectKey != nil || missing.LastError == nil || *missing.LastError != "Audio source not found" {
		t.Fatalf("missing asset = %+v", missing)
	}

	if err := os.WriteFile(filepath.Join(dir, "empty.wav"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	emptyID := newID(t)
	_, err = processor.Process(ctx, emptyID, "local", "empty", newID(t))
	if !errors.As(err, &process) || process.Message != "Audio file is empty" || !process.Permanent {
		t.Fatalf("empty error = %#v", err)
	}
	empty, err := NewStore(testPool).GetByRecording(ctx, emptyID)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Status != StatusFailed || empty.Generation != 0 {
		t.Fatalf("empty asset = %+v", empty)
	}

	if err := os.WriteFile(filepath.Join(dir, "noise.wav"), []byte("not audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	noiseID := newID(t)
	_, err = processor.Process(ctx, noiseID, "local", "noise", newID(t))
	if !errors.As(err, &process) || process.Message != "Audio stream not found" || !process.Permanent {
		t.Fatalf("noise error = %#v", err)
	}
	noise, err := NewStore(testPool).GetByRecording(ctx, noiseID)
	if err != nil {
		t.Fatal(err)
	}
	if noise.Status != StatusFailed || noise.Generation != 0 || noise.PlayableObjectKey != nil {
		t.Fatalf("noise asset = %+v", noise)
	}
}

func TestProcessTimeoutIsTemporary(t *testing.T) {
	requireFFmpeg(t)
	ctx := context.Background()
	processor := newProcessor(t, discardStorage{}, LocalProvider{Dir: fixtureDir(t)})
	processor.Timeout = time.Nanosecond
	recordingID := newID(t)
	_, err := processor.Process(ctx, recordingID, "local", "track-one", newID(t))
	var process *ProcessError
	if !errors.As(err, &process) || process.Permanent || process.Message != "Audio processing timed out" {
		t.Fatalf("timeout error = %#v", err)
	}
	asset, err := NewStore(testPool).GetByRecording(ctx, recordingID)
	if err != nil {
		t.Fatal(err)
	}
	if asset.Status != StatusFailed || asset.Generation != 1 || asset.LastError == nil || *asset.LastError != "Audio processing timed out" {
		t.Fatalf("timeout asset = %+v", asset)
	}
}

type discardStorage struct{}

func (discardStorage) Put(context.Context, string, string, io.Reader, int64) error {
	return nil
}

func (discardStorage) Delete(context.Context, string) error { return nil }

func (discardStorage) PresignGet(context.Context, string, time.Duration) (string, time.Time, error) {
	return "", time.Time{}, errors.New("not stored")
}

func newProcessor(t *testing.T, objects ObjectStorage, source Provider) *Processor {
	t.Helper()
	return &Processor{
		DB:       testPool,
		Objects:  objects,
		Source:   source,
		TempDir:  t.TempDir(),
		MaxBytes: 200 << 20,
		Timeout:  30 * time.Second,
	}
}

func openBucket(t *testing.T) ObjectStorage {
	t.Helper()
	store, err := NewS3Store(S3Config{
		Endpoint:     getenv("S3_ENDPOINT", "http://127.0.0.1:9000"),
		Region:       getenv("S3_REGION", "us-east-1"),
		Bucket:       getenv("S3_BUCKET", "music"),
		AccessKey:    getenv("S3_ACCESS_KEY", "music"),
		SecretKey:    getenv("S3_SECRET_KEY", "musicsecret"),
		UsePathStyle: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func presign(t *testing.T, objects ObjectStorage, key string) string {
	t.Helper()
	url, _, err := objects.PresignGet(context.Background(), key, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return url
}

func httpGet(t *testing.T, url string) []byte {
	t.Helper()
	response, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d %s", url, response.StatusCode, body)
	}
	return body
}

func fileChecksum(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func fixtureDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(filepath.Dir(migrationsDir()), "testdata", "audio")
}

func requireFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Fatal("ffmpeg is not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Fatal("ffprobe is not installed")
	}
}

func bytesContain(haystack, needle []byte) bool {
	return len(haystack) >= len(needle) && (string(haystack[:4]) == "ftyp" || indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle []byte) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func newID(t *testing.T) string {
	t.Helper()
	id, err := platform.NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func duplicateDatabase(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42P04"
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
