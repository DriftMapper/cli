package buildinfo

import (
	"os"
	"strings"
	"testing"
	"time"
)

// TestParse_RoundTripFromGenerate is the load-bearing test of this
// package: the producer and the parser must never disagree about the
// contract. Whatever Generate writes today, Parse must read back exactly.
func TestParse_RoundTripFromGenerate(t *testing.T) {
	info := parseGenerated(t)

	if info.SchemaVersion != schemaVersion {
		t.Errorf("SchemaVersion = %q, want %q", info.SchemaVersion, schemaVersion)
	}
	if info.BuildInstanceID != "abc123" {
		t.Errorf("BuildInstanceID = %q, want abc123", info.BuildInstanceID)
	}
	want := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)
	if !info.BuiltAt.Equal(want) {
		t.Errorf("BuiltAt = %v, want %v", info.BuiltAt, want)
	}
	if info.ResolutionURL != "https://driftmapper.test/r/abc123" {
		t.Errorf("ResolutionURL = %q, want https://driftmapper.test/r/abc123", info.ResolutionURL)
	}
}

// TestParse_ToleratesThirdPartyFormatting: parsers built against this
// format must not assume our generator's exact bytes. Attribute order,
// quoting style, tag case, extra attributes, self-closing slashes, and
// surrounding markup are all legal variations.
func TestParse_ToleratesThirdPartyFormatting(t *testing.T) {
	doc := `<!DOCTYPE html>
<HTML>
<HEAD>
<meta charset='utf-8'>
<META   CONTENT="1" NAME="driftmapper:schema-version" />
<meta name=driftmapper:build-id content=b&amp;id-77 >
<meta name='driftmapper:built-at' content='2026-05-06T07:08:09Z'
><meta data-x="ignored" name="driftmapper:resolution-url"
       content="https://other.test/r/b&id-77"/>
<meta name="driftmapper:new-thing-later-version" content="future tags are additive">
</HEAD>
</HTML>`

	info, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if info.SchemaVersion != "1" {
		t.Errorf("SchemaVersion = %q, want 1", info.SchemaVersion)
	}
	if info.BuildInstanceID != "b&id-77" {
		t.Errorf("BuildInstanceID = %q, want b&id-77 (HTML entities must decode)", info.BuildInstanceID)
	}
	want := time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)
	if !info.BuiltAt.Equal(want) {
		t.Errorf("BuiltAt = %v, want %v", info.BuiltAt, want)
	}
	if info.ResolutionURL != "https://other.test/r/b&id-77" {
		t.Errorf("ResolutionURL = %q, want https://other.test/r/b&id-77", info.ResolutionURL)
	}
}

// TestParse_IgnoresLookalikeTags: <metadata> must not match "<meta", and
// non-driftmapper meta tags must be ignored entirely.
func TestParse_IgnoresLookalikeTags(t *testing.T) {
	doc := `<metadata name="driftmapper:build-id" content="from-metadata">
<meta name="viewport" content="width=device-width">
<meta name="driftmapper:schema-version" content="1">
<meta name="driftmapper:build-id" content="real-id">
<meta name="driftmapper:built-at" content="2026-01-01T00:00:00Z">
<meta name="driftmapper:resolution-url" content="https://x.test/r/real-id">
</metadata>`
	info, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if info.BuildInstanceID != "real-id" {
		t.Errorf("BuildInstanceID = %q, want real-id (the <metadata> lookalike must be ignored)", info.BuildInstanceID)
	}
}

func TestParse_UnsupportedSchemaVersionFailsLoud(t *testing.T) {
	doc := replaceTag(t, parseGeneratedDoc(t), "schema-version", "7")
	_, err := Parse([]byte(doc))
	if err == nil {
		t.Fatal("Parse: want error for unrecognized schema version, got nil")
	}
	for _, want := range []string{"unsupported build-info schema version", "7"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to contain %q (FORMAT-build-info.md mandates failing loudly, not guessing)", err.Error(), want)
		}
	}
}

func TestParse_MissingRequiredTagsAreNamed(t *testing.T) {
	base := parseGeneratedDoc(t)
	for _, tc := range []struct{ tag, wantErr string }{
		{"schema-version", "missing driftmapper:schema-version"},
		{"build-id", "missing driftmapper:build-id"},
		{"built-at", "missing driftmapper:built-at"},
		{"resolution-url", "missing driftmapper:resolution-url"},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			_, err := Parse([]byte(replaceTag(t, base, tc.tag, "")))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestParse_RejectsMalformedBuiltAt(t *testing.T) {
	doc := replaceTag(t, parseGeneratedDoc(t), "built-at", "not-a-timestamp")
	_, err := Parse([]byte(doc))
	if err == nil || !strings.Contains(err.Error(), "RFC 3339") {
		t.Errorf("err = %v, want an RFC 3339 parse error", err)
	}
}

func TestParse_EmptyAndGarbageInputDoNotPanic(t *testing.T) {
	for _, in := range []string{"", "<<>>", `<meta`, `<meta name="driftmapper:build-id" content="x` /* unterminated */} {
		if _, err := Parse([]byte(in)); err != nil && strings.Contains(err.Error(), "unsupported") {
			t.Errorf("Parse(%q): unexpected unsupported-version error: %v", in, err)
		}
	}
}

// --- helpers -------------------------------------------------------------

func parseGeneratedDoc(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/build-info.html"
	if err := Generate(path, fixtureBuild(t)); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}
	return string(raw)
}

func parseGenerated(t *testing.T) Info {
	t.Helper()
	info, err := Parse([]byte(parseGeneratedDoc(t)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return info
}

// replaceTag swaps one driftmapper tag's content value (empty string
// removes the whole line), leaving the rest of doc untouched.
func replaceTag(t *testing.T, doc, tag, newContent string) string {
	t.Helper()
	name := "driftmapper:" + tag
	lines := strings.Split(doc, "\n")
	for i, line := range lines {
		if strings.Contains(line, `name="`+name+`"`) {
			if newContent == "" {
				return strings.Join(append(lines[:i:i], lines[i+1:]...), "\n")
			}
			lines[i] = `<meta name="` + name + `" content="` + newContent + `">`
			return strings.Join(lines, "\n")
		}
	}
	t.Fatalf("tag %q not found in generated doc", name)
	return ""
}
