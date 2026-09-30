// Package audio owns playable files. It does not import the catalog.
package audio

import (
	"errors"
	"time"
)

const (
	StatusPending    = "PENDING"
	StatusAcquiring  = "ACQUIRING"
	StatusProcessing = "PROCESSING"
	StatusReady      = "READY"
	StatusFailed     = "FAILED"
)

var (
	// ErrNotFound means the recording has no audio asset.
	ErrNotFound = errors.New("not found")
	// ErrSourceNotFound means the provider has no file for the recording.
	ErrSourceNotFound = errors.New("audio source not found")
	// ErrSuperseded means a newer attempt owns the asset.
	ErrSuperseded = errors.New("audio attempt superseded")
)

// Asset is one playable file for a recording.
type Asset struct {
	ID                string
	RecordingID       string
	SourceProvider    string
	SourceReference   *string
	Generation        int
	OriginalObjectKey *string
	PlayableObjectKey *string
	Codec             *string
	Container         *string
	Bitrate           *int
	SampleRate        *int
	Channels          *int
	DurationMS        *int
	SizeBytes         *int64
	Checksum          *string
	Status            string
	LastError         *string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// ProcessError is a file-level audio failure. The asset row is already FAILED.
type ProcessError struct {
	Message   string
	Err       error
	Permanent bool
}

func (e *ProcessError) Error() string {
	return e.Message
}

func (e *ProcessError) Unwrap() error {
	return e.Err
}
