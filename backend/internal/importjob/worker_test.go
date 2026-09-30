package importjob

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

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
	if _, ok := progress["audio_ready"]; ok {
		t.Fatalf("audio progress is present: %s", finished.Progress)
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
	worker := &Worker{
		Store:     NewStore(testPool),
		DB:        testPool,
		Catalogs:  catalogs,
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
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		job, err := NewStore(testPool).Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status == status {
			return
		}
		if job.Status == StatusFailed && status != StatusFailed {
			t.Fatalf("job failed: %v", job.LastError)
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
