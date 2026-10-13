package importjob

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/helltale/music/backend/internal/catalog/provider"
)

func TestConcurrentEnqueueKeepsOneJob(t *testing.T) {
	ctx := context.Background()
	pushOtherJobs(t)
	store := NewStore(testPool)
	externalID := newID(t)
	var wg sync.WaitGroup
	ids := make(chan string, 16)
	created := 0
	var mu sync.Mutex
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := store.Enqueue(ctx, "queue-test", externalID)
			if err != nil {
				t.Errorf("enqueue: %v", err)
				return
			}
			if result.Created {
				mu.Lock()
				created++
				mu.Unlock()
			}
			ids <- result.Job.ID
		}()
	}
	wg.Wait()
	close(ids)
	first := ""
	for id := range ids {
		if first == "" {
			first = id
		}
		if id != first {
			t.Fatalf("jobs %s and %s", first, id)
		}
	}
	if created != 1 || first == "" {
		t.Fatalf("created=%d id=%s", created, first)
	}
}

func TestLargeArtistImport(t *testing.T) {
	const releases = 20
	const perRelease = 2
	ctx := context.Background()
	source := wideCatalog{releases: releases, perRelease: perRelease}
	providerName := "wide"
	artistExternal := "wide-artist"
	removeExternalCatalog(t, providerName, artistExternal, source.releaseIDs(), source.recordingIDs())
	t.Cleanup(func() {
		removeExternalCatalog(t, providerName, artistExternal, source.releaseIDs(), source.recordingIDs())
	})
	pushOtherJobs(t)
	enqueued, err := NewStore(testPool).Enqueue(ctx, providerName, artistExternal)
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := startWorker(t, runCtx, map[string]CatalogSource{providerName: source})
	waitStatusFor(t, enqueued.Job.ID, StatusCompleted, time.Minute)
	recordings := releases * perRelease
	if countRows(t, `SELECT count(*) FROM recording_external_ids WHERE provider = 'wide'`) != recordings {
		t.Fatalf("recordings = %d", countRows(t, `SELECT count(*) FROM recording_external_ids WHERE provider = 'wide'`))
	}
	if countRows(t, `SELECT count(*) FROM release_external_ids WHERE provider = 'wide'`) != releases {
		t.Fatal("release count")
	}
	if countRows(t, `SELECT count(*) FROM artist_external_ids WHERE provider = 'wide' AND external_id = 'wide-artist'`) != 1 {
		t.Fatal("artist duplicated")
	}
	job, err := NewStore(testPool).Get(ctx, enqueued.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	var progress map[string]any
	if err := json.Unmarshal(job.Progress, &progress); err != nil {
		t.Fatal(err)
	}
	if progress["releases_done"] != float64(releases) || progress["recordings_imported"] != float64(recordings) || progress["audio_ready"] != float64(recordings) || progress["audio_failed"] != float64(0) {
		t.Fatalf("progress %s", job.Progress)
	}
	cancel()
	waitDone(t, done)
}

func TestProviderTimeoutRequeues(t *testing.T) {
	pushOtherJobs(t)
	enqueued, err := NewStore(testPool).Enqueue(context.Background(), "slow", newID(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startWorker(t, ctx, map[string]CatalogSource{"slow": timeoutCatalog{}})
	waitRequeued(t, enqueued.Job.ID)
	job, err := NewStore(testPool).Get(context.Background(), enqueued.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != StatusQueued || job.Attempt != 1 || job.LastError == nil || *job.LastError != "Temporary failure" {
		t.Fatalf("job = %+v", job)
	}
	cancel()
	waitDone(t, done)
}

func TestHotIndexesExist(t *testing.T) {
	want := []string{
		"recordings_isrc_idx",
		"release_tracks_recording_id_idx",
		"artist_external_ids_provider_id",
		"artist_external_ids_one_provider",
		"release_external_ids_provider_id",
		"recording_external_ids_provider_id",
		"recording_external_ids_one_provider",
		"import_jobs_one_active_subject",
		"import_jobs_claim_idx",
		"import_jobs_running_lease_idx",
		"artists_sync_due_idx",
	}
	for _, name := range want {
		var n int
		if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM pg_indexes WHERE schemaname = 'public' AND indexname = $1`, name).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("index %s missing", name)
		}
	}
}

type wideCatalog struct {
	releases   int
	perRelease int
}

func (w wideCatalog) GetArtist(context.Context, string) (provider.Artist, error) {
	return provider.Artist{ExternalID: "wide-artist", Name: "Wide Artist"}, nil
}

func (w wideCatalog) GetArtistReleases(context.Context, string) ([]provider.ReleaseSummary, error) {
	out := make([]provider.ReleaseSummary, w.releases)
	for i := range out {
		out[i] = provider.ReleaseSummary{ExternalID: w.releaseID(i), Title: w.releaseID(i), ReleaseType: "album"}
	}
	return out, nil
}

func (w wideCatalog) GetReleaseDetails(_ context.Context, externalID string) (provider.Release, error) {
	n := -1
	for i := range w.releases {
		if w.releaseID(i) == externalID {
			n = i
			break
		}
	}
	if n < 0 {
		return provider.Release{}, provider.ErrNotFound
	}
	credit := provider.Credit{ExternalID: "wide-artist", Name: "Wide Artist", Role: "primary"}
	ms := 1000
	tracks := make([]provider.Track, w.perRelease)
	for i := range tracks {
		tracks[i] = provider.Track{
			ExternalID:  w.recordingID(n, i),
			Title:       w.recordingID(n, i),
			DurationMS:  &ms,
			DiscNumber:  1,
			TrackNumber: i + 1,
			Artists:     []provider.Credit{credit},
		}
	}
	return provider.Release{
		ExternalID:  externalID,
		Title:       externalID,
		ReleaseType: "album",
		Artists:     []provider.Credit{credit},
		Tracks:      tracks,
	}, nil
}

func (w wideCatalog) releaseID(i int) string {
	return "wide-release-" + itoa(i)
}

func (w wideCatalog) recordingID(release, track int) string {
	return "wide-recording-" + itoa(release) + "-" + itoa(track)
}

func (w wideCatalog) releaseIDs() []string {
	out := make([]string, w.releases)
	for i := range out {
		out[i] = w.releaseID(i)
	}
	return out
}

func (w wideCatalog) recordingIDs() []string {
	out := make([]string, 0, w.releases*w.perRelease)
	for release := range w.releases {
		for track := range w.perRelease {
			out = append(out, w.recordingID(release, track))
		}
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits [12]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return string(digits[i:])
}

type timeoutCatalog struct{}

func (timeoutCatalog) GetArtist(context.Context, string) (provider.Artist, error) {
	return provider.Artist{}, context.DeadlineExceeded
}

func (timeoutCatalog) GetArtistReleases(context.Context, string) ([]provider.ReleaseSummary, error) {
	return nil, context.DeadlineExceeded
}

func (timeoutCatalog) GetReleaseDetails(context.Context, string) (provider.Release, error) {
	return provider.Release{}, context.DeadlineExceeded
}
