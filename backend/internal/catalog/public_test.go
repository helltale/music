package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPublicCatalog(t *testing.T) {
	store, rollback := newStore(t)
	defer rollback()
	ctx := context.Background()
	artist := mustArtist(t, store, "Public Artist")
	description := "bio"
	artist.Description = &description
	if err := store.UpdateArtist(ctx, artist); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	release, err := store.CreateRelease(ctx, Release{ID: newID(t), Title: "Public Album", ReleaseType: "album", ReleaseDate: &day})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddReleaseArtist(ctx, release.ID, artist.ID, RolePrimary); err != nil {
		t.Fatal(err)
	}
	duration := 180000
	isrc := "USTST2600001"
	ready := mustRecording(t, store, "Ready Track")
	if err := store.UpdateRecording(ctx, Recording{ID: ready.ID, Title: ready.Title, ISRC: &isrc, DurationMS: &duration}); err != nil {
		t.Fatal(err)
	}
	silent := mustRecording(t, store, "Silent Track")
	if err := store.AddRecordingArtist(ctx, ready.ID, artist.ID, RolePrimary); err != nil {
		t.Fatal(err)
	}
	if err := store.AddReleaseTrack(ctx, ReleaseTrack{ReleaseID: release.ID, RecordingID: ready.ID, DiscNumber: 1, TrackNumber: 1}); err != nil {
		t.Fatal(err)
	}
	if err := store.AddReleaseTrack(ctx, ReleaseTrack{ReleaseID: release.ID, RecordingID: silent.ID, DiscNumber: 1, TrackNumber: 2}); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	Mount(mux, store, nil, playableSet{ready.ID: true})

	rec := get(mux, "/api/v1/artists?q=public")
	if rec.Code != http.StatusOK || !jsonContains(t, rec.Body.Bytes(), artist.Name) {
		t.Fatalf("artists %d %s", rec.Code, rec.Body.String())
	}
	rec = get(mux, "/api/v1/artists/"+artist.ID)
	var artistBody artistDocument
	if err := json.Unmarshal(rec.Body.Bytes(), &artistBody); err != nil {
		t.Fatal(err)
	}
	if artistBody.Name != "Public Artist" || artistBody.Description == nil || *artistBody.Description != "bio" {
		t.Fatalf("artist %+v", artistBody)
	}
	rec = get(mux, "/api/v1/artists/"+artist.ID+"/releases")
	var releases releaseListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &releases); err != nil {
		t.Fatal(err)
	}
	if releases.Total != 1 || releases.Items[0].ReleaseDate == nil || *releases.Items[0].ReleaseDate != "2024-01-15" {
		t.Fatalf("releases %+v", releases)
	}
	rec = get(mux, "/api/v1/releases/"+release.ID)
	var releaseBody releaseDocument
	if err := json.Unmarshal(rec.Body.Bytes(), &releaseBody); err != nil {
		t.Fatal(err)
	}
	if len(releaseBody.Tracks) != 2 || !releaseBody.Tracks[0].PlaybackAvailable || releaseBody.Tracks[1].PlaybackAvailable {
		t.Fatalf("tracks %+v", releaseBody.Tracks)
	}
	if releaseBody.Tracks[0].DurationMS == nil || *releaseBody.Tracks[0].DurationMS != 180000 {
		t.Fatalf("duration %+v", releaseBody.Tracks[0])
	}
	rec = get(mux, "/api/v1/recordings/"+ready.ID)
	var recordingBody recordingDocument
	if err := json.Unmarshal(rec.Body.Bytes(), &recordingBody); err != nil {
		t.Fatal(err)
	}
	if !recordingBody.PlaybackAvailable || recordingBody.ISRC == nil || *recordingBody.ISRC != isrc || len(recordingBody.Artists) != 1 {
		t.Fatalf("recording %+v", recordingBody)
	}

	rec = get(mux, "/api/v1/artists/not-a-uuid")
	if rec.Code != http.StatusBadRequest || !jsonContains(t, rec.Body.Bytes(), "VALIDATION_ERROR") {
		t.Fatalf("bad id %d %s", rec.Code, rec.Body.String())
	}
	rec = get(mux, "/api/v1/artists/"+newID(t))
	if rec.Code != http.StatusNotFound || !jsonContains(t, rec.Body.Bytes(), "ARTIST_NOT_FOUND") {
		t.Fatalf("missing artist %d %s", rec.Code, rec.Body.String())
	}
	rec = get(mux, "/api/v1/releases/"+newID(t))
	if rec.Code != http.StatusNotFound || !jsonContains(t, rec.Body.Bytes(), "RELEASE_NOT_FOUND") {
		t.Fatalf("missing release %d %s", rec.Code, rec.Body.String())
	}
	rec = get(mux, "/api/v1/recordings/"+newID(t))
	if rec.Code != http.StatusNotFound || !jsonContains(t, rec.Body.Bytes(), "RECORDING_NOT_FOUND") {
		t.Fatalf("missing recording %d %s", rec.Code, rec.Body.String())
	}
}

type playableSet map[string]bool

func (p playableSet) PlayableRecordings(_ context.Context, ids []string) (map[string]bool, error) {
	ready := map[string]bool{}
	for _, id := range ids {
		if p[id] {
			ready[id] = true
		}
	}
	return ready, nil
}

func get(mux *http.ServeMux, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func jsonContains(t *testing.T, body []byte, part string) bool {
	t.Helper()
	return len(body) > 0 && string(body) != "" && (len(part) == 0 || contains(string(body), part))
}

func contains(s, part string) bool {
	return len(s) >= len(part) && (s == part || len(part) == 0 || indexOf(s, part) >= 0)
}

func indexOf(s, part string) int {
	for i := 0; i+len(part) <= len(s); i++ {
		if s[i:i+len(part)] == part {
			return i
		}
	}
	return -1
}
