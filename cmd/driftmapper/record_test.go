package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/driftmapper/cli/internal/apiclient"
	"github.com/driftmapper/protocol"
)

// stubToken replaces acquireToken for the test so no CI provider is needed.
func stubToken(t *testing.T, token string, err error) {
	t.Helper()
	orig := acquireToken
	acquireToken = func(context.Context) (string, error) { return token, err }
	t.Cleanup(func() { acquireToken = orig })
}

// recordServer answers POST /v1/deployments with status and body, handing the decoded
// request body and raw JSON object to the test.
func recordServer(t *testing.T, status int, resp any) (url string, raw *map[string]any) {
	t.Helper()
	got := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/deployments" || r.Method != http.MethodPost {
			t.Errorf("request = %s %s, want POST /v1/deployments", r.Method, r.URL.Path)
		}
		if h := r.Header.Get("Authorization"); h != "Bearer tok" {
			t.Errorf("Authorization = %q, want Bearer tok", h)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &got
}

func deploymentOK() map[string]any {
	return map[string]any{"data": protocol.Deployment{
		App: "shop", Module: "api", Environment: "production", BuildInstanceId: "b1",
		CommitSha: "abcdef1234567", DeployedAt: time.Now(),
	}}
}

func clearRecordEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"DRIFTMAPPER_ENVIRONMENT", "DRIFTMAPPER_APP", "DRIFTMAPPER_MODULE", "DRIFTMAPPER_COMMIT", "DRIFTMAPPER_BUILD_ID", "DRIFTMAPPER_DEPLOYMENT_URL"} {
		t.Setenv(k, "")
	}
}

func TestRunRecord_SendsOnlyWhatWasGivenAndPrintsTheResult(t *testing.T) {
	clearRecordEnv(t)
	stubToken(t, "tok", nil)
	url, raw := recordServer(t, http.StatusCreated, deploymentOK())
	t.Setenv("DRIFTMAPPER_API_URL", url)

	var stdout, stderr bytes.Buffer
	code := runRecord(context.Background(), []string{"--environment", "production"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if len(*raw) != 1 || (*raw)["environment"] != "production" {
		t.Errorf("request body = %v, want only environment (the server applies the other defaults)", *raw)
	}
	if out := stdout.String(); !strings.Contains(out, "Recorded shop/api abcdef1 in production (build b1)") {
		t.Errorf("stdout = %q", out)
	}
}

func TestRunRecord_FlagsBeatEnvironmentVariables(t *testing.T) {
	clearRecordEnv(t)
	stubToken(t, "tok", nil)
	url, raw := recordServer(t, http.StatusCreated, deploymentOK())
	t.Setenv("DRIFTMAPPER_API_URL", url)
	t.Setenv("DRIFTMAPPER_ENVIRONMENT", "from-env")
	t.Setenv("DRIFTMAPPER_COMMIT", "1111111")
	t.Setenv("DRIFTMAPPER_APP", "app-env")
	t.Setenv("DRIFTMAPPER_MODULE", "module-env")
	t.Setenv("DRIFTMAPPER_DEPLOYMENT_URL", "https://env.example.com/")

	var stdout, stderr bytes.Buffer
	code := runRecord(context.Background(), []string{"--environment", "from-flag", "--url", "https://flag.example.com/"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	want := map[string]any{
		"environment": "from-flag", "commit_sha": "1111111", "app": "app-env", "module": "module-env",
		"url": "https://flag.example.com/",
	}
	for k, v := range want {
		if (*raw)[k] != v {
			t.Errorf("%s = %v, want %v (body %v)", k, (*raw)[k], v, *raw)
		}
	}
}

func TestRunRecord_NeverSendsATimeOrBothBuildSelectors(t *testing.T) {
	clearRecordEnv(t)
	stubToken(t, "tok", nil)
	url, raw := recordServer(t, http.StatusOK, deploymentOK())
	t.Setenv("DRIFTMAPPER_API_URL", url)

	var stdout, stderr bytes.Buffer
	if code := runRecord(context.Background(), []string{"--environment", "production", "--build-id", "b1"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	for _, k := range []string{"deployed_at", "created_at", "commit_sha"} {
		if _, ok := (*raw)[k]; ok {
			t.Errorf("request carried %q: %v", k, *raw)
		}
	}
	if !strings.Contains(stdout.String(), "Already recorded") {
		t.Errorf("a 200 should read as already recorded: %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code := runRecord(context.Background(), []string{"--build-id", "b1", "--commit", "abcdef1"}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "mutually exclusive") {
		t.Errorf("both selectors: exit = %d, stderr = %q, want 2 and mutually exclusive", code, stderr.String())
	}
}

func TestRunRecord_NeedsACIToken(t *testing.T) {
	clearRecordEnv(t)
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
	var stdout, stderr bytes.Buffer
	code := runRecord(context.Background(), []string{"--environment", "production"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "from CI only") {
		t.Errorf("exit = %d, stderr = %q, want 1 and the CI-only message", code, stderr.String())
	}
}

func TestRunRecord_RejectsStrayArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runRecord(context.Background(), []string{"production"}, &stdout, &stderr); code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
}

func TestRunRecord_FailuresExitNonZeroWithGuidance(t *testing.T) {
	clearRecordEnv(t)
	stubToken(t, "tok", nil)
	for _, c := range []struct {
		name, code, status, wantInStderr string
		httpStatus                       int
	}{
		{"unregistered build", "build_not_registered", "", "run `driftmapper` there first", http.StatusUnprocessableEntity},
		{"unbound repository", "no_live_policy", "", "Add a repository", http.StatusForbidden},
		{"validation", "validation", "", "validation:", http.StatusUnprocessableEntity},
	} {
		url, _ := recordServer(t, c.httpStatus, map[string]any{"error": map[string]any{"code": c.code, "message": "boom"}})
		t.Setenv("DRIFTMAPPER_API_URL", url)
		var stdout, stderr bytes.Buffer
		code := runRecord(context.Background(), []string{"--environment", "production"}, &stdout, &stderr)
		if code != 1 || !strings.Contains(stderr.String(), c.wantInStderr) {
			t.Errorf("%s: exit = %d, stderr = %q, want 1 containing %q", c.name, code, stderr.String(), c.wantInStderr)
		}
	}

	url, _ := recordServer(t, http.StatusForbidden, map[string]any{"error": map[string]any{
		"code": "claim_mismatch", "message": "nope", "details": map[string]string{"mismatched_claim": "module", "expected": "e", "actual": "a"}}})
	t.Setenv("DRIFTMAPPER_API_URL", url)
	var stdout, stderr bytes.Buffer
	if code := runRecord(context.Background(), []string{"--environment", "production"}, &stdout, &stderr); code != 1 ||
		!strings.Contains(stderr.String(), "only the repository that registered a module") {
		t.Errorf("module mismatch: exit = %d, stderr = %q", code, stderr.String())
	}
}

func TestRecordDeploymentError_WrapsOtherErrorsGenerically(t *testing.T) {
	plain := errors.New("connection refused")
	if err := recordDeploymentError(plain); !errors.Is(err, plain) || !strings.HasPrefix(err.Error(), "record deployment:") {
		t.Errorf("err = %v", err)
	}
	apiErr := &apiclient.Error{Code: "rate_limited", Message: "slow down"}
	if err := recordDeploymentError(apiErr); !errors.Is(err, apiErr) {
		t.Errorf("err = %v, want it to wrap the api error", err)
	}
}
