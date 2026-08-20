package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/driftmapper/cli/internal/apiclient"
	"github.com/driftmapper/protocol"
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

// authorizeServer stubs POST /v1/repositories/authorize, returning status/
// body for every request regardless of the challenge value presented.
func authorizeServer(t *testing.T, status int, body map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestMaybeAuthorize_NoOpWhenChallengeEmpty(t *testing.T) {
	// No server at all — a request here would fail to connect and this test
	// would fail, proving the empty-challenge path makes no network call.
	client := apiclient.New("http://127.0.0.1:0", "tok")

	var stdout bytes.Buffer
	if err := maybeAuthorize(context.Background(), &stdout, client, ""); err != nil {
		t.Fatalf("maybeAuthorize: %v", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty (no-op)", stdout.String())
	}
}

func TestMaybeAuthorize_SuccessPrintsRemovalReminder(t *testing.T) {
	srv := authorizeServer(t, http.StatusCreated, map[string]any{
		"data": protocol.RepositoryAuthorization{RepositoryId: "repo1", OrganizationId: "org1"},
	})
	client := apiclient.New(srv.URL, "tok")

	var stdout bytes.Buffer
	if err := maybeAuthorize(context.Background(), &stdout, client, "chal_abc"); err != nil {
		t.Fatalf("maybeAuthorize: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "repo1") || !strings.Contains(out, "org1") {
		t.Errorf("stdout = %q, want it to mention repo1 and org1", out)
	}
	if !strings.Contains(out, "DRIFTMAPPER_CHALLENGE") {
		t.Errorf("stdout = %q, want a reminder to remove DRIFTMAPPER_CHALLENGE", out)
	}
}

func TestMaybeAuthorize_FailsLoudOnInvalidChallenge(t *testing.T) {
	srv := authorizeServer(t, http.StatusForbidden, map[string]any{
		"error": map[string]any{"code": "invalid_challenge", "message": "Invalid or expired challenge."},
	})
	client := apiclient.New(srv.URL, "tok")

	var stdout bytes.Buffer
	err := maybeAuthorize(context.Background(), &stdout, client, "chal_abc")
	if err == nil {
		t.Fatal("maybeAuthorize: want error, got nil")
	}
	if !strings.Contains(err.Error(), "authorize repository") || !strings.Contains(err.Error(), "invalid_challenge") {
		t.Errorf("err = %q, want it to mention both \"authorize repository\" and \"invalid_challenge\"", err.Error())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty on failure (no removal reminder)", stdout.String())
	}
}

func TestMaybeAuthorize_FailsLoudOnRepositoryAlreadyBound(t *testing.T) {
	srv := authorizeServer(t, http.StatusConflict, map[string]any{
		"error": map[string]any{"code": "repository_already_bound", "message": "This repository is already bound to a different organization."},
	})
	client := apiclient.New(srv.URL, "tok")

	err := maybeAuthorize(context.Background(), &bytes.Buffer{}, client, "chal_abc")
	if err == nil {
		t.Fatal("maybeAuthorize: want error, got nil")
	}
	if !strings.Contains(err.Error(), "repository_already_bound") {
		t.Errorf("err = %q, want it to mention repository_already_bound", err.Error())
	}
}
