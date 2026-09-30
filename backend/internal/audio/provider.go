package audio

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Provider opens the original audio for one recording.
type Provider interface {
	Open(ctx context.Context, recordingExternalID string) (source io.ReadCloser, reference string, err error)
}

// LocalProvider reads {Dir}/{externalID}.wav.
type LocalProvider struct {
	Dir string
}

// Open returns the wav file. reference is the file name stored on the asset.
func (p LocalProvider) Open(_ context.Context, recordingExternalID string) (io.ReadCloser, string, error) {
	if !safeID(recordingExternalID) {
		return nil, "", ErrSourceNotFound
	}
	name := recordingExternalID + ".wav"
	file, err := os.Open(filepath.Join(p.Dir, name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", ErrSourceNotFound
		}
		return nil, "", err
	}
	return file, name, nil
}

func safeID(id string) bool {
	return id != "" && id != "." && id != ".." && !strings.ContainsAny(id, `/\`) && filepath.Base(id) == id
}
