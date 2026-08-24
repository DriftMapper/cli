// Package buildinfo generates build-info.html (spec §2.3) from a build
// registration response — nothing else. build_instance_id and
// resolution_url are both server-provided; this package constructs no URLs
// and derives no values of its own, which is what keeps generation a pure
// function of one response: testable against a fixture with no server, and
// incapable of drifting from server routing (spec §5.1b's registerBuild
// doc, restated here because it's the load-bearing invariant of this whole
// package).
package buildinfo

import (
	"fmt"
	"html/template"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/driftmapper/protocol"
)

// schemaVersion is the machine-readable contract version (spec §2.3). Bump
// only in lockstep with every pinger version that parses this file — it is
// a public contract, same N-2-style compatibility posture as the wire
// protocol. A new driftmapper:* meta tag is additive, not a bump, matching
// this codebase's "clients must tolerate unknown fields" posture elsewhere
// (protocol/openapi.yaml's compatibility policy) — resolution-url and
// built-at below were both added on that basis.
const schemaVersion = "1"

// tmpl renders all representations — the namespaced meta tags, the visible
// unauth-tier content, and the click-only login link (DRFT-52) — from one
// templateData value, so they can never disagree with each other.
// html/template's contextual autoescaping is what makes reusing LoginURL
// safe across both href occurrences below.
//
// Deliberately no auto-redirect script (DRFT-52 removed it): a visitor sees
// this file's own content — the same build_instance_id/built_at the
// resolution page's unauth tier would show (spec §2.7, DRFT-40's disclosure
// boundary) — before ever being asked to sign in. The login link fires only
// on click, matching FORMAT-build-info.md's "human-facing ... free to
// change shape without a version bump" layer.
var tmpl = template.Must(template.New("build-info").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="driftmapper:schema-version" content="{{.SchemaVersion}}">
<meta name="driftmapper:build-id" content="{{.BuildInstanceID}}">
<meta name="driftmapper:built-at" content="{{.BuiltAt}}">
<meta name="driftmapper:resolution-url" content="{{.ResolutionURL}}">
<title>Driftmapper build info</title>
</head>
<body>
<p>Build ID: {{.BuildInstanceID}}</p>
<p>Built at: {{.BuiltAt}}</p>
<p><a href="{{.LoginURL}}">View full build details</a></p>
<noscript><a href="{{.LoginURL}}">View full build details</a></noscript>
</body>
</html>
`))

type templateData struct {
	SchemaVersion   string
	BuildInstanceID string
	BuiltAt         string
	ResolutionURL   string
	LoginURL        string
}

// Generate writes outputPath atomically (write-to-temp-then-rename in the
// same directory) so a reader never observes a partially-written file.
func Generate(outputPath string, build protocol.Build) error {
	if build.BuildInstanceId == "" {
		return fmt.Errorf("build_instance_id is empty")
	}
	if build.ResolutionUrl == nil || *build.ResolutionUrl == "" {
		return fmt.Errorf("resolution_url is empty")
	}

	login, err := loginURL(*build.ResolutionUrl)
	if err != nil {
		return fmt.Errorf("build login URL: %w", err)
	}

	dir := filepath.Dir(outputPath)
	if dir == "" {
		dir = "."
	}
	tmp, err := os.CreateTemp(dir, ".driftmapper-build-info-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once the rename below succeeds

	data := templateData{
		SchemaVersion:   schemaVersion,
		BuildInstanceID: build.BuildInstanceId,
		BuiltAt:         build.BuiltAt.UTC().Format(time.RFC3339),
		ResolutionURL:   *build.ResolutionUrl,
		LoginURL:        login,
	}
	if err := tmpl.Execute(tmp, data); err != nil {
		tmp.Close()
		return fmt.Errorf("render build-info.html: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, outputPath); err != nil {
		return fmt.Errorf("rename into place: %w", err)
	}
	return nil
}

// loginURL derives `/login?next=<path>` on resolutionURL's own origin
// (spec DRFT-52): the human link skips straight past the resolution page's
// unauth tier — this file already shows everything that tier would — and
// lands on sign-in with the resolution path queued up to redirect back to
// afterward. `next` (not `returnTo`) matches cmd/app/internal/handler/
// auth.go's actual query param name.
func loginURL(resolutionURL string) (string, error) {
	u, err := url.Parse(resolutionURL)
	if err != nil {
		return "", fmt.Errorf("parse resolution_url: %w", err)
	}
	next := u.Path
	if u.RawQuery != "" {
		next += "?" + u.RawQuery
	}
	login := *u
	login.Path = "/login"
	q := url.Values{}
	q.Set("next", next)
	login.RawQuery = q.Encode()
	return login.String(), nil
}
