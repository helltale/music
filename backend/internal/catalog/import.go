package catalog

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helltale/music/backend/internal/platform"
)

// InvalidInputError is a permanent catalog contract failure.
type InvalidInputError struct {
	Reason string
}

func (e *InvalidInputError) Error() string {
	return "invalid catalog input: " + e.Reason
}

// Credit is an artist role on a recording or a release.
type Credit struct {
	ExternalID string
	Name       string
	Role       string
}

// TrackImport is one recording placed on a release.
// ExternalID identifies the recording and does not change between releases.
type TrackImport struct {
	ExternalID    string
	Title         string
	ISRC          string
	DurationMS    *int
	DiscNumber    int
	TrackNumber   int
	TitleOverride string
	Artists       []Credit
}

// ReleaseImport is one release in an artist import snapshot.
type ReleaseImport struct {
	ExternalID  string
	Title       string
	ReleaseType string
	ReleaseDate *time.Time
	Artists     []Credit
	Tracks      []TrackImport
}

// ArtistImport is the catalog snapshot for one provider artist.
// The provider is read before this service runs.
type ArtistImport struct {
	Provider    string
	ExternalID  string
	Name        string
	Description *string
	Releases    []ReleaseImport
}

// AmbiguousISRC reports recordings that shared an ISRC and were not merged.
type AmbiguousISRC struct {
	ExternalID   string
	ISRC         string
	RecordingIDs []string
}

// ImportResult is the catalog state after one artist import.
type ImportResult struct {
	ArtistID     string
	RecordingIDs map[string]string
	ReleaseIDs   map[string]string
	Ambiguous    []AmbiguousISRC
}

type recordingDraft struct {
	ExternalID string
	Title      string
	ISRC       *string
	DurationMS *int
	Artists    []Credit
}

// Importer writes a catalog snapshot in short transactions.
// Order is fixed: the artist, then each recording, then each release.
type Importer struct {
	begin interface {
		Begin(context.Context) (pgx.Tx, error)
	}
}

// NewImporter uses a PostgreSQL pool.
func NewImporter(begin interface {
	Begin(context.Context) (pgx.Tx, error)
}) *Importer {
	return &Importer{begin: begin}
}

// ImportArtist creates missing catalog rows and updates metadata of matched ones.
// A unique conflict rolls the current aggregate back and retries once against the committed winner.
func (im *Importer) ImportArtist(ctx context.Context, in ArtistImport) (ImportResult, error) {
	cleaned, recordings, err := prepareImport(in)
	if err != nil {
		return ImportResult{}, err
	}

	var artistID string
	if err := im.withTx(ctx, func(store *Store) error {
		id, err := writeArtist(ctx, store, cleaned)
		artistID = id
		return err
	}); err != nil {
		return ImportResult{}, err
	}

	result := ImportResult{
		ArtistID:     artistID,
		RecordingIDs: map[string]string{},
		ReleaseIDs:   map[string]string{},
	}
	for _, recording := range recordings {
		var written recordingWrite
		if err := im.withTx(ctx, func(store *Store) error {
			row, err := writeRecording(ctx, store, cleaned.Provider, recording)
			written = row
			return err
		}); err != nil {
			return ImportResult{}, err
		}
		result.RecordingIDs[recording.ExternalID] = written.ID
		if len(written.AmbiguousIDs) > 0 {
			isrc := ""
			if recording.ISRC != nil {
				isrc = *recording.ISRC
			}
			result.Ambiguous = append(result.Ambiguous, AmbiguousISRC{
				ExternalID:   recording.ExternalID,
				ISRC:         isrc,
				RecordingIDs: written.AmbiguousIDs,
			})
		}
	}
	for _, release := range cleaned.Releases {
		var releaseID string
		if err := im.withTx(ctx, func(store *Store) error {
			id, err := writeRelease(ctx, store, cleaned.Provider, release, result.RecordingIDs)
			releaseID = id
			return err
		}); err != nil {
			return ImportResult{}, err
		}
		result.ReleaseIDs[release.ExternalID] = releaseID
	}
	return result, nil
}

func (im *Importer) withTx(ctx context.Context, fn func(*Store) error) error {
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		err = im.once(ctx, fn)
		if err == nil || !uniqueViolation(err) {
			return err
		}
	}
	return err
}

func (im *Importer) once(ctx context.Context, fn func(*Store) error) error {
	tx, err := im.begin.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin catalog transaction: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := fn(NewStore(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit catalog transaction: %w", err)
	}
	return nil
}

func writeArtist(ctx context.Context, store *Store, in ArtistImport) (string, error) {
	id, err := NewResolver(store).ResolveArtist(ctx, in.Provider, in.ExternalID)
	if errors.Is(err, ErrNotFound) {
		id, err = platform.NewUUID()
		if err != nil {
			return "", err
		}
		if _, err := store.CreateArtist(ctx, Artist{ID: id, Name: in.Name, Description: in.Description}); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	} else if err := store.UpdateArtist(ctx, Artist{ID: id, Name: in.Name, Description: in.Description}); err != nil {
		return "", err
	}
	if err := store.BindArtistExternalID(ctx, ExternalID{EntityID: id, Provider: in.Provider, ExternalID: in.ExternalID}); err != nil {
		return "", err
	}
	return id, nil
}

type recordingWrite struct {
	ID           string
	AmbiguousIDs []string
}

func writeRecording(ctx context.Context, store *Store, provider string, recording recordingDraft) (recordingWrite, error) {
	var primaries []string
	for _, artist := range recording.Artists {
		if artist.Role == RolePrimary {
			primaries = append(primaries, artist.Name)
		}
	}
	decision, err := NewResolver(store).ResolveRecording(ctx, RecordingQuery{
		Provider:           provider,
		ExternalID:         recording.ExternalID,
		Title:              recording.Title,
		ISRC:               recording.ISRC,
		DurationMS:         recording.DurationMS,
		PrimaryArtistNames: primaries,
	})
	if err != nil {
		return recordingWrite{}, err
	}

	id := decision.ID
	row := Recording{ID: id, Title: recording.Title, ISRC: recording.ISRC, DurationMS: recording.DurationMS}
	if decision.Create {
		id, err = platform.NewUUID()
		if err != nil {
			return recordingWrite{}, err
		}
		row.ID = id
		if _, err := store.CreateRecording(ctx, row); err != nil {
			return recordingWrite{}, err
		}
	} else if err := store.UpdateRecording(ctx, row); err != nil {
		return recordingWrite{}, err
	}
	if err := store.BindRecordingExternalID(ctx, ExternalID{EntityID: id, Provider: provider, ExternalID: recording.ExternalID}); err != nil {
		return recordingWrite{}, err
	}

	roles := make([]ArtistRole, 0, len(recording.Artists))
	for _, credit := range recording.Artists {
		artistID, err := ensureArtist(ctx, store, provider, credit)
		if err != nil {
			return recordingWrite{}, err
		}
		roles = append(roles, ArtistRole{ArtistID: artistID, Role: credit.Role})
	}
	if err := store.ReplaceRecordingArtists(ctx, id, roles); err != nil {
		return recordingWrite{}, err
	}
	return recordingWrite{ID: id, AmbiguousIDs: decision.AmbiguousIDs}, nil
}

func writeRelease(ctx context.Context, store *Store, provider string, release ReleaseImport, recordingIDs map[string]string) (string, error) {
	id, err := NewResolver(store).ResolveRelease(ctx, provider, release.ExternalID)
	row := Release{
		ID:          id,
		Title:       release.Title,
		ReleaseType: release.ReleaseType,
		ReleaseDate: release.ReleaseDate,
	}
	if errors.Is(err, ErrNotFound) {
		id, err = platform.NewUUID()
		if err != nil {
			return "", err
		}
		row.ID = id
		if _, err := store.CreateRelease(ctx, row); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	} else if err := store.UpdateRelease(ctx, row); err != nil {
		return "", err
	}
	if err := store.BindReleaseExternalID(ctx, ExternalID{EntityID: id, Provider: provider, ExternalID: release.ExternalID}); err != nil {
		return "", err
	}

	roles := make([]ArtistRole, 0, len(release.Artists))
	for _, credit := range release.Artists {
		artistID, err := ensureArtist(ctx, store, provider, credit)
		if err != nil {
			return "", err
		}
		roles = append(roles, ArtistRole{ArtistID: artistID, Role: credit.Role})
	}
	if err := store.ReplaceReleaseArtists(ctx, id, roles); err != nil {
		return "", err
	}

	tracks := make([]ReleaseTrack, 0, len(release.Tracks))
	for _, track := range release.Tracks {
		recordingID, ok := recordingIDs[track.ExternalID]
		if !ok {
			return "", fmt.Errorf("recording %s was not imported", track.ExternalID)
		}
		tracks = append(tracks, ReleaseTrack{
			RecordingID:   recordingID,
			DiscNumber:    track.DiscNumber,
			TrackNumber:   track.TrackNumber,
			TitleOverride: optionalText(track.TitleOverride),
		})
	}
	if err := store.ReplaceReleaseTracks(ctx, id, tracks); err != nil {
		return "", err
	}
	return id, nil
}

func ensureArtist(ctx context.Context, store *Store, provider string, credit Credit) (string, error) {
	id, err := NewResolver(store).ResolveArtist(ctx, provider, credit.ExternalID)
	if errors.Is(err, ErrNotFound) {
		id, err = platform.NewUUID()
		if err != nil {
			return "", err
		}
		if _, err := store.CreateArtist(ctx, Artist{ID: id, Name: credit.Name}); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	} else if err := store.UpdateArtistName(ctx, id, credit.Name); err != nil {
		return "", err
	}
	if err := store.BindArtistExternalID(ctx, ExternalID{EntityID: id, Provider: provider, ExternalID: credit.ExternalID}); err != nil {
		return "", err
	}
	return id, nil
}

func prepareImport(in ArtistImport) (ArtistImport, []recordingDraft, error) {
	in.Provider = strings.TrimSpace(in.Provider)
	in.ExternalID = strings.TrimSpace(in.ExternalID)
	in.Name = strings.TrimSpace(in.Name)
	if in.Provider == "" || in.ExternalID == "" || in.Name == "" {
		return ArtistImport{}, nil, &InvalidInputError{Reason: "artist"}
	}
	if in.Description != nil {
		description := strings.TrimSpace(*in.Description)
		if description == "" {
			return ArtistImport{}, nil, &InvalidInputError{Reason: "artist description"}
		}
		in.Description = &description
	}

	seenArtists := map[string]string{in.ExternalID: in.Name}
	recordings := map[string]recordingDraft{}
	var order []recordingDraft
	for i := range in.Releases {
		release := &in.Releases[i]
		release.ExternalID = strings.TrimSpace(release.ExternalID)
		release.Title = strings.TrimSpace(release.Title)
		release.ReleaseType = strings.TrimSpace(release.ReleaseType)
		if release.ExternalID == "" || release.Title == "" || !validReleaseType(release.ReleaseType) {
			return ArtistImport{}, nil, &InvalidInputError{Reason: "release"}
		}
		if err := cleanCredits(&release.Artists, seenArtists); err != nil {
			return ArtistImport{}, nil, err
		}
		if !hasPrimary(release.Artists) {
			return ArtistImport{}, nil, &InvalidInputError{Reason: "release primary artist"}
		}
		positions := map[[2]int]struct{}{}
		onRelease := map[string]struct{}{}
		for j := range release.Tracks {
			track := &release.Tracks[j]
			if err := cleanTrack(track, seenArtists); err != nil {
				return ArtistImport{}, nil, err
			}
			position := [2]int{track.DiscNumber, track.TrackNumber}
			if _, ok := positions[position]; ok {
				return ArtistImport{}, nil, &InvalidInputError{Reason: "track position"}
			}
			positions[position] = struct{}{}
			if _, ok := onRelease[track.ExternalID]; ok {
				return ArtistImport{}, nil, &InvalidInputError{Reason: "duplicate recording on release"}
			}
			onRelease[track.ExternalID] = struct{}{}

			draft := recordingDraft{
				ExternalID: track.ExternalID,
				Title:      track.Title,
				DurationMS: track.DurationMS,
				Artists:    track.Artists,
			}
			if track.ISRC != "" {
				isrc := track.ISRC
				draft.ISRC = &isrc
			}
			previous, ok := recordings[track.ExternalID]
			if !ok {
				recordings[track.ExternalID] = draft
				order = append(order, draft)
				continue
			}
			if !sameRecording(previous, draft) {
				return ArtistImport{}, nil, &InvalidInputError{Reason: "recording identity conflict"}
			}
		}
	}
	return in, order, nil
}

func cleanTrack(track *TrackImport, artists map[string]string) error {
	track.ExternalID = strings.TrimSpace(track.ExternalID)
	track.Title = strings.TrimSpace(track.Title)
	track.TitleOverride = strings.TrimSpace(track.TitleOverride)
	if track.ExternalID == "" {
		return &InvalidInputError{Reason: "recording external id"}
	}
	if track.Title == "" || track.DiscNumber < 1 || track.TrackNumber < 1 {
		return &InvalidInputError{Reason: "track"}
	}
	isrc, err := normalizeISRC(track.ISRC)
	if err != nil {
		return err
	}
	track.ISRC = isrc
	if track.DurationMS != nil && *track.DurationMS < 0 {
		return &InvalidInputError{Reason: "duration"}
	}
	if err := cleanCredits(&track.Artists, artists); err != nil {
		return err
	}
	if !hasPrimary(track.Artists) {
		return &InvalidInputError{Reason: "recording primary artist"}
	}
	return nil
}

func cleanCredits(credits *[]Credit, seen map[string]string) error {
	used := map[string]struct{}{}
	for i := range *credits {
		credit := &(*credits)[i]
		credit.ExternalID = strings.TrimSpace(credit.ExternalID)
		credit.Name = strings.TrimSpace(credit.Name)
		credit.Role = strings.TrimSpace(credit.Role)
		if credit.ExternalID == "" || credit.Name == "" || !validRole(credit.Role) {
			return &InvalidInputError{Reason: "credit"}
		}
		key := credit.ExternalID + "\x00" + credit.Role
		if _, ok := used[key]; ok {
			return &InvalidInputError{Reason: "duplicate credit"}
		}
		used[key] = struct{}{}
		if previous, ok := seen[credit.ExternalID]; ok && previous != credit.Name {
			return &InvalidInputError{Reason: "artist name conflict"}
		}
		seen[credit.ExternalID] = credit.Name
	}
	return nil
}

func hasPrimary(credits []Credit) bool {
	for _, credit := range credits {
		if credit.Role == RolePrimary {
			return true
		}
	}
	return false
}

func validRole(role string) bool {
	return role == RolePrimary || role == RoleFeatured
}

func validReleaseType(value string) bool {
	switch value {
	case ReleaseAlbum, ReleaseSingle, ReleaseEP, ReleaseCompilation:
		return true
	default:
		return false
	}
}

func sameRecording(left, right recordingDraft) bool {
	if left.Title != right.Title || !sameISRC(left.ISRC, right.ISRC) || !sameDuration(left.DurationMS, right.DurationMS) {
		return false
	}
	return sameCredits(left.Artists, right.Artists)
}

func sameISRC(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func sameDuration(left, right *int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func sameCredits(left, right []Credit) bool {
	if len(left) != len(right) {
		return false
	}
	used := make([]bool, len(right))
	for _, credit := range left {
		found := false
		for i, other := range right {
			if used[i] || credit.ExternalID != other.ExternalID || credit.Role != other.Role || credit.Name != other.Name {
				continue
			}
			used[i] = true
			found = true
			break
		}
		if !found {
			return false
		}
	}
	return true
}

func optionalText(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
