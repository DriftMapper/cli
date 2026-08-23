// Package apiclient is a minimal client for the CLI's public-tier operations
// (spec §5.2a/§4.5): POST /v1/builds and POST /v1/repositories/authorize. It
// speaks the {"data"}/{"error"} response envelope documented in
// driftmapper/protocol's openapi.yaml — protocol's generated types cover
// the resource shapes but not the envelope itself, so it's unwrapped here.
package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/driftmapper/protocol"
)

// Client is a bearer-token HTTP client for one Driftmapper API deployment.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// New returns a Client. token is the raw OIDC ID token acquired via
// internal/oidcclient — presented directly as the bearer credential, per
// spec §4's "no Driftmapper-issued static token" rule.
func New(baseURL, token string) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), token: token, http: http.DefaultClient}
}

// envelope is the generic shape of every cmd/api response. Data is left as
// RawMessage and decoded into the operation-specific type by the caller.
type envelope struct {
	Data  json.RawMessage `json:"data"`
	Error *errorBody      `json:"error"`
}

type errorBody struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Details map[string]string `json:"details"`
}

// Error is returned by RegisterBuild for any non-2xx response, carrying the
// structured code/details a caller can act on — e.g. branching on
// Code == "claim_mismatch" to print Details["mismatched_claim"] rather than
// a generic failure (spec §4.5's DX requirement, surfaced all the way to
// the CLI's own error output).
type Error struct {
	StatusCode int
	Code       string
	Message    string
	Details    map[string]string
}

func (e *Error) Error() string {
	if len(e.Details) == 0 {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	var parts []string
	for k, v := range e.Details {
		parts = append(parts, fmt.Sprintf("%s=%q", k, v))
	}
	return fmt.Sprintf("%s: %s (%s)", e.Code, e.Message, strings.Join(parts, ", "))
}

// RegisterBuild calls POST /v1/builds. created is false when the server
// returned 200 (an identical retry within the same run attempt resolved to
// an existing build, spec §2.5a) and true on 201.
func (c *Client) RegisterBuild(ctx context.Context, reg protocol.BuildRegistration) (build protocol.Build, created bool, err error) {
	data, status, err := c.doJSON(ctx, "/v1/builds", reg, http.StatusOK, http.StatusCreated)
	if err != nil {
		return protocol.Build{}, false, err
	}
	if err := json.Unmarshal(data, &build); err != nil {
		return protocol.Build{}, false, fmt.Errorf("decode build (status %d): %w", status, err)
	}
	return build, status == http.StatusCreated, nil
}

// AuthorizeRepository calls POST /v1/repositories/authorize (spec §4.5,
// DRFT-62/DRFT-66) — redeems challenge and, on success, returns confirmation
// that the token's repository is now bound to an org. challenge is never
// logged or written to disk by this method; it goes straight into the
// request body.
func (c *Client) AuthorizeRepository(ctx context.Context, challenge string) (protocol.RepositoryAuthorization, error) {
	data, _, err := c.doJSON(ctx, "/v1/repositories/authorize",
		protocol.RepositoryAuthorizeRequest{Challenge: challenge}, http.StatusCreated)
	if err != nil {
		return protocol.RepositoryAuthorization{}, err
	}
	var auth protocol.RepositoryAuthorization
	if err := json.Unmarshal(data, &auth); err != nil {
		return protocol.RepositoryAuthorization{}, fmt.Errorf("decode repository authorization: %w", err)
	}
	return auth, nil
}

// deployRetryBackoff is RecordDeployment's bounded retry schedule for
// transient failures — three attempts beyond the first, ~1s/2s/4s apart.
// Deploy events are infrequent and block a CI job's exit code, so a brief
// network blip shouldn't fail the whole deploy step the way it might for a
// higher-volume call; this is deliberately a small local schedule, not a
// general retry policy layer — nothing else in this codebase needs one yet.
var deployRetryBackoff = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}

// RecordDeployment calls POST /v1/deployments (spec's deploy-marking
// design, DRFT-81/82/88) — records that the newest build registered for
// commitSHA, under the token's repository, is deployed to environment. The
// server resolves commitSHA to a build_instance_id itself (DRFT-92: a
// deploy step generally can't know the opaque, server-derived build ID a
// separate registration step produced). created is false when the server
// returned 200 (an identical retry, same CI run, hit the dedupe key), true
// on 201 — same convention as RegisterBuild.
//
// Retries transient failures (connection errors, 5xx, 429) per
// deployRetryBackoff; a genuine 4xx (no_live_policy, validation, not_found)
// is permanent and returned on the first attempt, never retried.
func (c *Client) RecordDeployment(ctx context.Context, commitSHA, environment string) (deployment protocol.Deployment, created bool, err error) {
	for attempt := 0; ; attempt++ {
		deployment, created, err = c.recordDeploymentOnce(ctx, commitSHA, environment)
		if err == nil || !isTransient(err) || attempt >= len(deployRetryBackoff) {
			return deployment, created, err
		}
		select {
		case <-time.After(deployRetryBackoff[attempt]):
		case <-ctx.Done():
			return protocol.Deployment{}, false, ctx.Err()
		}
	}
}

func (c *Client) recordDeploymentOnce(ctx context.Context, commitSHA, environment string) (deployment protocol.Deployment, created bool, err error) {
	data, status, err := c.doJSON(ctx, "/v1/deployments", protocol.DeploymentRequest{
		CommitSha:   commitSHA,
		Environment: environment,
	}, http.StatusOK, http.StatusCreated)
	if err != nil {
		return protocol.Deployment{}, false, err
	}
	if err := json.Unmarshal(data, &deployment); err != nil {
		return protocol.Deployment{}, false, fmt.Errorf("decode deployment (status %d): %w", status, err)
	}
	return deployment, status == http.StatusCreated, nil
}

// GetDeployment calls GET /v1/deployments/{id} (DRFT-98) — the read half
// of deployment-keyed verification: `driftmapper verify <deployment-id>`
// knows only the handle its deploy step emitted and needs what the row
// carries (expected build-instance id, environment, recorded url).
func (c *Client) GetDeployment(ctx context.Context, deploymentID int64) (deployment protocol.Deployment, err error) {
	data, status, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/v1/deployments/%d", deploymentID), nil, http.StatusOK)
	if err != nil {
		return protocol.Deployment{}, err
	}
	if err := json.Unmarshal(data, &deployment); err != nil {
		return protocol.Deployment{}, fmt.Errorf("decode deployment (status %d): %w", status, err)
	}
	return deployment, nil
}

// RecordVerification calls POST /v1/verifications. The enriched request
// (DRFT-98) records what an opinionated caller observed: deployment_id
// provenance, source_url fetched, observed_build_instance_id parsed from
// the deployed meta tags, and outcome (verified/mismatch/fetch_failed/
// parse_failed). A bare attestation sends only the required pair.
// created is false when the server returned 200 (an identical retry,
// same CI run, hit the dedupe key), true on 201.
//
// Retries transient failures per deployRetryBackoff; a genuine 4xx is
// permanent and returned on the first attempt, never retried.
func (c *Client) RecordVerification(ctx context.Context, req protocol.VerificationRequest) (verification protocol.Verification, created bool, err error) {
	for attempt := 0; ; attempt++ {
		verification, created, err = c.recordVerificationOnce(ctx, req)
		if err == nil || !isTransient(err) || attempt >= len(deployRetryBackoff) {
			return verification, created, err
		}
		select {
		case <-time.After(deployRetryBackoff[attempt]):
		case <-ctx.Done():
			return protocol.Verification{}, false, ctx.Err()
		}
	}
}

func (c *Client) recordVerificationOnce(ctx context.Context, req protocol.VerificationRequest) (verification protocol.Verification, created bool, err error) {
	data, status, err := c.doJSON(ctx, "/v1/verifications", req, http.StatusOK, http.StatusCreated)
	if err != nil {
		return protocol.Verification{}, false, err
	}
	if err := json.Unmarshal(data, &verification); err != nil {
		return protocol.Verification{}, false, fmt.Errorf("decode verification (status %d): %w", status, err)
	}
	return verification, status == http.StatusCreated, nil
}

// isTransient reports whether err is worth retrying: a connection-level
// failure (doJSON's http.Client.Do never got a response at all, surfaced as
// *url.Error) or a *Error carrying a 5xx or 429 — server overload/rate
// limiting, not a rejection of the request itself. Any other *Error (422
// validation, 403 no_live_policy/claim_mismatch, 404 not_found) is a
// permanent rejection that retrying can't fix.
func isTransient(err error) bool {
	var apiErr *Error
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode >= 500 || apiErr.StatusCode == http.StatusTooManyRequests
	}
	var urlErr *url.Error
	return errors.As(err, &urlErr)
}

// doJSON POSTs body as JSON to path and unwraps the {"data"}/{"error"}
// envelope every cmd/api response uses. On any status not in okStatuses, it
// returns *Error with the server's structured code/message/details; on
// success it returns the raw `data` field for the caller to unmarshal into
// its own operation-specific type.
func (c *Client) doJSON(ctx context.Context, path string, body any, okStatuses ...int) (data json.RawMessage, status int, err error) {
	return c.do(ctx, http.MethodPost, path, body, okStatuses...)
}

func (c *Client) do(ctx context.Context, method, path string, body any, okStatuses ...int) (data json.RawMessage, status int, err error) {
	var reqBody io.Reader
	if body != nil {
		marshaled, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(marshaled)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("request %s: %w", path, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response: %w", err)
	}

	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("decode response (status %d): %w", resp.StatusCode, err)
	}

	if !slices.Contains(okStatuses, resp.StatusCode) {
		apiErr := &Error{StatusCode: resp.StatusCode}
		if env.Error != nil {
			apiErr.Code = env.Error.Code
			apiErr.Message = env.Error.Message
			apiErr.Details = env.Error.Details
		} else {
			apiErr.Code = "unknown"
			apiErr.Message = fmt.Sprintf("unexpected status %d", resp.StatusCode)
		}
		return nil, resp.StatusCode, apiErr
	}

	return env.Data, resp.StatusCode, nil
}
