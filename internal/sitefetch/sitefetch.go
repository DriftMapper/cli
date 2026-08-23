// Package sitefetch fetches one deployed build-info.html over the web
// with the posture `driftmapper verify` promises: HTTPS only, on the
// initial URL and every redirect hop; caller-supplied headers dropped
// the moment a redirect leaves the original host; bounded time, hops,
// and body size.
//
// Deliberately no SSRF machinery (IP denylists, DNS re-resolution): this
// binary runs inside the customer's own CI, fetching a URL the customer
// themselves configured — the same machine could curl it directly, so
// there is no privilege boundary to defend. The server-side poller that
// fetches *registered* URLs from shared infrastructure is the one that
// needs that posture (see createMonitoredUrl in driftmapper/protocol's
// openapi.yaml), and it keeps it.
package sitefetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// DefaultTimeout bounds one Do call end to end — connect, redirects,
	// and body read together. A verify step should fail fast with a clear
	// message rather than hang a CI job; reruns are cheap.
	DefaultTimeout = 15 * time.Second

	// MaxBodyBytes caps the response read at 1 MiB. Generated build-info
	// files are ~1 KiB; anything near this cap is not one, and reading an
	// unbounded response from a misbehaving host has no upside.
	MaxBodyBytes = 1 << 20

	// MaxRedirectHops mirrors net/http's own default (10) so behavior is
	// unambiguous regardless of client construction.
	MaxRedirectHops = 10
)

var (
	// ErrNotHTTPS is returned for http:// targets, or any redirect that
	// would downgrade the scheme. errors.Is-able by callers mapping
	// failures to verification outcomes.
	ErrNotHTTPS = errors.New("only https:// URLs are fetched")

	// ErrTooLarge when the response body exceeds MaxBodyBytes.
	ErrTooLarge = errors.New("response exceeds size limit")

	// ErrTooManyRedirects after MaxRedirectHops hops.
	ErrTooManyRedirects = errors.New("too many redirects")
)

// StatusError is a completed HTTP exchange whose status was not 200.
type StatusError struct {
	StatusCode int
	Status     string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("unexpected HTTP status %s", e.Status)
}

// Header is one caller-supplied request header (-header on the verify
// command). Values may carry bearer tokens, so they are never logged and
// are stripped from any cross-origin redirect hop.
type Header struct {
	Name  string
	Value string
}

// Result is a successful fetch. URL is the final URL after redirects —
// what actually served the bytes — which is what gets recorded as a
// verification's source_url.
type Result struct {
	URL  string
	Body []byte
}

// Fetcher performs opinionated GETs. The zero value is not usable; use
// New, or construct directly in tests to swap Client/CheckScheme/Timeout.
type Fetcher struct {
	Client      *http.Client
	CheckScheme func(*url.URL) error
	Timeout     time.Duration
	Headers     []Header
}

// RequireHTTPS is the production scheme policy: https:// only.
func RequireHTTPS(u *url.URL) error {
	if u.Scheme != "https" {
		return fmt.Errorf("%w: got %q", ErrNotHTTPS, u.Scheme+":")
	}
	return nil
}

// New returns a production Fetcher carrying headers.
func New(headers []Header) *Fetcher {
	return NewWithClient(&http.Client{}, headers)
}

// NewWithClient behaves like New but routes requests through the given
// client — for callers embedding their own transport. The posture is
// identical: scheme policy, redirect handling with cross-origin header
// stripping, size cap.
func NewWithClient(client *http.Client, headers []Header) *Fetcher {
	f := &Fetcher{
		Client:      client,
		CheckScheme: RequireHTTPS,
		Timeout:     DefaultTimeout,
		Headers:     headers,
	}
	f.Client.CheckRedirect = f.checkRedirect
	return f
}

// checkRedirect refuses downgrades and loops, and strips caller-supplied
// headers once a redirect leaves the original host — a staging token must
// not leak to wherever the target happens to bounce through.
func (f *Fetcher) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= MaxRedirectHops {
		return fmt.Errorf("%w after %d hops", ErrTooManyRedirects, len(via))
	}
	if err := f.CheckScheme(req.URL); err != nil {
		return fmt.Errorf("redirect: %w", err)
	}
	if req.URL.Host != via[0].URL.Host {
		for _, h := range f.Headers {
			req.Header.Del(h.Name)
		}
	}
	return nil
}

// Do GETs rawURL under the package posture and returns the final URL plus
// up to MaxBodyBytes of body. Any non-200 response is a *StatusError;
// every other failure (scheme, transport, timeout, size) is wrapped with
// its sentinel where one applies.
func (f *Fetcher) Do(ctx context.Context, rawURL string) (*Result, error) {
	u, err := url.Parse(rawURL)
	if err != nil || !strings.Contains(rawURL, ":") {
		return nil, fmt.Errorf("parse URL: %w", err)
	}
	if err := f.CheckScheme(u); err != nil {
		return nil, err
	}
	if u.Host == "" {
		return nil, fmt.Errorf("URL %q has no host", rawURL)
	}

	timeout := f.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "text/html")
	for _, h := range f.Headers {
		req.Header.Set(h.Name, h.Value)
	}

	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, &StatusError{StatusCode: resp.StatusCode, Status: resp.Status}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if len(body) > MaxBodyBytes {
		return nil, fmt.Errorf("%w (%d bytes)", ErrTooLarge, MaxBodyBytes)
	}
	return &Result{URL: resp.Request.URL.String(), Body: body}, nil
}
