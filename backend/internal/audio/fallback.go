package audio

import (
	"context"
	"errors"
	"io"
	"sync"
)

// Fallback tries Primary, then Secondary when the primary has no file.
// LastProvider is the name of the provider that opened the last file.
type Fallback struct {
	Primary       Provider
	PrimaryName   string
	Secondary     Provider
	SecondaryName string

	mu   sync.Mutex
	last string
}

// Open returns the first file that exists.
func (f *Fallback) Open(ctx context.Context, recordingExternalID string) (io.ReadCloser, string, error) {
	f.set("")
	if f.Primary != nil {
		source, reference, err := f.Primary.Open(ctx, recordingExternalID)
		if err == nil {
			f.set(f.PrimaryName)
			return source, reference, nil
		}
		if !errors.Is(err, ErrSourceNotFound) {
			return nil, "", err
		}
	}
	if f.Secondary == nil {
		return nil, "", ErrSourceNotFound
	}
	source, reference, err := f.Secondary.Open(ctx, recordingExternalID)
	if err != nil {
		if errors.Is(err, ErrSourceNotFound) {
			f.set(f.SecondaryName)
		}
		return nil, "", err
	}
	f.set(f.SecondaryName)
	return source, reference, nil
}

// LastProvider reports which provider satisfied the latest successful Open.
func (f *Fallback) LastProvider() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.last
}

func (f *Fallback) set(name string) {
	f.mu.Lock()
	f.last = name
	f.mu.Unlock()
}
