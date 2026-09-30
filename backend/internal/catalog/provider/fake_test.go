package provider

import (
	"context"
	"errors"
	"testing"
)

func TestFakeCatalogShape(t *testing.T) {
	ctx := context.Background()
	fake := NewFake()

	artist, err := fake.GetArtist(ctx, "test-artist")
	if err != nil {
		t.Fatal(err)
	}
	if artist.Name != "Test Artist" || artist.ExternalID != "test-artist" || artist.Description != nil {
		t.Fatalf("artist = %+v", artist)
	}

	releases, err := fake.GetArtistReleases(ctx, "test-artist")
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 2 || releases[0].ExternalID != "first-album" || releases[1].ExternalID != "first-album-deluxe" {
		t.Fatalf("releases = %+v", releases)
	}

	album, err := fake.GetReleaseDetails(ctx, "first-album")
	if err != nil {
		t.Fatal(err)
	}
	deluxe, err := fake.GetReleaseDetails(ctx, "first-album-deluxe")
	if err != nil {
		t.Fatal(err)
	}
	if len(album.Tracks) != 2 || len(deluxe.Tracks) != 3 {
		t.Fatalf("tracks album=%d deluxe=%d", len(album.Tracks), len(deluxe.Tracks))
	}
	if album.Tracks[0].ExternalID != "track-one" || deluxe.Tracks[0].ExternalID != "track-one" {
		t.Fatal("track one is not the same recording on both releases")
	}
	if album.Tracks[1].ExternalID != "track-two" || deluxe.Tracks[1].ExternalID != "track-two" {
		t.Fatal("track two is not the same recording on both releases")
	}
	if deluxe.Tracks[2].ExternalID != "track-three" {
		t.Fatalf("deluxe tail = %+v", deluxe.Tracks[2])
	}
	if album.Tracks[0].ISRC != "USTST2600001" || album.Tracks[0].DurationMS == nil || *album.Tracks[0].DurationMS != 180000 {
		t.Fatalf("track one = %+v", album.Tracks[0])
	}
	if album.Tracks[1].ISRC != "" || album.Tracks[1].DurationMS == nil || *album.Tracks[1].DurationMS != 200000 {
		t.Fatalf("track two = %+v", album.Tracks[1])
	}
	if deluxe.Tracks[2].ISRC != "" || deluxe.Tracks[2].DurationMS == nil || *deluxe.Tracks[2].DurationMS != 150000 {
		t.Fatalf("track three = %+v", deluxe.Tracks[2])
	}
	for _, release := range []Release{album, deluxe} {
		if len(release.Artists) != 1 || release.Artists[0].ExternalID != "test-artist" || release.Artists[0].Role != "primary" {
			t.Fatalf("release artists = %+v", release.Artists)
		}
		for _, track := range release.Tracks {
			if track.DiscNumber != 1 || len(track.Artists) != 1 || track.Artists[0].Role != "primary" {
				t.Fatalf("track = %+v", track)
			}
		}
	}

	album.Tracks[0].Title = "changed"
	*album.Tracks[0].DurationMS = 1
	again, err := fake.GetReleaseDetails(ctx, "first-album")
	if err != nil {
		t.Fatal(err)
	}
	if again.Tracks[0].Title != "Track One" || *again.Tracks[0].DurationMS != 180000 {
		t.Fatal("caller mutated the fixture")
	}

	if _, err := fake.GetArtist(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing artist = %v", err)
	}
	if _, err := fake.GetReleaseDetails(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing release = %v", err)
	}
	if _, err := fake.GetArtistReleases(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing releases = %v", err)
	}
}

func TestFakeSearch(t *testing.T) {
	ctx := context.Background()
	fake := NewFake()
	items, total, err := fake.SearchArtists(ctx, "  TeSt ", 20)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 1 || items[0].Provider != ProviderFake || items[0].ExternalID != "test-artist" || items[0].Name != "Test Artist" {
		t.Fatalf("items=%+v total=%d", items, total)
	}
	items, total, err = fake.SearchArtists(ctx, "no-such-artist", 20)
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || len(items) != 0 {
		t.Fatalf("empty search items=%+v total=%d", items, total)
	}
	items, total, err = fake.SearchArtists(ctx, "artist", 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 0 {
		t.Fatalf("limited search items=%d total=%d", len(items), total)
	}
}
