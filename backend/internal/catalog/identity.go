package catalog

import (
	"context"
	"errors"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

const durationToleranceMS = 2000

// RecordingCandidate is one recording the matcher can compare.
type RecordingCandidate struct {
	ID           string
	Title        string
	ISRC         *string
	DurationMS   *int
	PrimaryNames []string
}

// RecordingQuery is the identity input for one recording.
type RecordingQuery struct {
	Provider           string
	ExternalID         string
	Title              string
	ISRC               *string
	DurationMS         *int
	PrimaryArtistNames []string
}

// RecordingDecision is the resolver result.
// Create means the caller inserts a new recording.
// AmbiguousIDs lists recordings that share the incoming ISRC and were not merged.
type RecordingDecision struct {
	ID           string
	Create       bool
	AmbiguousIDs []string
}

// Resolver decides whether a catalog entity already exists.
// Call it inside the transaction that writes the entity.
type Resolver struct {
	store *Store
}

// NewResolver binds the resolver to a store or an open transaction.
func NewResolver(store *Store) *Resolver {
	return &Resolver{store: store}
}

// ResolveArtist returns the artist id for a provider identifier.
func (r *Resolver) ResolveArtist(ctx context.Context, provider, externalID string) (string, error) {
	return r.store.FindArtistByExternalID(ctx, provider, externalID)
}

// ResolveRelease returns the release id for a provider identifier.
func (r *Resolver) ResolveRelease(ctx context.Context, provider, externalID string) (string, error) {
	return r.store.FindReleaseByExternalID(ctx, provider, externalID)
}

// ResolveRecording applies the catalog match order:
// provider external id, a single ISRC, then one normalized primary-artist and title
// match within ±2000 ms whose ISRC does not contradict.
func (r *Resolver) ResolveRecording(ctx context.Context, query RecordingQuery) (RecordingDecision, error) {
	id, err := r.store.FindRecordingByExternalID(ctx, query.Provider, query.ExternalID)
	if err == nil {
		return RecordingDecision{ID: id}, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return RecordingDecision{}, err
	}

	if query.ISRC != nil {
		found, err := r.store.ListRecordingsByISRC(ctx, *query.ISRC)
		if err != nil {
			return RecordingDecision{}, err
		}
		if len(found) == 1 {
			return RecordingDecision{ID: found[0].ID}, nil
		}
		if len(found) > 1 {
			ids := make([]string, len(found))
			for i, recording := range found {
				ids[i] = recording.ID
			}
			return RecordingDecision{Create: true, AmbiguousIDs: ids}, nil
		}
	}

	candidates, err := r.store.ListRecordingCandidates(ctx)
	if err != nil {
		return RecordingDecision{}, err
	}
	title := normalizeName(query.Title)
	primaries := normalizeNames(query.PrimaryArtistNames)
	var matched []string
	for _, candidate := range candidates {
		if normalizeName(candidate.Title) != title {
			continue
		}
		if !durationClose(candidate.DurationMS, query.DurationMS) {
			continue
		}
		if !sameNames(normalizeNames(candidate.PrimaryNames), primaries) {
			continue
		}
		if isrcContradicts(candidate.ISRC, query.ISRC) {
			continue
		}
		matched = append(matched, candidate.ID)
	}
	if len(matched) == 1 {
		return RecordingDecision{ID: matched[0]}, nil
	}
	return RecordingDecision{Create: true}, nil
}

func normalizeName(value string) string {
	value = norm.NFC.String(value)
	value = strings.Join(strings.Fields(value), " ")
	return strings.ToLower(value)
}

func normalizeNames(values []string) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = normalizeName(value)
	}
	return out
}

func sameNames(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	used := make([]bool, len(right))
	for _, name := range left {
		found := false
		for i, other := range right {
			if used[i] || name != other {
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

func durationClose(left, right *int) bool {
	if left == nil || right == nil {
		return false
	}
	delta := *left - *right
	if delta < 0 {
		delta = -delta
	}
	return delta <= durationToleranceMS
}

func isrcContradicts(left, right *string) bool {
	if left == nil || right == nil {
		return false
	}
	return *left != *right
}

func normalizeISRC(value string) (string, error) {
	var b strings.Builder
	for _, r := range strings.TrimSpace(value) {
		if r == '-' || unicode.IsSpace(r) {
			continue
		}
		b.WriteRune(unicode.ToUpper(r))
	}
	isrc := b.String()
	if isrc == "" {
		return "", nil
	}
	if len(isrc) != 12 || !isrcBytes(isrc) {
		return "", &InvalidInputError{Reason: "isrc"}
	}
	return isrc, nil
}

func isrcBytes(isrc string) bool {
	for i := 0; i < 2; i++ {
		if isrc[i] < 'A' || isrc[i] > 'Z' {
			return false
		}
	}
	for i := 2; i < 5; i++ {
		c := isrc[i]
		if (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			return false
		}
	}
	for i := 5; i < 12; i++ {
		if isrc[i] < '0' || isrc[i] > '9' {
			return false
		}
	}
	return true
}
