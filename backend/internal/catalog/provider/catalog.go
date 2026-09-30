// Package provider is the catalog boundary for external metadata.
// Implementations return normalized data. They do not write the local catalog.
package provider

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound means the provider has no entity with that external id.
var ErrNotFound = errors.New("not found")

const ProviderFake = "fake"

// ArtistCandidate is one search hit. It has no local catalog id.
type ArtistCandidate struct {
	Provider   string
	ExternalID string
	Name       string
}

// Artist is a provider artist.
type Artist struct {
	ExternalID  string
	Name        string
	Description *string
}

// Credit is an artist role supplied by the provider.
type Credit struct {
	ExternalID string
	Name       string
	Role       string
}

// ReleaseSummary identifies a release without its tracks.
type ReleaseSummary struct {
	ExternalID  string
	Title       string
	ReleaseType string
	ReleaseDate *time.Time
}

// Track is one recording on a release.
// ExternalID identifies the recording and stays the same on every release.
type Track struct {
	ExternalID    string
	Title         string
	ISRC          string
	DurationMS    *int
	DiscNumber    int
	TrackNumber   int
	TitleOverride string
	Artists       []Credit
}

// Release is a release with its tracks.
type Release struct {
	ExternalID  string
	Title       string
	ReleaseType string
	ReleaseDate *time.Time
	Artists     []Credit
	Tracks      []Track
}

// ArtistSearcher is the admin search capability.
// total is the full match count. items is capped by limit. offset is not applied here.
type ArtistSearcher interface {
	SearchArtists(ctx context.Context, query string, limit int) (items []ArtistCandidate, total int, err error)
}

// ArtistReader loads one provider artist.
type ArtistReader interface {
	GetArtist(ctx context.Context, externalID string) (Artist, error)
}

// ReleaseReader loads the releases of one provider artist.
type ReleaseReader interface {
	GetArtistReleases(ctx context.Context, externalID string) ([]ReleaseSummary, error)
	GetReleaseDetails(ctx context.Context, externalID string) (Release, error)
}
