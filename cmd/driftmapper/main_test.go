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
	"github.com/driftmapper/cli/internal/sitefetch"
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

func TestRegisterBuildError_NoLivePolicyGetsActionableGuidance(t *testing.T) {
	apiErr := &apiclient.Error{StatusCode: http.StatusForbidden, Code: "no_live_policy", Message: "This repository has no live trusted-workload policy."}
	err := registerBuildError(apiErr)
	if err == nil {
		t.Fatal("registerBuildError: want error, got nil")
	}
	for _, want := range []string{"register build", apiErr.Message, "Add a repository", "DRIFTMAPPER_CHALLENGE"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to contain %q", err.Error(), want)
		}
	}
}

func TestRegisterBuildError_OtherCodesWrapGenerically(t *testing.T) {
	apiErr := &apiclient.Error{StatusCode: http.StatusForbidden, Code: "policy_revoked", Message: "The trusted-workload policy for this repository was revoked."}
	err := registerBuildError(apiErr)
	if err == nil {
		t.Fatal("registerBuildError: want error, got nil")
	}
	if !strings.Contains(err.Error(), "register build") || !strings.Contains(err.Error(), "policy_revoked") {
		t.Errorf("err = %q, want it to mention both \"register build\" and \"policy_revoked\"", err.Error())
	}
	if strings.Contains(err.Error(), "DRIFTMAPPER_CHALLENGE") {
		t.Errorf("err = %q, want no challenge guidance for a non-no_live_policy error", err.Error())
	}
}

// deploymentServer stubs POST /v1/deployments, returning status/body for
// every request regardless of the payload presented.
func deploymentServer(t *testing.T, status int, body map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRecordDeployment_SuccessPrintsConfirmation(t *testing.T) {
	srv := deploymentServer(t, http.StatusCreated, map[string]any{
		"data": protocol.Deployment{BuildInstanceId: "build1", Environment: "production"},
	})
	client := apiclient.New(srv.URL, "tok")

	var stdout bytes.Buffer
	if err := recordDeployment(context.Background(), &stdout, client, "abc1234", "production"); err != nil {
		t.Fatalf("recordDeployment: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "Recorded") || !strings.Contains(out, "build1") || !strings.Contains(out, "production") {
		t.Errorf("stdout = %q, want it to mention Recorded, build1, and production", out)
	}
}

func TestRecordDeployment_IdempotentRetryPrintsDistinctVerb(t *testing.T) {
	srv := deploymentServer(t, http.StatusOK, map[string]any{
		"data": protocol.Deployment{BuildInstanceId: "build1", Environment: "production"},
	})
	client := apiclient.New(srv.URL, "tok")

	var stdout bytes.Buffer
	if err := recordDeployment(context.Background(), &stdout, client, "abc1234", "production"); err != nil {
		t.Fatalf("recordDeployment: %v", err)
	}
	if !strings.Contains(stdout.String(), "Already recorded") {
		t.Errorf("stdout = %q, want it to mention \"Already recorded\"", stdout.String())
	}
}

func TestRecordDeployment_FailsLoudOnError(t *testing.T) {
	srv := deploymentServer(t, http.StatusUnprocessableEntity, map[string]any{
		"error": map[string]any{"code": "validation", "message": "environment must be 1-63 characters..."},
	})
	client := apiclient.New(srv.URL, "tok")

	var stdout bytes.Buffer
	err := recordDeployment(context.Background(), &stdout, client, "abc1234", "Not Valid")
	if err == nil {
		t.Fatal("recordDeployment: want error, got nil")
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty on failure", stdout.String())
	}
}

func TestDeployError_NoLivePolicyGetsActionableGuidance(t *testing.T) {
	apiErr := &apiclient.Error{StatusCode: http.StatusForbidden, Code: "no_live_policy", Message: "This repository has no live trusted-workload policy."}
	err := deployError(apiErr)
	if err == nil {
		t.Fatal("deployError: want error, got nil")
	}
	for _, want := range []string{"record deployment", apiErr.Message, "Add a repository", "DRIFTMAPPER_CHALLENGE"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to contain %q", err.Error(), want)
		}
	}
}

func TestDeployError_NotFoundGetsActionableGuidance(t *testing.T) {
	apiErr := &apiclient.Error{StatusCode: http.StatusNotFound, Code: "not_found", Message: "No build is registered for this commit."}
	err := deployError(apiErr)
	if err == nil {
		t.Fatal("deployError: want error, got nil")
	}
	for _, want := range []string{"record deployment", apiErr.Message, "no build is registered"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to contain %q", err.Error(), want)
		}
	}
}

func TestDeployError_OtherCodesWrapGenerically(t *testing.T) {
	apiErr := &apiclient.Error{StatusCode: http.StatusForbidden, Code: "claim_mismatch", Message: "claim mismatch on workflow"}
	err := deployError(apiErr)
	if err == nil {
		t.Fatal("deployError: want error, got nil")
	}
	if !strings.Contains(err.Error(), "record deployment") || !strings.Contains(err.Error(), "claim_mismatch") {
		t.Errorf("err = %q, want it to mention both \"record deployment\" and \"claim_mismatch\"", err.Error())
	}
	if strings.Contains(err.Error(), "DRIFTMAPPER_CHALLENGE") {
		t.Errorf("err = %q, want no challenge guidance for a non-no_live_policy error", err.Error())
	}
}

func TestRunDeploy_RequiresEnvFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runDeploy(context.Background(), []string{"-commit", "abc1234"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("exit code = %d, want 2 (usage error)", code)
	}
}

func TestRunDeploy_RequiresCommit(t *testing.T) {
	t.Setenv("GITHUB_SHA", "") // no -commit flag and no fallback env var

	var stdout, stderr bytes.Buffer
	code := runDeploy(context.Background(), []string{"-env", "production"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("exit code = %d, want 2 (usage error)", code)
	}
}

func TestRunDeploy_CommitDefaultsToGitHubSHA(t *testing.T) {
	t.Setenv("GITHUB_SHA", "abc1234")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "")

	var stdout, stderr bytes.Buffer
	// -commit is unset, but $GITHUB_SHA satisfies the requiredness check —
	// reaching the OIDC step (and failing there) proves the flag default
	// resolved, rather than the usage error firing first.
	code := runDeploy(context.Background(), []string{"-env", "production"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("exit code = %d, want 1 (past usage validation, failed at OIDC)", code)
	}
	if !strings.Contains(stderr.String(), "acquire OIDC token") {
		t.Errorf("stderr = %q, want it to mention acquiring an OIDC token", stderr.String())
	}
}

func TestRunDeploy_RejectsPositionalArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runDeploy(context.Background(), []string{"-env", "production", "-commit", "abc1234", "stray-arg"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("exit code = %d, want 2 (usage error) — the build-instance-id positional arg is gone as of DRFT-92", code)
	}
}

func TestRunDeploy_FailsWithoutOIDCEnv(t *testing.T) {
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "")

	var stdout, stderr bytes.Buffer
	code := runDeploy(context.Background(), []string{"-env", "production", "-commit", "abc1234"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "acquire OIDC token") {
		t.Errorf("stderr = %q, want it to mention acquiring an OIDC token", stderr.String())
	}
}

func TestRunDeploy_BestEffortWarnsAndExitsZeroOnFailure(t *testing.T) {
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "")

	var stdout, stderr bytes.Buffer
	code := runDeploy(context.Background(), []string{"-env", "production", "-commit", "abc1234", "-best-effort"}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("exit code = %d, want 0 (-best-effort)", code)
	}
	if !strings.Contains(stderr.String(), "acquire OIDC token") {
		t.Errorf("stderr = %q, want it to still mention the underlying failure", stderr.String())
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

// --- verify (DRFT-98): resolve → fetch → parse → compare → record ----

// verifyPage renders a conforming build-info.html for buildInstanceID —
// the same shape internal/buildinfo's Generate emits.
func verifyPage(buildInstanceID string) string {
	return `<!doctype html><html><head>
<meta name="driftmapper:schema-version" content="1">
<meta name="driftmapper:build-id" content="` + buildInstanceID + `">
<meta name="driftmapper:built-at" content="2026-08-23T00:00:00Z">
<meta name="driftmapper:resolution-url" content="https://driftmapper.test/r/` + buildInstanceID + `">
</head><body></body></html>`
}

// verifyHarness wires everything one full runVerify flow touches:
// a token endpoint for oidcclient, an API stub (GET /v1/deployments/current
// answers depBodyFn(pageURL), asserting the env query param names
// "production"; cross-repo repo params land in h.repos; POST
// /v1/verifications bodies are captured into h.verifications unless
// h.failVerifications makes the stub return 500), and an HTTPS page server
// playing the deployed target — TLS because production sitefetch refuses
// plain http://127.0.0.1, so these tests exercise the real scheme policy
// rather than bypassing it. Path "/gone" on the default page always 404s,
// for fetch-failure cases.
type verifyHarness struct {
	page              *httptest.Server
	verifications     []protocol.VerificationRequest
	repos             []string
	failVerifications bool
}

func newVerifyHarness(t *testing.T, pageHandler http.HandlerFunc, depBodyFn func(pageURL string) map[string]any) *verifyHarness {
	t.Helper()

	if depBodyFn == nil {
		depBodyFn = func(string) map[string]any { return deploymentBody(nil) }
	}

	page := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/gone" {
			http.NotFound(w, r)
			return
		}
		if pageHandler != nil {
			pageHandler(w, r)
			return
		}
		w.Write([]byte(verifyPage("b-1")))
	}))
	t.Cleanup(page.Close)

	orig := verifyFetcher
	verifyFetcher = func(headers []sitefetch.Header) *sitefetch.Fetcher {
		return sitefetch.NewWithClient(page.Client(), headers)
	}
	t.Cleanup(func() { verifyFetcher = orig })

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"value": "oidc-token"})
	}))
	t.Cleanup(tokenSrv.Close)

	h := &verifyHarness{page: page}

	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/deployments/current":
			if got := r.URL.Query().Get("env"); got != "production" {
				t.Errorf("env = %q, want production (the harness's positional)", got)
			}
			h.repos = append(h.repos, r.URL.Query().Get("repo"))
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(depBodyFn(page.URL))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/verifications":
			if h.failVerifications {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]any{"code": "internal", "message": "boom"},
				})
				return
			}
			var req protocol.VerificationRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode verification body: %v", err)
			}
			h.verifications = append(h.verifications, req)
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{
				"data": protocol.Verification{
					Kind:            protocol.Verify,
					Id:              1,
					BuildInstanceId: req.BuildInstanceId,
					Environment:     req.Environment,
				},
			})
		default:
			t.Errorf("unexpected API call %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(apiSrv.Close)

	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", tokenSrv.URL+"/token")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "request-token")
	t.Setenv("DRIFTMAPPER_API_URL", apiSrv.URL)

	return h
}

// runVerifyHarness is the common incantation: build the harness whose
// recorded deployment URL comes from depBodyFn, then run runVerify for
// environment "production" with args.
func runVerifyHarness(t *testing.T, args []string, pageHandler http.HandlerFunc, depBodyFn func(string) map[string]any) (h *verifyHarness, stdout, stderr *bytes.Buffer, exit int) {
	t.Helper()
	h = newVerifyHarness(t, pageHandler, depBodyFn)
	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	exit = runVerify(context.Background(), append(args, "production"), stdout, stderr)
	return h, stdout, stderr, exit
}

func pageAt(url string) func(string) map[string]any {
	return func(string) map[string]any { return deploymentBody(&url) }
}

// deploymentBody is a well-formed GET deployment response envelope for
// build "b-1" in production, with the recorded url the caller chooses.
func deploymentBody(url *string) map[string]any {
	return map[string]any{
		"data": map[string]any{
			"id":                7,
			"kind":              "deploy",
			"repository_id":     "5678",
			"environment":       "production",
			"build_instance_id": "b-1",
			"url":               url,
			"deployed_by":       "ci:github:5678",
			"created_at":        "2026-08-23T00:00:00Z",
		},
	}
}

func TestRunVerify_VerifiedRecordsAndExitsZero(t *testing.T) {
	h, stdout, stderr, code := runVerifyHarness(t, nil, nil, func(u string) map[string]any { return deploymentBody(&u) })

	if code != 0 {
		t.Errorf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if out := stdout.String(); !strings.Contains(out, "Verified deployment 7") || !strings.Contains(out, "b-1") {
		t.Errorf("stdout = %q, want confirmation naming the deployment and build", out)
	}
	if len(h.verifications) != 1 {
		t.Fatalf("recorded %d verifications, want exactly 1", len(h.verifications))
	}
}

func TestRunVerify_EnrichesTheRecordedAssertion(t *testing.T) {
	h, _, _, code := runVerifyHarness(t, nil, nil, func(u string) map[string]any { return deploymentBody(&u) })
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if len(h.verifications) != 1 {
		t.Fatalf("recorded %d verifications, want exactly 1", len(h.verifications))
	}
	req := h.verifications[0]
	if req.BuildInstanceId != "b-1" || req.Environment != "production" {
		t.Errorf("assertion = %+v, want build b-1 in production", req)
	}
	if req.DeploymentId == nil || *req.DeploymentId != 7 {
		t.Errorf("deployment_id = %v, want 7", req.DeploymentId)
	}
	if req.Outcome == nil || *req.Outcome != protocol.VerificationRequestOutcomeVerified {
		t.Errorf("outcome = %v, want verified", req.Outcome)
	}
	if req.ObservedBuildInstanceId == nil || *req.ObservedBuildInstanceId != "b-1" {
		t.Errorf("observed_build_instance_id = %v, want b-1", req.ObservedBuildInstanceId)
	}
	if req.SourceUrl == nil || !strings.HasPrefix(*req.SourceUrl, h.page.URL) {
		t.Errorf("source_url = %v, want it to point at the fetched page", req.SourceUrl)
	}
}

func TestRunVerify_MismatchIsDriftExitThree(t *testing.T) {
	wrong := strings.Replace(verifyPage("b-1"), `build-id" content="b-1"`, `build-id" content="someone-elses-build"`, 1)
	handler := func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(wrong)) }
	h, stdout, _, code := runVerifyHarness(t, nil, handler, func(u string) map[string]any { return deploymentBody(&u) })

	if code != 3 {
		t.Errorf("exit code = %d, want 3 (the drift signal)", code)
	}
	if out := stdout.String(); !strings.Contains(out, "DRIFT") || !strings.Contains(out, "b-1") || !strings.Contains(out, "someone-elses-build") {
		t.Errorf("stdout = %q, want a DRIFT line naming expected and observed builds", out)
	}
	if len(h.verifications) != 1 {
		t.Fatalf("mismatch was not recorded before exiting (%d rows)", len(h.verifications))
	}
	if got := *h.verifications[0].Outcome; got != protocol.VerificationRequestOutcomeMismatch {
		t.Errorf("outcome = %q, want mismatch", got)
	}
	observed := h.verifications[0].ObservedBuildInstanceId
	if observed == nil || *observed != "someone-elses-build" {
		t.Errorf("observed_build_instance_id = %v, want someone-elses-build", observed)
	}
}

func TestRunVerify_BestEffortNeverSwallowsMismatch(t *testing.T) {
	wrong := strings.Replace(verifyPage("b-1"), `content="b-1"`, `content="wrong-build"`, 1)
	handler := func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(wrong)) }
	_, _, _, code := runVerifyHarness(t, []string{"-best-effort"}, handler, func(u string) map[string]any { return deploymentBody(&u) })

	if code != 3 {
		t.Errorf("exit code = %d, want 3 (-best-effort covers outages, never drift)", code)
	}
}

func TestRunVerify_FetchFailureIsRecordedThenExitsOne(t *testing.T) {
	h, _, stderr, code := runVerifyHarness(t, nil, nil, func(u string) map[string]any {
		gone := u + "/gone"
		return deploymentBody(&gone)
	})

	if code != 1 {
		t.Errorf("exit code = %d, want 1 (stderr: %s)", code, stderr.String())
	}
	if len(h.verifications) != 1 {
		t.Fatalf("fetch failure must still be recorded as an assertion (%d rows)", len(h.verifications))
	}
	req := h.verifications[0]
	if got := *req.Outcome; got != protocol.VerificationRequestOutcomeFetchFailed {
		t.Errorf("outcome = %q, want fetch_failed", got)
	}
	if req.ObservedBuildInstanceId != nil {
		t.Errorf("observed_build_instance_id = %v, want nil (nothing was parsed)", req.ObservedBuildInstanceId)
	}
}

func TestRunVerify_BestEffortSwallowsFetchFailure(t *testing.T) {
	_, _, stderr, code := runVerifyHarness(t, []string{"-best-effort"}, nil, func(u string) map[string]any {
		gone := u + "/gone"
		return deploymentBody(&gone)
	})
	if code != 0 {
		t.Errorf("exit code = %d, want 0 under -best-effort", code)
	}
	if !strings.Contains(stderr.String(), "best-effort") {
		t.Errorf("stderr = %q, want a best-effort warning", stderr.String())
	}
}

func TestRunVerify_ParseFailureIsRecordedThenExitsOne(t *testing.T) {
	junk := func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html><body>not a build-info file</body></html>"))
	}
	h, _, _, code := runVerifyHarness(t, nil, junk, func(u string) map[string]any { return deploymentBody(&u) })

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if len(h.verifications) != 1 {
		t.Fatalf("parse failure must still be recorded (%d rows)", len(h.verifications))
	}
	if got := *h.verifications[0].Outcome; got != protocol.VerificationRequestOutcomeParseFailed {
		t.Errorf("outcome = %q, want parse_failed", got)
	}
}

func TestRunVerify_UnsupportedSchemaVersionFailsLoud(t *testing.T) {
	v9 := strings.Replace(verifyPage("b-1"), `schema-version" content="1`, `schema-version" content="9`, 1)
	handler := func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(v9)) }
	h, _, stderr, code := runVerifyHarness(t, nil, handler, func(u string) map[string]any { return deploymentBody(&u) })

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "unsupported build-info schema version") {
		t.Errorf("stderr = %q, want the loud unsupported-version error", stderr.String())
	}
	if len(h.verifications) != 1 {
		t.Fatalf("parse failure must still be recorded (%d rows)", len(h.verifications))
	}
	if got := *h.verifications[0].Outcome; got != protocol.VerificationRequestOutcomeParseFailed {
		t.Errorf("outcome = %q, want parse_failed", got)
	}
}

func TestRunVerify_DeploymentWithoutURLNeedsTheFlag(t *testing.T) {
	h, _, stderr, code := runVerifyHarness(t, nil, nil, nil)

	if code != 1 {
		t.Errorf("exit code = %d, want 1 (a runtime gap, not a usage error)", code)
	}
	if !strings.Contains(stderr.String(), "no url recorded") {
		t.Errorf("stderr = %q, want actionable no-url guidance", stderr.String())
	}
	if len(h.verifications) != 0 {
		t.Errorf("nothing should be recorded when there is nothing to check (%d rows)", len(h.verifications))
	}
}

func TestRunVerify_URLOverrideRescuesURLlessDeployment(t *testing.T) {
	h := newVerifyHarness(t, nil, nil)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := runVerify(context.Background(), []string{"-url", h.page.URL + "/build-info.html", "production"}, stdout, stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if len(h.verifications) != 1 {
		t.Fatalf("want exactly 1 recorded assertion, got %d", len(h.verifications))
	}
	src := h.verifications[0].SourceUrl
	if src == nil || !strings.HasPrefix(*src, h.page.URL) {
		t.Errorf("source_url = %v, want the -url override pointing at %s", src, h.page.URL)
	}
}

func TestRunVerify_DeployedURLWinsOverConflictingFlag(t *testing.T) {
	h := newVerifyHarness(t, nil, func(u string) map[string]any {
		recorded := u + "/build-info.html"
		return deploymentBody(&recorded)
	})
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := runVerify(context.Background(), []string{"-url", "https://elsewhere.example.test/build-info.html", "production"}, stdout, stderr)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "ignoring -url") {
		t.Errorf("stderr = %q, want a warning that the flag lost to the recorded URL", stderr.String())
	}
	want := h.page.URL + "/build-info.html"
	if src := h.verifications[0].SourceUrl; src == nil || *src != want {
		t.Errorf("source_url = %v, want the deployment's own URL %q", src, want)
	}
}

func TestRunVerify_UnknownEnvironmentFailsWithGuidanceAndNoRecording(t *testing.T) {
	h := newVerifyHarness(t, nil, nil)
	api404 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"code": "not_found", "message": "nothing verifiable under that coordinate."},
		})
	}))
	defer api404.Close()
	t.Setenv("DRIFTMAPPER_API_URL", api404.URL)

	var stdout, stderr bytes.Buffer
	code := runVerify(context.Background(), []string{"prodction"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	for _, want := range []string{`environment "prodction"`, "deploy step"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
		}
	}
	if len(h.verifications) != 0 {
		t.Errorf("nothing can be recorded without a deployment (%d rows)", len(h.verifications))
	}
}

func TestRunVerify_RejectsMalformedEnvironment(t *testing.T) {
	// Note: no leading-hyphen cases here — flag.Parse consumes those as
	// flags before positional validation ever runs ("flag provided but not
	// defined"), which is its own usage error.
	for _, bad := range []string{"Prod_1", "prod/", ".", strings.Repeat("p", 64)} {
		stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		code := runVerify(context.Background(), []string{bad}, stdout, stderr)
		if code != 2 {
			t.Errorf("runVerify(%q) exit code = %d, want 2 (usage error)", bad, code)
		}
		if !strings.Contains(stderr.String(), "lowercase alphanumeric") {
			t.Errorf("stderr = %q, want the environment slug rule", stderr.String())
		}
	}
}

func TestRunVerify_RequiresExactlyOneArgument(t *testing.T) {
	for _, args := range [][]string{nil, {"production", "stray"}} {
		stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		if code := runVerify(context.Background(), args, stdout, stderr); code != 2 {
			t.Errorf("runVerify(%v) exit code = %d, want 2", args, code)
		}
	}
}

func TestRunVerify_CrossRepoFlagReachesTheAPIQuery(t *testing.T) {
	h, _, _, code := runVerifyHarness(t, []string{"-repo", "acme/checkout"}, nil, func(u string) map[string]any { return deploymentBody(&u) })
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if len(h.repos) != 1 || h.repos[0] != "acme/checkout" {
		t.Errorf("repo query params = %v, want exactly [acme/checkout]", h.repos)
	}
	if len(h.verifications) != 1 {
		t.Fatalf("recorded %d verifications, want exactly 1", len(h.verifications))
	}
}

func TestRunVerify_MismatchExitsThreeEvenWhenRecordingFails(t *testing.T) {
	for _, bestEffort := range []bool{false, true} {
		wrong := strings.Replace(verifyPage("b-1"), `content="b-1"`, `content="someone-elses-build"`, 1)
		handler := func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(wrong)) }
		h := newVerifyHarness(t, handler, func(u string) map[string]any { return deploymentBody(&u) })
		h.failVerifications = true

		args := []string{"production"}
		if bestEffort {
			args = []string{"-best-effort", "production"}
		}
		stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		code := runVerify(context.Background(), args, stdout, stderr)

		if code != 3 {
			t.Errorf("best-effort=%v: exit code = %d, want 3 (drift outranks an API outage; stderr: %s)", bestEffort, code, stderr.String())
		}
		if out := stdout.String(); !strings.Contains(out, "DRIFT") || !strings.Contains(out, "(recording failed)") {
			t.Errorf("stdout = %q, want the DRIFT line even though the row did not land", out)
		}
		if !strings.Contains(stderr.String(), "record verification") {
			t.Errorf("stderr = %q, want the record failure surfaced too", stderr.String())
		}
		if len(h.verifications) != 0 {
			t.Errorf("recorded %d rows, want 0 (the stub is down)", len(h.verifications))
		}
	}
}

func TestResolveDeploymentError_NoLivePolicyGetsActionableGuidance(t *testing.T) {
	err := resolveDeploymentError("", "production", &apiclient.Error{
		StatusCode: http.StatusForbidden,
		Code:       "no_live_policy",
		Message:    "This repository has no live trusted-workload policy.",
	})
	for _, want := range []string{`environment "production"`, "Add a repository", "DRIFTMAPPER_CHALLENGE"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to contain %q", err.Error(), want)
		}
	}
}

func TestResolveDeploymentError_OwnRepoNotFoundNamesTheTypedEnvironment(t *testing.T) {
	err := resolveDeploymentError("", "prodction", &apiclient.Error{
		StatusCode: http.StatusNotFound,
		Code:       "not_found",
		Message:    "nothing verifiable under that coordinate.",
	})
	for _, want := range []string{`environment "prodction"`, "deploy step"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to contain %q (existence hiding collapses the causes; the typed name is the fix loop)", err.Error(), want)
		}
	}
	if strings.Contains(err.Error(), "verify binding") {
		t.Errorf("err = %q, want no binding guidance for an own-repository read", err.Error())
	}
}

func TestResolveDeploymentError_CrossRepoNotFoundNamesBindingAndTarget(t *testing.T) {
	err := resolveDeploymentError("acme/checkout", "staging", &apiclient.Error{
		StatusCode: http.StatusNotFound,
		Code:       "not_found",
		Message:    "nothing verifiable under that coordinate.",
	})
	for _, want := range []string{"acme/checkout", `"staging"`, "deploy step", "verify binding"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to contain %q (existence hiding collapses four causes; the message names all recoverable ones)", err.Error(), want)
		}
	}
}

func TestRecordVerificationError_NoLivePolicyGetsActionableGuidance(t *testing.T) {
	err := recordVerificationError(&apiclient.Error{
		StatusCode: http.StatusForbidden,
		Code:       "no_live_policy",
		Message:    "This repository has no live trusted-workload policy.",
	})
	for _, want := range []string{"record verification", "Add a repository", "DRIFTMAPPER_CHALLENGE"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to contain %q", err.Error(), want)
		}
	}
}

func TestHeaderFlags_ParseAndReject(t *testing.T) {
	var hs headerFlags
	if err := hs.Set("Authorization: Bearer tok"); err != nil {
		t.Fatalf("Set valid header: %v", err)
	}
	if err := hs.Set("X-Custom: value-with-dashes"); err != nil {
		t.Fatalf("Set second header: %v", err)
	}
	if len(hs) != 2 || hs[0].Name != "Authorization" || hs[0].Value != "Bearer tok" {
		t.Errorf("headers = %+v, want Authorization/Bearer tok plus X-Custom", hs)
	}
	for _, bad := range []string{"novalue", ": valueonly", "Bad Name: v"} {
		if err := (&headerFlags{}).Set(bad); err == nil {
			t.Errorf("Set(%q): want error", bad)
		}
	}
}
