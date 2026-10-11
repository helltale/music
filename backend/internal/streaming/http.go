package streaming

import (
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/helltale/music/backend/internal/catalog"
	"github.com/helltale/music/backend/internal/platform"
)

var uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Mount registers GET /api/v1/recordings/{id}/play.
func Mount(mux *http.ServeMux, service *Service) {
	h := handler{service: service}
	mux.HandleFunc("GET /api/v1/recordings/{id}/play", h.play)
}

type handler struct {
	service *Service
}

func (h handler) play(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !uuidPattern.MatchString(id) {
		platform.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid recording id")
		return
	}
	playback, err := h.service.Play(r.Context(), id)
	if errors.Is(err, catalog.ErrNotFound) {
		platform.WriteError(w, http.StatusNotFound, "RECORDING_NOT_FOUND", "Recording not found")
		return
	}
	if errors.Is(err, ErrNotReady) {
		platform.WriteError(w, http.StatusConflict, "AUDIO_NOT_READY", "Audio is not ready")
		return
	}
	if err != nil {
		slog.Error("playback", "request_id", w.Header().Get("X-Request-Id"), "recording_id", id, "error", err.Error())
		platform.WriteError(w, http.StatusInternalServerError, "INTERNAL", "Internal error")
		return
	}
	slog.Info("playback",
		"request_id", w.Header().Get("X-Request-Id"),
		"recording_id", playback.RecordingID,
		"audio_asset_id", playback.AudioAssetID,
	)
	writeJSON(w, http.StatusOK, playResponse{
		RecordingID:  playback.RecordingID,
		AudioAssetID: playback.AudioAssetID,
		URL:          playback.URL,
		ExpiresAt:    playback.ExpiresAt,
		ContentType:  playback.ContentType,
		DurationMS:   playback.DurationMS,
	})
}

type playResponse struct {
	RecordingID  string    `json:"recording_id"`
	AudioAssetID string    `json:"audio_asset_id"`
	URL          string    `json:"url"`
	ExpiresAt    time.Time `json:"expires_at"`
	ContentType  string    `json:"content_type"`
	DurationMS   *int      `json:"duration_ms"`
}
