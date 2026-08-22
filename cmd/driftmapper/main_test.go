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

// verificationServer stubs POST /v1/verifications, returning status/body
// for every request regardless of the payload presented.
func verificationServer(t *testing.T, status int, body map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRecordVerification_SuccessPrintsConfirmation(t *testing.T) {
	srv := verificationServer(t, http.StatusCreated, map[string]any{
		"data": protocol.Verification{BuildInstanceId: "build1", Environment: "production"},
	})
	client := apiclient.New(srv.URL, "tok")

	var stdout bytes.Buffer
	if err := recordVerification(context.Background(), &stdout, client, "build1", "production"); err != nil {
		t.Fatalf("recordVerification: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "Verified") || !strings.Contains(out, "build1") || !strings.Contains(out, "production") {
		t.Errorf("stdout = %q, want it to mention Verified, build1, and production", out)
	}
}

func TestRecordVerification_IdempotentRetryPrintsDistinctVerb(t *testing.T) {
	srv := verificationServer(t, http.StatusOK, map[string]any{
		"data": protocol.Verification{BuildInstanceId: "build1", Environment: "production"},
	})
	client := apiclient.New(srv.URL, "tok")

	var stdout bytes.Buffer
	if err := recordVerification(context.Background(), &stdout, client, "build1", "production"); err != nil {
		t.Fatalf("recordVerification: %v", err)
	}
	if !strings.Contains(stdout.String(), "Already verified") {
		t.Errorf("stdout = %q, want it to mention \"Already verified\"", stdout.String())
	}
}

func TestRecordVerification_FailsLoudOnError(t *testing.T) {
	srv := verificationServer(t, http.StatusUnprocessableEntity, map[string]any{
		"error": map[string]any{"code": "validation", "message": "environment must be 1-63 characters..."},
	})
	client := apiclient.New(srv.URL, "tok")

	var stdout bytes.Buffer
	err := recordVerification(context.Background(), &stdout, client, "build1", "Not Valid")
	if err == nil {
		t.Fatal("recordVerification: want error, got nil")
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty on failure", stdout.String())
	}
}

func TestVerifyError_NoLivePolicyGetsActionableGuidance(t *testing.T) {
	apiErr := &apiclient.Error{StatusCode: http.StatusForbidden, Code: "no_live_policy", Message: "This repository has no live trusted-workload policy."}
	err := verifyError(apiErr)
	if err == nil {
		t.Fatal("verifyError: want error, got nil")
	}
	for _, want := range []string{"record verification", apiErr.Message, "Add a repository", "DRIFTMAPPER_CHALLENGE"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to contain %q", err.Error(), want)
		}
	}
}

func TestVerifyError_NotFoundGetsActionableGuidance(t *testing.T) {
	apiErr := &apiclient.Error{StatusCode: http.StatusNotFound, Code: "not_found", Message: "No build is registered for this id."}
	err := verifyError(apiErr)
	if err == nil {
		t.Fatal("verifyError: want error, got nil")
	}
	for _, want := range []string{"record verification", apiErr.Message, "verify binding"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to contain %q", err.Error(), want)
		}
	}
}

func TestVerifyError_OtherCodesWrapGenerically(t *testing.T) {
	apiErr := &apiclient.Error{StatusCode: http.StatusForbidden, Code: "claim_mismatch", Message: "claim mismatch on workflow"}
	err := verifyError(apiErr)
	if err == nil {
		t.Fatal("verifyError: want error, got nil")
	}
	if !strings.Contains(err.Error(), "record verification") || !strings.Contains(err.Error(), "claim_mismatch") {
		t.Errorf("err = %q, want it to mention both \"record verification\" and \"claim_mismatch\"", err.Error())
	}
	if strings.Contains(err.Error(), "DRIFTMAPPER_CHALLENGE") {
		t.Errorf("err = %q, want no challenge guidance for a non-no_live_policy error", err.Error())
	}
}

func TestRunVerify_RequiresEnvFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runVerify(context.Background(), []string{"build1"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("exit code = %d, want 2 (usage error)", code)
	}
}

func TestRunVerify_RequiresBuildID(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runVerify(context.Background(), []string{"-env", "production"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("exit code = %d, want 2 (usage error)", code)
	}
}

func TestRunVerify_RejectsExtraArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runVerify(context.Background(), []string{"-env", "production", "build1", "stray"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("exit code = %d, want 2 (usage error)", code)
	}
}

func TestRunVerify_FailsWithoutOIDCEnv(t *testing.T) {
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "")

	var stdout, stderr bytes.Buffer
	code := runVerify(context.Background(), []string{"-env", "production", "build1"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "acquire OIDC token") {
		t.Errorf("stderr = %q, want it to mention acquiring an OIDC token", stderr.String())
	}
}

func TestRunVerify_BestEffortWarnsAndExitsZeroOnFailure(t *testing.T) {
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "")

	var stdout, stderr bytes.Buffer
	code := runVerify(context.Background(), []string{"-env", "production", "-best-effort", "build1"}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("exit code = %d, want 0 (-best-effort)", code)
	}
	if !strings.Contains(stderr.String(), "acquire OIDC token") {
		t.Errorf("stderr = %q, want it to still mention the underlying failure", stderr.String())
	}
}
