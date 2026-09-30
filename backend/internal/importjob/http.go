package importjob

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/helltale/music/backend/internal/platform"
)

const (
	defaultPageLimit = 20
	maxPageLimit     = 100
)

var uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Mount registers the import job HTTP routes.
func Mount(mux *http.ServeMux, store *Store, catalogs map[string]CatalogSource) {
	h := handler{store: store, catalogs: catalogs}
	mux.HandleFunc("POST /api/v1/admin/catalog/artists/import", h.importArtist)
	mux.HandleFunc("GET /api/v1/admin/import-jobs", h.list)
	mux.HandleFunc("GET /api/v1/admin/import-jobs/{id}", h.get)
	mux.HandleFunc("POST /api/v1/admin/import-jobs/{id}/retry", h.retry)
}

type handler struct {
	store    *Store
	catalogs map[string]CatalogSource
}

func (h handler) importArtist(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider   string `json:"provider"`
		ExternalID string `json:"external_id"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		platform.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid JSON")
		return
	}
	body.Provider = strings.TrimSpace(body.Provider)
	body.ExternalID = strings.TrimSpace(body.ExternalID)
	if body.Provider == "" || body.ExternalID == "" {
		platform.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Provider and external id are required")
		return
	}
	if _, ok := h.catalogs[body.Provider]; !ok {
		platform.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Unknown provider")
		return
	}
	result, err := h.store.Enqueue(r.Context(), body.Provider, body.ExternalID)
	if err != nil {
		slog.Error("enqueue import", "request_id", w.Header().Get("X-Request-Id"), "error", err.Error())
		platform.WriteError(w, http.StatusInternalServerError, "INTERNAL", "Internal error")
		return
	}
	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, jobDocument(result.Job))
}

func (h handler) get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !uuidPattern.MatchString(id) {
		platform.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid job id")
		return
	}
	job, err := h.store.Get(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		platform.WriteError(w, http.StatusNotFound, "IMPORT_JOB_NOT_FOUND", "Import job not found")
		return
	}
	if err != nil {
		slog.Error("get import job", "request_id", w.Header().Get("X-Request-Id"), "error", err.Error())
		platform.WriteError(w, http.StatusInternalServerError, "INTERNAL", "Internal error")
		return
	}
	writeJSON(w, http.StatusOK, jobDocument(job))
}

func (h handler) list(w http.ResponseWriter, r *http.Request) {
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status != "" && !knownStatus(status) {
		platform.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid status")
		return
	}
	limit, offset, ok := pageQuery(w, r)
	if !ok {
		return
	}
	items, total, err := h.store.List(r.Context(), status, limit, offset)
	if err != nil {
		slog.Error("list import jobs", "request_id", w.Header().Get("X-Request-Id"), "error", err.Error())
		platform.WriteError(w, http.StatusInternalServerError, "INTERNAL", "Internal error")
		return
	}
	docs := make([]jobResponse, 0, len(items))
	for _, job := range items {
		docs = append(docs, jobDocument(job))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":  docs,
		"limit":  limit,
		"offset": offset,
		"total":  total,
	})
}

func (h handler) retry(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !uuidPattern.MatchString(id) {
		platform.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid job id")
		return
	}
	if _, err := io.Copy(io.Discard, r.Body); err != nil {
		platform.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid JSON")
		return
	}
	job, err := h.store.Retry(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		platform.WriteError(w, http.StatusNotFound, "IMPORT_JOB_NOT_FOUND", "Import job not found")
		return
	}
	if errors.Is(err, ErrNotRetryable) {
		platform.WriteError(w, http.StatusConflict, "JOB_NOT_RETRYABLE", "Job cannot be retried")
		return
	}
	if err != nil {
		slog.Error("retry import job", "request_id", w.Header().Get("X-Request-Id"), "error", err.Error())
		platform.WriteError(w, http.StatusInternalServerError, "INTERNAL", "Internal error")
		return
	}
	writeJSON(w, http.StatusOK, jobDocument(job))
}

type jobResponse struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	Status      string          `json:"status"`
	Provider    string          `json:"provider"`
	ExternalID  string          `json:"external_id"`
	Attempt     int             `json:"attempt"`
	MaxAttempts int             `json:"max_attempts"`
	Progress    json.RawMessage `json:"progress"`
	LastError   *string         `json:"last_error"`
	CreatedAt   time.Time       `json:"created_at"`
	StartedAt   *time.Time      `json:"started_at"`
	FinishedAt  *time.Time      `json:"finished_at"`
}

func jobDocument(job Job) jobResponse {
	progress := job.Progress
	if len(progress) == 0 {
		progress = json.RawMessage(`{}`)
	}
	return jobResponse{
		ID:          job.ID,
		Type:        job.Type,
		Status:      job.Status,
		Provider:    job.Provider,
		ExternalID:  job.ExternalID,
		Attempt:     job.Attempt,
		MaxAttempts: job.MaxAttempts,
		Progress:    progress,
		LastError:   job.LastError,
		CreatedAt:   job.CreatedAt,
		StartedAt:   job.StartedAt,
		FinishedAt:  job.FinishedAt,
	}
}

func knownStatus(status string) bool {
	switch status {
	case StatusQueued, StatusRunning, StatusCompleted, StatusPartiallyCompleted, StatusFailed:
		return true
	default:
		return false
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
