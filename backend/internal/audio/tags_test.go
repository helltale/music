package audio

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bogem/id3v2"
)

func TestAudioMetadataTransformerPassesThroughNonMP3(t *testing.T) {
	dir := t.TempDir()
	in := []byte("RIFFdemo-audio")
	body, err := (AudioMetadataTransformer{TempDir: dir, MaxBytes: 1 << 20}).Transform(
		context.Background(),
		io.NopCloser(bytes.NewReader(in)),
		TrackTags{Artist: "Test Artist", Album: "First Album", Title: "Track One"},
	)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := body.Close(); err != nil {
		t.Fatal(err)
	}
	if string(got) != string(in) {
		t.Fatalf("body = %q", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temp files = %d", len(entries))
	}
}

func TestAudioMetadataTransformerWritesID3(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, "plain.mp3")
	if err := os.WriteFile(original, minimalMP3(), 0o644); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}
	body, err := (AudioMetadataTransformer{TempDir: dir, MaxBytes: 1 << 20}).Transform(
		context.Background(),
		io.NopCloser(bytes.NewReader(payload)),
		TrackTags{Artist: "Test Artist", Album: "First Album", Title: "Track One"},
	)
	if err != nil {
		t.Fatal(err)
	}
	tagged, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := body.Close(); err != nil {
		t.Fatal(err)
	}
	tag, err := id3v2.ParseReader(bytes.NewReader(tagged), id3v2.Options{Parse: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tag.Close()
	if tag.Artist() != "Test Artist" || tag.Album() != "First Album" || tag.Title() != "Track One" {
		t.Fatalf("tags artist=%q album=%q title=%q", tag.Artist(), tag.Album(), tag.Title())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "plain.mp3" {
		t.Fatalf("temp dir = %+v", entries)
	}
}

func TestMetadataArgs(t *testing.T) {
	args := metadataArgs(TrackTags{Artist: "A\nB", Album: "First Album", Title: "Track One"})
	want := []string{"-metadata", "title=Track One", "-metadata", "artist=A B", "-metadata", "album=First Album"}
	if len(args) != len(want) {
		t.Fatalf("args = %#v", args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args = %#v", args)
		}
	}
	if metadataArgs(TrackTags{}) != nil {
		t.Fatal("empty tags produced arguments")
	}
}

func TestTranscodeWritesMetadata(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe is not installed")
	}
	dir := t.TempDir()
	dest := filepath.Join(dir, "playable.m4a")
	err := transcode(context.Background(), filepath.Join(fixtureDir(t), "track-one.wav"), dest, TrackTags{
		Artist: "Test Artist",
		Album:  "First Album",
		Title:  "Track One",
	})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_format", dest)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Format struct {
			Tags map[string]string `json:"tags"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Format.Tags["artist"] != "Test Artist" || parsed.Format.Tags["album"] != "First Album" || parsed.Format.Tags["title"] != "Track One" {
		t.Fatalf("tags = %+v", parsed.Format.Tags)
	}
}

func minimalMP3() []byte {
	// ID3v2.3 header with an empty body, then one MPEG frame sync.
	header := []byte{
		'I', 'D', '3',
		3, 0,
		0,
		0, 0, 0, 0,
	}
	frame := []byte{0xff, 0xfb, 0x90, 0xc0}
	return append(header, frame...)
}
