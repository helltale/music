package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/helltale/music/backend/internal/catalog/provider"
	"github.com/helltale/music/backend/internal/platform"
)

const (
	defaultPageLimit = 20
	maxPageLimit     = 100
)

// Playable reports which recordings can be played. The catalog package does not import audio.
type Playable interface {
	PlayableRecordings(ctx context.Context, recordingIDs []string) (map[string]bool, error)
}

// Mount registers the public catalog routes and the admin artist search.
func Mount(mux *http.ServeMux, store *Store, searcher provider.ArtistSearcher, playable Playable) {
	public := publicHandler{store: store, playable: playable}
	mux.HandleFunc("GET /api/v1/artists", public.listArtists)
	mux.HandleFunc("GET /api/v1/artists/{id}", public.getArtist)
	mux.HandleFunc("GET /api/v1/artists/{id}/releases", public.listArtistReleases)
	mux.HandleFunc("GET /api/v1/releases/{id}", public.getRelease)
	mux.HandleFunc("GET /api/v1/recordings/{id}", public.getRecording)
	mux.HandleFunc("GET /api/v1/admin/catalog/artists/search", adminArtistSearch(searcher))
	mux.HandleFunc("GET /api/v1/admin/artists/{id}", public.getArtistSync)
	mux.HandleFunc("PATCH /api/v1/admin/artists/{id}", public.setArtistSync)
}

func adminArtistSearch(searcher provider.ArtistSearcher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := strings.TrimSpace(r.URL.Query().Get("q"))
		if query == "" {
			platform.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Query is required")
			return
		}
		limit, offset, ok := pageQuery(w, r)
		if !ok {
			return
		}
		items, total, err := searcher.SearchArtists(r.Context(), query, limit)
		if err != nil {
			slog.Error("artist search", "request_id", w.Header().Get("X-Request-Id"), "error", err.Error())
			platform.WriteError(w, http.StatusInternalServerError, "INTERNAL", "Internal error")
			return
		}
		body := artistSearchResponse{
			Items:  make([]artistSearchItem, 0, len(items)),
			Limit:  limit,
			Offset: offset,
			Total:  total,
		}
		for _, item := range items {
			body.Items = append(body.Items, artistSearchItem{
				Provider:   item.Provider,
				ExternalID: item.ExternalID,
				Name:       item.Name,
			})
		}
		writeJSON(w, http.StatusOK, body)
	}
}

type artistSearchItem struct {
	Provider   string `json:"provider"`
	ExternalID string `json:"external_id"`
	Name       string `json:"name"`
}

type artistSearchResponse struct {
	Items  []artistSearchItem `json:"items"`
	Limit  int                `json:"limit"`
	Offset int                `json:"offset"`
	Total  int                `json:"total"`
}

func (h publicHandler) getArtistSync(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	row, err := h.store.GetArtistSync(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		platformError(w, http.StatusNotFound, "ARTIST_NOT_FOUND", "Artist not found")
		return
	}
	if err != nil {
		writeInternal(w, r, "get artist sync", err)
		return
	}
	writeJSON(w, http.StatusOK, newArtistSyncDocument(row))
}

func (h publicHandler) setArtistSync(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		SyncEnabled *bool `json:"sync_enabled"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		platformError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid JSON")
		return
	}
	if _, err := io.Copy(io.Discard, r.Body); err != nil {
		platformError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid JSON")
		return
	}
	if body.SyncEnabled == nil {
		platformError(w, http.StatusBadRequest, "VALIDATION_ERROR", "sync_enabled is required")
		return
	}
	row, err := h.store.SetSyncEnabled(r.Context(), id, *body.SyncEnabled)
	if errors.Is(err, ErrNotFound) {
		platformError(w, http.StatusNotFound, "ARTIST_NOT_FOUND", "Artist not found")
		return
	}
	if err != nil {
		writeInternal(w, r, "set artist sync", err)
		return
	}
	writeJSON(w, http.StatusOK, newArtistSyncDocument(row))
}

type artistSyncDocument struct {
	ID           string     `json:"id"`
	SyncEnabled  bool       `json:"sync_enabled"`
	LastSyncedAt *time.Time `json:"last_synced_at"`
	NextSyncAt   *time.Time `json:"next_sync_at"`
}

func newArtistSyncDocument(row ArtistSync) artistSyncDocument {
	return artistSyncDocument{
		ID:           row.ID,
		SyncEnabled:  row.SyncEnabled,
		LastSyncedAt: row.LastSyncedAt,
		NextSyncAt:   row.NextSyncAt,
	}
}

func pageQuery(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	limit := defaultPageLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maxPageLimit {
			platform.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid limit")
			return 0, 0, false
		}
		limit = parsed
	}
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			platform.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid offset")
			return 0, 0, false
		}
		offset = parsed
	}
	return limit, offset, true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
