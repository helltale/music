package importjob

import (
	"context"
	"testing"
	"time"

	"github.com/helltale/music/backend/internal/catalog"
	"github.com/helltale/music/backend/internal/catalog/provider"
)

func TestScheduleEnqueuesOneSyncJob(t *testing.T) {
	ctx := context.Background()
	if _, err := testPool.Exec(ctx, `UPDATE artists SET sync_enabled = false, next_sync_at = NULL WHERE sync_enabled`); err != nil {
		t.Fatal(err)
	}
	artists := catalog.NewStore(testPool)
	artistID := newID(t)
	externalID := "sched-" + artistID
	if _, err := artists.CreateArtist(ctx, catalog.Artist{ID: artistID, Name: "Sched " + artistID}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM import_jobs WHERE provider = 'fake' AND subject_external_id = $1`, externalID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM artist_external_ids WHERE artist_id = $1`, artistID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM artists WHERE id = $1`, artistID)
	})
	if err := artists.BindArtistExternalID(ctx, catalog.ExternalID{EntityID: artistID, Provider: provider.ProviderFake, ExternalID: externalID}); err != nil {
		t.Fatal(err)
	}
	if _, err := artists.SetSyncEnabled(ctx, artistID, true); err != nil {
		t.Fatal(err)
	}

	worker := &Worker{
		Store:        NewStore(testPool),
		DB:           testPool,
		Catalogs:     map[string]CatalogSource{provider.ProviderFake: provider.NewFake()},
		SyncInterval: time.Hour,
		WorkerID:     newID(t),
	}
	if err := worker.schedule(ctx); err != nil {
		t.Fatal(err)
	}
	if countRows(t, `SELECT count(*) FROM import_jobs WHERE provider = 'fake' AND subject_external_id = '`+externalID+`' AND status = 'QUEUED' AND payload->>'sync' = 'true'`) != 1 {
		t.Fatal("sync job was not queued")
	}
	var next time.Time
	if err := testPool.QueryRow(ctx, `SELECT next_sync_at FROM artists WHERE id = $1`, artistID).Scan(&next); err != nil {
		t.Fatal(err)
	}
	if !next.After(time.Now().Add(30 * time.Minute)) {
		t.Fatalf("next_sync_at = %s", next)
	}
	if err := worker.schedule(ctx); err != nil {
		t.Fatal(err)
	}
	if countRows(t, `SELECT count(*) FROM import_jobs WHERE provider = 'fake' AND subject_external_id = '`+externalID+`'`) != 1 {
		t.Fatal("second schedule created another job")
	}
}

func TestScheduleSkipsUnknownProvider(t *testing.T) {
	ctx := context.Background()
	if _, err := testPool.Exec(ctx, `UPDATE artists SET sync_enabled = false, next_sync_at = NULL WHERE sync_enabled`); err != nil {
		t.Fatal(err)
	}
	artists := catalog.NewStore(testPool)
	artistID := newID(t)
	if _, err := artists.CreateArtist(ctx, catalog.Artist{ID: artistID, Name: "Other " + artistID}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM artist_external_ids WHERE artist_id = $1`, artistID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM artists WHERE id = $1`, artistID)
	})
	if err := artists.BindArtistExternalID(ctx, catalog.ExternalID{EntityID: artistID, Provider: "other", ExternalID: "other-" + artistID}); err != nil {
		t.Fatal(err)
	}
	if _, err := artists.SetSyncEnabled(ctx, artistID, true); err != nil {
		t.Fatal(err)
	}
	worker := &Worker{
		Store:        NewStore(testPool),
		DB:           testPool,
		Catalogs:     map[string]CatalogSource{provider.ProviderFake: provider.NewFake()},
		SyncInterval: time.Hour,
		WorkerID:     newID(t),
	}
	if err := worker.schedule(ctx); err != nil {
		t.Fatal(err)
	}
	if countRows(t, `SELECT count(*) FROM import_jobs WHERE provider = 'other' AND subject_external_id = 'other-`+artistID+`'`) != 0 {
		t.Fatal("unknown provider was enqueued")
	}
}
