package audio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"time"
)

// MediaSourceAdapter downloads one allowed audio file.
type MediaSourceAdapter interface {
	DownloadPayload(ctx context.Context, sourceURL string) (io.ReadCloser, error)
}

// GenericHTTPAdapter fetches a direct audio URL with the standard HTTP client.
// It accepts https, and http only when AllowHTTP is set. Private addresses are
// rejected unless AllowPrivate is set. Streaming hosts are rejected.
type GenericHTTPAdapter struct {
	UserAgent string
	// Timeout bounds the whole GET. Zero uses two minutes.
	Timeout time.Duration
	// AllowHTTP accepts http URLs. Production leaves this false.
	AllowHTTP bool
	// AllowPrivate accepts loopback and private addresses. Tests use it for httptest.
	AllowPrivate bool
}

// DownloadPayload performs one GET and returns the response body.
// The caller closes the body. A non-audio response is closed before the error is returned.
func (a *GenericHTTPAdapter) DownloadPayload(ctx context.Context, sourceURL string) (io.ReadCloser, error) {
	parsed, err := url.Parse(sourceURL)
	if err != nil || !a.allowedURL(parsed) || deniedHost(parsed.Hostname()) {
		return nil, ErrSourceNotFound
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return nil, err
	}
	if a.UserAgent != "" {
		req.Header.Set("User-Agent", a.UserAgent)
	}
	req.Header.Set("Accept", "audio/*;q=0.9, */*;q=0.1")
	resp, err := a.client().Do(req)
	if err != nil {
		if errors.Is(err, ErrSourceNotFound) || ctx.Err() != nil {
			return nil, err
		}
		return nil, &fetchError{temporary: true, err: err}
	}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		return nil, ErrSourceNotFound
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode >= 500 {
		resp.Body.Close()
		return nil, &fetchError{temporary: true, status: resp.StatusCode}
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, &fetchError{status: resp.StatusCode}
	}
	return a.audioBody(resp, sourceURL)
}

func (a *GenericHTTPAdapter) audioBody(resp *http.Response, sourceURL string) (io.ReadCloser, error) {
	if !audioContentType(resp.Header.Get("Content-Type")) {
		resp.Body.Close()
		return nil, ErrSourceNotFound
	}
	prefix := make([]byte, 16)
	n, readErr := io.ReadFull(resp.Body, prefix)
	if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) && !errors.Is(readErr, io.EOF) {
		resp.Body.Close()
		return nil, &fetchError{temporary: true, err: readErr}
	}
	if n == 0 || looksLikeHTML(prefix[:n]) {
		resp.Body.Close()
		return nil, ErrSourceNotFound
	}
	reference := sourceURL
	if resp.Request != nil && resp.Request.URL != nil && a.allowedURL(resp.Request.URL) && !deniedHost(resp.Request.URL.Hostname()) {
		reference = resp.Request.URL.String()
	}
	reader := io.MultiReader(bytes.NewReader(prefix[:n]), resp.Body)
	return &httpPayload{Reader: reader, closer: resp.Body, reference: reference}, nil
}

func (a *GenericHTTPAdapter) client() *http.Client {
	timeout := a.Timeout
	if timeout <= 0 {
		timeout = freeDownloadFileWait
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if err := a.publicAddr(ctx, addr); err != nil {
			return nil, err
		}
		return dialer.DialContext(ctx, network, addr)
	}
	return &http.Client{
		Timeout:       timeout,
		Transport:     transport,
		CheckRedirect: a.redirect,
	}
}

func (a *GenericHTTPAdapter) redirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return errors.New("free download redirect limit")
	}
	if req.URL == nil || !a.allowedURL(req.URL) || deniedHost(req.URL.Hostname()) {
		return ErrSourceNotFound
	}
	return nil
}

func (a *GenericHTTPAdapter) publicAddr(ctx context.Context, addr string) error {
	if a.AllowPrivate {
		return nil
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return ErrSourceNotFound
	}
	if ip := net.ParseIP(host); ip != nil {
		if !publicIP(ip) {
			return ErrSourceNotFound
		}
		return nil
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return err
	}
	if len(ips) == 0 {
		return ErrSourceNotFound
	}
	for _, ip := range ips {
		if !publicIP(ip.IP) {
			return ErrSourceNotFound
		}
	}
	return nil
}

func (a *GenericHTTPAdapter) allowedURL(parsed *url.URL) bool {
	if parsed == nil || parsed.Hostname() == "" || parsed.User != nil {
		return false
	}
	if parsed.Scheme != "https" && !(a.AllowHTTP && parsed.Scheme == "http") {
		return false
	}
	return audioExt(path.Ext(parsed.Path))
}

// SourceReference is the final URL after redirects.
func (b *httpPayload) SourceReference() string { return b.reference }

type httpPayload struct {
	io.Reader
	closer    io.Closer
	reference string
}

func (b *httpPayload) Close() error { return b.closer.Close() }

// fetchError is one failed file GET. temporary errors are retried by FreeDownload.
type fetchError struct {
	status    int
	temporary bool
	err       error
}

func (e *fetchError) Error() string {
	if e.err != nil {
		if e.status == 0 {
			return e.err.Error()
		}
		return fmt.Sprintf("free download status %d: %s", e.status, e.err.Error())
	}
	return fmt.Sprintf("free download status %d", e.status)
}

func (e *fetchError) Unwrap() error { return e.err }
