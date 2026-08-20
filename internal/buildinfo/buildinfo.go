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
	"os"
	"path/filepath"

	"github.com/driftmapper/protocol"
)

// schemaVersion is the machine-readable contract version (spec §2.3). Bump
// only in lockstep with every pinger version that parses this file — it is
// a public contract, same N-2-style compatibility posture as the wire
// protocol. A new driftmapper:* meta tag is additive, not a bump, matching
// this codebase's "clients must tolerate unknown fields" posture elsewhere
// (protocol/openapi.yaml's compatibility policy) — resolution-url below was
// added on that basis.
const schemaVersion = "1"

// tmpl renders all three representations — the namespaced meta tags, the
// auto-redirect script, and the noscript fallback link — from one
// (buildInstanceID, resolutionURL) pair, so they can never disagree with
// each other. html/template's contextual autoescaping is what makes reusing
// ResolutionURL safe across both the <script> and href contexts below: each
// occurrence is escaped for the context it actually appears in.
var tmpl = template.Must(template.New("build-info").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="driftmapper:schema-version" content="{{.SchemaVersion}}">
<meta name="driftmapper:build-id" content="{{.BuildInstanceID}}">
<meta name="driftmapper:resolution-url" content="{{.ResolutionURL}}">
<title>Driftmapper build info</title>
<script>window.location.replace({{.ResolutionURL}});</script>
</head>
<body>
<p><a href="{{.ResolutionURL}}">View this build on Driftmapper</a></p>
<noscript><a href="{{.ResolutionURL}}">View this build on Driftmapper</a></noscript>
</body>
</html>
`))

type templateData struct {
	SchemaVersion   string
	BuildInstanceID string
	ResolutionURL   string
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
		ResolutionURL:   *build.ResolutionUrl,
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
