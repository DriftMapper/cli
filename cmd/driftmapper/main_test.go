package main

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
)

// stubBrowserOpen replaces browserOpen for the duration of the test,
// recording the URL it was called with instead of spawning a real browser.
// A non-nil err is returned from every call, to simulate a headless/SSH
// environment with no browser available.
func stubBrowserOpen(t *testing.T, err error) *string {
	t.Helper()
	var got string
	orig := browserOpen
	browserOpen = func(url string) error {
		got = url
		return err
	}
	t.Cleanup(func() { browserOpen = orig })
	return &got
}

func TestRunCompare_OpensAndPrintsURL(t *testing.T) {
	t.Setenv("DRIFTMAPPER_DASHBOARD_URL", "https://app.driftmapper.test")
	opened := stubBrowserOpen(t, nil)

	var stdout, stderr bytes.Buffer
	code := runCompare(context.Background(), []string{"build-a", "build-b"}, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
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

func TestRunCompare_PassesOptionalLabels(t *testing.T) {
	t.Setenv("DRIFTMAPPER_DASHBOARD_URL", "https://app.driftmapper.test")
	opened := stubBrowserOpen(t, nil)

	var stdout, stderr bytes.Buffer
	code := runCompare(context.Background(), []string{
		"-a-url", "https://staging.example.test/",
		"-b-url", "https://prod.example.test/",
		"build-a", "build-b",
	}, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	u, err := url.Parse(*opened)
	if err != nil {
		t.Fatalf("opened URL %q did not parse: %v", *opened, err)
	}
	if got := u.Query().Get("a_url"); got != "https://staging.example.test/" {
		t.Errorf("a_url = %q, want https://staging.example.test/", got)
	}
	if got := u.Query().Get("b_url"); got != "https://prod.example.test/" {
		t.Errorf("b_url = %q, want https://prod.example.test/", got)
	}
}

func TestRunCompare_FallsBackToPrintingURLOnOpenFailure(t *testing.T) {
	t.Setenv("DRIFTMAPPER_DASHBOARD_URL", "https://app.driftmapper.test")
	stubBrowserOpen(t, errors.New("no browser available"))

	var stdout, stderr bytes.Buffer
	code := runCompare(context.Background(), []string{"build-a", "build-b"}, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0 (open failure is not fatal)", code)
	}
	if !strings.Contains(stdout.String(), "/compare") {
		t.Errorf("stdout = %q, want it to still contain the compare URL", stdout.String())
	}
	if !strings.Contains(stderr.String(), "could not open browser") {
		t.Errorf("stderr = %q, want a note that the browser could not be opened", stderr.String())
	}
}

func TestRunCompare_RequiresDashboardURL(t *testing.T) {
	t.Setenv("DRIFTMAPPER_DASHBOARD_URL", "")
	stubBrowserOpen(t, nil)

	var stdout, stderr bytes.Buffer
	code := runCompare(context.Background(), []string{"build-a", "build-b"}, &stdout, &stderr)

	if code != 2 {
		t.Errorf("exit code = %d, want 2 (error)", code)
	}
	if !strings.Contains(stderr.String(), "DRIFTMAPPER_DASHBOARD_URL") {
		t.Errorf("stderr = %q, want a mention of DRIFTMAPPER_DASHBOARD_URL", stderr.String())
	}
}

func TestRunCompare_RequiresTwoArgs(t *testing.T) {
	t.Setenv("DRIFTMAPPER_DASHBOARD_URL", "https://app.driftmapper.test")
	stubBrowserOpen(t, nil)

	var stdout, stderr bytes.Buffer
	code := runCompare(context.Background(), []string{"build-a"}, &stdout, &stderr)

	if code != 2 {
		t.Errorf("exit code = %d, want 2 (usage error)", code)
	}
}
