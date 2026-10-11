package audio

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

const freeRecordingID = "11111111-1111-1111-1111-111111111111"

func TestFreeDownloadReadsDirectFile(t *testing.T) {
	var fileHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "music-test" {
			http.Error(w, "agent", http.StatusBadRequest)
			return
		}
		switch r.URL.Path {
		case "/recording/" + freeRecordingID:
			if !strings.Contains(r.URL.RawQuery, "url-rels") {
				http.Error(w, "include", http.StatusBadRequest)
				return
			}
			w.Write([]byte(`{"relations":[` +
				`{"type":"free streaming","type-id":"b1","url":{"resource":"` + r.Host + `/stream.mp3"}},` +
				`{"type":"purchase for download","type-id":"b2","url":{"resource":"https://shop.example/buy.mp3"}},` +
				`{"type":"download for free","type-id":"45d0cbc5-d65b-4e77-bdfd-8a75207cb5c5","ended":true,"url":{"resource":"http://` + r.Host + `/old.mp3"}},` +
				`{"type":"download for free","type-id":"45d0cbc5-d65b-4e77-bdfd-8a75207cb5c5","url":{"resource":"http://` + r.Host + `/track.wav"}}` +
				`]}`))
		case "/track.wav":
			fileHits++
			w.Header().Set("Content-Type", "audio/wav")
			w.Write([]byte("RIFFdemo-audio"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	source := newTestFree(srv.URL)
	body, reference, err := source.Open(context.Background(), freeRecordingID)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "RIFFdemo-audio" || !strings.HasSuffix(reference, "/track.wav") || fileHits != 1 {
		t.Fatalf("body=%q reference=%s hits=%d", got, reference, fileHits)
	}
}

func TestFreeDownloadUsesArchiveMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/recording/" + freeRecordingID:
			w.Write([]byte(`{"relations":[{"type":"download for free","type-id":"45d0cbc5-d65b-4e77-bdfd-8a75207cb5c5","url":{"resource":"` + serverURL(r) + `/details/item"}}]}`))
		case "/metadata/item":
			w.Write([]byte(`{"files":[{"name":"cover.jpg","size":"10"},{"name":"song.flac","size":"9000"},{"name":"song.mp3","size":"1000"}]}`))
		case "/download/item/song.mp3":
			w.Header().Set("Content-Type", "audio/mpeg")
			w.Write([]byte("ID3tiny-mp3"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	source := newTestFree(srv.URL)
	body, reference, err := source.Open(context.Background(), freeRecordingID)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	got, _ := io.ReadAll(body)
	if string(got) != "ID3tiny-mp3" || !strings.Contains(reference, "/download/item/song.mp3") {
		t.Fatalf("body=%q reference=%s", got, reference)
	}
}

func TestFreeDownloadRejectsUnusableSources(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path != "/recording/"+freeRecordingID {
			http.Error(w, "file was fetched", http.StatusInternalServerError)
			return
		}
		w.Write([]byte(`{"relations":[{"type":"download for free","type-id":"45d0cbc5-d65b-4e77-bdfd-8a75207cb5c5","url":{"resource":"https://www.youtube.com/watch.mp3"}}]}`))
	}))
	defer srv.Close()
	source := newTestFree(srv.URL)
	if _, _, err := source.Open(context.Background(), "track-one"); !errors.Is(err, ErrSourceNotFound) || hits != 0 {
		t.Fatalf("local id err=%v hits=%d", err, hits)
	}
	if _, _, err := source.Open(context.Background(), freeRecordingID); !errors.Is(err, ErrSourceNotFound) || hits != 1 {
		t.Fatalf("youtube err=%v hits=%d", err, hits)
	}

	blocked := newTestFree(srv.URL)
	blocked.AllowPrivate = false
	// Replace the relation by pointing the client at a handler that offers a loopback file.
	loop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".mp3") {
			t.Error("private file was requested")
		}
		w.Write([]byte(`{"relations":[{"type":"download for free","type-id":"45d0cbc5-d65b-4e77-bdfd-8a75207cb5c5","url":{"resource":"http://127.0.0.1/secret.mp3"}}]}`))
	}))
	defer loop.Close()
	blocked.BaseURL = loop.URL
	if _, _, err := blocked.Open(context.Background(), freeRecordingID); !errors.Is(err, ErrSourceNotFound) {
		t.Fatal(err)
	}
}

func TestFreeDownloadRetriesLookup(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"relations":[]}`))
	}))
	defer srv.Close()
	source := newTestFree(srv.URL)
	if _, _, err := source.Open(context.Background(), freeRecordingID); !errors.Is(err, ErrSourceNotFound) || calls != 2 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestFallbackPrefersLocalFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/track-one.wav", []byte("local-wav"), 0o644); err != nil {
		t.Fatal(err)
	}
	fallback := &Fallback{
		Primary:       LocalProvider{Dir: dir},
		PrimaryName:   ProviderLocal,
		Secondary:     failIfOpened{},
		SecondaryName: ProviderFreeDownload,
	}
	body, reference, err := fallback.Open(context.Background(), "track-one")
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	got, _ := io.ReadAll(body)
	if string(got) != "local-wav" || reference != "track-one.wav" || fallback.LastProvider() != ProviderLocal {
		t.Fatalf("body=%q reference=%s provider=%s", got, reference, fallback.LastProvider())
	}
}

func TestFreeDownloadBecomesPlayable(t *testing.T) {
	requireFFmpeg(t)
	wav, err := os.ReadFile(fixtureDir(t) + "/track-one.wav")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".wav") {
			w.Header().Set("Content-Type", "audio/wav")
			w.Write(wav)
			return
		}
		w.Write([]byte(`{"relations":[{"type":"download for free","type-id":"45d0cbc5-d65b-4e77-bdfd-8a75207cb5c5","url":{"resource":"` + serverURL(r) + `/track.wav"}}]}`))
	}))
	defer srv.Close()

	source := &Fallback{
		Primary:       LocalProvider{Dir: t.TempDir()},
		PrimaryName:   ProviderLocal,
		Secondary:     newTestFree(srv.URL),
		SecondaryName: ProviderFreeDownload,
	}
	recordingID := newID(t)
	asset, err := newProcessor(t, discardStorage{}, source).Process(context.Background(), recordingID, ProviderLocal, freeRecordingID, newID(t))
	if err != nil {
		t.Fatal(err)
	}
	if asset.Status != StatusReady || asset.SourceProvider != ProviderFreeDownload {
		t.Fatalf("asset = %+v", asset)
	}
	if asset.SourceReference == nil || !strings.HasSuffix(*asset.SourceReference, "/track.wav") {
		t.Fatalf("reference = %v", asset.SourceReference)
	}
	if asset.Codec == nil || *asset.Codec != "aac" || asset.PlayableObjectKey == nil {
		t.Fatalf("playable = %+v", asset)
	}
}

func TestFreeDownloadLiveHeader(t *testing.T) {
	if os.Getenv("MUSICBRAINZ_LIVE") == "" {
		t.Skip("set MUSICBRAINZ_LIVE=1 to download a legal free recording")
	}
	source := NewFreeDownload("", "")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	body, reference, err := source.Open(ctx, "8ac3fb86-b2d9-4959-a289-90dc86de5fa1")
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	prefix := make([]byte, 3)
	if _, err := io.ReadFull(body, prefix); err != nil {
		t.Fatal(err)
	}
	mp3 := string(prefix) == "ID3" || (prefix[0] == 0xff && prefix[1]&0xe0 == 0xe0)
	if !mp3 || !strings.Contains(reference, "archive.org/") || !strings.HasSuffix(strings.Split(reference, "?")[0], ".mp3") {
		t.Fatalf("prefix=%q reference=%s", prefix, reference)
	}
}

func newTestFree(base string) *FreeDownload {
	source := NewFreeDownload(base, "music-test")
	source.ArchiveBase = base
	source.Interval = -1
	source.AllowHTTP = true
	source.AllowPrivate = true
	return source
}

func serverURL(r *http.Request) string {
	return "http://" + r.Host
}

type failIfOpened struct{}

func (failIfOpened) Open(context.Context, string) (io.ReadCloser, string, error) {
	return nil, "", errors.New("secondary provider was opened")
}
