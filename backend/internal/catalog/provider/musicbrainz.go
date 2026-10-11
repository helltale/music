package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	ProviderMusicBrainz = "musicbrainz"

	defaultMusicBrainzBaseURL   = "https://musicbrainz.org/ws/2"
	defaultMusicBrainzUserAgent = "music/1.0 ( https://github.com/helltale/music )"
	musicBrainzPageLimit        = 100
	musicBrainzMaxPages         = 100
	musicBrainzBodyLimit        = 20 << 20
	musicBrainzAttempts         = 2
	musicBrainzHTTPTimeout      = 5 * time.Second
)

// ErrPermanent is a provider rejection that must not be retried.
var ErrPermanent = errors.New("permanent provider failure")

// ErrUnusable means one release cannot be mapped onto the catalog contract.
// The import keeps the artist and the releases that did map.
var ErrUnusable = errors.New("unusable provider release")

var mbidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// MusicBrainz reads the public MusicBrainz web service.
// It does not write the local catalog and it does not download audio.
// The client keeps at most one request per second. MusicBrainz requires a
// User-Agent that names the application. There is no API key.
type MusicBrainz struct {
	BaseURL   string
	UserAgent string
	// Interval is the pause between requests. Zero means one second.
	// A negative value disables the pause for tests.
	Interval time.Duration
	// Attempts is how many times a timed-out or rate-limited call is tried.
	// Zero means the default.
	Attempts int
	HTTP     *http.Client

	mu     sync.Mutex
	next   time.Time
	names  map[string]string
	chosen map[string]releaseChoice
}

type releaseChoice struct {
	ReleaseID   string
	Title       string
	ReleaseType string
	ReleaseDate *time.Time
	Artists     []Credit
	EditionDate string
	EditionID   string
}

// NewMusicBrainz returns a client for the public web service.
// Empty baseURL and userAgent use the MusicBrainz defaults.
func NewMusicBrainz(baseURL, userAgent string) *MusicBrainz {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = defaultMusicBrainzBaseURL
	}
	userAgent = strings.TrimSpace(userAgent)
	if userAgent == "" {
		userAgent = defaultMusicBrainzUserAgent
	}
	return &MusicBrainz{
		BaseURL:   baseURL,
		UserAgent: userAgent,
		HTTP:      &http.Client{Timeout: musicBrainzHTTPTimeout},
	}
}

// SearchArtists queries the MusicBrainz artist index.
// The query is sent with dismax so Lucene operators in a name are not syntax.
// offset is not applied: the catalog HTTP API does not pass it through.
func (m *MusicBrainz) SearchArtists(ctx context.Context, query string, limit int) ([]ArtistCandidate, int, error) {
	query = strings.TrimSpace(query)
	if query == "" || limit < 1 {
		return []ArtistCandidate{}, 0, nil
	}
	if limit > musicBrainzPageLimit {
		limit = musicBrainzPageLimit
	}
	values := url.Values{}
	values.Set("query", query)
	values.Set("fmt", "json")
	values.Set("dismax", "true")
	values.Set("limit", fmt.Sprintf("%d", limit))
	var page mbArtistSearch
	if err := m.getJSON(ctx, "artist?"+values.Encode(), &page); err != nil {
		return nil, 0, err
	}
	items := make([]ArtistCandidate, 0, len(page.Artists))
	for _, artist := range page.Artists {
		if !validMBID(artist.ID) || strings.TrimSpace(artist.Name) == "" {
			continue
		}
		items = append(items, ArtistCandidate{
			Provider:   ProviderMusicBrainz,
			ExternalID: strings.ToLower(artist.ID),
			Name:       strings.TrimSpace(artist.Name),
		})
		if len(items) == limit {
			break
		}
	}
	return items, page.Count, nil
}

// GetArtist loads one artist. The description is the disambiguation comment.
func (m *MusicBrainz) GetArtist(ctx context.Context, externalID string) (Artist, error) {
	if !validMBID(externalID) {
		return Artist{}, ErrNotFound
	}
	var raw mbArtist
	if err := m.getJSON(ctx, "artist/"+url.PathEscape(externalID)+"?fmt=json", &raw); err != nil {
		return Artist{}, err
	}
	name := strings.TrimSpace(raw.Name)
	if !validMBID(raw.ID) || name == "" {
		return Artist{}, ErrNotFound
	}
	id := strings.ToLower(raw.ID)
	m.rememberName(id, name)
	artist := Artist{ExternalID: id, Name: name}
	if comment := strings.TrimSpace(raw.Disambiguation); comment != "" {
		artist.Description = &comment
	}
	return artist, nil
}

// GetArtistReleases lists one official edition of each album, single, EP, and compilation.
// The external id is the release-group MBID, so later editions of the same album stay one release.
func (m *MusicBrainz) GetArtistReleases(ctx context.Context, externalID string) ([]ReleaseSummary, error) {
	if !validMBID(externalID) {
		return nil, ErrNotFound
	}
	artistID := strings.ToLower(externalID)
	choices := map[string]releaseChoice{}
	offset := 0
	truncated := false
	for page := 0; page < musicBrainzMaxPages; page++ {
		values := url.Values{}
		values.Set("artist", artistID)
		values.Set("status", "official")
		values.Set("inc", "release-groups+artist-credits")
		values.Set("fmt", "json")
		values.Set("limit", fmt.Sprintf("%d", musicBrainzPageLimit))
		values.Set("offset", fmt.Sprintf("%d", offset))
		var raw mbReleaseBrowse
		if err := m.getJSON(ctx, "release?"+values.Encode(), &raw); err != nil {
			return nil, err
		}
		for _, release := range raw.Releases {
			choice, ok := choiceFromEdition(release, artistID, m.artistName(artistID))
			if !ok {
				continue
			}
			current, seen := choices[choice.groupID]
			if !seen || betterEdition(choice.choice, current) {
				choices[choice.groupID] = choice.choice
			}
		}
		offset += len(raw.Releases)
		if len(raw.Releases) == 0 || offset >= raw.Count {
			break
		}
		if page == musicBrainzMaxPages-1 {
			truncated = true
		}
	}
	if truncated {
		return nil, fmt.Errorf("musicbrainz release list exceeded %d pages: %w", musicBrainzMaxPages, ErrPermanent)
	}
	summaries := make([]ReleaseSummary, 0, len(choices))
	for groupID, choice := range choices {
		m.rememberChoice(groupID, choice)
		date := choice.ReleaseDate
		summaries = append(summaries, ReleaseSummary{
			ExternalID:  groupID,
			Title:       choice.Title,
			ReleaseType: choice.ReleaseType,
			ReleaseDate: date,
		})
	}
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].Title == summaries[j].Title {
			return summaries[i].ExternalID < summaries[j].ExternalID
		}
		return summaries[i].Title < summaries[j].Title
	})
	return summaries, nil
}

// GetReleaseDetails loads the track list of the edition chosen for a release group.
func (m *MusicBrainz) GetReleaseDetails(ctx context.Context, externalID string) (Release, error) {
	if !validMBID(externalID) {
		return Release{}, ErrNotFound
	}
	groupID := strings.ToLower(externalID)
	choice, ok := m.choiceFor(groupID)
	if !ok {
		var err error
		choice, err = m.chooseRelease(ctx, groupID)
		if err != nil {
			return Release{}, err
		}
	}
	var raw mbRelease
	path := "release/" + url.PathEscape(choice.ReleaseID) + "?inc=recordings+artist-credits+isrcs&fmt=json"
	if err := m.getJSON(ctx, path, &raw); err != nil {
		if errors.Is(err, ErrNotFound) {
			return Release{}, ErrUnusable
		}
		return Release{}, err
	}
	tracks := mapTracks(raw.Media)
	if len(tracks) == 0 || !hasPrimary(choice.Artists) {
		return Release{}, ErrUnusable
	}
	return Release{
		ExternalID:  groupID,
		Title:       choice.Title,
		ReleaseType: choice.ReleaseType,
		ReleaseDate: choice.ReleaseDate,
		Artists:     choice.Artists,
		Tracks:      tracks,
	}, nil
}

func (m *MusicBrainz) chooseRelease(ctx context.Context, groupID string) (releaseChoice, error) {
	var group mbGroupLookup
	path := "release-group/" + url.PathEscape(groupID) + "?inc=artists+releases+artist-credits&fmt=json"
	if err := m.getJSON(ctx, path, &group); err != nil {
		return releaseChoice{}, err
	}
	var best releaseChoice
	found := false
	for _, release := range group.Releases {
		release.ReleaseGroup = &group.mbGroup
		choice, ok := choiceFromEdition(release, "", "")
		if !ok {
			continue
		}
		if !found || betterEdition(choice.choice, best) {
			best = choice.choice
			found = true
		}
	}
	if !found {
		return releaseChoice{}, ErrUnusable
	}
	m.rememberChoice(groupID, best)
	return best, nil
}

type builtChoice struct {
	groupID string
	choice  releaseChoice
}

func choiceFromEdition(release mbRelease, subjectID, subjectName string) (builtChoice, bool) {
	if release.ReleaseGroup == nil || !validMBID(release.ReleaseGroup.ID) || !validMBID(release.ID) {
		return builtChoice{}, false
	}
	if !strings.EqualFold(strings.TrimSpace(release.Status), "official") {
		return builtChoice{}, false
	}
	releaseType := mapReleaseType(release.ReleaseGroup.PrimaryType, release.ReleaseGroup.SecondaryTypes)
	title := strings.TrimSpace(release.ReleaseGroup.Title)
	if releaseType == "" || title == "" {
		return builtChoice{}, false
	}
	credits := mapCredits(release.ReleaseGroup.ArtistCredit)
	if len(credits) == 0 {
		credits = mapCredits(release.ArtistCredit)
	}
	credits = ensureSubject(credits, subjectID, subjectName)
	if !hasPrimary(credits) {
		return builtChoice{}, false
	}
	return builtChoice{
		groupID: strings.ToLower(release.ReleaseGroup.ID),
		choice: releaseChoice{
			ReleaseID:   strings.ToLower(release.ID),
			Title:       title,
			ReleaseType: releaseType,
			ReleaseDate: fullDate(release.ReleaseGroup.FirstReleaseDate),
			Artists:     credits,
			EditionDate: strings.TrimSpace(release.Date),
			EditionID:   strings.ToLower(release.ID),
		},
	}, true
}

func betterEdition(next, current releaseChoice) bool {
	nextFull := fullDate(next.EditionDate) != nil
	currentFull := fullDate(current.EditionDate) != nil
	if nextFull != currentFull {
		return nextFull
	}
	if next.EditionDate != current.EditionDate {
		if next.EditionDate == "" {
			return false
		}
		if current.EditionDate == "" {
			return true
		}
		return next.EditionDate < current.EditionDate
	}
	return next.EditionID < current.EditionID
}

func mapReleaseType(primary string, secondary []string) string {
	for _, value := range secondary {
		if strings.EqualFold(value, "Compilation") {
			return "compilation"
		}
	}
	switch strings.ToLower(strings.TrimSpace(primary)) {
	case "album":
		return "album"
	case "single":
		return "single"
	case "ep":
		return "ep"
	default:
		return ""
	}
}

func mapTracks(media []mbMedium) []Track {
	tracks := make([]Track, 0)
	seenPosition := map[[2]int]struct{}{}
	seenRecording := map[string]struct{}{}
	for _, medium := range media {
		if medium.Position < 1 {
			continue
		}
		for _, track := range medium.Tracks {
			recordingID := strings.ToLower(strings.TrimSpace(track.Recording.ID))
			title := strings.TrimSpace(track.Recording.Title)
			if title == "" {
				title = strings.TrimSpace(track.Title)
			}
			if !validMBID(recordingID) || title == "" || track.Position < 1 {
				continue
			}
			position := [2]int{medium.Position, track.Position}
			if _, ok := seenPosition[position]; ok {
				continue
			}
			if _, ok := seenRecording[recordingID]; ok {
				continue
			}
			credits := mapCredits(track.Recording.ArtistCredit)
			if !hasPrimary(credits) {
				continue
			}
			seenPosition[position] = struct{}{}
			seenRecording[recordingID] = struct{}{}
			override := ""
			if trackTitle := strings.TrimSpace(track.Title); trackTitle != "" && trackTitle != title {
				override = trackTitle
			}
			tracks = append(tracks, Track{
				ExternalID:    recordingID,
				Title:         title,
				ISRC:          pickISRC(track.Recording.ISRCs),
				DurationMS:    positiveMS(track.Recording.Length),
				DiscNumber:    medium.Position,
				TrackNumber:   track.Position,
				TitleOverride: override,
				Artists:       credits,
			})
		}
	}
	return tracks
}

func mapCredits(raw []mbNameCredit) []Credit {
	credits := make([]Credit, 0, len(raw))
	featured := false
	seen := map[string]struct{}{}
	for _, credit := range raw {
		name := strings.TrimSpace(credit.Artist.Name)
		if name == "" {
			name = strings.TrimSpace(credit.Name)
		}
		id := strings.ToLower(strings.TrimSpace(credit.Artist.ID))
		if !validMBID(id) || name == "" {
			if marksFeatured(credit.Joinphrase) {
				featured = true
			}
			continue
		}
		role := "primary"
		if featured {
			role = "featured"
		}
		key := id + "\x00" + role
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			credits = append(credits, Credit{ExternalID: id, Name: name, Role: role})
		}
		if marksFeatured(credit.Joinphrase) {
			featured = true
		}
	}
	return credits
}

func marksFeatured(join string) bool {
	folded := strings.ToLower(join)
	return strings.Contains(folded, "feat") || strings.Contains(folded, "ft.")
}

func ensureSubject(credits []Credit, id, name string) []Credit {
	id = strings.ToLower(strings.TrimSpace(id))
	name = strings.TrimSpace(name)
	if !validMBID(id) || name == "" {
		return credits
	}
	for _, credit := range credits {
		if credit.ExternalID == id {
			return credits
		}
	}
	return append(credits, Credit{ExternalID: id, Name: name, Role: "primary"})
}

func hasPrimary(credits []Credit) bool {
	for _, credit := range credits {
		if credit.Role == "primary" {
			return true
		}
	}
	return false
}

func pickISRC(values []string) string {
	valid := make([]string, 0, len(values))
	for _, value := range values {
		isrc := normalizeISRC(value)
		if isrc != "" {
			valid = append(valid, isrc)
		}
	}
	sort.Strings(valid)
	if len(valid) == 0 {
		return ""
	}
	return valid[0]
}

func normalizeISRC(value string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(value) {
		if r == '-' || r == ' ' {
			continue
		}
		if r >= 'a' && r <= 'z' {
			r = r - 'a' + 'A'
		}
		b.WriteRune(r)
	}
	isrc := b.String()
	if len(isrc) != 12 {
		return ""
	}
	for i := 0; i < 2; i++ {
		if isrc[i] < 'A' || isrc[i] > 'Z' {
			return ""
		}
	}
	for i := 2; i < 5; i++ {
		c := isrc[i]
		if (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			return ""
		}
	}
	for i := 5; i < 12; i++ {
		if isrc[i] < '0' || isrc[i] > '9' {
			return ""
		}
	}
	return isrc
}

func positiveMS(value *int) *int {
	if value == nil || *value <= 0 {
		return nil
	}
	ms := *value
	return &ms
}

func fullDate(value string) *time.Time {
	parsed, err := time.Parse("2006-01-02", strings.TrimSpace(value))
	if err != nil {
		return nil
	}
	date := time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, time.UTC)
	return &date
}

func validMBID(value string) bool {
	return mbidPattern.MatchString(strings.TrimSpace(value))
}

func (m *MusicBrainz) rememberName(id, name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.names == nil {
		m.names = map[string]string{}
	}
	m.names[id] = name
}

func (m *MusicBrainz) artistName(id string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.names[id]
}

func (m *MusicBrainz) rememberChoice(groupID string, choice releaseChoice) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.chosen == nil {
		m.chosen = map[string]releaseChoice{}
	}
	m.chosen[groupID] = choice
}

func (m *MusicBrainz) choiceFor(groupID string) (releaseChoice, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	choice, ok := m.chosen[groupID]
	return choice, ok
}

func (m *MusicBrainz) getJSON(ctx context.Context, path string, dest any) error {
	attempts := m.Attempts
	if attempts < 1 {
		attempts = musicBrainzAttempts
	}
	var last error
	for attempt := 0; attempt < attempts; attempt++ {
		if err := m.wait(ctx); err != nil {
			return err
		}
		status, body, err := m.fetch(ctx, path)
		if err != nil {
			last = err
			continue
		}
		switch {
		case status == http.StatusNotFound:
			return ErrNotFound
		case status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable || status >= 500:
			last = fmt.Errorf("musicbrainz status %d", status)
			continue
		case status == http.StatusOK:
			if err := json.Unmarshal(body, dest); err != nil {
				return fmt.Errorf("musicbrainz response: %w", err)
			}
			return nil
		case status >= 400 && status < 500:
			return fmt.Errorf("musicbrainz status %d: %w", status, ErrPermanent)
		default:
			last = fmt.Errorf("musicbrainz status %d", status)
		}
	}
	if last == nil {
		last = errors.New("musicbrainz request failed")
	}
	return last
}

func (m *MusicBrainz) fetch(ctx context.Context, path string) (int, []byte, error) {
	endpoint := m.BaseURL + "/" + strings.TrimLeft(path, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("User-Agent", m.UserAgent)
	req.Header.Set("Accept", "application/json")
	client := m.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, musicBrainzBodyLimit+1))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	if len(body) > musicBrainzBodyLimit {
		return resp.StatusCode, nil, errors.New("musicbrainz response is too large")
	}
	return resp.StatusCode, body, nil
}

func (m *MusicBrainz) wait(ctx context.Context) error {
	interval := m.Interval
	if interval < 0 {
		return ctx.Err()
	}
	if interval == 0 {
		interval = time.Second
	}
	m.mu.Lock()
	now := time.Now()
	wait := time.Duration(0)
	if now.Before(m.next) {
		wait = m.next.Sub(now)
	}
	m.next = now.Add(wait).Add(interval)
	m.mu.Unlock()
	if wait == 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type mbArtistSearch struct {
	Count   int        `json:"count"`
	Artists []mbArtist `json:"artists"`
}

type mbArtist struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Disambiguation string `json:"disambiguation"`
}

type mbReleaseBrowse struct {
	Count    int         `json:"release-count"`
	Offset   int         `json:"release-offset"`
	Releases []mbRelease `json:"releases"`
}

type mbGroupLookup struct {
	mbGroup
	Releases []mbRelease `json:"releases"`
}

type mbGroup struct {
	ID               string         `json:"id"`
	Title            string         `json:"title"`
	PrimaryType      string         `json:"primary-type"`
	SecondaryTypes   []string       `json:"secondary-types"`
	FirstReleaseDate string         `json:"first-release-date"`
	ArtistCredit     []mbNameCredit `json:"artist-credit"`
}

type mbRelease struct {
	ID           string         `json:"id"`
	Title        string         `json:"title"`
	Status       string         `json:"status"`
	Date         string         `json:"date"`
	ArtistCredit []mbNameCredit `json:"artist-credit"`
	ReleaseGroup *mbGroup       `json:"release-group"`
	Media        []mbMedium     `json:"media"`
}

type mbNameCredit struct {
	Name       string   `json:"name"`
	Joinphrase string   `json:"joinphrase"`
	Artist     mbArtist `json:"artist"`
}

type mbMedium struct {
	Position int       `json:"position"`
	Tracks   []mbTrack `json:"tracks"`
}

type mbTrack struct {
	Title     string         `json:"title"`
	Position  int            `json:"position"`
	Recording mbRecording    `json:"recording"`
	Artists   []mbNameCredit `json:"artist-credit"`
}

type mbRecording struct {
	ID           string         `json:"id"`
	Title        string         `json:"title"`
	Length       *int           `json:"length"`
	ISRCs        []string       `json:"isrcs"`
	ArtistCredit []mbNameCredit `json:"artist-credit"`
}
