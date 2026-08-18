package compare

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
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

func TestRun_MatchWhenBuildIDsEqual(t *testing.T) {
	a := buildInfoServer(t, "same-build")
	b := buildInfoServer(t, "same-build")

	result, err := Run(context.Background(), http.DefaultClient, a.URL, b.URL)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Match {
		t.Error("Match = false, want true for identical build IDs")
	}
}

func TestRun_NoMatchWhenBuildIDsDiffer(t *testing.T) {
	a := buildInfoServer(t, "build-a")
	b := buildInfoServer(t, "build-b")

	result, err := Run(context.Background(), http.DefaultClient, a.URL, b.URL)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Match {
		t.Error("Match = true, want false for different build IDs")
	}
	if result.A.Info.BuildInstanceID != "build-a" || result.B.Info.BuildInstanceID != "build-b" {
		t.Errorf("A/B build IDs = %q/%q, want build-a/build-b", result.A.Info.BuildInstanceID, result.B.Info.BuildInstanceID)
	}
}

func TestRun_ErrorsOnNonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	ok := buildInfoServer(t, "build-a")

	if _, err := Run(context.Background(), http.DefaultClient, srv.URL, ok.URL); err == nil {
		t.Error("Run against a 404: want error, got nil")
	}
}

func TestRun_ErrorsOnUnparsableBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><body>not a build-info file</body></html>`)
	}))
	defer srv.Close()
	ok := buildInfoServer(t, "build-a")

	if _, err := Run(context.Background(), http.DefaultClient, srv.URL, ok.URL); err == nil {
		t.Error("Run against a non-build-info page: want error, got nil")
	}
}
