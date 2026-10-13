package importjob

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helltale/music/backend/internal/audio"
	"github.com/helltale/music/backend/internal/catalog"
	"github.com/helltale/music/backend/internal/catalog/provider"
)

func TestWorkerImportsFakeCatalog(t *testing.T) {
	ctx := context.Background()
	if err := removeDemoCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := removeDemoCatalog(context.Background()); err != nil {
			t.Error(err)
		}
	})
	pushOtherJobs(t)

	store := NewStore(testPool)
	first, err := store.Enqueue(ctx, provider.ProviderFake, "test-artist")
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := startWorker(t, runCtx, map[string]CatalogSource{provider.ProviderFake: provider.NewFake()})
	waitStatus(t, first.Job.ID, StatusCompleted)
	finished, err := store.Get(ctx, first.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	var progress map[string]any
	if err := json.Unmarshal(finished.Progress, &progress); err != nil {
		t.Fatal(err)
	}
	if progress["releases_total"] != float64(2) || progress["releases_done"] != float64(2) || progress["recordings_total"] != float64(3) || progress["recordings_imported"] != float64(3) {
		t.Fatalf("progress %s", finished.Progress)
	}
	if progress["audio_ready"] != float64(3) || progress["audio_failed"] != float64(0) {
		t.Fatalf("audio progress %s", finished.Progress)
	}
	var lastSynced *time.Time
	var nextSync *time.Time
	if err := testPool.QueryRow(ctx, `
		SELECT a.last_synced_at, a.next_sync_at
		FROM artists a
		JOIN artist_external_ids e ON e.artist_id = a.id
		WHERE e.provider = 'fake' AND e.external_id = 'test-artist'
	`).Scan(&lastSynced, &nextSync); err != nil {
		t.Fatal(err)
	}
	if lastSynced == nil || nextSync != nil {
		t.Fatalf("last=%v next=%v", lastSynced, nextSync)
	}
	if countRows(t, `SELECT count(*) FROM recordings WHERE isrc = 'USTST2600001'`) != 1 {
		t.Fatal("track one was duplicated")
	}
	if countRows(t, `SELECT count(*) FROM release_tracks WHERE recording_id = (SELECT id FROM recordings WHERE isrc = 'USTST2600001')`) != 2 {
		t.Fatal("track one is not on both releases")
	}

	second, err := store.Enqueue(ctx, provider.ProviderFake, "test-artist")
	if err != nil {
		t.Fatal(err)
	}
	if !second.Created || second.Job.ID == first.Job.ID {
		t.Fatal("completed subject did not enqueue a new job")
	}
	waitStatus(t, second.Job.ID, StatusCompleted)
	if countRows(t, `SELECT count(*) FROM artist_external_ids WHERE provider = 'fake' AND external_id = 'test-artist'`) != 1 {
		t.Fatal("repeat import created another artist")
	}
	cancel()
	waitDone(t, done)
}

func TestRecordingTagsPrefersSmallerRelease(t *testing.T) {
	tags := recordingTags(catalog.ArtistImport{
		Name: "Test Artist",
		Releases: []catalog.ReleaseImport{
			{
				ExternalID: "first-album-deluxe",
				Title:      "First Album Deluxe",
				Tracks: []catalog.TrackImport{
					{ExternalID: "track-one", Title: "Track One"},
					{ExternalID: "track-three", Title: "Track Three"},
				},
			},
			{
				ExternalID: "first-album",
				Title:      "First Album",
				Tracks: []catalog.TrackImport{
					{ExternalID: "track-one", Title: "Track One"},
					{ExternalID: "track-two", Title: "Track Two"},
				},
			},
		},
	})
	if tags["track-one"] != (audio.TrackTags{Artist: "Test Artist", Album: "First Album", Title: "Track One"}) {
		t.Fatalf("track-one = %+v", tags["track-one"])
	}
	if tags["track-two"] != (audio.TrackTags{Artist: "Test Artist", Album: "First Album", Title: "Track Two"}) {
		t.Fatalf("track-two = %+v", tags["track-two"])
	}
	if tags["track-three"] != (audio.TrackTags{Artist: "Test Artist", Album: "First Album Deluxe", Title: "Track Three"}) {
		t.Fatalf("track-three = %+v", tags["track-three"])
	}
}

func TestWorkerPassesTrackTags(t *testing.T) {
	ctx := context.Background()
	if err := removeDemoCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := removeDemoCatalog(context.Background()); err != nil {
			t.Error(err)
		}
	})
	pushOtherJobs(t)
	enqueued, err := NewStore(testPool).Enqueue(ctx, provider.ProviderFake, "test-artist")
	if err != nil {
		t.Fatal(err)
	}
	pipeline := &scriptedAudio{}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := startWorkerWithAudio(t, runCtx, map[string]CatalogSource{provider.ProviderFake: provider.NewFake()}, pipeline)
	waitStatus(t, enqueued.Job.ID, StatusCompleted)
	cancel()
	waitDone(t, done)
	seen := pipeline.tagsSeen()
	want := map[string]audio.TrackTags{
		"track-one":   {Artist: "Test Artist", Album: "First Album", Title: "Track One"},
		"track-two":   {Artist: "Test Artist", Album: "First Album", Title: "Track Two"},
		"track-three": {Artist: "Test Artist", Album: "First Album Deluxe", Title: "Track Three"},
	}
	if len(seen) != len(want) {
		t.Fatalf("tags = %+v", seen)
	}
	for id, tags := range want {
		if seen[id] != tags {
			t.Fatalf("%s = %+v", id, seen[id])
		}
	}
}

func TestTwoWorkersOneJob(t *testing.T) {
	pushOtherJobs(t)
	store := NewStore(testPool)
	enqueued, err := store.Enqueue(context.Background(), "gate", newID(t))
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{}, 2)
	gate := &gateCatalog{entered: entered}
	catalogs := map[string]CatalogSource{"gate": gate}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := startWorker(t, ctx, catalogs)
	second := startWorker(t, ctx, catalogs)
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("no worker claimed the job")
	}
	select {
	case <-entered:
		t.Fatal("two workers entered one job")
	case <-time.After(300 * time.Millisecond):
	}
	cancel()
	waitDone(t, first)
	waitDone(t, second)
	job, err := store.Get(context.Background(), enqueued.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != StatusQueued || job.Attempt != 0 {
		t.Fatalf("after shutdown = %+v", job)
	}
}

func TestWorkerFailsUnknownArtist(t *testing.T) {
	pushOtherJobs(t)
	store := NewStore(testPool)
	enqueued, err := store.Enqueue(context.Background(), provider.ProviderFake, "missing-artist")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startWorker(t, ctx, map[string]CatalogSource{provider.ProviderFake: provider.NewFake()})
	waitStatus(t, enqueued.Job.ID, StatusFailed)
	job, err := store.Get(context.Background(), enqueued.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Attempt != 1 || job.LastError == nil || *job.LastError != "Artist not found" {
		t.Fatalf("job = %+v", job)
	}
	cancel()
	waitDone(t, done)
}

func TestWorkerFailsPermanentProviderError(t *testing.T) {
	pushOtherJobs(t)
	store := NewStore(testPool)
	enqueued, err := store.Enqueue(context.Background(), "rejected", newID(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startWorker(t, ctx, map[string]CatalogSource{
		"rejected": errCatalog{err: fmt.Errorf("musicbrainz status 400: %w", provider.ErrPermanent)},
	})
	waitStatus(t, enqueued.Job.ID, StatusFailed)
	job, err := store.Get(context.Background(), enqueued.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Attempt != 1 || job.LastError == nil || *job.LastError != "Provider request was rejected" {
		t.Fatalf("job = %+v", job)
	}
	cancel()
	waitDone(t, done)
}

func TestWorkerSkipsUnusableRelease(t *testing.T) {
	ctx := context.Background()
	removeExternalCatalog(t, "mixed", "mixed-artist", []string{"mixed-album"}, []string{"mixed-track"})
	t.Cleanup(func() {
		removeExternalCatalog(t, "mixed", "mixed-artist", []string{"mixed-album"}, []string{"mixed-track"})
	})
	pushOtherJobs(t)
	store := NewStore(testPool)
	enqueued, err := store.Enqueue(ctx, "mixed", "mixed-artist")
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := startWorker(t, runCtx, map[string]CatalogSource{"mixed": mixedCatalog{}})
	waitStatus(t, enqueued.Job.ID, StatusCompleted)
	job, err := store.Get(ctx, enqueued.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	var progress map[string]any
	if err := json.Unmarshal(job.Progress, &progress); err != nil {
		t.Fatal(err)
	}
	if progress["releases_total"] != float64(1) || progress["recordings_imported"] != float64(1) || progress["audio_ready"] != float64(1) {
		t.Fatalf("progress %s", job.Progress)
	}
	cancel()
	waitDone(t, done)
}

type mixedCatalog struct{}

func (mixedCatalog) GetArtist(context.Context, string) (provider.Artist, error) {
	return provider.Artist{ExternalID: "mixed-artist", Name: "Mixed Artist"}, nil
}

func (mixedCatalog) GetArtistReleases(context.Context, string) ([]provider.ReleaseSummary, error) {
	return []provider.ReleaseSummary{
		{ExternalID: "mixed-album", Title: "Mixed Album", ReleaseType: "album"},
		{ExternalID: "mixed-bad", Title: "Bad", ReleaseType: "album"},
	}, nil
}

func (mixedCatalog) GetReleaseDetails(_ context.Context, externalID string) (provider.Release, error) {
	if externalID == "mixed-bad" {
		return provider.Release{}, provider.ErrUnusable
	}
	ms := 1000
	credit := provider.Credit{ExternalID: "mixed-artist", Name: "Mixed Artist", Role: "primary"}
	return provider.Release{
		ExternalID:  "mixed-album",
		Title:       "Mixed Album",
		ReleaseType: "album",
		Artists:     []provider.Credit{credit},
		Tracks: []provider.Track{{
			ExternalID:  "mixed-track",
			Title:       "Mixed Track",
			DurationMS:  &ms,
			DiscNumber:  1,
			TrackNumber: 1,
			Artists:     []provider.Credit{credit},
		}},
	}, nil
}

func TestWorkerPartialAudioKeepsCatalog(t *testing.T) {
	ctx := context.Background()
	if err := removeDemoCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := removeDemoCatalog(context.Background()); err != nil {
			t.Error(err)
		}
	})
	pushOtherJobs(t)
	store := NewStore(testPool)
	enqueued, err := store.Enqueue(ctx, provider.ProviderFake, "test-artist")
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := startWorkerWithAudio(t, runCtx, map[string]CatalogSource{provider.ProviderFake: provider.NewFake()}, &scriptedAudio{
		errs: map[string]error{
			"track-three": &audio.ProcessError{Message: "Audio source not found", Permanent: true},
		},
	})
	waitStatus(t, enqueued.Job.ID, StatusPartiallyCompleted)
	job, err := store.Get(ctx, enqueued.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.LastError != nil {
		t.Fatalf("last_error = %s", *job.LastError)
	}
	var progress map[string]any
	if err := json.Unmarshal(job.Progress, &progress); err != nil {
		t.Fatal(err)
	}
	if progress["recordings_imported"] != float64(3) || progress["audio_ready"] != float64(2) || progress["audio_failed"] != float64(1) {
		t.Fatalf("progress %s", job.Progress)
	}
	if countRows(t, `SELECT count(*) FROM artists a JOIN artist_external_ids e ON e.artist_id = a.id WHERE e.provider = 'fake' AND e.external_id = 'test-artist'`) != 1 {
		t.Fatal("catalog artist is missing")
	}
	if countRows(t, `SELECT count(*) FROM recordings r JOIN recording_external_ids e ON e.recording_id = r.id WHERE e.provider = 'fake' AND e.external_id IN ('track-one', 'track-two', 'track-three')`) != 3 {
		t.Fatal("catalog recordings were removed")
	}
	cancel()
	waitDone(t, done)
}

func TestWorkerRequeuesTemporaryAudio(t *testing.T) {
	ctx := context.Background()
	if err := removeDemoCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := removeDemoCatalog(context.Background()); err != nil {
			t.Error(err)
		}
	})
	pushOtherJobs(t)
	store := NewStore(testPool)
	enqueued, err := store.Enqueue(ctx, provider.ProviderFake, "test-artist")
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := startWorkerWithAudio(t, runCtx, map[string]CatalogSource{provider.ProviderFake: provider.NewFake()}, &scriptedAudio{
		errs: map[string]error{
			"track-two": &audio.ProcessError{Message: "Audio processing timed out", Permanent: false},
		},
	})
	waitRequeued(t, enqueued.Job.ID)
	job, err := store.Get(ctx, enqueued.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.LastError == nil || *job.LastError != "Temporary failure" {
		t.Fatalf("job = %+v", job)
	}
	var progress map[string]any
	if err := json.Unmarshal(job.Progress, &progress); err != nil {
		t.Fatal(err)
	}
	if progress["recordings_imported"] != float64(3) || progress["audio_ready"] != float64(2) || progress["audio_failed"] != float64(0) {
		t.Fatalf("progress %s", job.Progress)
	}
	if countRows(t, `SELECT count(*) FROM artists a JOIN artist_external_ids e ON e.artist_id = a.id WHERE e.provider = 'fake' AND e.external_id = 'test-artist'`) != 1 {
		t.Fatal("catalog artist is missing")
	}
	cancel()
	waitDone(t, done)
}

func TestWorkerRequeuesTemporaryFailure(t *testing.T) {
	pushOtherJobs(t)
	store := NewStore(testPool)
	enqueued, err := store.Enqueue(context.Background(), "down", newID(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startWorker(t, ctx, map[string]CatalogSource{"down": errCatalog{err: errors.New("database unavailable")}})
	waitRequeued(t, enqueued.Job.ID)
	job, err := store.Get(context.Background(), enqueued.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Attempt != 1 || job.LastError == nil || *job.LastError != "Temporary failure" || !job.NextAttemptAt.After(time.Now()) {
		t.Fatalf("job = %+v", job)
	}
	cancel()
	waitDone(t, done)
}

type gateCatalog struct {
	entered chan struct{}
}

func (g *gateCatalog) GetArtist(ctx context.Context, _ string) (provider.Artist, error) {
	select {
	case g.entered <- struct{}{}:
	case <-ctx.Done():
		return provider.Artist{}, ctx.Err()
	}
	<-ctx.Done()
	return provider.Artist{}, ctx.Err()
}

func (g *gateCatalog) GetArtistReleases(context.Context, string) ([]provider.ReleaseSummary, error) {
	return nil, nil
}

func (g *gateCatalog) GetReleaseDetails(context.Context, string) (provider.Release, error) {
	return provider.Release{}, nil
}

type errCatalog struct {
	err error
}

func (e errCatalog) GetArtist(context.Context, string) (provider.Artist, error) {
	return provider.Artist{}, e.err
}

func (e errCatalog) GetArtistReleases(context.Context, string) ([]provider.ReleaseSummary, error) {
	return nil, e.err
}

func (e errCatalog) GetReleaseDetails(context.Context, string) (provider.Release, error) {
	return provider.Release{}, e.err
}

func startWorker(t *testing.T, ctx context.Context, catalogs map[string]CatalogSource) <-chan struct{} {
	t.Helper()
	return startWorkerWithAudio(t, ctx, catalogs, &scriptedAudio{})
}

func startWorkerWithAudio(t *testing.T, ctx context.Context, catalogs map[string]CatalogSource, pipeline recordingAudio) <-chan struct{} {
	t.Helper()
	worker := &Worker{
		Store:     NewStore(testPool),
		DB:        testPool,
		Catalogs:  catalogs,
		Audio:     pipeline,
		WorkerID:  newID(t),
		Lease:     time.Minute,
		Heartbeat: time.Hour,
		Poll:      10 * time.Millisecond,
	}
	done := make(chan struct{})
	go func() {
		if err := worker.Run(ctx); err != nil {
			t.Errorf("worker: %v", err)
		}
		close(done)
	}()
	return done
}

func waitStatus(t *testing.T, id, status string) {
	t.Helper()
	waitStatusFor(t, id, status, 10*time.Second)
}

func waitStatusFor(t *testing.T, id, status string, limit time.Duration) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		job, err := NewStore(testPool).Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status == status {
			return
		}
		if job.Status == StatusFailed || job.Status == StatusCompleted || job.Status == StatusPartiallyCompleted {
			t.Fatalf("job reached %s, last_error=%v progress=%s", job.Status, job.LastError, job.Progress)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("job %s did not reach %s", id, status)
}

func waitRequeued(t *testing.T, id string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		job, err := NewStore(testPool).Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status == StatusQueued && job.Attempt > 0 {
			return
		}
		if job.Status == StatusFailed {
			t.Fatalf("job failed: %v", job.LastError)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("job %s was not requeued", id)
}

type scriptedAudio struct {
	errs    map[string]error
	hook    func(context.Context, pgx.Tx) error
	mu      sync.Mutex
	pending audio.TrackTags
	seen    map[string]audio.TrackTags
}

func (s *scriptedAudio) SetAfterPublish(fn func(context.Context, pgx.Tx) error) {
	s.hook = fn
}

func (s *scriptedAudio) SetTrackTags(tags audio.TrackTags) {
	s.mu.Lock()
	s.pending = tags
	s.mu.Unlock()
}

func (s *scriptedAudio) tagsSeen() map[string]audio.TrackTags {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]audio.TrackTags, len(s.seen))
	for id, tags := range s.seen {
		out[id] = tags
	}
	return out
}

func (s *scriptedAudio) Process(ctx context.Context, recordingID, _, externalID, _ string) (audio.Asset, error) {
	s.mu.Lock()
	if s.seen == nil {
		s.seen = map[string]audio.TrackTags{}
	}
	s.seen[externalID] = s.pending
	s.mu.Unlock()
	if err := s.errs[externalID]; err != nil {
		return audio.Asset{}, err
	}
	if s.hook != nil {
		tx, err := testPool.Begin(ctx)
		if err != nil {
			return audio.Asset{}, err
		}
		defer tx.Rollback(ctx)
		if err := s.hook(ctx, tx); err != nil {
			return audio.Asset{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return audio.Asset{}, err
		}
	}
	return audio.Asset{Status: audio.StatusReady, RecordingID: recordingID}, nil
}

func waitDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not stop")
	}
}

func pushOtherJobs(t *testing.T) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `UPDATE import_jobs SET next_attempt_at = now() + interval '1 day' WHERE status = 'QUEUED'`); err != nil {
		t.Fatal(err)
	}
}

func countRows(t *testing.T, query string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(), query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func removeExternalCatalog(t *testing.T, providerName, artistExternal string, releaseExternals, recordingExternals []string) {
	t.Helper()
	ctx := context.Background()
	if _, err := testPool.Exec(ctx, `DELETE FROM import_jobs WHERE provider = $1 AND subject_external_id = $2`, providerName, artistExternal); err != nil {
		t.Fatal(err)
	}
	releaseIDs := externalEntityIDs(t, `SELECT release_id::text FROM release_external_ids WHERE provider = $1 AND external_id = ANY($2)`, providerName, releaseExternals)
	recordingIDs := externalEntityIDs(t, `SELECT recording_id::text FROM recording_external_ids WHERE provider = $1 AND external_id = ANY($2)`, providerName, recordingExternals)
	artistIDs := externalEntityIDs(t, `SELECT artist_id::text FROM artist_external_ids WHERE provider = $1 AND external_id = $2`, providerName, artistExternal)
	statements := []struct {
		query string
		args  []any
	}{
		{`DELETE FROM audio_assets WHERE recording_id::text = ANY($1)`, []any{recordingIDs}},
		{`DELETE FROM release_tracks WHERE release_id::text = ANY($1) OR recording_id::text = ANY($2)`, []any{releaseIDs, recordingIDs}},
		{`DELETE FROM release_artists WHERE release_id::text = ANY($1)`, []any{releaseIDs}},
		{`DELETE FROM recording_artists WHERE recording_id::text = ANY($1)`, []any{recordingIDs}},
		{`DELETE FROM release_external_ids WHERE release_id::text = ANY($1)`, []any{releaseIDs}},
		{`DELETE FROM recording_external_ids WHERE recording_id::text = ANY($1)`, []any{recordingIDs}},
		{`DELETE FROM artist_external_ids WHERE artist_id::text = ANY($1)`, []any{artistIDs}},
		{`DELETE FROM releases WHERE id::text = ANY($1)`, []any{releaseIDs}},
		{`DELETE FROM recordings WHERE id::text = ANY($1)`, []any{recordingIDs}},
		{`DELETE FROM artists WHERE id::text = ANY($1)`, []any{artistIDs}},
	}
	for _, statement := range statements {
		if _, err := testPool.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
}

func externalEntityIDs(t *testing.T, query string, args ...any) []string {
	t.Helper()
	rows, err := testPool.Query(context.Background(), query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

func removeDemoCatalog(ctx context.Context) error {
	if _, err := testPool.Exec(ctx, `DELETE FROM import_jobs WHERE provider = 'fake' AND subject_external_id = 'test-artist'`); err != nil {
		return err
	}
	releaseIDs, err := idList(ctx, `SELECT release_id::text FROM release_external_ids WHERE provider = 'fake' AND external_id IN ('first-album', 'first-album-deluxe')`)
	if err != nil {
		return err
	}
	recordingIDs, err := idList(ctx, `SELECT recording_id::text FROM recording_external_ids WHERE provider = 'fake' AND external_id IN ('track-one', 'track-two', 'track-three')`)
	if err != nil {
		return err
	}
	artistIDs, err := idList(ctx, `SELECT artist_id::text FROM artist_external_ids WHERE provider = 'fake' AND external_id = 'test-artist'`)
	if err != nil {
		return err
	}
	statements := []struct {
		query string
		args  []any
	}{
		{`DELETE FROM audio_assets WHERE recording_id::text = ANY($1)`, []any{recordingIDs}},
		{`DELETE FROM release_tracks WHERE release_id::text = ANY($1) OR recording_id::text = ANY($2)`, []any{releaseIDs, recordingIDs}},
		{`DELETE FROM release_artists WHERE release_id::text = ANY($1)`, []any{releaseIDs}},
		{`DELETE FROM recording_artists WHERE recording_id::text = ANY($1)`, []any{recordingIDs}},
		{`DELETE FROM release_external_ids WHERE release_id::text = ANY($1)`, []any{releaseIDs}},
		{`DELETE FROM recording_external_ids WHERE recording_id::text = ANY($1)`, []any{recordingIDs}},
		{`DELETE FROM artist_external_ids WHERE artist_id::text = ANY($1)`, []any{artistIDs}},
		{`DELETE FROM releases WHERE id::text = ANY($1)`, []any{releaseIDs}},
		{`DELETE FROM recordings WHERE id::text = ANY($1)`, []any{recordingIDs}},
		{`DELETE FROM artists WHERE id::text = ANY($1)`, []any{artistIDs}},
	}
	for _, statement := range statements {
		if _, err := testPool.Exec(ctx, statement.query, statement.args...); err != nil {
			return err
		}
	}
	return nil
}

func idList(ctx context.Context, query string) ([]string, error) {
	rows, err := testPool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
