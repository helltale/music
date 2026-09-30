package catalog

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestImportRecordingOnTwoReleases(t *testing.T) {
	ctx := context.Background()
	provider := "catalog-import"
	artistExternalID := newExternalID(t)
	recordingExternalID := newExternalID(t)
	featuredExternalID := newExternalID(t)
	firstReleaseID := newExternalID(t)
	secondReleaseID := newExternalID(t)
	isrc := uniqueISRC(t)
	duration := 180000
	artistName := "Harbor " + artistExternalID

	result := importArtist(t, ArtistImport{
		Provider:   provider,
		ExternalID: artistExternalID,
		Name:       artistName,
		Releases: []ReleaseImport{
			releaseInput(firstReleaseID, "Meteora", artistExternalID, artistName, trackInput(recordingExternalID, "Numb", hyphenateISRC(isrc), &duration, artistExternalID, artistName, featuredExternalID)),
			releaseInput(secondReleaseID, "Meteora Anniversary", artistExternalID, artistName, trackInput(recordingExternalID, "Numb", isrc, &duration, artistExternalID, artistName, featuredExternalID)),
		},
	})

	if len(result.RecordingIDs) != 1 || len(result.ReleaseIDs) != 2 {
		t.Fatalf("result = %+v", result)
	}
	recordingID := result.RecordingIDs[recordingExternalID]
	store := NewStore(testPool)
	first, err := store.ListReleaseTracks(ctx, result.ReleaseIDs[firstReleaseID])
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.ListReleaseTracks(ctx, result.ReleaseIDs[secondReleaseID])
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || len(second) != 1 || first[0].RecordingID != recordingID || second[0].RecordingID != recordingID {
		t.Fatalf("tracks = %+v %+v", first, second)
	}
	recording, err := store.GetRecording(ctx, recordingID)
	if err != nil {
		t.Fatal(err)
	}
	if recording.ISRC == nil || *recording.ISRC != isrc {
		t.Fatalf("isrc = %v", recording.ISRC)
	}
	roles, err := store.ListRecordingArtists(ctx, recordingID)
	if err != nil {
		t.Fatal(err)
	}
	if len(roles) != 2 {
		t.Fatalf("roles = %+v", roles)
	}
	if countWhere(t, `SELECT count(*) FROM recordings WHERE isrc = $1`, isrc) != 1 {
		t.Fatal("recording was duplicated")
	}
}

func TestImportRepeatKeepsIdentity(t *testing.T) {
	ctx := context.Background()
	provider := "catalog-import"
	artistExternalID := newExternalID(t)
	recordingExternalID := newExternalID(t)
	releaseExternalID := newExternalID(t)
	duration := 200000
	name := "Harbor " + artistExternalID
	description := "first"
	snapshot := ArtistImport{
		Provider:    provider,
		ExternalID:  artistExternalID,
		Name:        name,
		Description: &description,
		Releases: []ReleaseImport{
			releaseInput(releaseExternalID, "First Title", artistExternalID, name, trackInput(recordingExternalID, "First Song", "", &duration, artistExternalID, name, "")),
		},
	}
	first := importArtist(t, snapshot)
	syncedBefore := externalSyncedAt(t, "artist_external_ids", "artist_id", first.ArtistID, provider)

	description = "second"
	snapshot.Description = &description
	snapshot.Releases[0].Title = "Second Title"
	snapshot.Releases[0].Tracks[0].Title = "Second Song"
	second := importArtist(t, snapshot)

	if second.ArtistID != first.ArtistID ||
		second.RecordingIDs[recordingExternalID] != first.RecordingIDs[recordingExternalID] ||
		second.ReleaseIDs[releaseExternalID] != first.ReleaseIDs[releaseExternalID] {
		t.Fatalf("ids changed: %+v -> %+v", first, second)
	}
	store := NewStore(testPool)
	artist, err := store.GetArtist(ctx, first.ArtistID)
	if err != nil {
		t.Fatal(err)
	}
	if artist.Description == nil || *artist.Description != "second" {
		t.Fatalf("description = %v", artist.Description)
	}
	recording, err := store.GetRecording(ctx, first.RecordingIDs[recordingExternalID])
	if err != nil {
		t.Fatal(err)
	}
	if recording.Title != "Second Song" {
		t.Fatalf("title = %s", recording.Title)
	}
	release, err := store.GetRelease(ctx, first.ReleaseIDs[releaseExternalID])
	if err != nil {
		t.Fatal(err)
	}
	if release.Title != "Second Title" {
		t.Fatalf("release = %s", release.Title)
	}
	syncedAfter := externalSyncedAt(t, "artist_external_ids", "artist_id", first.ArtistID, provider)
	if syncedAfter.Before(syncedBefore) {
		t.Fatal("last_synced_at moved backwards")
	}
	if countWhere(t, `SELECT count(*) FROM artist_external_ids WHERE provider = $1 AND external_id = $2`, provider, artistExternalID) != 1 {
		t.Fatal("artist external id duplicated")
	}
}

func TestImportExternalIDWinsOverISRC(t *testing.T) {
	provider := "catalog-import"
	artistExternalID := newExternalID(t)
	recordingExternalID := newExternalID(t)
	releaseExternalID := newExternalID(t)
	name := "Harbor " + artistExternalID
	duration := 150000
	isrc := uniqueISRC(t)
	seeded := seedRecording(t, "Other Song "+isrc, "Other Artist "+isrc, &isrc, &duration)

	first := importArtist(t, ArtistImport{
		Provider:   provider,
		ExternalID: artistExternalID,
		Name:       name,
		Releases: []ReleaseImport{
			releaseInput(releaseExternalID, "Album", artistExternalID, name, trackInput(recordingExternalID, "Owned Song", "", &duration, artistExternalID, name, "")),
		},
	})
	again := importArtist(t, ArtistImport{
		Provider:   provider,
		ExternalID: artistExternalID,
		Name:       name,
		Releases: []ReleaseImport{
			releaseInput(releaseExternalID, "Album", artistExternalID, name, trackInput(recordingExternalID, "Owned Song", isrc, &duration, artistExternalID, name, "")),
		},
	})
	if again.RecordingIDs[recordingExternalID] != first.RecordingIDs[recordingExternalID] {
		t.Fatal("external id match created another recording")
	}
	if again.RecordingIDs[recordingExternalID] == seeded {
		t.Fatal("external id match followed the isrc")
	}
	if countWhere(t, `SELECT count(*) FROM recordings WHERE isrc = $1`, isrc) != 2 {
		t.Fatal("isrc was merged across different external ids")
	}
}

func TestImportMatchesSingleISRC(t *testing.T) {
	provider := "catalog-import"
	isrc := uniqueISRC(t)
	duration := 10000
	seeded := seedRecording(t, "Original "+isrc, "Someone "+isrc, &isrc, &duration)
	artistExternalID := newExternalID(t)
	recordingExternalID := newExternalID(t)
	releaseExternalID := newExternalID(t)
	name := "Harbor " + artistExternalID
	incomingDuration := 12000

	result := importArtist(t, ArtistImport{
		Provider:   provider,
		ExternalID: artistExternalID,
		Name:       name,
		Releases: []ReleaseImport{
			releaseInput(releaseExternalID, "Album", artistExternalID, name, trackInput(recordingExternalID, "Different Title "+isrc, isrc, &incomingDuration, artistExternalID, name, "")),
		},
	})
	if result.RecordingIDs[recordingExternalID] != seeded {
		t.Fatalf("matched %s, seeded %s", result.RecordingIDs[recordingExternalID], seeded)
	}
	if countWhere(t, `SELECT count(*) FROM recordings WHERE isrc = $1`, isrc) != 1 {
		t.Fatal("isrc match duplicated the recording")
	}
	recording, err := NewStore(testPool).GetRecording(context.Background(), seeded)
	if err != nil {
		t.Fatal(err)
	}
	if recording.Title != "Different Title "+isrc || recording.DurationMS == nil || *recording.DurationMS != incomingDuration {
		t.Fatalf("recording = %+v", recording)
	}
}

func TestImportAmbiguousISRCCreatesRecording(t *testing.T) {
	provider := "catalog-import"
	isrc := uniqueISRC(t)
	duration := 10000
	first := seedRecording(t, "One "+isrc, "Someone "+isrc, &isrc, &duration)
	second := seedRecording(t, "Two "+isrc, "Someone "+isrc, &isrc, &duration)
	artistExternalID := newExternalID(t)
	recordingExternalID := newExternalID(t)
	releaseExternalID := newExternalID(t)
	name := "Harbor " + artistExternalID

	result := importArtist(t, ArtistImport{
		Provider:   provider,
		ExternalID: artistExternalID,
		Name:       name,
		Releases: []ReleaseImport{
			releaseInput(releaseExternalID, "Album", artistExternalID, name, trackInput(recordingExternalID, "Three "+isrc, isrc, &duration, artistExternalID, name, "")),
		},
	})
	created := result.RecordingIDs[recordingExternalID]
	if created == first || created == second {
		t.Fatal("ambiguous isrc was merged")
	}
	if len(result.Ambiguous) != 1 || !containsAll(result.Ambiguous[0].RecordingIDs, first, second) {
		t.Fatalf("ambiguous = %+v", result.Ambiguous)
	}
	if countWhere(t, `SELECT count(*) FROM recordings WHERE isrc = $1`, isrc) != 3 {
		t.Fatal("expected a third recording")
	}
}

func TestImportFuzzyMatch(t *testing.T) {
	provider := "catalog-import"
	suffix := newExternalID(t)
	title := "Signal " + suffix
	artistName := "Harbor " + suffix
	duration := 10000
	seeded := seedRecording(t, title, artistName, nil, &duration)
	artistExternalID := newExternalID(t)
	recordingExternalID := newExternalID(t)
	releaseExternalID := newExternalID(t)
	incomingDuration := 11500

	result := importArtist(t, ArtistImport{
		Provider:   provider,
		ExternalID: artistExternalID,
		Name:       "  " + artistName + " ",
		Releases: []ReleaseImport{
			releaseInput(releaseExternalID, "Album "+suffix, artistExternalID, artistName, trackInput(recordingExternalID, "  signal   "+suffix, "", &incomingDuration, artistExternalID, " "+artistName+" ", "")),
		},
	})
	if result.RecordingIDs[recordingExternalID] != seeded {
		t.Fatalf("fuzzy match = %s, seeded %s", result.RecordingIDs[recordingExternalID], seeded)
	}
}

func TestImportFuzzyRejectsDistantDuration(t *testing.T) {
	provider := "catalog-import"
	suffix := newExternalID(t)
	title := "Signal " + suffix
	artistName := "Harbor " + suffix
	duration := 10000
	seeded := seedRecording(t, title, artistName, nil, &duration)
	incoming := 13001
	result := importNamedSong(t, provider, title, artistName, "", &incoming)
	if result == seeded {
		t.Fatal("duration outside tolerance was merged")
	}
	if countWhere(t, `SELECT count(*) FROM recordings WHERE title = $1`, title) != 2 {
		t.Fatal("expected a new recording")
	}
}

func TestImportFuzzyRejectsTwoCandidates(t *testing.T) {
	provider := "catalog-import"
	suffix := newExternalID(t)
	title := "Signal " + suffix
	artistName := "Harbor " + suffix
	firstDuration := 10000
	secondDuration := 11000
	seedRecording(t, title, artistName, nil, &firstDuration)
	seedRecording(t, title, artistName, nil, &secondDuration)
	incoming := 10000
	importNamedSong(t, provider, title, artistName, "", &incoming)
	if countWhere(t, `SELECT count(*) FROM recordings WHERE title = $1`, title) != 3 {
		t.Fatal("two candidates were merged")
	}
}

func TestImportFuzzyRejectsContradictingISRC(t *testing.T) {
	provider := "catalog-import"
	suffix := newExternalID(t)
	title := "Signal " + suffix
	artistName := "Harbor " + suffix
	duration := 10000
	existing := uniqueISRC(t)
	incoming := uniqueISRC(t)
	seeded := seedRecording(t, title, artistName, &existing, &duration)
	created := importNamedSong(t, provider, title, artistName, incoming, &duration)
	if created == seeded {
		t.Fatal("contradicting isrc was merged")
	}
	if countWhere(t, `SELECT count(*) FROM recordings WHERE title = $1`, title) != 2 {
		t.Fatal("expected a new recording")
	}
}

func TestImportDoesNotMergeArtistsByName(t *testing.T) {
	provider := "catalog-import"
	name := "Shared Harbor " + newExternalID(t)
	first := newExternalID(t)
	second := newExternalID(t)
	left := importArtist(t, ArtistImport{Provider: provider, ExternalID: first, Name: name})
	right := importArtist(t, ArtistImport{Provider: provider, ExternalID: second, Name: name})
	if left.ArtistID == right.ArtistID {
		t.Fatal("artists with the same name were merged")
	}
}

func TestImportRejectsMissingRecordingExternalID(t *testing.T) {
	provider := "catalog-import-" + newExternalID(t)
	artistExternalID := newExternalID(t)
	name := "Harbor " + artistExternalID
	duration := 1000
	_, err := NewImporter(testPool).ImportArtist(context.Background(), ArtistImport{
		Provider:   provider,
		ExternalID: artistExternalID,
		Name:       name,
		Releases: []ReleaseImport{
			releaseInput(newExternalID(t), "Album", artistExternalID, name, trackInput("", "Song", "", &duration, artistExternalID, name, "")),
		},
	})
	var invalid *InvalidInputError
	if !errors.As(err, &invalid) || invalid.Reason != "recording external id" {
		t.Fatalf("error = %v", err)
	}
	if countWhere(t, `SELECT count(*) FROM artist_external_ids WHERE provider = $1`, provider) != 0 {
		t.Fatal("invalid import wrote a row")
	}
}

func importArtist(t *testing.T, in ArtistImport) ImportResult {
	t.Helper()
	result, err := NewImporter(testPool).ImportArtist(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func importNamedSong(t *testing.T, provider, title, artistName, isrc string, duration *int) string {
	t.Helper()
	artistExternalID := newExternalID(t)
	recordingExternalID := newExternalID(t)
	result := importArtist(t, ArtistImport{
		Provider:   provider,
		ExternalID: artistExternalID,
		Name:       artistName,
		Releases: []ReleaseImport{
			releaseInput(newExternalID(t), "Album "+recordingExternalID, artistExternalID, artistName, trackInput(recordingExternalID, title, isrc, duration, artistExternalID, artistName, "")),
		},
	})
	return result.RecordingIDs[recordingExternalID]
}

func releaseInput(externalID, title, artistExternalID, artistName string, track TrackImport) ReleaseImport {
	return ReleaseImport{
		ExternalID:  externalID,
		Title:       title,
		ReleaseType: ReleaseAlbum,
		Artists:     []Credit{{ExternalID: artistExternalID, Name: artistName, Role: RolePrimary}},
		Tracks:      []TrackImport{track},
	}
}

func trackInput(externalID, title, isrc string, duration *int, artistExternalID, artistName, featuredExternalID string) TrackImport {
	artists := []Credit{{ExternalID: artistExternalID, Name: artistName, Role: RolePrimary}}
	if featuredExternalID != "" {
		artists = append(artists, Credit{ExternalID: featuredExternalID, Name: "Guest " + featuredExternalID, Role: RoleFeatured})
	}
	return TrackImport{
		ExternalID:  externalID,
		Title:       title,
		ISRC:        isrc,
		DurationMS:  duration,
		DiscNumber:  1,
		TrackNumber: 1,
		Artists:     artists,
	}
}

func seedRecording(t *testing.T, title, artistName string, isrc *string, duration *int) string {
	t.Helper()
	ctx := context.Background()
	store := NewStore(testPool)
	artist, err := store.CreateArtist(ctx, Artist{ID: newID(t), Name: artistName})
	if err != nil {
		t.Fatal(err)
	}
	recording, err := store.CreateRecording(ctx, Recording{ID: newID(t), Title: title, ISRC: isrc, DurationMS: duration})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddRecordingArtist(ctx, recording.ID, artist.ID, RolePrimary); err != nil {
		t.Fatal(err)
	}
	return recording.ID
}

func countWhere(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func externalSyncedAt(t *testing.T, table, idColumn, entityID, provider string) time.Time {
	t.Helper()
	var synced time.Time
	query := fmt.Sprintf(`SELECT last_synced_at FROM %s WHERE %s = $1 AND provider = $2`, table, idColumn)
	if err := testPool.QueryRow(context.Background(), query, entityID, provider).Scan(&synced); err != nil {
		t.Fatal(err)
	}
	return synced
}

func containsAll(ids []string, want ...string) bool {
	for _, id := range want {
		found := false
		for _, got := range ids {
			if got == id {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func newExternalID(t *testing.T) string {
	t.Helper()
	return newID(t)
}

func uniqueISRC(t *testing.T) string {
	t.Helper()
	var raw [6]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatal(err)
	}
	registrant := string([]byte{'A' + raw[0]%26, 'A' + raw[1]%26, '0' + raw[2]%10})
	n := (int(raw[3])<<16 | int(raw[4])<<8 | int(raw[5])) % 10000000
	return fmt.Sprintf("ZZ%s%07d", registrant, n)
}

func hyphenateISRC(isrc string) string {
	return isrc[:2] + "-" + isrc[2:5] + "-" + isrc[5:]
}
