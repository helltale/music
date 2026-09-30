package catalog

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/helltale/music/backend/internal/catalog/provider"
	"github.com/helltale/music/backend/internal/platform"
)

const (
	defaultPageLimit = 20
	maxPageLimit     = 100
)

// Mount registers the catalog HTTP routes implemented so far.
func Mount(mux *http.ServeMux, searcher provider.ArtistSearcher) {
	mux.HandleFunc("GET /api/v1/admin/catalog/artists/search", adminArtistSearch(searcher))
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
