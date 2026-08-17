// Package apiclient is a minimal client for the one public-tier operation
// the MVP CLI ever calls: POST /v1/builds (spec §5.2a). It speaks the
// {"data"}/{"error"} response envelope documented in
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
	body, err := json.Marshal(reg)
	if err != nil {
		return protocol.Build{}, false, fmt.Errorf("marshal build registration: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/builds", bytes.NewReader(body))
	if err != nil {
		return protocol.Build{}, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return protocol.Build{}, false, fmt.Errorf("register build: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return protocol.Build{}, false, fmt.Errorf("read response: %w", err)
	}

	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return protocol.Build{}, false, fmt.Errorf("decode response (status %d): %w", resp.StatusCode, err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		apiErr := &Error{StatusCode: resp.StatusCode}
		if env.Error != nil {
			apiErr.Code = env.Error.Code
			apiErr.Message = env.Error.Message
			apiErr.Details = env.Error.Details
		} else {
			apiErr.Code = "unknown"
			apiErr.Message = fmt.Sprintf("unexpected status %d", resp.StatusCode)
		}
		return protocol.Build{}, false, apiErr
	}

	if err := json.Unmarshal(env.Data, &build); err != nil {
		return protocol.Build{}, false, fmt.Errorf("decode build (status %d): %w", resp.StatusCode, err)
	}
	return build, resp.StatusCode == http.StatusCreated, nil
}
