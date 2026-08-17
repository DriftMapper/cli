package apiclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
