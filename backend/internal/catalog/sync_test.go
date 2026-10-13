package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/helltale/music/backend/internal/catalog/provider"
	"github.com/helltale/music/backend/internal/platform"
)

func TestArtistSyncHTTP(t *testing.T) {
	ctx := context.Background()
	store := NewStore(testPool)
	mux := http.NewServeMux()
	Mount(mux, store, provider.NewFake(), nil)
	handler := platform.Middleware(platform.NewLogger(), mux)

	rec := patchJSON(t, handler, "/api/v1/admin/artists/not-a-uuid", `{"sync_enabled":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id %d %s", rec.Code, rec.Body.String())
	}
	rec = patchJSON(t, handler, "/api/v1/admin/artists/"+newID(t), `{"sync_enabled":true}`)
	if rec.Code != http.StatusNotFound || !bytes.Contains(rec.Body.Bytes(), []byte("ARTIST_NOT_FOUND")) {
		t.Fatalf("missing %d %s", rec.Code, rec.Body.String())
	}

	artistID := newID(t)
	if _, err := store.CreateArtist(ctx, Artist{ID: artistID, Name: "Sync " + artistID}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM artists WHERE id = $1`, artistID)
	})

	rec = patchJSON(t, handler, "/api/v1/admin/artists/"+artistID, `{}`)
	if rec.Code != http.StatusBadRequest || !bytes.Contains(rec.Body.Bytes(), []byte("VALIDATION_ERROR")) {
		t.Fatalf("missing field %d %s", rec.Code, rec.Body.String())
	}
	rec = patchJSON(t, handler, "/api/v1/admin/artists/"+artistID, `{"sync_enabled":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("enable %d %s", rec.Code, rec.Body.String())
	}
	var enabled artistSyncDocument
	if err := json.Unmarshal(rec.Body.Bytes(), &enabled); err != nil {
		t.Fatal(err)
	}
	if !enabled.SyncEnabled || enabled.NextSyncAt == nil || enabled.NextSyncAt.After(time.Now().Add(time.Minute)) {
		t.Fatalf("enabled = %+v", enabled)
	}

	rec = requestSync(t, handler, http.MethodGet, "/api/v1/admin/artists/"+artistID, "")
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(`"sync_enabled":true`)) {
		t.Fatalf("get %d %s", rec.Code, rec.Body.String())
	}

	rec = patchJSON(t, handler, "/api/v1/admin/artists/"+artistID, `{"sync_enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("disable %d %s", rec.Code, rec.Body.String())
	}
	var disabled artistSyncDocument
	if err := json.Unmarshal(rec.Body.Bytes(), &disabled); err != nil {
		t.Fatal(err)
	}
	if disabled.SyncEnabled || disabled.NextSyncAt != nil {
		t.Fatalf("disabled = %+v", disabled)
	}
}

func patchJSON(t *testing.T, handler http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return requestSync(t, handler, http.MethodPatch, path, body)
}

func requestSync(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
