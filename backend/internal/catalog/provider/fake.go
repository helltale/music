package provider

import (
	"context"
	"strings"
)

const (
	fakeArtistID     = "test-artist"
	fakeAlbumID      = "first-album"
	fakeDeluxeID     = "first-album-deluxe"
	fakeTrackOneID   = "track-one"
	fakeTrackTwoID   = "track-two"
	fakeTrackThreeID = "track-three"
	fakeArtistName   = "Test Artist"
	fakeTrackOneISRC = "USTST2600001"
	fakeTrackOneMS   = 180000
	fakeTrackTwoMS   = 200000
	fakeTrackThreeMS = 150000
	fakeReleaseType  = "album"
)

// Fake is the in-process demo catalog.
type Fake struct{}

// NewFake returns the demo catalog provider.
func NewFake() *Fake {
	return &Fake{}
}

// SearchArtists matches the artist name as a case-insensitive substring.
func (f *Fake) SearchArtists(_ context.Context, query string, limit int) ([]ArtistCandidate, int, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	var matched []ArtistCandidate
	if query != "" && strings.Contains(strings.ToLower(fakeArtistName), query) {
		matched = append(matched, ArtistCandidate{
			Provider:   ProviderFake,
			ExternalID: fakeArtistID,
			Name:       fakeArtistName,
		})
	}
	total := len(matched)
	if limit < 0 {
		limit = 0
	}
	if len(matched) > limit {
		matched = matched[:limit]
	}
	if matched == nil {
		matched = []ArtistCandidate{}
	}
	return matched, total, nil
}

// GetArtist returns the demo artist.
func (f *Fake) GetArtist(_ context.Context, externalID string) (Artist, error) {
	if externalID != fakeArtistID {
		return Artist{}, ErrNotFound
	}
	return Artist{ExternalID: fakeArtistID, Name: fakeArtistName}, nil
}

// GetArtistReleases returns both demo albums.
func (f *Fake) GetArtistReleases(_ context.Context, externalID string) ([]ReleaseSummary, error) {
	if externalID != fakeArtistID {
		return nil, ErrNotFound
	}
	album := demoAlbum()
	deluxe := demoDeluxe()
	return []ReleaseSummary{
		{ExternalID: album.ExternalID, Title: album.Title, ReleaseType: album.ReleaseType},
		{ExternalID: deluxe.ExternalID, Title: deluxe.Title, ReleaseType: deluxe.ReleaseType},
	}, nil
}

// GetReleaseDetails returns one demo release and its tracks.
func (f *Fake) GetReleaseDetails(_ context.Context, externalID string) (Release, error) {
	switch externalID {
	case fakeAlbumID:
		return cloneRelease(demoAlbum()), nil
	case fakeDeluxeID:
		return cloneRelease(demoDeluxe()), nil
	default:
		return Release{}, ErrNotFound
	}
}

func demoAlbum() Release {
	return Release{
		ExternalID:  fakeAlbumID,
		Title:       "First Album",
		ReleaseType: fakeReleaseType,
		Artists:     []Credit{primaryCredit()},
		Tracks: []Track{
			track(fakeTrackOneID, "Track One", fakeTrackOneISRC, fakeTrackOneMS, 1),
			track(fakeTrackTwoID, "Track Two", "", fakeTrackTwoMS, 2),
		},
	}
}

func demoDeluxe() Release {
	album := demoAlbum()
	album.ExternalID = fakeDeluxeID
	album.Title = "First Album Deluxe"
	album.Tracks = append(album.Tracks, track(fakeTrackThreeID, "Track Three", "", fakeTrackThreeMS, 3))
	return album
}

func primaryCredit() Credit {
	return Credit{ExternalID: fakeArtistID, Name: fakeArtistName, Role: "primary"}
}

func track(externalID, title, isrc string, durationMS, number int) Track {
	return Track{
		ExternalID:  externalID,
		Title:       title,
		ISRC:        isrc,
		DurationMS:  &durationMS,
		DiscNumber:  1,
		TrackNumber: number,
		Artists:     []Credit{primaryCredit()},
	}
}

func cloneRelease(release Release) Release {
	release.Artists = append([]Credit(nil), release.Artists...)
	tracks := make([]Track, len(release.Tracks))
	for i, item := range release.Tracks {
		tracks[i] = item
		tracks[i].Artists = append([]Credit(nil), item.Artists...)
		if item.DurationMS != nil {
			duration := *item.DurationMS
			tracks[i].DurationMS = &duration
		}
	}
	release.Tracks = tracks
	return release
}
