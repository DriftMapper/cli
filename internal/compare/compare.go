// Package compare implements the unauthenticated `driftmapper compare`
// command (spec DRFT-26): fetch build-info.html from two deployed targets
// and report whether they resolve to the same build.
//
// This is deliberately thin. protocol/openapi.yaml's getBuild operation is
// userSession-only — "There is no unauthenticated variant of this
// endpoint" — and the public disclosure tier (BuildPublic) it would
// otherwise expose is not yet machine-parseable anywhere unauthenticated:
// the resolution page's OG tags are human prose, not per-field data.
// Comparison is therefore build_instance_id equality only, read from the
// same build-info.html contract the CLI itself writes (internal/buildinfo)
// — nothing here calls the API. A richer unauth diff (repository name,
// built-at) is deferred until that public metadata is exposed in a
// structured form server-side.
package compare

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/driftmapper/cli/internal/buildinfo"
)

// Target is one fetched side of a comparison.
type Target struct {
	URL  string         `json:"url"`
	Info buildinfo.Info `json:"info"`
}

// Result is the outcome of comparing two targets.
type Result struct {
	A     Target `json:"a"`
	B     Target `json:"b"`
	Match bool   `json:"match"`
}

// Fetch retrieves and parses build-info.html at url.
func Fetch(ctx context.Context, client *http.Client, url string) (Target, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Target{}, fmt.Errorf("build request for %s: %w", url, err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return Target{}, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Target{}, fmt.Errorf("fetch %s: unexpected status %d", url, resp.StatusCode)
	}

	info, err := buildinfo.Parse(resp.Body)
	if err != nil {
		return Target{}, fmt.Errorf("parse %s: %w", url, err)
	}
	return Target{URL: url, Info: info}, nil
}

// Run fetches both targets and diffs them on build_instance_id.
func Run(ctx context.Context, client *http.Client, urlA, urlB string) (Result, error) {
	a, err := Fetch(ctx, client, urlA)
	if err != nil {
		return Result{}, err
	}
	b, err := Fetch(ctx, client, urlB)
	if err != nil {
		return Result{}, err
	}
	return Result{A: a, B: b, Match: a.Info.BuildInstanceID == b.Info.BuildInstanceID}, nil
}

// OpenURL builds the SPA compare view URL (DRFT-29) that `-open` deep-links
// to, given the dashboard's base origin (config.DashboardURL). Per that
// view's own URL contract (static/apps/dashboard's compare-page.ts doc
// comment): build-instance IDs go in a/b, the original target URLs the
// caller passed to Run go in a_url/b_url as optional display-only labels —
// the SPA cannot fetch a customer's deployed env itself (no CORS on an
// arbitrary origin), so it never re-resolves them.
func (r Result) OpenURL(dashboardURL string) (string, error) {
	u, err := url.Parse(dashboardURL)
	if err != nil {
		return "", fmt.Errorf("parse dashboard URL %q: %w", dashboardURL, err)
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "/compare"
	q := url.Values{}
	q.Set("a", r.A.Info.BuildInstanceID)
	q.Set("b", r.B.Info.BuildInstanceID)
	q.Set("a_url", r.A.URL)
	q.Set("b_url", r.B.URL)
	u.RawQuery = q.Encode()
	return u.String(), nil
}
