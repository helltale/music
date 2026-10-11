package importjob

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/helltale/music/backend/internal/audio"
	"github.com/helltale/music/backend/internal/catalog/provider"
)

func TestArtistImportStoresPlayableAudio(t *testing.T) {
	requireFFmpeg(t)
	ctx := context.Background()
	if err := removeDemoCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	objects := openBucket(t)
	t.Cleanup(func() {
		if err := removeDemoCatalog(context.Background()); err != nil {
			t.Error(err)
		}
	})
	t.Cleanup(func() { deleteDemoObjects(t, objects) })

	pushOtherJobs(t)
	store := NewStore(testPool)
	enqueued, err := store.Enqueue(ctx, provider.ProviderFake, "test-artist")
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := startWorkerWithAudio(t, runCtx, map[string]CatalogSource{provider.ProviderFake: provider.NewFake()}, newPipeline(t, objects, fixtureAudioDir()))
	waitStatusFor(t, enqueued.Job.ID, StatusCompleted, time.Minute)
	assertAudioProgress(t, enqueued.Job.ID, 3, 0)
	assertAssets(t, map[string]assetExpect{
		"track-one":   {status: audio.StatusReady, generation: 1},
		"track-two":   {status: audio.StatusReady, generation: 1},
		"track-three": {status: audio.StatusReady, generation: 1},
	})
	playable := assetKey(t, "track-one", false)
	body := httpGet(t, presign(t, objects, playable))
	if !containsBytes(body, []byte("ftyp")) {
		t.Fatal("playable is not an mp4")
	}
	if countRows(t, `SELECT count(*) FROM artist_external_ids WHERE provider = 'fake' AND external_id = 'test-artist'`) != 1 {
		t.Fatal("artist was duplicated")
	}
	if countRows(t, `SELECT count(*) FROM release_tracks WHERE recording_id = (SELECT recording_id FROM recording_external_ids WHERE provider = 'fake' AND external_id = 'track-one')`) != 2 {
		t.Fatal("track one is not on both releases")
	}

	second, err := store.Enqueue(ctx, provider.ProviderFake, "test-artist")
	if err != nil {
		t.Fatal(err)
	}
	waitStatusFor(t, second.Job.ID, StatusCompleted, time.Minute)
	assertAudioProgress(t, second.Job.ID, 3, 0)
	assertAssets(t, map[string]assetExpect{
		"track-one":   {status: audio.StatusReady, generation: 1},
		"track-two":   {status: audio.StatusReady, generation: 1},
		"track-three": {status: audio.StatusReady, generation: 1},
	})
	cancel()
	waitDone(t, done)
}

func TestArtistImportKeepsCatalogWhenAudioFileIsMissing(t *testing.T) {
	requireFFmpeg(t)
	ctx := context.Background()
	if err := removeDemoCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	objects := openBucket(t)
	t.Cleanup(func() {
		if err := removeDemoCatalog(context.Background()); err != nil {
			t.Error(err)
		}
	})
	t.Cleanup(func() { deleteDemoObjects(t, objects) })

	dir := t.TempDir()
	for _, name := range []string{"track-one.wav", "track-two.wav"} {
		copyFile(t, filepath.Join(fixtureAudioDir(), name), filepath.Join(dir, name))
	}
	pushOtherJobs(t)
	store := NewStore(testPool)
	enqueued, err := store.Enqueue(ctx, provider.ProviderFake, "test-artist")
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := startWorkerWithAudio(t, runCtx, map[string]CatalogSource{provider.ProviderFake: provider.NewFake()}, newPipeline(t, objects, dir))
	waitStatusFor(t, enqueued.Job.ID, StatusPartiallyCompleted, time.Minute)
	job, err := store.Get(ctx, enqueued.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.LastError != nil {
		t.Fatalf("last_error = %s", *job.LastError)
	}
	assertAudioProgress(t, enqueued.Job.ID, 2, 1)
	assertAssets(t, map[string]assetExpect{
		"track-one":   {status: audio.StatusReady, generation: 1},
		"track-two":   {status: audio.StatusReady, generation: 1},
		"track-three": {status: audio.StatusFailed, generation: 0, lastError: "Audio source not found"},
	})
	if countRows(t, `SELECT count(*) FROM releases r JOIN release_external_ids e ON e.release_id = r.id WHERE e.provider = 'fake' AND e.external_id IN ('first-album', 'first-album-deluxe')`) != 2 {
		t.Fatal("releases were not kept")
	}
	if countRows(t, `SELECT count(*) FROM recordings r JOIN recording_external_ids e ON e.recording_id = r.id WHERE e.provider = 'fake' AND e.external_id IN ('track-one', 'track-two', 'track-three')`) != 3 {
		t.Fatal("recordings were not kept")
	}
	cancel()
	waitDone(t, done)
}

type assetExpect struct {
	status     string
	generation int
	lastError  string
}

func newPipeline(t *testing.T, objects audio.ObjectStorage, dir string) *audio.Processor {
	t.Helper()
	return &audio.Processor{
		DB:       testPool,
		Objects:  objects,
		Source:   audio.LocalProvider{Dir: dir},
		TempDir:  t.TempDir(),
		MaxBytes: 200 << 20,
		Timeout:  30 * time.Second,
	}
}

func assertAudioProgress(t *testing.T, jobID string, ready, failed int) {
	t.Helper()
	job, err := NewStore(testPool).Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	var progress map[string]any
	if err := json.Unmarshal(job.Progress, &progress); err != nil {
		t.Fatal(err)
	}
	if progress["releases_done"] != float64(2) || progress["recordings_imported"] != float64(3) || progress["audio_ready"] != float64(ready) || progress["audio_failed"] != float64(failed) {
		t.Fatalf("progress %s", job.Progress)
	}
	if progress["artist_id"] == "" {
		t.Fatalf("progress %s", job.Progress)
	}
}

func assertAssets(t *testing.T, want map[string]assetExpect) {
	t.Helper()
	for externalID, expected := range want {
		var status string
		var generation int
		var lastError *string
		err := testPool.QueryRow(context.Background(), `
			SELECT a.status, a.generation, a.last_error
			FROM audio_assets a
			JOIN recording_external_ids e ON e.recording_id = a.recording_id
			WHERE e.provider = 'fake' AND e.external_id = $1
		`, externalID).Scan(&status, &generation, &lastError)
		if err != nil {
			t.Fatalf("%s: %v", externalID, err)
		}
		if status != expected.status || generation != expected.generation {
			t.Fatalf("%s status=%s generation=%d", externalID, status, generation)
		}
		if expected.lastError != "" && (lastError == nil || *lastError != expected.lastError) {
			t.Fatalf("%s last_error=%v", externalID, lastError)
		}
	}
}

func assetKey(t *testing.T, externalID string, original bool) string {
	t.Helper()
	var originalKey, playableKey *string
	err := testPool.QueryRow(context.Background(), `
		SELECT a.original_object_key, a.playable_object_key
		FROM audio_assets a
		JOIN recording_external_ids e ON e.recording_id = a.recording_id
		WHERE e.provider = 'fake' AND e.external_id = $1
	`, externalID).Scan(&originalKey, &playableKey)
	if err != nil {
		t.Fatal(err)
	}
	if original {
		if originalKey == nil {
			t.Fatal("missing original key")
		}
		return *originalKey
	}
	if playableKey == nil {
		t.Fatal("missing playable key")
	}
	return *playableKey
}

func deleteDemoObjects(t *testing.T, objects audio.ObjectStorage) {
	t.Helper()
	rows, err := testPool.Query(context.Background(), `
		SELECT original_object_key, playable_object_key
		FROM audio_assets
		WHERE recording_id IN (
			SELECT recording_id FROM recording_external_ids
			WHERE provider = 'fake' AND external_id IN ('track-one', 'track-two', 'track-three')
		)`)
	if err != nil {
		t.Error(err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var originalKey, playableKey *string
		if err := rows.Scan(&originalKey, &playableKey); err != nil {
			t.Error(err)
			return
		}
		for _, key := range []*string{originalKey, playableKey} {
			if key != nil {
				if err := objects.Delete(context.Background(), *key); err != nil {
					t.Error(err)
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Error(err)
	}
}

func openBucket(t *testing.T) audio.ObjectStorage {
	t.Helper()
	store, err := audio.NewS3Store(audio.S3Config{
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

func presign(t *testing.T, objects audio.ObjectStorage, key string) string {
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
		t.Fatalf("GET status %d", response.StatusCode)
	}
	return body
}

func containsBytes(haystack, needle []byte) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	body, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func fixtureAudioDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "testdata/audio"
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata", "audio")
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
