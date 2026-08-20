package compare

import (
	"net/url"
	"testing"
)

func TestOpenURL(t *testing.T) {
	result := Result{IDA: "build-a", IDB: "build-b", LabelA: "https://staging.example.test/", LabelB: "https://prod.example.test/"}

	got, err := result.OpenURL("https://app.driftmapper.test")
	if err != nil {
		t.Fatalf("OpenURL: %v", err)
	}

	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("OpenURL returned unparsable URL %q: %v", got, err)
	}
	if u.Path != "/compare" {
		t.Errorf("path = %q, want /compare", u.Path)
	}
	q := u.Query()
	want := map[string]string{
		"a":     "build-a",
		"b":     "build-b",
		"a_url": "https://staging.example.test/",
		"b_url": "https://prod.example.test/",
	}
	for k, v := range want {
		if got := q.Get(k); got != v {
			t.Errorf("query[%q] = %q, want %q", k, got, v)
		}
	}
}

func TestOpenURL_OmitsBlankLabels(t *testing.T) {
	result := Result{IDA: "build-a", IDB: "build-b"}

	got, err := result.OpenURL("https://app.driftmapper.test")
	if err != nil {
		t.Fatalf("OpenURL: %v", err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("OpenURL returned unparsable URL %q: %v", got, err)
	}
	q := u.Query()
	if q.Has("a_url") || q.Has("b_url") {
		t.Errorf("query = %v, want a_url/b_url omitted when blank", q)
	}
}

func TestOpenURL_PreservesPathPrefix(t *testing.T) {
	result := Result{IDA: "build-a", IDB: "build-b"}

	got, err := result.OpenURL("https://app.driftmapper.test/dashboard/")
	if err != nil {
		t.Fatalf("OpenURL: %v", err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("OpenURL returned unparsable URL %q: %v", got, err)
	}
	if u.Path != "/dashboard/compare" {
		t.Errorf("path = %q, want /dashboard/compare", u.Path)
	}
}

func TestOpenURL_ErrorsOnUnparsableDashboardURL(t *testing.T) {
	result := Result{IDA: "build-a", IDB: "build-b"}

	if _, err := result.OpenURL("http://[::1]:namedport"); err == nil {
		t.Error("OpenURL with an unparsable dashboard URL: want error, got nil")
	}
}
