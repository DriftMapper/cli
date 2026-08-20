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
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

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

// doJSON POSTs body as JSON to path and unwraps the {"data"}/{"error"}
// envelope every cmd/api response uses. On any status not in okStatuses, it
// returns *Error with the server's structured code/message/details; on
// success it returns the raw `data` field for the caller to unmarshal into
// its own operation-specific type.
func (c *Client) doJSON(ctx context.Context, path string, body any, okStatuses ...int) (data json.RawMessage, status int, err error) {
	reqBody, err := json.Marshal(body)
	if err != nil {
		return nil, 0, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(reqBody))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

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
