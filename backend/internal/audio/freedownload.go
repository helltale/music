package audio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultMusicBrainzBase  = "https://musicbrainz.org/ws/2"
	defaultMusicBrainzAgent = "music/1.0 ( https://github.com/helltale/music )"
	defaultArchiveBase      = "https://archive.org"
	// freeDownloadRelationID is the MusicBrainz recording-URL type "download for free".
	freeDownloadRelationID   = "45d0cbc5-d65b-4e77-bdfd-8a75207cb5c5"
	freeDownloadAttempts     = 2
	freeDownloadLookupLimit  = 1 << 20
	freeDownloadMetadataWait = 10 * time.Second
	freeDownloadFileWait     = 2 * time.Minute
)

var mbidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// FreeDownload opens audio only when MusicBrainz links the recording to a legal free download.
// Streaming pages, purchase pages, and commercial streaming hosts are ignored.
type FreeDownload struct {
	BaseURL     string
	UserAgent   string
	ArchiveBase string
	// Interval is the pause between outbound requests. Zero means one second.
	// A negative value disables the pause for tests.
	Interval time.Duration
	Attempts int
	// AllowHTTP accepts http URLs. Production leaves this false.
	AllowHTTP bool
	// AllowPrivate accepts loopback and private addresses. Tests use it for httptest.
	AllowPrivate bool

	mu   sync.Mutex
	next time.Time
}

// NewFreeDownload reads the public MusicBrainz web service.
// Empty baseURL and userAgent use the same defaults as the catalog adapter.
func NewFreeDownload(baseURL, userAgent string) *FreeDownload {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = defaultMusicBrainzBase
	}
	userAgent = strings.TrimSpace(userAgent)
	if userAgent == "" {
		userAgent = defaultMusicBrainzAgent
	}
	return &FreeDownload{BaseURL: baseURL, UserAgent: userAgent}
}

// Open resolves a recording MBID to one legal audio file.
// Any other external id, including the local demo ids, is not found.
func (f *FreeDownload) Open(ctx context.Context, recordingExternalID string) (io.ReadCloser, string, error) {
	if !mbidPattern.MatchString(strings.TrimSpace(recordingExternalID)) {
		return nil, "", ErrSourceNotFound
	}
	var recording mbRecordingURLs
	path := "recording/" + url.PathEscape(recordingExternalID) + "?inc=url-rels&fmt=json"
	if err := f.getJSON(ctx, f.BaseURL+"/"+path, &recording); err != nil {
		if errors.Is(err, errFreeNotFound) {
			return nil, "", ErrSourceNotFound
		}
		return nil, "", err
	}
	relations := freeRelations(recording.Relations)
	if len(relations) == 0 {
		return nil, "", ErrSourceNotFound
	}
	var temporary error
	for _, relation := range relations {
		fileURL, err := f.resolve(ctx, relation)
		if errors.Is(err, ErrSourceNotFound) {
			continue
		}
		if err != nil {
			temporary = err
			continue
		}
		body, reference, err := f.download(ctx, fileURL)
		if errors.Is(err, ErrSourceNotFound) {
			continue
		}
		if err != nil {
			temporary = err
			continue
		}
		return body, reference, nil
	}
	if temporary != nil {
		return nil, "", temporary
	}
	return nil, "", ErrSourceNotFound
}

func (f *FreeDownload) resolve(ctx context.Context, resource string) (string, error) {
	parsed, err := url.Parse(resource)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil {
		return "", ErrSourceNotFound
	}
	if deniedHost(parsed.Hostname()) {
		return "", ErrSourceNotFound
	}
	if id, direct := f.archiveItem(parsed); id != "" {
		if direct && f.allowedURL(parsed) {
			return parsed.String(), nil
		}
		return f.archiveAudio(ctx, id)
	}
	if !f.allowedURL(parsed) {
		return "", ErrSourceNotFound
	}
	return parsed.String(), nil
}

func (f *FreeDownload) archiveAudio(ctx context.Context, id string) (string, error) {
	var meta archiveMetadata
	metaURL := f.archiveRoot() + "/metadata/" + url.PathEscape(id)
	if err := f.getJSON(ctx, metaURL, &meta); err != nil {
		if errors.Is(err, errFreeNotFound) {
			return "", ErrSourceNotFound
		}
		return "", err
	}
	name := chooseArchiveFile(meta.Files)
	if name == "" {
		return "", ErrSourceNotFound
	}
	var escaped []string
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return "", ErrSourceNotFound
		}
		escaped = append(escaped, url.PathEscape(part))
	}
	return f.archiveRoot() + "/download/" + url.PathEscape(id) + "/" + strings.Join(escaped, "/"), nil
}

func (f *FreeDownload) download(ctx context.Context, fileURL string) (io.ReadCloser, string, error) {
	parsed, err := url.Parse(fileURL)
	if err != nil || !f.allowedURL(parsed) {
		return nil, "", ErrSourceNotFound
	}
	attempts := f.Attempts
	if attempts < 1 {
		attempts = freeDownloadAttempts
	}
	adapter := f.payloadSource()
	var last error
	for attempt := 0; attempt < attempts; attempt++ {
		if err := f.wait(ctx); err != nil {
			return nil, "", err
		}
		body, err := adapter.DownloadPayload(ctx, fileURL)
		if err == nil {
			reference := fileURL
			if ref, ok := body.(interface{ SourceReference() string }); ok && ref.SourceReference() != "" {
				reference = ref.SourceReference()
			}
			return body, reference, nil
		}
		if errors.Is(err, ErrSourceNotFound) || ctx.Err() != nil {
			return nil, "", err
		}
		var fetch *fetchError
		if errors.As(err, &fetch) && !fetch.temporary {
			return nil, "", err
		}
		last = err
	}
	if last == nil {
		last = errors.New("free download request failed")
	}
	return nil, "", last
}

func (f *FreeDownload) payloadSource() MediaSourceAdapter {
	return &GenericHTTPAdapter{
		UserAgent:    f.UserAgent,
		AllowHTTP:    f.AllowHTTP,
		AllowPrivate: f.AllowPrivate,
	}
}

func (f *FreeDownload) getJSON(ctx context.Context, rawURL string, dest any) error {
	attempts := f.Attempts
	if attempts < 1 {
		attempts = freeDownloadAttempts
	}
	var last error
	for attempt := 0; attempt < attempts; attempt++ {
		if err := f.wait(ctx); err != nil {
			return err
		}
		resp, err := f.do(ctx, f.lookupClient(), rawURL)
		if errors.Is(err, ErrSourceNotFound) {
			return ErrSourceNotFound
		}
		if err != nil {
			last = err
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, freeDownloadLookupLimit+1))
		resp.Body.Close()
		if readErr != nil {
			last = readErr
			continue
		}
		if len(body) > freeDownloadLookupLimit {
			return errors.New("free download metadata is too large")
		}
		switch {
		case resp.StatusCode == http.StatusNotFound:
			return errFreeNotFound
		case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode >= 500:
			last = fmt.Errorf("free download status %d", resp.StatusCode)
			continue
		case resp.StatusCode == http.StatusOK:
			if err := json.Unmarshal(body, dest); err != nil {
				return fmt.Errorf("free download metadata: %w", err)
			}
			return nil
		case resp.StatusCode >= 400 && resp.StatusCode < 500:
			return errFreeNotFound
		default:
			last = fmt.Errorf("free download status %d", resp.StatusCode)
		}
	}
	if last == nil {
		last = errors.New("free download request failed")
	}
	return last
}

func (f *FreeDownload) do(ctx context.Context, client *http.Client, rawURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", f.UserAgent)
	req.Header.Set("Accept", "application/json, audio/*;q=0.9, */*;q=0.1")
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, ErrSourceNotFound) {
			return nil, ErrSourceNotFound
		}
		return nil, err
	}
	return resp, nil
}

func (f *FreeDownload) lookupClient() *http.Client {
	return &http.Client{Timeout: freeDownloadMetadataWait, CheckRedirect: f.sameHostRedirect}
}

func (f *FreeDownload) sameHostRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 3 {
		return errors.New("free download redirect limit")
	}
	if req.URL.Host != via[0].URL.Host {
		return ErrSourceNotFound
	}
	return nil
}

func (f *FreeDownload) allowedURL(parsed *url.URL) bool {
	if parsed == nil || parsed.Hostname() == "" || parsed.User != nil {
		return false
	}
	if parsed.Scheme != "https" && !(f.AllowHTTP && parsed.Scheme == "http") {
		return false
	}
	return audioExt(path.Ext(parsed.Path))
}

func (f *FreeDownload) archiveRoot() string {
	root := strings.TrimRight(strings.TrimSpace(f.ArchiveBase), "/")
	if root == "" {
		return defaultArchiveBase
	}
	return root
}

func (f *FreeDownload) wait(ctx context.Context) error {
	interval := f.Interval
	if interval < 0 {
		return nil
	}
	if interval == 0 {
		interval = time.Second
	}
	f.mu.Lock()
	now := time.Now()
	delay := time.Duration(0)
	if now.Before(f.next) {
		delay = f.next.Sub(now)
	}
	f.next = now.Add(delay).Add(interval)
	f.mu.Unlock()
	if delay == 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func freeRelations(relations []mbRelation) []string {
	urls := make([]string, 0, len(relations))
	for _, relation := range relations {
		if relation.Ended {
			continue
		}
		if !strings.EqualFold(relation.TypeID, freeDownloadRelationID) && !strings.EqualFold(strings.TrimSpace(relation.Type), "download for free") {
			continue
		}
		resource := strings.TrimSpace(relation.URL.Resource)
		if resource == "" {
			continue
		}
		urls = append(urls, resource)
	}
	sort.Strings(urls)
	return urls
}

func (f *FreeDownload) archiveItem(parsed *url.URL) (id string, direct bool) {
	if !f.archiveHost(parsed.Hostname()) {
		return "", false
	}
	parts := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(parts) < 2 || (parts[0] != "details" && parts[0] != "download") {
		return "", false
	}
	id, err := url.PathUnescape(parts[1])
	if err != nil || !safeArchiveID(id) {
		return "", false
	}
	if parts[0] == "download" && len(parts) >= 3 && audioExt(path.Ext(parts[len(parts)-1])) {
		return id, true
	}
	return id, false
}

func (f *FreeDownload) archiveHost(host string) bool {
	host = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(host)), "www.")
	root, err := url.Parse(f.archiveRoot())
	if err == nil {
		base := strings.TrimPrefix(strings.ToLower(root.Hostname()), "www.")
		if host == base {
			return true
		}
	}
	return host == "archive.org" || strings.HasSuffix(host, ".archive.org")
}

func safeArchiveID(id string) bool {
	if id == "" || len(id) > 400 || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return false
	}
	for _, r := range id {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func chooseArchiveFile(files []archiveFile) string {
	type candidate struct {
		name string
		rank int
		size int64
	}
	var found []candidate
	for _, file := range files {
		name := strings.TrimSpace(file.Name)
		ext := strings.ToLower(path.Ext(name))
		rank, ok := audioRank[ext]
		if !ok || strings.Contains(name, "..") {
			continue
		}
		size, _ := strconv.ParseInt(strings.TrimSpace(file.Size), 10, 64)
		found = append(found, candidate{name: name, rank: rank, size: size})
	}
	if len(found) == 0 {
		return ""
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].rank != found[j].rank {
			return found[i].rank < found[j].rank
		}
		if found[i].size != found[j].size {
			if found[i].size == 0 {
				return false
			}
			if found[j].size == 0 {
				return true
			}
			return found[i].size < found[j].size
		}
		return found[i].name < found[j].name
	})
	return found[0].name
}

func audioExt(ext string) bool {
	_, ok := audioRank[strings.ToLower(ext)]
	return ok
}

var audioRank = map[string]int{
	".mp3": 0, ".ogg": 1, ".opus": 2, ".m4a": 3, ".aac": 4, ".flac": 5, ".wav": 6, ".aif": 7, ".aiff": 7,
}

func deniedHost(host string) bool {
	host = strings.TrimPrefix(strings.ToLower(host), "www.")
	for _, suffix := range []string{
		"youtube.com", "youtu.be", "music.youtube.com",
		"spotify.com", "apple.com", "soundcloud.com", "tidal.com", "deezer.com",
	} {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}

func audioContentType(value string) bool {
	value = strings.ToLower(strings.TrimSpace(strings.Split(value, ";")[0]))
	switch {
	case value == "", value == "application/octet-stream", value == "binary/octet-stream", value == "application/ogg":
		return true
	case strings.HasPrefix(value, "audio/"):
		return true
	default:
		return false
	}
}

func looksLikeHTML(prefix []byte) bool {
	trimmed := bytes.TrimSpace(prefix)
	return bytes.HasPrefix(bytes.ToLower(trimmed), []byte("<!doctype")) || bytes.HasPrefix(bytes.ToLower(trimmed), []byte("<html"))
}

func publicIP(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	return true
}

var errFreeNotFound = errors.New("free download not found")

type mbRecordingURLs struct {
	Relations []mbRelation `json:"relations"`
}

type mbRelation struct {
	Type   string `json:"type"`
	TypeID string `json:"type-id"`
	Ended  bool   `json:"ended"`
	URL    struct {
		Resource string `json:"resource"`
	} `json:"url"`
}

type archiveMetadata struct {
	Files []archiveFile `json:"files"`
}

type archiveFile struct {
	Name string `json:"name"`
	Size string `json:"size"`
}
