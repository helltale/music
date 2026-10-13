package audio

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/bogem/id3v2"
)

// TrackTags is the artist, album, and title written onto one recording.
type TrackTags struct {
	Artist string
	Album  string
	Title  string
}

func (t TrackTags) empty() bool {
	return strings.TrimSpace(t.Artist) == "" && strings.TrimSpace(t.Album) == "" && strings.TrimSpace(t.Title) == ""
}

// AudioMetadataTransformer writes ID3 tags onto an MP3 stream.
// Other containers are returned unchanged. ID3v2 lives at the start of the file,
// so an MP3 is copied into a temporary file no larger than MaxBytes before the tags are written.
// Close on the returned MP3 reader closes and removes that file.
type AudioMetadataTransformer struct {
	TempDir  string
	MaxBytes int64
}

// Transform returns a stream with ID3 artist, album, and title when the payload is an MP3.
// On error the input is closed. On success the caller closes the returned reader,
// which also closes the input.
func (t AudioMetadataTransformer) Transform(ctx context.Context, in io.ReadCloser, tags TrackTags) (io.ReadCloser, error) {
	if in == nil {
		return nil, errors.New("audio stream is missing")
	}
	if tags.empty() {
		return in, nil
	}
	prefix := make([]byte, 16)
	n, err := readPrefix(ctx, in, prefix)
	if err != nil {
		in.Close()
		return nil, err
	}
	if !looksLikeMP3(prefix[:n]) {
		return &streamBody{Reader: io.MultiReader(bytes.NewReader(prefix[:n]), ctxReader{ctx: ctx, r: in}), closer: in}, nil
	}
	return t.tagMP3(ctx, in, prefix[:n], tags)
}

func (t AudioMetadataTransformer) tagMP3(ctx context.Context, in io.ReadCloser, prefix []byte, tags TrackTags) (io.ReadCloser, error) {
	file, err := os.CreateTemp(t.TempDir, "audio-tags-*")
	if err != nil {
		in.Close()
		return nil, err
	}
	path := file.Name()
	limit := t.MaxBytes
	if limit <= 0 {
		limit = 1
	}
	reader := ctxReader{ctx: ctx, r: io.MultiReader(bytes.NewReader(prefix), in)}
	written, copyErr := io.Copy(file, io.LimitReader(reader, limit+1))
	closeFile := file.Close()
	closeIn := in.Close()
	if copyErr != nil {
		os.Remove(path)
		return nil, copyErr
	}
	if closeFile != nil {
		os.Remove(path)
		return nil, closeFile
	}
	if closeIn != nil {
		os.Remove(path)
		return nil, closeIn
	}
	if written > limit {
		os.Remove(path)
		return nil, &ProcessError{Message: "Audio file is too large", Permanent: true}
	}
	if err := writeID3(path, tags); err != nil {
		os.Remove(path)
		return nil, err
	}
	tagged, err := os.Open(path)
	if err != nil {
		os.Remove(path)
		return nil, err
	}
	return &tempAudio{File: tagged, path: path}, nil
}

func writeID3(path string, tags TrackTags) error {
	tag, err := id3v2.Open(path, id3v2.Options{Parse: true})
	if err != nil {
		return err
	}
	defer tag.Close()
	tag.SetArtist(strings.TrimSpace(tags.Artist))
	tag.SetAlbum(strings.TrimSpace(tags.Album))
	tag.SetTitle(strings.TrimSpace(tags.Title))
	return tag.Save()
}

func readPrefix(ctx context.Context, r io.Reader, buf []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	n, err := io.ReadFull(r, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return n, err
	}
	return n, nil
}

func looksLikeMP3(prefix []byte) bool {
	if len(prefix) >= 3 && bytes.Equal(prefix[:3], []byte("ID3")) {
		return true
	}
	return len(prefix) >= 2 && prefix[0] == 0xff && prefix[1]&0xe0 == 0xe0
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

type streamBody struct {
	io.Reader
	closer io.Closer
}

func (b *streamBody) Close() error { return b.closer.Close() }

type tempAudio struct {
	*os.File
	path string
}

func (f *tempAudio) Close() error {
	err := f.File.Close()
	removeErr := os.Remove(f.path)
	if err != nil {
		return err
	}
	if removeErr != nil && !os.IsNotExist(removeErr) {
		return removeErr
	}
	return nil
}
