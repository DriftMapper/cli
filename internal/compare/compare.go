// Package compare implements `driftmapper compare` (spec DRFT-50): a thin,
// unauthenticated browser launcher for the SPA compare view (DRFT-29).
//
// This supersedes DRFT-26/DRFT-31's original design, which fetched
// build-info.html unauthenticated from two deployed targets and diffed
// build_instance_id locally. That was a read command in everything but
// name — it directly contradicted DRFT-21's founding rule for this CLI ("No
// read commands. Viewing a build happens by opening the build-info file —
// that's the core loop"). This package now performs zero network calls: it
// only builds the SPA compare view's URL from two build-instance IDs the
// caller already has (read off build-info.html themselves, per that same
// core loop) and hands off to the browser. The authenticated, field-by-field
// diff happens in the browser, with whatever session the user's browser
// carries — see DRFT-29's compare-page.ts for that contract.
package compare

import (
	"fmt"
	"net/url"
	"strings"
)

// Result is the pair of build-instance IDs (and optional display labels)
// `driftmapper compare` hands off to the SPA compare view.
type Result struct {
	IDA, IDB       string
	LabelA, LabelB string
}

// OpenURL builds the SPA compare view URL (DRFT-29) that `compare` opens,
// given the dashboard's base origin (config.DashboardURL). Per that view's
// own URL contract (static/apps/dashboard's compare-page.ts doc comment):
// build-instance IDs go in a/b — the SPA cannot fetch a customer's deployed
// env itself (no CORS on an arbitrary origin), so it never re-resolves
// them — and LabelA/LabelB go in a_url/b_url as optional display-only
// labels, omitted entirely when blank.
func (r Result) OpenURL(dashboardURL string) (string, error) {
	u, err := url.Parse(dashboardURL)
	if err != nil {
		return "", fmt.Errorf("parse dashboard URL %q: %w", dashboardURL, err)
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "/compare"
	q := url.Values{}
	q.Set("a", r.IDA)
	q.Set("b", r.IDB)
	if r.LabelA != "" {
		q.Set("a_url", r.LabelA)
	}
	if r.LabelB != "" {
		q.Set("b_url", r.LabelB)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}
