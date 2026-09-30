package importjob

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/helltale/music/backend/internal/catalog/provider"
	"github.com/helltale/music/backend/internal/platform"
)

func TestImportHTTP(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, NewStore(testPool), map[string]CatalogSource{
		provider.ProviderFake: provider.NewFake(),
	})
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
