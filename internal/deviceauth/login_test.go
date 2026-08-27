package deviceauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeHub simulates hub's three device-code endpoints for Login/AccessToken
// tests. approveAfterPolls > 0 makes /device/token return
// authorization_pending for that many polls before succeeding, so Login's
// retry loop gets real exercise.
type fakeHub struct {
	approveAfterPolls int32
	polls             atomic.Int32
	refreshCalls      atomic.Int32
	failRefresh       bool
}

func (f *fakeHub) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/device/code", func(w http.ResponseWriter, r *http.Request) {
		writeData(w, http.StatusOK, Pending{
			DeviceCode: "raw-device-code", UserCode: "ABCD-1234",
			VerificationURI: "http://hub.test/device", VerificationURIComplete: "http://hub.test/device?user_code=ABCD-1234",
			ExpiresIn: 600, Interval: 0, // 0 -> Login's 5s default; tests below override via short-circuit polling
		})
	})
	mux.HandleFunc("/device/token", func(w http.ResponseWriter, r *http.Request) {
		n := f.polls.Add(1)
		if n <= f.approveAfterPolls {
			writeError(w, http.StatusBadRequest, "authorization_pending", "waiting")
			return
		}
		writeData(w, http.StatusOK, Token{AccessToken: "access-1", SealedSession: "sealed-1"})
	})
	mux.HandleFunc("/device/refresh", func(w http.ResponseWriter, r *http.Request) {
		f.refreshCalls.Add(1)
		if f.failRefresh {
			writeError(w, http.StatusUnauthorized, "session_invalid", "expired")
			return
		}
		var req struct {
			SealedSession string `json:"sealed_session"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		writeData(w, http.StatusOK, Token{AccessToken: "access-refreshed", SealedSession: "sealed-refreshed:" + req.SealedSession})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func writeData(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": message}})
}

// withFastPolling swaps pollDelay to near-zero for the duration of one
// test, so Login's real retry loop runs without multi-second sleeps.
func withFastPolling(t *testing.T) {
	t.Helper()
	old := pollDelay
	pollDelay = func(int) time.Duration { return time.Millisecond }
	t.Cleanup(func() { pollDelay = old })
}

func TestLogin_Success(t *testing.T) {
	t.Setenv("DRIFTMAPPER_CONFIG_DIR", t.TempDir())
	withFastPolling(t)
	f := &fakeHub{approveAfterPolls: 2}
	srv := f.server(t)

	var opened string
	var stdout bytes.Buffer
	err := Login(context.Background(), srv.URL, func(url string) error { opened = url; return nil }, &stdout)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if opened != "http://hub.test/device?user_code=ABCD-1234" {
		t.Errorf("openBrowser called with %q", opened)
	}
	if !strings.Contains(stdout.String(), "ABCD-1234") {
		t.Errorf("stdout = %q, want it to contain the user code", stdout.String())
	}
	if f.polls.Load() < 3 {
		t.Errorf("polls = %d, want at least 3 (2 pending + 1 success)", f.polls.Load())
	}

	creds, err := Load()
	if err != nil {
		t.Fatalf("Load after Login: %v", err)
	}
	if creds == nil || creds.SealedSession != "sealed-1" {
		t.Fatalf("creds = %+v, want SealedSession=sealed-1", creds)
	}
}

func TestLogin_BrowserOpenFailsButFlowStillCompletes(t *testing.T) {
	t.Setenv("DRIFTMAPPER_CONFIG_DIR", t.TempDir())
	withFastPolling(t)
	f := &fakeHub{approveAfterPolls: 0}
	srv := f.server(t)

	var stdout bytes.Buffer
	err := Login(context.Background(), srv.URL, func(string) error { return errors.New("no display") }, &stdout)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if !strings.Contains(stdout.String(), "open this URL yourself") {
		t.Errorf("stdout = %q, want a fallback URL message", stdout.String())
	}
}

func TestAccessToken_NotLoggedIn(t *testing.T) {
	t.Setenv("DRIFTMAPPER_CONFIG_DIR", t.TempDir())

	_, err := AccessToken(context.Background(), "http://unused.test")
	if !errors.Is(err, ErrNotLoggedIn) {
		t.Errorf("err = %v, want ErrNotLoggedIn", err)
	}
}

func TestAccessToken_RefreshesAndPersistsRotatedSession(t *testing.T) {
	t.Setenv("DRIFTMAPPER_CONFIG_DIR", t.TempDir())
	if err := Save(&Credentials{SealedSession: "sealed-old"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	f := &fakeHub{}
	srv := f.server(t)

	tok, err := AccessToken(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if tok != "access-refreshed" {
		t.Errorf("token = %q, want access-refreshed", tok)
	}
	if f.refreshCalls.Load() != 1 {
		t.Errorf("refreshCalls = %d, want 1", f.refreshCalls.Load())
	}

	creds, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if creds == nil || creds.SealedSession != "sealed-refreshed:sealed-old" {
		t.Errorf("persisted creds = %+v, want the rotated sealed session", creds)
	}
}

func TestAccessToken_ExpiredSession(t *testing.T) {
	t.Setenv("DRIFTMAPPER_CONFIG_DIR", t.TempDir())
	if err := Save(&Credentials{SealedSession: "sealed-dead"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	f := &fakeHub{failRefresh: true}
	srv := f.server(t)

	_, err := AccessToken(context.Background(), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "run `driftmapper login` again") {
		t.Errorf("err = %v, want an expired-session message", err)
	}
}
