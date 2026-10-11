// Package streaming signs playback URLs. It reads the catalog and audio rows
// and does not proxy the file.
package streaming

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/helltale/music/backend/internal/audio"
	"github.com/helltale/music/backend/internal/catalog"
)

const contentType = "audio/mp4"

// ErrNotReady means the recording exists and has no READY playable asset.
var ErrNotReady = errors.New("audio not ready")

// Recordings loads a catalog recording.
type Recordings interface {
	GetRecording(ctx context.Context, id string) (catalog.Recording, error)
}

// Assets loads the audio asset for a recording.
type Assets interface {
	GetByRecording(ctx context.Context, recordingID string) (audio.Asset, error)
}

// Service prepares one playback URL.
type Service struct {
	Recordings Recordings
	Assets     Assets
	Objects    audio.ObjectStorage
	TTL        time.Duration
}

// Playback is the public play response. It does not include the object key or secrets.
type Playback struct {
	RecordingID  string
	AudioAssetID string
	URL          string
	ExpiresAt    time.Time
	ContentType  string
	DurationMS   *int
}

// Play returns a presigned GET for a READY asset.
// A missing recording is catalog.ErrNotFound. Any other asset state is ErrNotReady.
func (s *Service) Play(ctx context.Context, recordingID string) (Playback, error) {
	if _, err := s.Recordings.GetRecording(ctx, recordingID); err != nil {
		return Playback{}, err
	}
	asset, err := s.Assets.GetByRecording(ctx, recordingID)
	if errors.Is(err, audio.ErrNotFound) {
		return Playback{}, ErrNotReady
	}
	if err != nil {
		return Playback{}, err
	}
	key, ok := playableKey(asset)
	if !ok {
		return Playback{}, ErrNotReady
	}
	url, expiresAt, err := s.Objects.PresignGet(ctx, key, s.ttl())
	if err != nil {
		return Playback{}, err
	}
	return Playback{
		RecordingID:  recordingID,
		AudioAssetID: asset.ID,
		URL:          url,
		ExpiresAt:    expiresAt,
		ContentType:  contentType,
		DurationMS:   asset.DurationMS,
	}, nil
}

func (s *Service) ttl() time.Duration {
	if s.TTL <= 0 {
		return time.Hour
	}
	return s.TTL
}

func playableKey(asset audio.Asset) (string, bool) {
	if asset.Status != audio.StatusReady || asset.PlayableObjectKey == nil {
		return "", false
	}
	key := strings.TrimSpace(*asset.PlayableObjectKey)
	if key == "" {
		return "", false
	}
	return key, true
}
