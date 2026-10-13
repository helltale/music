package audio

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProcessStopsOversizedReadAndRemovesTemp(t *testing.T) {
	body := &countingReader{left: 1 << 20}
	processor := newProcessor(t, discardStorage{}, stubProvider{body: body, reference: "huge.bin"})
	processor.MaxBytes = 64
	_, err := processor.Process(context.Background(), newID(t), ProviderLocal, "huge", newID(t))
	var process *ProcessError
	if !errors.As(err, &process) || !process.Permanent || process.Message != "Audio file is too large" {
		t.Fatalf("error = %#v", err)
	}
	if int64(body.read) > processor.MaxBytes+1 {
		t.Fatalf("read %d bytes, limit %d", body.read, processor.MaxBytes)
	}
	assertTempEmpty(t, processor.TempDir)
}

func TestProcessStorageOutageIsTemporaryAndRemovesTemp(t *testing.T) {
	requireFFmpeg(t)
	processor := newProcessor(t, outageStorage{}, LocalProvider{Dir: fixtureDir(t)})
	recordingID := newID(t)
	_, err := processor.Process(context.Background(), recordingID, ProviderLocal, "track-one", newID(t))
	var process *ProcessError
	if !errors.As(err, &process) || process.Permanent || process.Message != "Audio processing failed" {
		t.Fatalf("error = %#v", err)
	}
	asset, err := NewStore(testPool).GetByRecording(context.Background(), recordingID)
	if err != nil {
		t.Fatal(err)
	}
	if asset.Status == StatusReady || asset.PlayableObjectKey != nil {
		t.Fatalf("outage published asset: %+v", asset)
	}
	assertTempEmpty(t, processor.TempDir)
}

func TestProcessTimeoutRemovesTemp(t *testing.T) {
	requireFFmpeg(t)
	processor := newProcessor(t, discardStorage{}, LocalProvider{Dir: fixtureDir(t)})
	processor.Timeout = time.Nanosecond
	_, err := processor.Process(context.Background(), newID(t), ProviderLocal, "track-one", newID(t))
	var process *ProcessError
	if !errors.As(err, &process) || process.Permanent {
		t.Fatalf("error = %#v", err)
	}
	assertTempEmpty(t, processor.TempDir)
}

type countingReader struct {
	left int
	read int
}

func (c *countingReader) Read(p []byte) (int, error) {
	if c.left == 0 {
		return 0, io.EOF
	}
	n := len(p)
	if n > c.left {
		n = c.left
	}
	for i := range n {
		p[i] = 'x'
	}
	c.left -= n
	c.read += n
	return n, nil
}

type stubProvider struct {
	body      io.Reader
	reference string
}

func (s stubProvider) Open(context.Context, string) (io.ReadCloser, string, error) {
	return io.NopCloser(s.body), s.reference, nil
}

type outageStorage struct{}

func (outageStorage) Put(context.Context, string, string, io.Reader, int64) error {
	return errors.New("minio unavailable")
}

func (outageStorage) Delete(context.Context, string) error { return nil }

func (outageStorage) PresignGet(context.Context, string, time.Duration) (string, time.Time, error) {
	return "", time.Time{}, errors.New("minio unavailable")
}

func assertTempEmpty(t *testing.T, dir string) {
	t.Helper()
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			t.Fatalf("temp file remains: %s", path)
		}
		if entry.IsDir() && path != dir && !strings.HasPrefix(path, dir) {
			t.Fatalf("temp dir escaped: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
