package buildinfo

import (
	"fmt"
	"html"
	"strings"
	"time"
)

// Info is one parsed build-info.html's machine-readable layer (spec §2.3):
// exactly the four versioned meta tags FORMAT-build-info.md documents.
// The human-facing layer of the file is never read — parsers must not
// depend on it, since it may change shape without a version bump.
type Info struct {
	SchemaVersion   string
	BuildInstanceID string
	BuiltAt         time.Time
	ResolutionURL   string
}

// metaPrefix namespaces every machine-readable tag in the file.
const metaPrefix = "driftmapper:"

// Parse reads a build-info document and returns its machine-readable
// layer. It is this format's first first-party reference parser
// (FORMAT-build-info.md) — written to the same standard any third-party
// parser must meet, because verify runs against whatever a deployed URL
// actually serves, years after this CLI was built.
//
// Tolerant by contract: attribute order, single/double/unquoted values,
// self-closing slashes, uppercase tags and attributes, unknown HTML, and
// unrecognized driftmapper:* tags (a new tag is additive, never a version
// bump). Loud by contract: a recognized schema-version gates the whole
// tag set, so an unrecognized version number fails with an explicit
// error rather than a silent best-effort parse, and every required tag
// under that version is named when missing.
func Parse(data []byte) (Info, error) {
	tags := map[string]string{}
	src := string(data)
	for pos := 0; pos < len(src); {
		lt := strings.IndexByte(src[pos:], '<')
		if lt < 0 {
			break
		}
		pos += lt
		end := strings.IndexByte(src[pos:], '>')
		if end < 0 {
			break // truncated tag; nothing more well-formed follows
		}
		tag := src[pos : pos+end+1]
		pos += end + 1
		if !isMetaTag(tag) {
			continue
		}
		name, content, ok := metaTagAttrs(tag)
		if !ok || len(name) <= len(metaPrefix) {
			continue
		}
		key := strings.ToLower(name)
		if !strings.HasPrefix(key, metaPrefix) {
			continue
		}
		if _, dup := tags[key]; !dup {
			tags[key] = html.UnescapeString(content)
		}
	}

	version, ok := tags[metaPrefix+"schema-version"]
	if !ok {
		return Info{}, fmt.Errorf("missing %sschema-version meta tag — is this URL a driftmapper-generated build-info.html?", metaPrefix)
	}
	if version != schemaVersion {
		return Info{}, fmt.Errorf("unsupported build-info schema version %q — this parser understands version %q only", version, schemaVersion)
	}

	id, ok := tags[metaPrefix+"build-id"]
	if !ok {
		return Info{}, fmt.Errorf("missing %sbuild-id meta tag", metaPrefix)
	}
	builtAtRaw, ok := tags[metaPrefix+"built-at"]
	if !ok {
		return Info{}, fmt.Errorf("missing %sbuilt-at meta tag", metaPrefix)
	}
	resolution, ok := tags[metaPrefix+"resolution-url"]
	if !ok {
		return Info{}, fmt.Errorf("missing %sresolution-url meta tag", metaPrefix)
	}
	builtAt, err := time.Parse(time.RFC3339, builtAtRaw)
	if err != nil {
		return Info{}, fmt.Errorf("parse %sbuilt-at %q as RFC 3339: %w", metaPrefix, builtAtRaw, err)
	}

	return Info{
		SchemaVersion:   version,
		BuildInstanceID: id,
		BuiltAt:         builtAt,
		ResolutionURL:   resolution,
	}, nil
}

// isMetaTag reports whether tag (from '<' through '>') opens with a
// case-insensitive "<meta" followed by a tag-name boundary.
func isMetaTag(tag string) bool {
	const name = "meta"
	if len(tag) < 1+len(name)+1 {
		return false
	}
	if !strings.EqualFold(tag[:1+len(name)], "<"+name) {
		return false
	}
	switch c := tag[1+len(name)]; c {
	case ' ', '\t', '\n', '\r', '/', '>':
		return true
	default:
		return false // e.g. <metadata> is not our tag
	}
}

// metaTagAttrs extracts the name/content attribute pair from one <meta>
// tag body. ok is false unless both are present.
func metaTagAttrs(tag string) (name, content string, ok bool) {
	attrs := map[string]string{}
	i := len("<meta")
	for i < len(tag) {
		for i < len(tag) && (tag[i] == ' ' || tag[i] == '\t' || tag[i] == '\n' || tag[i] == '\r' || tag[i] == '/') {
			i++
		}
		start := i
		for i < len(tag) && tag[i] != '=' && tag[i] != ' ' && tag[i] != '\t' && tag[i] != '\n' && tag[i] != '\r' && tag[i] != '/' {
			i++
		}
		attrName := strings.ToLower(tag[start:i])
		for i < len(tag) && (tag[i] == ' ' || tag[i] == '\t') {
			i++
		}
		value := ""
		if i < len(tag) && tag[i] == '=' {
			i++
			var next int
			value, next = readAttrValue(tag, i)
			i = next
		}
		if attrName != "" {
			if _, dup := attrs[attrName]; !dup {
				attrs[attrName] = value
			}
		}
	}

	name, hasName := attrs["name"]
	content, hasContent := attrs["content"]
	return name, content, hasName && hasContent
}

// readAttrValue reads one HTML attribute value starting at i: either
// quoted (single or double) or unquoted up to whitespace or a slash.
func readAttrValue(s string, i int) (value string, next int) {
	if i >= len(s) {
		return "", i
	}
	switch q := s[i]; q {
	case '\'', '"':
		end := strings.IndexByte(s[i+1:], q)
		if end < 0 {
			return s[i:], len(s) // unterminated quote; take the rest
		}
		return s[i+1 : i+1+end], i + 1 + end + 1
	default:
		start := i
		for i < len(s) && s[i] != ' ' && s[i] != '\t' && s[i] != '\n' && s[i] != '\r' && s[i] != '/' {
			i++
		}
		return s[start:i], i
	}
}
