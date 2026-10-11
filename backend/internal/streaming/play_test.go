package streaming

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/helltale/music/backend/internal/audio"
	"github.com/helltale/music/backend/internal/catalog"
	"github.com/helltale/music/backend/internal/platform"
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	adminURL := getenv("TEST_ADMIN_DATABASE_URL", "postgres://music:music@127.0.0.1:5432/music?sslmode=disable")
	testURL := getenv("TEST_DATABASE_URL", "postgres://music:music@127.0.0.1:5432/music_playback_test?sslmode=disable")

	admin, err := platform.OpenPostgres(ctx, adminURL)
	if err != nil {
		os.Stderr.WriteString("playback tests: " + err.Error() + "\n")
		os.Exit(1)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE music_playback_test`); err != nil && !duplicateDatabase(err) {
		os.Stderr.WriteString("create music_playback_test: " + err.Error() + "\n")
		os.Exit(1)
	}
	admin.Close()

	pool, err := platform.OpenPostgres(ctx, testURL)
	if err != nil {
		os.Stderr.WriteString("playback tests: " + err.Error() + "\n")
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

func TestPlayRequiresReadyAsset(t *testing.T) {
	ctx := context.Background()
	service := &Service{
		Recordings: catalog.NewStore(testPool),
		Assets:     audio.NewStore(testPool),
		Objects:    discardStorage{},
		TTL:        time.Hour,
	}
	mux := http.NewServeMux()
	Mount(mux, service)

	missing := httptest.NewRequest(http.MethodGet, "/api/v1/recordings/"+newID(t)+"/play", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, missing)
	assertError(t, rec, http.StatusNotFound, "RECORDING_NOT_FOUND")

	bad := httptest.NewRequest(http.MethodGet, "/api/v1/recordings/not-a-uuid/play", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, bad)
	assertError(t, rec, http.StatusBadRequest, "VALIDATION_ERROR")

	bare := createRecording(t)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, playRequest(bare))
	assertError(t, rec, http.StatusConflict, "AUDIO_NOT_READY")

	failed := createRecording(t)
	if _, err := audio.NewStore(testPool).Ensure(ctx, failed, audio.ProviderLocal, "missing.wav"); err != nil {
		t.Fatal(err)
	}
	if err := audio.NewStore(testPool).MarkFailed(ctx, failed, 0, "Audio source not found"); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, playRequest(failed))
	assertError(t, rec, http.StatusConflict, "AUDIO_NOT_READY")

	emptyKey := createRecording(t)
	if _, err := audio.NewStore(testPool).Ensure(ctx, emptyKey, audio.ProviderLocal, "empty.wav"); err != nil {
		t.Fatal(err)
	}
	generation, err := audio.NewStore(testPool).BeginAttempt(ctx, emptyKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := audio.NewStore(testPool).PublishReady(ctx, audio.Asset{RecordingID: emptyKey, Generation: generation, Status: audio.StatusReady}); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, playRequest(emptyKey))
	assertError(t, rec, http.StatusConflict, "AUDIO_NOT_READY")

	orphan := newID(t)
	if _, err := audio.NewStore(testPool).Ensure(ctx, orphan, audio.ProviderLocal, "orphan.wav"); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, playRequest(orphan))
	assertError(t, rec, http.StatusNotFound, "RECORDING_NOT_FOUND")
}

func TestPlaySignsPublicURLAndServesAudio(t *testing.T) {
	requireFFmpeg(t)
	ctx := context.Background()
	public := getenv("S3_PUBLIC_ENDPOINT", getenv("S3_ENDPOINT", "http://127.0.0.1:9000"))
	objects := openBucket(t, public)
	recordingID := createRecording(t)
	processor := &audio.Processor{
		DB:       testPool,
		Objects:  objects,
		Source:   audio.LocalProvider{Dir: fixtureDir(t)},
		TempDir:  t.TempDir(),
		MaxBytes: 200 << 20,
		Timeout:  30 * time.Second,
	}
	asset, err := processor.Process(ctx, recordingID, audio.ProviderLocal, "track-one", newID(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if asset.OriginalObjectKey != nil {
			_ = objects.Delete(context.Background(), *asset.OriginalObjectKey)
		}
		if asset.PlayableObjectKey != nil {
			_ = objects.Delete(context.Background(), *asset.PlayableObjectKey)
		}
	})

	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	previous := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(previous) })
	mux := http.NewServeMux()
	Mount(mux, &Service{
		Recordings: catalog.NewStore(testPool),
		Assets:     audio.NewStore(testPool),
		Objects:    objects,
		TTL:        time.Hour,
	})
	handler := platform.Middleware(logger, mux)
	rec := httptest.NewRecorder()
	before := time.Now()
	handler.ServeHTTP(rec, playRequest(recordingID))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"recording_id", "audio_asset_id", "url", "expires_at", "content_type", "duration_ms"} {
		if _, ok := body[key]; !ok {
			t.Fatalf("missing %s in %s", key, rec.Body.String())
		}
	}
	if len(body) != 6 {
		t.Fatalf("unexpected fields %s", rec.Body.String())
	}
	if body["recording_id"] != recordingID || body["audio_asset_id"] != asset.ID || body["content_type"] != "audio/mp4" {
		t.Fatalf("body %s", rec.Body.String())
	}
	duration, ok := body["duration_ms"].(float64)
	if !ok || duration <= 0 {
		t.Fatalf("duration %v", body["duration_ms"])
	}
	url, _ := body["url"].(string)
	if !strings.HasPrefix(url, public+"/music/recordings/"+recordingID+"/") || !strings.Contains(url, "playable.m4a") {
		t.Fatalf("url %s", url)
	}
	expires, err := time.Parse(time.RFC3339Nano, body["expires_at"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if expires.Before(before.Add(59*time.Minute)) || expires.After(time.Now().Add(61*time.Minute)) {
		t.Fatalf("expires_at %s", expires)
	}
	text := logs.String()
	if strings.Contains(text, url) || strings.Contains(text, "X-Amz-Signature") {
		t.Fatalf("log contains the signed url: %s", text)
	}
	if !strings.Contains(text, recordingID) || !strings.Contains(text, asset.ID) {
		t.Fatalf("log %s", text)
	}

	played := httpGet(t, url)
	if !bytes.Contains(played.body, []byte("ftyp")) {
		t.Fatal("downloaded object is not an mp4")
	}
	if played.header.Get("Content-Type") != "audio/mp4" {
		t.Fatalf("object content-type %s", played.header.Get("Content-Type"))
	}
	ranged := httpRange(t, url)
	if ranged.code != http.StatusPartialContent || len(ranged.body) == 0 {
		t.Fatalf("range status %d len %d", ranged.code, len(ranged.body))
	}
	probeDownloaded(t, played.body)
}

func TestPresignUsesPublicEndpoint(t *testing.T) {
	store, err := audio.NewS3Store(audio.S3Config{
		Endpoint:       "http://minio:9000",
		PublicEndpoint: "http://localhost:9000",
		Region:         "us-east-1",
		Bucket:         "music",
		AccessKey:      "music",
		SecretKey:      "musicsecret",
		UsePathStyle:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	url, _, err := store.PresignGet(context.Background(), "recordings/example/g1/playable.m4a", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(url, "http://localhost:9000/music/recordings/example/g1/playable.m4a") {
		t.Fatalf("url %s", url)
	}
}

type fetched struct {
	code   int
	header http.Header
	body   []byte
}

func playRequest(id string) *http.Request {
	return httptest.NewRequest(http.MethodGet, "/api/v1/recordings/"+id+"/play", nil)
}

func createRecording(t *testing.T) string {
	t.Helper()
	created, err := catalog.NewStore(testPool).CreateRecording(context.Background(), catalog.Recording{
		ID:    newID(t),
		Title: "Track One",
	})
	if err != nil {
		t.Fatal(err)
	}
	return created.ID
}

func assertError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != code || body.Error.Message == "" {
		t.Fatalf("error %s", rec.Body.String())
	}
}

func openBucket(t *testing.T, public string) audio.ObjectStorage {
	t.Helper()
	store, err := audio.NewS3Store(audio.S3Config{
		Endpoint:       getenv("S3_ENDPOINT", "http://127.0.0.1:9000"),
		PublicEndpoint: public,
		Region:         getenv("S3_REGION", "us-east-1"),
		Bucket:         getenv("S3_BUCKET", "music"),
		AccessKey:      getenv("S3_ACCESS_KEY", "music"),
		SecretKey:      getenv("S3_SECRET_KEY", "musicsecret"),
		UsePathStyle:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func httpGet(t *testing.T, url string) fetched {
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
		t.Fatalf("GET %d %s", response.StatusCode, body)
	}
	return fetched{code: response.StatusCode, header: response.Header, body: body}
}

func httpRange(t *testing.T, url string) fetched {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Range", "bytes=0-15")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return fetched{code: response.StatusCode, header: response.Header, body: body}
}

func probeDownloaded(t *testing.T, body []byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "playable.m4a")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", path)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil || seconds <= 0 {
		t.Fatalf("ffprobe duration %q", out)
	}
}

type discardStorage struct{}

func (discardStorage) Put(context.Context, string, string, io.Reader, int64) error { return nil }
func (discardStorage) Delete(context.Context, string) error                        { return nil }
func (discardStorage) PresignGet(context.Context, string, time.Duration) (string, time.Time, error) {
	return "", time.Time{}, errors.New("not stored")
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

func fixtureDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(filepath.Dir(migrationsDir()), "testdata", "audio")
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
