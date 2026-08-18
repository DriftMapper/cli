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
	htmlpkg "html"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"regexp"

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

// metaTagPattern matches exactly the driftmapper:* meta tags Generate
// writes. Parse looks for these three tags specifically rather than
// general HTML, so it tolerates anything about the surrounding markup
// changing except its own contract.
var metaTagPattern = regexp.MustCompile(`<meta name="driftmapper:([a-z-]+)" content="([^"]*)">`)

// Info is a build-info.html file's contents, decoded back into the fields
// Generate wrote — the read-side counterpart used by `driftmapper compare`
// (spec DRFT-26) to fetch and diff two deployed targets unauthenticated.
type Info struct {
	SchemaVersion   string `json:"schema_version"`
	BuildInstanceID string `json:"build_instance_id"`
	ResolutionURL   string `json:"resolution_url,omitempty"`
}

// Parse extracts Info from a build-info.html document. BuildInstanceID is
// required; SchemaVersion and ResolutionURL are populated when present but
// not validated, since a future schema version may carry neither in the
// same shape and Parse must keep working for identifying the build either
// way.
func Parse(r io.Reader) (Info, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return Info{}, fmt.Errorf("read build-info.html: %w", err)
	}

	tags := map[string]string{}
	for _, m := range metaTagPattern.FindAllSubmatch(b, -1) {
		tags[string(m[1])] = htmlpkg.UnescapeString(string(m[2]))
	}

	info := Info{
		SchemaVersion:   tags["schema-version"],
		BuildInstanceID: tags["build-id"],
		ResolutionURL:   tags["resolution-url"],
	}
	if info.BuildInstanceID == "" {
		return Info{}, fmt.Errorf("missing driftmapper:build-id meta tag")
	}
	return info, nil
}
