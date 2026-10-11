package importjob

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/helltale/music/backend/internal/catalog"
	"github.com/helltale/music/backend/internal/catalog/provider"
	"github.com/helltale/music/backend/internal/platform"
)

func TestImportHTTP(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, NewStore(testPool), map[string]CatalogSource{
		provider.ProviderFake: provider.NewFake(),
	}, catalog.NewStore(testPool))
	handler := platform.Middleware(platform.NewLogger(), mux)
	externalID := newID(t)

	rec := postJSON(t, handler, "/api/v1/admin/catalog/artists/import", `{"provider":"fake","external_id":"`+externalID+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status %d body %s", rec.Code, rec.Body.String())
	}
	var created jobResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Status != StatusQueued || created.Provider != "fake" || created.ExternalID != externalID || created.Attempt != 0 {
		t.Fatalf("created = %+v", created)
	}

	rec = postJSON(t, handler, "/api/v1/admin/catalog/artists/import", `{"provider":"fake","external_id":"`+externalID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("repeat status %d", rec.Code)
	}
	var again jobResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &again); err != nil {
		t.Fatal(err)
	}
	if again.ID != created.ID {
		t.Fatal("repeat import created another active job")
	}

	rec = postJSON(t, handler, "/api/v1/admin/catalog/artists/import", `{"provider":"other","external_id":"x"}`)
	if rec.Code != http.StatusBadRequest || !bytes.Contains(rec.Body.Bytes(), []byte("VALIDATION_ERROR")) {
		t.Fatalf("unknown provider %d %s", rec.Code, rec.Body.String())
	}
	rec = postJSON(t, handler, "/api/v1/admin/catalog/artists/import", `{"provider":"fake"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing id %d", rec.Code)
	}

	rec = request(t, handler, http.MethodGet, "/api/v1/admin/import-jobs/"+created.ID, "")
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(created.ID)) {
		t.Fatalf("get %d %s", rec.Code, rec.Body.String())
	}
	rec = request(t, handler, http.MethodGet, "/api/v1/admin/import-jobs/not-a-uuid", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id %d", rec.Code)
	}
	rec = request(t, handler, http.MethodGet, "/api/v1/admin/import-jobs/"+newID(t), "")
	if rec.Code != http.StatusNotFound || !bytes.Contains(rec.Body.Bytes(), []byte("IMPORT_JOB_NOT_FOUND")) {
		t.Fatalf("missing %d %s", rec.Code, rec.Body.String())
	}
	rec = request(t, handler, http.MethodPost, "/api/v1/admin/import-jobs/"+created.ID+"/retry", "")
	if rec.Code != http.StatusConflict || !bytes.Contains(rec.Body.Bytes(), []byte("JOB_NOT_RETRYABLE")) {
		t.Fatalf("retry queued %d %s", rec.Code, rec.Body.String())
	}
	rec = request(t, handler, http.MethodGet, "/api/v1/admin/import-jobs?status=NOPE", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad status %d", rec.Code)
	}
}

func TestSyncHTTP(t *testing.T) {
	ctx := t.Context()
	mux := http.NewServeMux()
	Mount(mux, NewStore(testPool), map[string]CatalogSource{
		provider.ProviderFake: provider.NewFake(),
	}, catalog.NewStore(testPool))
	handler := platform.Middleware(platform.NewLogger(), mux)
	artists := catalog.NewStore(testPool)

	rec := postJSON(t, handler, "/api/v1/admin/artists/not-a-uuid/sync", "")
	if rec.Code != http.StatusBadRequest || !bytes.Contains(rec.Body.Bytes(), []byte("VALIDATION_ERROR")) {
		t.Fatalf("bad id %d %s", rec.Code, rec.Body.String())
	}
	rec = postJSON(t, handler, "/api/v1/admin/artists/"+newID(t)+"/sync", "")
	if rec.Code != http.StatusNotFound || !bytes.Contains(rec.Body.Bytes(), []byte("ARTIST_NOT_FOUND")) {
		t.Fatalf("missing %d %s", rec.Code, rec.Body.String())
	}

	bareID := newID(t)
	if _, err := artists.CreateArtist(ctx, catalog.Artist{ID: bareID, Name: "Bare " + bareID}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { deleteArtist(t, bareID) })
	rec = postJSON(t, handler, "/api/v1/admin/artists/"+bareID+"/sync", "")
	if rec.Code != http.StatusConflict || !bytes.Contains(rec.Body.Bytes(), []byte("ARTIST_NOT_SYNCABLE")) {
		t.Fatalf("bare %d %s", rec.Code, rec.Body.String())
	}

	manyID := newID(t)
	if _, err := artists.CreateArtist(ctx, catalog.Artist{ID: manyID, Name: "Many " + manyID}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { deleteArtist(t, manyID) })
	if err := artists.BindArtistExternalID(ctx, catalog.ExternalID{EntityID: manyID, Provider: "fake", ExternalID: "many-" + manyID}); err != nil {
		t.Fatal(err)
	}
	if err := artists.BindArtistExternalID(ctx, catalog.ExternalID{EntityID: manyID, Provider: "other", ExternalID: "many-" + manyID}); err != nil {
		t.Fatal(err)
	}
	rec = postJSON(t, handler, "/api/v1/admin/artists/"+manyID+"/sync", "")
	if rec.Code != http.StatusConflict || !bytes.Contains(rec.Body.Bytes(), []byte("ARTIST_NOT_SYNCABLE")) {
		t.Fatalf("many %d %s", rec.Code, rec.Body.String())
	}

	oneID := newID(t)
	externalID := "sync-" + oneID
	if _, err := artists.CreateArtist(ctx, catalog.Artist{ID: oneID, Name: "One " + oneID}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM import_jobs WHERE provider = 'fake' AND subject_external_id = $1`, externalID)
		deleteArtist(t, oneID)
	})
	if err := artists.BindArtistExternalID(ctx, catalog.ExternalID{EntityID: oneID, Provider: "fake", ExternalID: externalID}); err != nil {
		t.Fatal(err)
	}
	rec = postJSON(t, handler, "/api/v1/admin/artists/"+oneID+"/sync", "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("create %d %s", rec.Code, rec.Body.String())
	}
	var created jobResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Provider != "fake" || created.ExternalID != externalID || created.Status != StatusQueued {
		t.Fatalf("created = %+v", created)
	}
	var sync bool
	if err := testPool.QueryRow(ctx, `SELECT payload->>'sync' = 'true' FROM import_jobs WHERE id = $1`, created.ID).Scan(&sync); err != nil {
		t.Fatal(err)
	}
	if !sync {
		t.Fatal("sync job payload is not set")
	}
	rec = postJSON(t, handler, "/api/v1/admin/artists/"+oneID+"/sync", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("repeat %d %s", rec.Code, rec.Body.String())
	}
	var again jobResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &again); err != nil {
		t.Fatal(err)
	}
	if again.ID != created.ID {
		t.Fatal("repeat sync created another active job")
	}
}

func deleteArtist(t *testing.T, id string) {
	t.Helper()
	ctx := context.Background()
	if _, err := testPool.Exec(ctx, `DELETE FROM artist_external_ids WHERE artist_id = $1`, id); err != nil {
		t.Error(err)
	}
	if _, err := testPool.Exec(ctx, `DELETE FROM artists WHERE id = $1`, id); err != nil {
		t.Error(err)
	}
}

func postJSON(t *testing.T, handler http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return request(t, handler, http.MethodPost, path, body)
}

func request(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
