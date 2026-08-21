package apiclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/driftmapper/protocol"
)

func TestRegisterBuild_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer tok")
		}
		var body protocol.BuildRegistration
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body.CommitSha != "abc123" {
			t.Errorf("request commit_sha = %q, want %q", body.CommitSha, "abc123")
		}

		resolutionURL := "https://driftmapper.test/r/build1"
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"data": protocol.Build{
				BuildInstanceId: "build1",
				ResolutionUrl:   &resolutionURL,
			},
		})
	}))
	defer srv.Close()

	client := New(srv.URL, "tok")
	build, created, err := client.RegisterBuild(context.Background(), protocol.BuildRegistration{CommitSha: "abc123"})
	if err != nil {
		t.Fatalf("RegisterBuild: %v", err)
	}
	if !created {
		t.Error("created = false, want true (201)")
	}
	if build.BuildInstanceId != "build1" {
		t.Errorf("BuildInstanceId = %q, want %q", build.BuildInstanceId, "build1")
	}
}

func TestRegisterBuild_IdempotentRetryReturns200NotCreated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resolutionURL := "https://driftmapper.test/r/build1"
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{
			"data": protocol.Build{BuildInstanceId: "build1", ResolutionUrl: &resolutionURL},
		})
	}))
	defer srv.Close()

	client := New(srv.URL, "tok")
	_, created, err := client.RegisterBuild(context.Background(), protocol.BuildRegistration{CommitSha: "abc123"})
	if err != nil {
		t.Fatalf("RegisterBuild: %v", err)
	}
	if created {
		t.Error("created = true, want false (200 retry)")
	}
}

func TestRegisterBuild_ClaimMismatchSurfacesStructuredDetails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"code":    "claim_mismatch",
				"message": `claim mismatch on workflow: declared "ci.yml", token has "other.yml"`,
				"details": map[string]string{
					"mismatched_claim": "workflow",
					"expected":         "ci.yml",
					"actual":           "other.yml",
				},
			},
		})
	}))
	defer srv.Close()

	client := New(srv.URL, "tok")
	_, _, err := client.RegisterBuild(context.Background(), protocol.BuildRegistration{CommitSha: "abc123"})
	if err == nil {
		t.Fatal("RegisterBuild: want error, got nil")
	}
	apiErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("err is %T, want *apiclient.Error", err)
	}
	if apiErr.Code != "claim_mismatch" {
		t.Errorf("Code = %q, want %q", apiErr.Code, "claim_mismatch")
	}
	if apiErr.Details["mismatched_claim"] != "workflow" || apiErr.Details["expected"] != "ci.yml" || apiErr.Details["actual"] != "other.yml" {
		t.Errorf("Details = %+v, want mismatched_claim/expected/actual populated", apiErr.Details)
	}
}

func TestRegisterBuild_UnknownFieldRejection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"code":    "unknown_field",
				"message": "Request body contains a field the server doesn't accept.",
			},
		})
	}))
	defer srv.Close()

	client := New(srv.URL, "tok")
	_, _, err := client.RegisterBuild(context.Background(), protocol.BuildRegistration{CommitSha: "abc123"})
	apiErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("err is %T, want *apiclient.Error", err)
	}
	if apiErr.Code != "unknown_field" {
		t.Errorf("Code = %q, want %q", apiErr.Code, "unknown_field")
	}
	if apiErr.StatusCode != http.StatusBadRequest {
		t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusBadRequest)
	}
}

// withFastDeployRetries shrinks deployRetryBackoff to near-zero for the
// duration of a test, so exercising the retry path doesn't cost real
// wall-clock seconds. Restores the real schedule via t.Cleanup.
func withFastDeployRetries(t *testing.T) {
	t.Helper()
	orig := deployRetryBackoff
	deployRetryBackoff = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	t.Cleanup(func() { deployRetryBackoff = orig })
}

func TestRecordDeployment_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/v1/deployments" {
			t.Errorf("path = %q, want /v1/deployments", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer tok")
		}
		var body protocol.DeploymentRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body.CommitSha != "abc1234" || body.Environment != "production" {
			t.Errorf("request = %+v, want commit_sha=abc1234 environment=production", body)
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"data": protocol.Deployment{BuildInstanceId: "build1", Environment: "production"},
		})
	}))
	defer srv.Close()

	client := New(srv.URL, "tok")
	deployment, created, err := client.RecordDeployment(context.Background(), "abc1234", "production")
	if err != nil {
		t.Fatalf("RecordDeployment: %v", err)
	}
	if !created {
		t.Error("created = false, want true (201)")
	}
	if deployment.BuildInstanceId != "build1" || deployment.Environment != "production" {
		t.Errorf("deployment = %+v, want build_instance_id=build1 environment=production", deployment)
	}
}

func TestRecordDeployment_IdempotentRetryReturns200NotCreated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{
			"data": protocol.Deployment{BuildInstanceId: "build1", Environment: "production"},
		})
	}))
	defer srv.Close()

	client := New(srv.URL, "tok")
	_, created, err := client.RecordDeployment(context.Background(), "abc1234", "production")
	if err != nil {
		t.Fatalf("RecordDeployment: %v", err)
	}
	if created {
		t.Error("created = true, want false (200 retry)")
	}
}

// TestRecordDeployment_ErrorCodes covers each distinct failure mode
// documented for recordDeployment (openapi.yaml) — deployError in
// cmd/driftmapper only special-cases no_live_policy and not_found, but
// every code must still surface its own code/message rather than a
// generic failure. All of these are permanent 4xx codes — a single
// request each, no retry.
func TestRecordDeployment_ErrorCodes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		code   string
	}{
		{"NoLivePolicy", http.StatusForbidden, "no_live_policy"},
		{"PolicyRevoked", http.StatusForbidden, "policy_revoked"},
		{"ClaimMismatch", http.StatusForbidden, "claim_mismatch"},
		{"InvalidEnvironment", http.StatusUnprocessableEntity, "validation"},
		{"NotFound", http.StatusNotFound, "not_found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				w.WriteHeader(tc.status)
				json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]any{"code": tc.code, "message": "failure: " + tc.code},
				})
			}))
			defer srv.Close()

			client := New(srv.URL, "tok")
			_, _, err := client.RecordDeployment(context.Background(), "abc1234", "production")
			apiErr, ok := err.(*Error)
			if !ok {
				t.Fatalf("err is %T, want *apiclient.Error", err)
			}
			if apiErr.Code != tc.code {
				t.Errorf("Code = %q, want %q", apiErr.Code, tc.code)
			}
			if apiErr.StatusCode != tc.status {
				t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, tc.status)
			}
			if requests != 1 {
				t.Errorf("requests = %d, want 1 (a permanent 4xx must not be retried)", requests)
			}
		})
	}
}

// TestRecordDeployment_RetriesOn5xxThenSucceeds and
// TestRecordDeployment_RetriesOn429ThenSucceeds cover isTransient's two
// retryable cases. TestRecordDeployment_GivesUpAfterExhaustingRetries
// confirms the schedule is bounded, not infinite.
func TestRecordDeployment_RetriesOn5xxThenSucceeds(t *testing.T) {
	withFastDeployRetries(t)
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "unavailable", "message": "try again"}})
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"data": protocol.Deployment{BuildInstanceId: "build1", Environment: "production"},
		})
	}))
	defer srv.Close()

	client := New(srv.URL, "tok")
	_, created, err := client.RecordDeployment(context.Background(), "abc1234", "production")
	if err != nil {
		t.Fatalf("RecordDeployment: %v", err)
	}
	if !created {
		t.Error("created = false, want true")
	}
	if requests != 3 {
		t.Errorf("requests = %d, want 3 (two 503s then a 201)", requests)
	}
}

func TestRecordDeployment_RetriesOn429ThenSucceeds(t *testing.T) {
	withFastDeployRetries(t)
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests < 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "rate_limited", "message": "slow down"}})
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"data": protocol.Deployment{BuildInstanceId: "build1", Environment: "production"},
		})
	}))
	defer srv.Close()

	client := New(srv.URL, "tok")
	_, _, err := client.RecordDeployment(context.Background(), "abc1234", "production")
	if err != nil {
		t.Fatalf("RecordDeployment: %v", err)
	}
	if requests != 2 {
		t.Errorf("requests = %d, want 2 (one 429 then a 201)", requests)
	}
}

func TestRecordDeployment_GivesUpAfterExhaustingRetries(t *testing.T) {
	withFastDeployRetries(t)
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "unavailable", "message": "try again"}})
	}))
	defer srv.Close()

	client := New(srv.URL, "tok")
	_, _, err := client.RecordDeployment(context.Background(), "abc1234", "production")
	if err == nil {
		t.Fatal("RecordDeployment: want error after exhausting retries, got nil")
	}
	wantRequests := len(deployRetryBackoff) + 1 // the initial attempt, plus every retry
	if requests != wantRequests {
		t.Errorf("requests = %d, want %d (bounded, not infinite)", requests, wantRequests)
	}
}

func TestAuthorizeRepository_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/v1/repositories/authorize" {
			t.Errorf("path = %q, want /v1/repositories/authorize", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer tok")
		}
		var body protocol.RepositoryAuthorizeRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body.Challenge != "chal_abc" {
			t.Errorf("request challenge = %q, want %q", body.Challenge, "chal_abc")
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"data": protocol.RepositoryAuthorization{RepositoryId: "repo1", OrganizationId: "org1"},
		})
	}))
	defer srv.Close()

	client := New(srv.URL, "tok")
	auth, err := client.AuthorizeRepository(context.Background(), "chal_abc")
	if err != nil {
		t.Fatalf("AuthorizeRepository: %v", err)
	}
	if auth.RepositoryId != "repo1" || auth.OrganizationId != "org1" {
		t.Errorf("auth = %+v, want repository_id=repo1 organization_id=org1", auth)
	}
}

// TestAuthorizeRepository_ErrorCodes covers each distinct redemption failure
// mode the server documents (openapi.yaml's authorizeRepository operation) —
// the CLI branches on none of these individually (Decision 3: fail loud on
// any redemption error), but each must still surface its own code/message
// rather than a generic failure.
func TestAuthorizeRepository_ErrorCodes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		code   string
	}{
		{"InvalidChallenge", http.StatusForbidden, "invalid_challenge"},
		{"RepositoryAlreadyBound", http.StatusConflict, "repository_already_bound"},
		{"UpgradeRequired", http.StatusPaymentRequired, "upgrade_required"},
		{"RateLimited", http.StatusTooManyRequests, "rate_limited"},
		{"Unauthorized", http.StatusUnauthorized, "unauthorized"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]any{"code": tc.code, "message": "failure: " + tc.code},
				})
			}))
			defer srv.Close()

			client := New(srv.URL, "tok")
			_, err := client.AuthorizeRepository(context.Background(), "chal_abc")
			apiErr, ok := err.(*Error)
			if !ok {
				t.Fatalf("err is %T, want *apiclient.Error", err)
			}
			if apiErr.Code != tc.code {
				t.Errorf("Code = %q, want %q", apiErr.Code, tc.code)
			}
			if apiErr.StatusCode != tc.status {
				t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, tc.status)
			}
		})
	}
}
