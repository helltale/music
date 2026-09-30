package catalog

import (
	"context"
	"testing"

	"github.com/helltale/music/backend/internal/catalog/provider"
)

func TestFakeCatalogImportsAsOneRecording(t *testing.T) {
	ctx := context.Background()
	if err := removeDemoCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := removeDemoCatalog(context.Background()); err != nil {
			t.Error(err)
		}
	})

	fake := provider.NewFake()
	first := importArtist(t, mustArtistImport(t, fake))
	second := importArtist(t, mustArtistImport(t, fake))
	if first.ArtistID != second.ArtistID {
		t.Fatal("repeat import created another artist")
	}
	for _, externalID := range []string{"track-one", "track-two", "track-three"} {
		if first.RecordingIDs[externalID] == "" || first.RecordingIDs[externalID] != second.RecordingIDs[externalID] {
			t.Fatalf("recording %s ids = %s %s", externalID, first.RecordingIDs[externalID], second.RecordingIDs[externalID])
		}
	}
	if first.ReleaseIDs["first-album"] != second.ReleaseIDs["first-album"] || first.ReleaseIDs["first-album-deluxe"] != second.ReleaseIDs["first-album-deluxe"] {
		t.Fatal("repeat import created another release")
	}

	store := NewStore(testPool)
	album, err := store.ListReleaseTracks(ctx, first.ReleaseIDs["first-album"])
	if err != nil {
		t.Fatal(err)
	}
	deluxe, err := store.ListReleaseTracks(ctx, first.ReleaseIDs["first-album-deluxe"])
	if err != nil {
		t.Fatal(err)
	}
	if len(album) != 2 || len(deluxe) != 3 {
		t.Fatalf("tracks album=%d deluxe=%d", len(album), len(deluxe))
	}
	if album[0].RecordingID != deluxe[0].RecordingID || album[0].RecordingID != first.RecordingIDs["track-one"] {
		t.Fatal("track one was copied onto the deluxe release")
	}
	if album[1].RecordingID != deluxe[1].RecordingID {
		t.Fatal("track two was copied onto the deluxe release")
	}
	recording, err := store.GetRecording(ctx, first.RecordingIDs["track-one"])
	if err != nil {
		t.Fatal(err)
	}
	if recording.Title != "Track One" || recording.ISRC == nil || *recording.ISRC != "USTST2600001" || recording.DurationMS == nil || *recording.DurationMS != 180000 {
		t.Fatalf("recording = %+v", recording)
	}
	if countWhere(t, `SELECT count(*) FROM recordings WHERE isrc = $1`, "USTST2600001") != 1 {
		t.Fatal("track one isrc was duplicated")
	}
}

func mustArtistImport(t *testing.T, fake *provider.Fake) ArtistImport {
	t.Helper()
	ctx := context.Background()
	artist, err := fake.GetArtist(ctx, "test-artist")
	if err != nil {
		t.Fatal(err)
	}
	summaries, err := fake.GetArtistReleases(ctx, artist.ExternalID)
	if err != nil {
		t.Fatal(err)
	}
	releases := make([]ReleaseImport, 0, len(summaries))
	for _, summary := range summaries {
		details, err := fake.GetReleaseDetails(ctx, summary.ExternalID)
		if err != nil {
			t.Fatal(err)
		}
		tracks := make([]TrackImport, 0, len(details.Tracks))
		for _, track := range details.Tracks {
			tracks = append(tracks, TrackImport{
				ExternalID:    track.ExternalID,
				Title:         track.Title,
				ISRC:          track.ISRC,
				DurationMS:    track.DurationMS,
				DiscNumber:    track.DiscNumber,
				TrackNumber:   track.TrackNumber,
				TitleOverride: track.TitleOverride,
				Artists:       creditsOf(track.Artists),
			})
		}
		releases = append(releases, ReleaseImport{
			ExternalID:  details.ExternalID,
			Title:       details.Title,
			ReleaseType: details.ReleaseType,
			ReleaseDate: details.ReleaseDate,
			Artists:     creditsOf(details.Artists),
			Tracks:      tracks,
		})
	}
	return ArtistImport{
		Provider:    provider.ProviderFake,
		ExternalID:  artist.ExternalID,
		Name:        artist.Name,
		Description: artist.Description,
		Releases:    releases,
	}
}

func creditsOf(credits []provider.Credit) []Credit {
	out := make([]Credit, len(credits))
	for i, credit := range credits {
		out[i] = Credit{ExternalID: credit.ExternalID, Name: credit.Name, Role: credit.Role}
	}
	return out
}

func removeDemoCatalog(ctx context.Context) error {
	releaseIDs, err := demoIDs(ctx, `SELECT release_id FROM release_external_ids WHERE provider = 'fake' AND external_id IN ('first-album', 'first-album-deluxe')`)
	if err != nil {
		return err
	}
	recordingIDs, err := demoIDs(ctx, `SELECT recording_id FROM recording_external_ids WHERE provider = 'fake' AND external_id IN ('track-one', 'track-two', 'track-three')`)
	if err != nil {
		return err
	}
	artistIDs, err := demoIDs(ctx, `SELECT artist_id FROM artist_external_ids WHERE provider = 'fake' AND external_id = 'test-artist'`)
	if err != nil {
		return err
	}
	statements := []struct {
		query string
		args  []any
	}{
		{`DELETE FROM release_tracks WHERE release_id::text = ANY($1) OR recording_id::text = ANY($2)`, []any{releaseIDs, recordingIDs}},
		{`DELETE FROM release_artists WHERE release_id::text = ANY($1)`, []any{releaseIDs}},
		{`DELETE FROM recording_artists WHERE recording_id::text = ANY($1)`, []any{recordingIDs}},
		{`DELETE FROM release_external_ids WHERE release_id::text = ANY($1)`, []any{releaseIDs}},
		{`DELETE FROM recording_external_ids WHERE recording_id::text = ANY($1)`, []any{recordingIDs}},
		{`DELETE FROM artist_external_ids WHERE artist_id::text = ANY($1)`, []any{artistIDs}},
		{`DELETE FROM releases WHERE id::text = ANY($1)`, []any{releaseIDs}},
		{`DELETE FROM recordings WHERE id::text = ANY($1)`, []any{recordingIDs}},
		{`DELETE FROM artists WHERE id::text = ANY($1)`, []any{artistIDs}},
	}
	for _, statement := range statements {
		if _, err := testPool.Exec(ctx, statement.query, statement.args...); err != nil {
			return err
		}
	}
	return nil
}

func demoIDs(ctx context.Context, query string) ([]string, error) {
	rows, err := testPool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
