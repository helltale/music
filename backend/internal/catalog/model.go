package catalog

import "time"

const (
	RolePrimary  = "primary"
	RoleFeatured = "featured"

	ReleaseAlbum       = "album"
	ReleaseSingle      = "single"
	ReleaseEP          = "ep"
	ReleaseCompilation = "compilation"
)

// Artist is a catalog row. ID is created by the caller.
type Artist struct {
	ID             string
	Name           string
	Description    *string
	ImageObjectKey *string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Release is a catalog row. ReleaseDate uses the calendar date in UTC.
type Release struct {
	ID             string
	Title          string
	ReleaseType    string
	ReleaseDate    *time.Time
	CoverObjectKey *string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Recording is a musical recording. It does not carry an audio status.
type Recording struct {
	ID         string
	Title      string
	ISRC       *string
	DurationMS *int
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// ArtistRole links an artist to a release or a recording.
type ArtistRole struct {
	ArtistID string
	Name     string
	Role     string
}

// ReleaseTrack is one position of a recording on a release.
type ReleaseTrack struct {
	ReleaseID     string
	RecordingID   string
	DiscNumber    int
	TrackNumber   int
	TitleOverride *string
	Title         string
	DurationMS    *int
}

// ExternalID is a provider identifier for one catalog entity.
type ExternalID struct {
	EntityID     string
	Provider     string
	ExternalID   string
	LastSyncedAt time.Time
}
