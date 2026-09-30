package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/helltale/music/backend/internal/catalog/provider"
	"github.com/helltale/music/backend/internal/platform"
)

func TestAdminArtistSearch(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, provider.NewFake())
	mux.HandleFunc("/", platform.NotFound)
	handler := platform.Middleware(platform.NewLogger(), mux)

	rec := searchRequest(t, handler, "/api/v1/admin/catalog/artists/search?q=test")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("content type %s", rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("X-Request-Id") == "" {
		t.Fatal("missing request id")
	}
	var body artistSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Total != 1 || body.Limit != 20 || body.Offset != 0 || len(body.Items) != 1 {
		t.Fatalf("body = %+v", body)
	}
	item := body.Items[0]
	if item.Provider != "fake" || item.ExternalID != "test-artist" || item.Name != "Test Artist" {
		t.Fatalf("item = %+v", item)
	}

	rec = searchRequest(t, handler, "/api/v1/admin/catalog/artists/search?q=TEST&offset=40")
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Offset != 40 || body.Total != 1 || len(body.Items) != 1 {
		t.Fatalf("offset was applied to the provider result: %+v", body)
	}

	rec = searchRequest(t, handler, "/api/v1/admin/catalog/artists/search?q=missing")
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || body.Total != 0 || len(body.Items) != 0 {
		t.Fatalf("empty = %d %+v", rec.Code, body)
	}
}

func TestAdminArtistSearchValidation(t *testing.T) {
	searcher := &recordingSearcher{}
	handler := adminArtistSearch(searcher)

	for _, path := range []string{
		"/api/v1/admin/catalog/artists/search",
		"/api/v1/admin/catalog/artists/search?q=%20%20",
		"/api/v1/admin/catalog/artists/search?q=test&limit=0",
		"/api/v1/admin/catalog/artists/search?q=test&limit=101",
		"/api/v1/admin/catalog/artists/search?q=test&limit=no",
		"/api/v1/admin/catalog/artists/search?q=test&offset=-1",
	} {
		searcher.called = false
		rec := searchRequest(t, handler, path)
		if rec.Code != http.StatusBadRequest || searcher.called {
			t.Fatalf("%s status=%d called=%v body=%s", path, rec.Code, searcher.called, rec.Body.String())
		}
		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Error.Code != "VALIDATION_ERROR" {
			t.Fatalf("%s code %s", path, body.Error.Code)
		}
	}
}

func TestAdminArtistSearchError(t *testing.T) {
	handler := adminArtistSearch(&recordingSearcher{err: errors.New("provider down")})
	rec := searchRequest(t, handler, "/api/v1/admin/catalog/artists/search?q=test")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "provider down") {
		t.Fatalf("body leaked the provider error: %s", rec.Body.String())
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
	if body.Error.Code != "INTERNAL" || body.Error.Message != "Internal error" {
		t.Fatalf("error = %+v", body.Error)
	}
}

type recordingSearcher struct {
	called bool
	err    error
}

func (s *recordingSearcher) SearchArtists(context.Context, string, int) ([]provider.ArtistCandidate, int, error) {
	s.called = true
	if s.err != nil {
		return nil, 0, s.err
	}
	return nil, 0, nil
}

func searchRequest(t *testing.T, handler http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
