// Package oidcclient acquires a workload OIDC token from the CI provider.
// It only ever acquires and presents a token — signature verification,
// issuer/audience checks, and claim mapping are exclusively server-side
// (spec §5.2: "must never ship in a binary running on customer
// infrastructure"). v1 supports GitHub Actions only, matching the server's
// v1 issuer registry (spec §4.3/§4.4).
package oidcclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
)

// AcquireGitHubActionsToken fetches a GitHub Actions OIDC ID token scoped to
// audience, using the runner-provided ACTIONS_ID_TOKEN_REQUEST_URL /
// ACTIONS_ID_TOKEN_REQUEST_TOKEN env vars. Both are only set when the
// workflow has requested `permissions: id-token: write` — their absence is
// reported as a clear, actionable error rather than a generic failure.
func AcquireGitHubActionsToken(ctx context.Context, audience string) (string, error) {
	reqURL := os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL")
	reqToken := os.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN")
	if reqURL == "" || reqToken == "" {
		return "", fmt.Errorf(
			"ACTIONS_ID_TOKEN_REQUEST_URL/ACTIONS_ID_TOKEN_REQUEST_TOKEN are not set — " +
				`add "permissions: { id-token: write }" to this job in the workflow YAML`)
	}

	u, err := url.Parse(reqURL)
	if err != nil {
		return "", fmt.Errorf("parse ACTIONS_ID_TOKEN_REQUEST_URL: %w", err)
	}
	q := u.Query()
	q.Set("audience", audience)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+reqToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request OIDC token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("request OIDC token: unexpected status %d from the Actions runner", resp.StatusCode)
	}

	var body struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decode OIDC token response: %w", err)
	}
	if body.Value == "" {
		return "", fmt.Errorf("OIDC token response had no value")
	}
	return body.Value, nil
}
