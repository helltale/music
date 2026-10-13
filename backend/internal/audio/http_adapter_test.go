package audio

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGenericHTTPAdapterReadsAudio(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "music-test" {
			http.Error(w, "agent", http.StatusBadRequest)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "audio/wav")
		w.Write([]byte("RIFFdemo-audio"))
	}))
	defer srv.Close()

	body, err := (&GenericHTTPAdapter{UserAgent: "music-test", AllowHTTP: true, AllowPrivate: true}).DownloadPayload(context.Background(), srv.URL+"/track.wav")
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "RIFFdemo-audio" {
		t.Fatalf("body = %q", got)
	}
	ref, ok := body.(interface{ SourceReference() string })
	if !ok || !strings.HasSuffix(ref.SourceReference(), "/track.wav") {
		t.Fatalf("reference = %v", ref)
	}
}

func TestGenericHTTPAdapterRejectsHTML(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<!doctype html><html></html>"))
	}))
	t.Cleanup(func() {
		closed := make(chan struct{})
		go func() {
			srv.Close()
			close(closed)
		}()
		select {
		case <-closed:
		case <-time.After(2 * time.Second):
			t.Error("response body was not closed")
		}
	})

	_, err := (&GenericHTTPAdapter{AllowHTTP: true, AllowPrivate: true}).DownloadPayload(context.Background(), srv.URL+"/page.mp3")
	if !errors.Is(err, ErrSourceNotFound) || hits != 1 {
		t.Fatalf("err=%v hits=%d", err, hits)
	}
}

func TestGenericHTTPAdapterRejectsPrivateAddress(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Write([]byte("ID3secret"))
	}))
	defer srv.Close()

	_, err := (&GenericHTTPAdapter{AllowHTTP: true}).DownloadPayload(context.Background(), srv.URL+"/secret.mp3")
	if !errors.Is(err, ErrSourceNotFound) || hits != 0 {
		t.Fatalf("err=%v hits=%d", err, hits)
	}
}

func TestGenericHTTPAdapterRejectsStreamingHost(t *testing.T) {
	_, err := (&GenericHTTPAdapter{}).DownloadPayload(context.Background(), "https://www.youtube.com/watch.mp3")
	if !errors.Is(err, ErrSourceNotFound) {
		t.Fatal(err)
	}
}
