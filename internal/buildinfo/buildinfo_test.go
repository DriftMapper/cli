package buildinfo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/driftmapper/protocol"
)

func TestParse_RoundTripsGenerate(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "build-info.html")
	if err := Generate(out, fixtureBuild(t)); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	f, err := os.Open(out)
	if err != nil {
		t.Fatalf("open output: %v", err)
	}
	defer f.Close()

	info, err := Parse(f)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := Info{
		SchemaVersion:   "1",
		BuildInstanceID: "abc123",
		ResolutionURL:   "https://driftmapper.test/r/abc123",
	}
	if info != want {
		t.Errorf("Parse = %+v, want %+v", info, want)
	}
}

func TestParse_UnescapesEntities(t *testing.T) {
	html := `<meta name="driftmapper:schema-version" content="1">
<meta name="driftmapper:build-id" content="abc123">
<meta name="driftmapper:resolution-url" content="https://driftmapper.test/r/abc123?a=1&amp;b=2">`

	info, err := Parse(strings.NewReader(html))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if want := "https://driftmapper.test/r/abc123?a=1&b=2"; info.ResolutionURL != want {
		t.Errorf("ResolutionURL = %q, want %q", info.ResolutionURL, want)
	}
}

func TestParse_RejectsMissingBuildID(t *testing.T) {
	html := `<meta name="driftmapper:schema-version" content="1">`

	if _, err := Parse(strings.NewReader(html)); err == nil {
		t.Error("Parse with no build-id tag: want error, got nil")
	}
}

func TestParse_RejectsArbitraryHTML(t *testing.T) {
	if _, err := Parse(strings.NewReader(`<html><body>not a build-info file</body></html>`)); err == nil {
		t.Error("Parse of unrelated HTML: want error, got nil")
	}
}

// fixtureBuild is exactly the shape a real POST /v1/builds response
// decodes into — the point of this test file is that Generate needs
// nothing else, per this package's doc comment.
func fixtureBuild(t *testing.T) protocol.Build {
	t.Helper()
	resolutionURL := "https://driftmapper.test/r/abc123"
	return protocol.Build{
		BuildInstanceId: "abc123",
		BuiltAt:         time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC),
		ResolutionUrl:   &resolutionURL,
	}
}

func TestGenerate_WritesExpectedContent(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "build-info.html")

	if err := Generate(out, fixtureBuild(t)); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	html := string(b)

	for _, want := range []string{
		`<meta name="driftmapper:schema-version" content="1">`,
		`<meta name="driftmapper:build-id" content="abc123">`,
		`<meta name="driftmapper:built-at" content="2026-01-02T15:04:05Z">`,
		`<meta name="driftmapper:resolution-url" content="https://driftmapper.test/r/abc123">`,
		`Build ID: abc123`,
		`Built at: 2026-01-02T15:04:05Z`,
		`<a href="https://driftmapper.test/login?next=%2Fr%2Fabc123">`,
		`<noscript><a href="https://driftmapper.test/login?next=%2Fr%2Fabc123">`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, html)
		}
	}
	if strings.Contains(html, "<script>") {
		t.Errorf("output must not auto-redirect on load, found a <script> tag:\n%s", html)
	}
}

func TestGenerate_NoStrayTempFiles(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "build-info.html")

	if err := Generate(out, fixtureBuild(t)); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "build-info.html" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("dir contents = %v, want exactly [build-info.html]", names)
	}
}

func TestGenerate_ConfigurableFilename(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "version.html")

	if err := Generate(out, fixtureBuild(t)); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("expected %s to exist: %v", out, err)
	}
}

func TestGenerate_RejectsEmptyBuildInstanceID(t *testing.T) {
	dir := t.TempDir()
	build := fixtureBuild(t)
	build.BuildInstanceId = ""

	if err := Generate(filepath.Join(dir, "build-info.html"), build); err == nil {
		t.Error("Generate with empty build_instance_id: want error, got nil")
	}
}

func TestGenerate_RejectsMissingResolutionURL(t *testing.T) {
	dir := t.TempDir()
	build := fixtureBuild(t)
	build.ResolutionUrl = nil

	if err := Generate(filepath.Join(dir, "build-info.html"), build); err == nil {
		t.Error("Generate with nil resolution_url: want error, got nil")
	}
}

// TestGenerate_EscapesUntrustedURL guards the html/template contextual-
// escaping assumption the whole package doc comment leans on: even though
// resolution_url is server-provided and trusted in practice, a value
// containing HTML/JS metacharacters must never break out of either the
// <script> or href context.
func TestGenerate_EscapesUntrustedURL(t *testing.T) {
	dir := t.TempDir()
	build := fixtureBuild(t)
	evil := `https://driftmapper.test/r/"><script>alert(1)</script>`
	build.ResolutionUrl = &evil

	out := filepath.Join(dir, "build-info.html")
	if err := Generate(out, build); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if strings.Contains(string(b), "<script>alert(1)</script>") {
		t.Errorf("resolution_url was not escaped, output:\n%s", b)
	}
}
