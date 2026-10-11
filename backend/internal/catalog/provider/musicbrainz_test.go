package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	adaID          = "66666666-6666-6666-6666-666666666666"
	otherID        = "77777777-7777-7777-7777-777777777777"
	studioGroupID  = "33333333-3333-3333-3333-333333333333"
	earlyEditionID = "11111111-1111-1111-1111-111111111111"
	lateEditionID  = "22222222-2222-2222-2222-222222222222"
	compGroupID    = "44444444-4444-4444-4444-444444444444"
	compEditionID  = "55555555-5555-5555-5555-555555555555"
	broadcastID    = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaa1"
	recordingID    = "88888888-8888-8888-8888-888888888888"
	secondTrackID  = "99999999-9999-9999-9999-999999999999"
	directGroupID  = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	directRelease  = "cccccccc-cccc-cccc-cccc-cccccccccccc"
)

func TestMusicBrainzMapsOfficialReleases(t *testing.T) {
	var mu sync.Mutex
	var releasePaths []string
	editions := []string{lateEditionJSON(), earlyEditionJSON(), compilationEditionJSON(), broadcastEditionJSON()}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "music-test" || r.Header.Get("Accept") != "application/json" {
			http.Error(w, "headers", http.StatusBadRequest)
			return
		}
		switch {
		case r.URL.Path == "/artist/"+adaID:
			w.Write([]byte(`{"id":"` + adaID + `","name":"Ada","disambiguation":"from the example"}`))
		case r.URL.Path == "/release":
			if r.URL.Query().Get("status") != "official" || !strings.Contains(r.URL.Query().Get("inc"), "release-groups") {
				http.Error(w, "browse", http.StatusBadRequest)
				return
			}
			offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
			if offset < 0 || offset >= len(editions) {
				w.Write([]byte(`{"release-count":4,"release-offset":0,"releases":[]}`))
				return
			}
			w.Write([]byte(`{"release-count":4,"release-offset":` + strconv.Itoa(offset) + `,"releases":[` + editions[offset] + `]}`))
		case strings.HasPrefix(r.URL.Path, "/release/"):
			mu.Lock()
			releasePaths = append(releasePaths, r.URL.Path)
			mu.Unlock()
			switch strings.TrimPrefix(r.URL.Path, "/release/") {
			case earlyEditionID:
				w.Write([]byte(studioReleaseJSON()))
			case compEditionID:
				w.Write([]byte(compilationReleaseJSON()))
			default:
				http.Error(w, "unexpected edition", http.StatusBadRequest)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	mb := NewMusicBrainz(srv.URL, "music-test")
	mb.Interval = -1
	ctx := context.Background()
	artist, err := mb.GetArtist(ctx, adaID)
	if err != nil {
		t.Fatal(err)
	}
	if artist.Name != "Ada" || artist.Description == nil || *artist.Description != "from the example" {
		t.Fatalf("artist = %+v", artist)
	}
	summaries, err := mb.GetArtistReleases(ctx, adaID)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 2 {
		t.Fatalf("summaries = %+v", summaries)
	}
	if summaries[0].ExternalID != compGroupID || summaries[0].ReleaseType != "compilation" || summaries[0].ReleaseDate != nil {
		t.Fatalf("compilation = %+v", summaries[0])
	}
	if summaries[1].ExternalID != studioGroupID || summaries[1].ReleaseType != "album" || summaries[1].Title != "Studio" {
		t.Fatalf("album = %+v", summaries[1])
	}
	if summaries[1].ReleaseDate == nil || summaries[1].ReleaseDate.Format("2006-01-02") != "1999-05-05" {
		t.Fatalf("album date = %v", summaries[1].ReleaseDate)
	}

	studio, err := mb.GetReleaseDetails(ctx, studioGroupID)
	if err != nil {
		t.Fatal(err)
	}
	if studio.ExternalID != studioGroupID || len(studio.Tracks) != 2 {
		t.Fatalf("studio = %+v", studio)
	}
	if studio.Artists[0].ExternalID != adaID || studio.Artists[0].Role != "primary" {
		t.Fatalf("studio artists = %+v", studio.Artists)
	}
	if studio.Tracks[0].ExternalID != recordingID || studio.Tracks[0].ISRC != "GBAYT1200001" || studio.Tracks[0].TitleOverride != "Mysterons (Single)" {
		t.Fatalf("first track = %+v", studio.Tracks[0])
	}
	if studio.Tracks[0].DurationMS == nil || *studio.Tracks[0].DurationMS != 180000 || studio.Tracks[0].DiscNumber != 1 {
		t.Fatalf("first track timing = %+v", studio.Tracks[0])
	}
	if studio.Tracks[1].ExternalID != secondTrackID || studio.Tracks[1].DiscNumber != 2 || studio.Tracks[1].DurationMS != nil {
		t.Fatalf("second track = %+v", studio.Tracks[1])
	}

	compilation, err := mb.GetReleaseDetails(ctx, compGroupID)
	if err != nil {
		t.Fatal(err)
	}
	if len(compilation.Artists) != 2 || compilation.Artists[1].ExternalID != adaID || compilation.Artists[1].Role != "primary" {
		t.Fatalf("compilation artists = %+v", compilation.Artists)
	}
	if compilation.Tracks[0].Artists[0].Role != "primary" || compilation.Tracks[0].Artists[1].Role != "featured" || compilation.Tracks[0].Artists[1].ExternalID != adaID {
		t.Fatalf("featured = %+v", compilation.Tracks[0].Artists)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(releasePaths, ",") != "/release/"+earlyEditionID+",/release/"+compEditionID {
		t.Fatalf("release paths = %v", releasePaths)
	}
}

func TestMusicBrainzDirectReleaseGroup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/release-group/" + directGroupID:
			w.Write([]byte(`{"id":"` + directGroupID + `","title":"Direct","primary-type":"EP","secondary-types":[],"first-release-date":"2001","releases":[{"id":"` + directRelease + `","title":"Direct","status":"Official","date":"2001-03-04","artist-credit":[{"name":"Ada","joinphrase":"","artist":{"id":"` + adaID + `","name":"Ada"}}]}]}`))
		case "/release/" + directRelease:
			w.Write([]byte(`{"id":"` + directRelease + `","title":"Direct","status":"Official","media":[{"position":1,"tracks":[{"position":1,"title":"One","recording":{"id":"` + recordingID + `","title":"One","length":1000,"artist-credit":[{"name":"Ada","joinphrase":"","artist":{"id":"` + adaID + `","name":"Ada"}}]}}]}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	mb := NewMusicBrainz(srv.URL, "music-test")
	mb.Interval = -1
	release, err := mb.GetReleaseDetails(context.Background(), directGroupID)
	if err != nil {
		t.Fatal(err)
	}
	if release.ReleaseType != "ep" || release.ReleaseDate != nil || len(release.Tracks) != 1 {
		t.Fatalf("release = %+v", release)
	}
}

func TestMusicBrainzSearchAndErrors(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path == "/artist" {
			if r.URL.Query().Get("dismax") != "true" || r.URL.Query().Get("query") != "Ada" {
				http.Error(w, "query", http.StatusBadRequest)
				return
			}
			w.Write([]byte(`{"count":5,"artists":[{"id":"` + adaID + `","name":"Ada"},{"id":"` + otherID + `","name":""},{"id":"not-an-id","name":"Nope"}]}`))
			return
		}
		switch r.URL.Path {
		case "/missing":
			http.NotFound(w, r)
		case "/rejected":
			http.Error(w, "bad", http.StatusBadRequest)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	mb := NewMusicBrainz(srv.URL, "music-test")
	mb.Interval = -1
	items, total, err := mb.SearchArtists(context.Background(), "Ada", 1)
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 || len(items) != 1 || items[0].Provider != ProviderMusicBrainz || items[0].ExternalID != adaID {
		t.Fatalf("items=%+v total=%d", items, total)
	}
	if _, _, err := mb.SearchArtists(context.Background(), "  ", 10); err != nil || hits != 1 {
		t.Fatalf("empty query hits=%d err=%v", hits, err)
	}
	if _, err := mb.GetArtist(context.Background(), "not-an-id"); !errors.Is(err, ErrNotFound) || hits != 1 {
		t.Fatalf("invalid id err=%v hits=%d", err, hits)
	}
	mb.BaseURL = srv.URL
	if err := mb.getJSON(context.Background(), "missing", &mbArtist{}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := mb.getJSON(context.Background(), "rejected", &mbArtist{}); !errors.Is(err, ErrPermanent) {
		t.Fatal(err)
	}
}

func TestMusicBrainzRetriesUnavailable(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"id":"` + adaID + `","name":"Ada"}`))
	}))
	defer srv.Close()
	mb := NewMusicBrainz(srv.URL, "music-test")
	mb.Interval = -1
	artist, err := mb.GetArtist(context.Background(), adaID)
	if err != nil {
		t.Fatal(err)
	}
	if artist.Name != "Ada" || calls != 2 {
		t.Fatalf("artist=%+v calls=%d", artist, calls)
	}
}

func TestMusicBrainzPacesRequests(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":"` + adaID + `","name":"Ada"}`))
	}))
	defer srv.Close()
	mb := NewMusicBrainz(srv.URL, "music-test")
	mb.Interval = 200 * time.Millisecond
	start := time.Now()
	ctx := context.Background()
	if _, err := mb.GetArtist(ctx, adaID); err != nil {
		t.Fatal(err)
	}
	if _, err := mb.GetArtist(ctx, adaID); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < 150*time.Millisecond {
		t.Fatalf("requests were not paced: %s", time.Since(start))
	}
}

func TestSearchListKeepsProviderOrder(t *testing.T) {
	list := SearchList{
		stubSearch{items: []ArtistCandidate{{Provider: "fake", ExternalID: "test-artist", Name: "Test Artist"}}, total: 1},
		stubSearch{items: []ArtistCandidate{{Provider: ProviderMusicBrainz, ExternalID: adaID, Name: "Ada"}}, total: 4},
	}
	items, total, err := list.SearchArtists(context.Background(), "a", 1)
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 || len(items) != 1 || items[0].Provider != "fake" {
		t.Fatalf("items=%+v total=%d", items, total)
	}
}

func TestMusicBrainzLiveRead(t *testing.T) {
	if os.Getenv("MUSICBRAINZ_LIVE") == "" {
		t.Skip("set MUSICBRAINZ_LIVE=1 to read the public MusicBrainz API")
	}
	mb := NewMusicBrainz("", "")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	items, _, err := mb.SearchArtists(ctx, "Portishead", 5)
	if err != nil {
		t.Fatal(err)
	}
	const portishead = "8f6bd1e4-fbe1-4f50-aa9b-94c450ec0f11"
	found := false
	for _, item := range items {
		if item.Provider == ProviderMusicBrainz && item.ExternalID == portishead && item.Name == "Portishead" {
			found = true
		}
	}
	if !found {
		t.Fatalf("search = %+v", items)
	}
	artist, err := mb.GetArtist(ctx, portishead)
	if err != nil || artist.Name != "Portishead" {
		t.Fatalf("artist=%+v err=%v", artist, err)
	}
	summaries, err := mb.GetArtistReleases(ctx, portishead)
	if err != nil {
		t.Fatal(err)
	}
	var album ReleaseSummary
	for _, summary := range summaries {
		if summary.ReleaseType == "album" && summary.Title == "Dummy" {
			album = summary
			break
		}
	}
	if album.ExternalID == "" || album.ReleaseDate == nil || album.ReleaseDate.Format("2006-01-02") != "1994-08-22" {
		t.Fatalf("dummy = %+v", album)
	}
	release, err := mb.GetReleaseDetails(ctx, album.ExternalID)
	if err != nil {
		t.Fatal(err)
	}
	if release.ReleaseType != "album" || len(release.Tracks) == 0 || !validMBID(release.Tracks[0].ExternalID) {
		t.Fatalf("release tracks = %d", len(release.Tracks))
	}
}

type stubSearch struct {
	items []ArtistCandidate
	total int
}

func (s stubSearch) SearchArtists(context.Context, string, int) ([]ArtistCandidate, int, error) {
	return s.items, s.total, nil
}

func lateEditionJSON() string {
	return editionJSON(lateEditionID, "2001-02-02", studioGroupID, "Studio", "Album", "", "1999-05-05", adaCredit())
}

func earlyEditionJSON() string {
	return editionJSON(earlyEditionID, "1999-01-01", studioGroupID, "Studio", "Album", "", "1999-05-05", adaCredit())
}

func compilationEditionJSON() string {
	return editionJSON(compEditionID, "1998-01-01", compGroupID, "Collected", "Album", "Compilation", "1999-06", otherCredit())
}

func broadcastEditionJSON() string {
	return editionJSON(broadcastID, "1990-01-01", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaa2", "Broadcast", "Broadcast", "", "1990-01-01", adaCredit())
}

func editionJSON(releaseID, date, groupID, title, primary, secondary, firstDate, credit string) string {
	secondaryJSON := "[]"
	if secondary != "" {
		secondaryJSON = `["` + secondary + `"]`
	}
	return `{"id":"` + releaseID + `","title":"` + title + `","status":"Official","date":"` + date + `","artist-credit":[` + credit + `],"release-group":{"id":"` + groupID + `","title":"` + title + `","primary-type":"` + primary + `","secondary-types":` + secondaryJSON + `,"first-release-date":"` + firstDate + `","artist-credit":[` + credit + `]}}`
}

func adaCredit() string {
	return `{"name":"Ada","joinphrase":"","artist":{"id":"` + adaID + `","name":"Ada"}}`
}

func otherCredit() string {
	return `{"name":"Other","joinphrase":"","artist":{"id":"` + otherID + `","name":"Other"}}`
}

func studioReleaseJSON() string {
	return `{"id":"` + earlyEditionID + `","title":"Studio","status":"Official","media":[{"position":1,"tracks":[{"position":1,"title":"Mysterons (Single)","recording":{"id":"` + recordingID + `","title":"Mysterons","length":180000,"isrcs":["gb-ayt-12-00001","ZZZZZ","usaaa2300002"],"artist-credit":[` + adaCredit() + `]}},{"position":2,"title":"Mysterons","recording":{"id":"` + recordingID + `","title":"Mysterons","length":180000,"artist-credit":[` + adaCredit() + `]}}]},{"position":2,"tracks":[{"position":1,"title":"Roads","recording":{"id":"` + secondTrackID + `","title":"Roads","length":0,"artist-credit":[` + adaCredit() + `]}}]}]}`
}

func compilationReleaseJSON() string {
	return `{"id":"` + compEditionID + `","title":"Collected","status":"Official","media":[{"position":1,"tracks":[{"position":1,"title":"Together","recording":{"id":"` + secondTrackID + `","title":"Together","length":2000,"artist-credit":[{"name":"Other","joinphrase":" feat. ","artist":{"id":"` + otherID + `","name":"Other"}},{"name":"Ada","joinphrase":"","artist":{"id":"` + adaID + `","name":"Ada"}}]}}]}]}`
}
