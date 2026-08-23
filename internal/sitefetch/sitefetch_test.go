package sitefetch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func testFetcher(t *testing.T, srv *httptest.Server, headers []Header) *Fetcher {
	t.Helper()
	f := &Fetcher{
		Client:  srv.Client(),
		Timeout: 5 * time.Second,
		Headers: headers,
		CheckScheme: func(u *url.URL) error {
			if u.Scheme != "http" && u.Scheme != "https" {
				return ErrNotHTTPS
			}
			return nil
		},
	}
	f.Client.CheckRedirect = f.checkRedirect
	return f
}

func TestDo_FetchesBodyAndReportsFinalURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != "text/html" {
			t.Errorf("Accept = %q, want text/html", got)
		}
		if r.Header.Get("X-Staging-Token") != "sekrit" {
			t.Error("custom header was not sent on the initial request")
		}
		w.Write([]byte("<html>build-info</html>"))
	}))
	defer srv.Close()

	res, err := testFetcher(t, srv, []Header{{Name: "X-Staging-Token", Value: "sekrit"}}).Do(context.Background(), srv.URL+"/build-info.html")
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if string(res.Body) != "<html>build-info</html>" {
		t.Errorf("body = %q", res.Body)
	}
	if !strings.HasSuffix(res.URL, "/build-info.html") {
		t.Errorf("URL = %q, want the requested path", res.URL)
	}
}

func TestDo_SameOriginRedirectKeepsCustomHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		if r.Header.Get("X-Staging-Token") == "" {
			t.Error("custom header was dropped on a same-origin redirect")
		}
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	res, err := testFetcher(t, srv, []Header{{Name: "X-Staging-Token", Value: "sekrit"}}).Do(context.Background(), srv.URL+"/start")
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if res.URL != srv.URL+"/final" {
		t.Errorf("URL = %q, want %s/final", res.URL, srv.URL)
	}
}

func TestDo_CrossOriginRedirectStripsCustomHeaders(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, otherURL+"/final", http.StatusFound)
	}))
	defer origin.Close()
	var leaked bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("Authorization") != ""
		w.Write([]byte("ok"))
	}))
	defer other.Close()
	otherURL = other.URL

	if _, err := testFetcher(t, origin, []Header{{Name: "Authorization", Value: "Bearer staging-secret"}}).Do(context.Background(), origin.URL); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if leaked {
		t.Error("Authorization header followed a cross-origin redirect — secrets must not leak off-host")
	}
}

func TestDo_RefusesDowngradeRedirect(t *testing.T) {
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plainURL+"/escape", http.StatusFound)
	}))
	defer tlsSrv.Close()
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the downgraded target must never actually be contacted")
	}))
	defer plain.Close()
	plainURL = plain.URL

	f := &Fetcher{Client: tlsSrv.Client(), Timeout: 5 * time.Second, CheckScheme: RequireHTTPS}
	f.Client.CheckRedirect = f.checkRedirect

	_, err := f.Do(context.Background(), tlsSrv.URL)
	if !errors.Is(err, ErrNotHTTPS) {
		t.Fatalf("err = %v, want it to wrap ErrNotHTTPS", err)
	}
}

func TestDo_RefusesPlainHTTPUpfront(t *testing.T) {
	f := New(nil)
	if _, err := f.Do(context.Background(), "http://example.test/build-info.html"); !errors.Is(err, ErrNotHTTPS) {
		t.Fatalf("err = %v, want it to wrap ErrNotHTTPS", err)
	}
}

func TestDo_Non200IsStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := testFetcher(t, srv, nil).Do(context.Background(), srv.URL)
	var statusErr *StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("err = %v (%T), want *StatusError", err, err)
	}
	if statusErr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want 404", statusErr.StatusCode)
	}
}

func TestDo_EnforcesSizeCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, MaxBodyBytes+1))
	}))
	defer srv.Close()

	_, err := testFetcher(t, srv, nil).Do(context.Background(), srv.URL)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want it to wrap ErrTooLarge", err)
	}
}

func TestDo_BoundsRedirectHops(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/next", http.StatusFound)
	}))
	defer srv.Close()

	_, err := testFetcher(t, srv, nil).Do(context.Background(), srv.URL+"/loop")
	if !errors.Is(err, ErrTooManyRedirects) {
		t.Fatalf("err = %v, want it to wrap ErrTooManyRedirects", err)
	}
}

func TestDo_TimeoutBoundsTheWholeFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.Write([]byte("too late"))
	}))
	defer srv.Close()

	f := testFetcher(t, srv, nil)
	f.Timeout = 50 * time.Millisecond
	_, err := f.Do(context.Background(), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("err = %v, want a deadline-exceeded failure", err)
	}
}

// The package-level vars below exist only so two-server tests can point
// at each other's URLs inside handler closures declared before both
// servers exist.
var (
	otherURL string
	plainURL string
)
