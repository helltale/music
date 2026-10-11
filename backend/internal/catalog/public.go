package catalog

import (
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"time"
)

var uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type publicHandler struct {
	store    *Store
	playable Playable
}

func (h publicHandler) listArtists(w http.ResponseWriter, r *http.Request) {
	limit, offset, ok := pageQuery(w, r)
	if !ok {
		return
	}
	items, total, err := h.store.ListArtists(r.Context(), r.URL.Query().Get("q"), limit, offset)
	if err != nil {
		writeInternal(w, r, "list artists", err)
		return
	}
	body := artistListResponse{Items: make([]artistItem, 0, len(items)), Limit: limit, Offset: offset, Total: total}
	for _, artist := range items {
		body.Items = append(body.Items, artistItem{ID: artist.ID, Name: artist.Name})
	}
	writeJSON(w, http.StatusOK, body)
}

func (h publicHandler) getArtist(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	artist, err := h.store.GetArtist(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		platformError(w, http.StatusNotFound, "ARTIST_NOT_FOUND", "Artist not found")
		return
	}
	if err != nil {
		writeInternal(w, r, "get artist", err)
		return
	}
	writeJSON(w, http.StatusOK, newArtistDocument(artist))
}

func (h publicHandler) listArtistReleases(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, err := h.store.GetArtist(r.Context(), id); errors.Is(err, ErrNotFound) {
		platformError(w, http.StatusNotFound, "ARTIST_NOT_FOUND", "Artist not found")
		return
	} else if err != nil {
		writeInternal(w, r, "get artist", err)
		return
	}
	limit, offset, ok := pageQuery(w, r)
	if !ok {
		return
	}
	items, total, err := h.store.ListArtistReleases(r.Context(), id, limit, offset)
	if err != nil {
		writeInternal(w, r, "list artist releases", err)
		return
	}
	body := releaseListResponse{Items: make([]releaseItem, 0, len(items)), Limit: limit, Offset: offset, Total: total}
	for _, release := range items {
		body.Items = append(body.Items, newReleaseItem(release))
	}
	writeJSON(w, http.StatusOK, body)
}

func (h publicHandler) getRelease(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	release, err := h.store.GetRelease(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		platformError(w, http.StatusNotFound, "RELEASE_NOT_FOUND", "Release not found")
		return
	}
	if err != nil {
		writeInternal(w, r, "get release", err)
		return
	}
	artists, err := h.store.ListReleaseArtists(r.Context(), id)
	if err != nil {
		writeInternal(w, r, "list release artists", err)
		return
	}
	tracks, err := h.store.ListReleaseTracks(r.Context(), id)
	if err != nil {
		writeInternal(w, r, "list release tracks", err)
		return
	}
	ids := make([]string, len(tracks))
	for i, track := range tracks {
		ids[i] = track.RecordingID
	}
	playable, err := h.playableIDs(r, ids)
	if err != nil {
		writeInternal(w, r, "playable recordings", err)
		return
	}
	body := newReleaseDocument(release)
	body.Artists = roleItems(artists)
	body.Tracks = make([]trackItem, 0, len(tracks))
	for _, track := range tracks {
		body.Tracks = append(body.Tracks, trackItem{
			DiscNumber:        track.DiscNumber,
			TrackNumber:       track.TrackNumber,
			Title:             track.Title,
			RecordingID:       track.RecordingID,
			DurationMS:        track.DurationMS,
			PlaybackAvailable: playable[track.RecordingID],
		})
	}
	writeJSON(w, http.StatusOK, body)
}

func (h publicHandler) getRecording(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	recording, err := h.store.GetRecording(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		platformError(w, http.StatusNotFound, "RECORDING_NOT_FOUND", "Recording not found")
		return
	}
	if err != nil {
		writeInternal(w, r, "get recording", err)
		return
	}
	artists, err := h.store.ListRecordingArtists(r.Context(), id)
	if err != nil {
		writeInternal(w, r, "list recording artists", err)
		return
	}
	playable, err := h.playableIDs(r, []string{id})
	if err != nil {
		writeInternal(w, r, "playable recordings", err)
		return
	}
	writeJSON(w, http.StatusOK, recordingDocument{
		ID:                recording.ID,
		Title:             recording.Title,
		ISRC:              recording.ISRC,
		DurationMS:        recording.DurationMS,
		Artists:           roleItems(artists),
		PlaybackAvailable: playable[id],
	})
}

func (h publicHandler) playableIDs(r *http.Request, ids []string) (map[string]bool, error) {
	if h.playable == nil || len(ids) == 0 {
		return map[string]bool{}, nil
	}
	return h.playable.PlayableRecordings(r.Context(), ids)
}

func pathID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !uuidPattern.MatchString(id) {
		platformError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid id")
		return "", false
	}
	return id, true
}

func writeInternal(w http.ResponseWriter, r *http.Request, action string, err error) {
	slog.Error(action, "request_id", w.Header().Get("X-Request-Id"), "error", err.Error())
	platformError(w, http.StatusInternalServerError, "INTERNAL", "Internal error")
}

func platformError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

type artistItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type artistListResponse struct {
	Items  []artistItem `json:"items"`
	Limit  int          `json:"limit"`
	Offset int          `json:"offset"`
	Total  int          `json:"total"`
}

type artistDocument struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Description    *string `json:"description"`
	ImageObjectKey *string `json:"image_object_key"`
}

func newArtistDocument(artist Artist) artistDocument {
	return artistDocument{
		ID:             artist.ID,
		Name:           artist.Name,
		Description:    artist.Description,
		ImageObjectKey: artist.ImageObjectKey,
	}
}

type releaseItem struct {
	ID             string  `json:"id"`
	Title          string  `json:"title"`
	ReleaseType    string  `json:"release_type"`
	ReleaseDate    *string `json:"release_date"`
	CoverObjectKey *string `json:"cover_object_key"`
}

type releaseListResponse struct {
	Items  []releaseItem `json:"items"`
	Limit  int           `json:"limit"`
	Offset int           `json:"offset"`
	Total  int           `json:"total"`
}

func newReleaseItem(release Release) releaseItem {
	return releaseItem{
		ID:             release.ID,
		Title:          release.Title,
		ReleaseType:    release.ReleaseType,
		ReleaseDate:    dateString(release.ReleaseDate),
		CoverObjectKey: release.CoverObjectKey,
	}
}

type roleItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

type trackItem struct {
	DiscNumber        int    `json:"disc_number"`
	TrackNumber       int    `json:"track_number"`
	Title             string `json:"title"`
	RecordingID       string `json:"recording_id"`
	DurationMS        *int   `json:"duration_ms"`
	PlaybackAvailable bool   `json:"playback_available"`
}

type releaseDocument struct {
	ID             string      `json:"id"`
	Title          string      `json:"title"`
	ReleaseType    string      `json:"release_type"`
	ReleaseDate    *string     `json:"release_date"`
	CoverObjectKey *string     `json:"cover_object_key"`
	Artists        []roleItem  `json:"artists"`
	Tracks         []trackItem `json:"tracks"`
}

func newReleaseDocument(release Release) releaseDocument {
	return releaseDocument{
		ID:             release.ID,
		Title:          release.Title,
		ReleaseType:    release.ReleaseType,
		ReleaseDate:    dateString(release.ReleaseDate),
		CoverObjectKey: release.CoverObjectKey,
		Artists:        []roleItem{},
		Tracks:         []trackItem{},
	}
}

type recordingDocument struct {
	ID                string     `json:"id"`
	Title             string     `json:"title"`
	ISRC              *string    `json:"isrc"`
	DurationMS        *int       `json:"duration_ms"`
	Artists           []roleItem `json:"artists"`
	PlaybackAvailable bool       `json:"playback_available"`
}

func roleItems(roles []ArtistRole) []roleItem {
	items := make([]roleItem, 0, len(roles))
	for _, role := range roles {
		items = append(items, roleItem{ID: role.ArtistID, Name: role.Name, Role: role.Role})
	}
	return items
}

func dateString(value *time.Time) *string {
	if value == nil || value.IsZero() {
		return nil
	}
	formatted := value.UTC().Format("2006-01-02")
	return &formatted
}
