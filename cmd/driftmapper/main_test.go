package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func buildInfoServer(t *testing.T, buildID string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<!doctype html>
<meta name="driftmapper:schema-version" content="1">
<meta name="driftmapper:build-id" content="%s">
<meta name="driftmapper:resolution-url" content="https://driftmapper.test/r/%s">
`, buildID, buildID)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// stubBrowserOpen replaces browserOpen for the duration of the test,
// recording the URL it was called with instead of spawning a real browser.
func stubBrowserOpen(t *testing.T) *string {
	t.Helper()
	var got string
	orig := browserOpen
	browserOpen = func(url string) error {
		got = url
		return nil
	}
	t.Cleanup(func() { browserOpen = orig })
	return &got
}

func TestRunCompare_Open_PrintsAndOpensURL(t *testing.T) {
	t.Setenv("DRIFTMAPPER_DASHBOARD_URL", "https://app.driftmapper.test")
	opened := stubBrowserOpen(t)

	a := buildInfoServer(t, "build-a")
	b := buildInfoServer(t, "build-b")

	var stdout, stderr bytes.Buffer
	code := runCompare(context.Background(), []string{"-open", a.URL, b.URL}, &stdout, &stderr)

	if code != 1 {
		t.Errorf("exit code = %d, want 1 (drift)", code)
	}
	if *opened == "" {
		t.Fatal("browserOpen was not called")
	}
	u, err := url.Parse(*opened)
	if err != nil {
		t.Fatalf("opened URL %q did not parse: %v", *opened, err)
	}
	if u.Path != "/compare" {
		t.Errorf("path = %q, want /compare", u.Path)
	}
	if got := u.Query().Get("a"); got != "build-a" {
		t.Errorf("a = %q, want build-a", got)
	}
	if got := u.Query().Get("b"); got != "build-b" {
		t.Errorf("b = %q, want build-b", got)
	}
	if !strings.Contains(stdout.String(), *opened) {
		t.Errorf("stdout = %q, want it to contain the opened URL %q", stdout.String(), *opened)
	}
}

func TestRunCompare_Open_ErrorsWithoutDashboardURL(t *testing.T) {
	t.Setenv("DRIFTMAPPER_DASHBOARD_URL", "")
	stubBrowserOpen(t)

	a := buildInfoServer(t, "build-a")
	b := buildInfoServer(t, "build-a")

	var stdout, stderr bytes.Buffer
	code := runCompare(context.Background(), []string{"-open", a.URL, b.URL}, &stdout, &stderr)

	if code != 2 {
		t.Errorf("exit code = %d, want 2 (error)", code)
	}
	if !strings.Contains(stderr.String(), "DRIFTMAPPER_DASHBOARD_URL") {
		t.Errorf("stderr = %q, want a mention of DRIFTMAPPER_DASHBOARD_URL", stderr.String())
	}
}

func TestRunCompare_RejectsJSONAndOpenTogether(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runCompare(context.Background(), []string{"-json", "-open", "http://a.test", "http://b.test"}, &stdout, &stderr)

	if code != 2 {
		t.Errorf("exit code = %d, want 2 (usage error)", code)
	}
}
