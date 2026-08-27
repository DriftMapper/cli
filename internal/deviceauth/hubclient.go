// Package deviceauth implements `driftmapper login`/`logout` (DRFT-30, the
// write credential for a build registered from a laptop with no CI at
// all — DRFT-124/DRFT-129's third principal) and the credential storage
// backing it.
//
// Hub-brokered: this talks directly to cmd/hub's device-code endpoints
// (POST /device/code, /device/token, /device/refresh — see
// server/internal/handler/device.go), not cmd/api. Those endpoints are
// hand-rolled JSON, not part of driftmapper/protocol's generated types —
// same precedent the dashboard SPA's own POST /session/api-token already
// set (DRFT-114): a hub-internal contract, not the CLI-facing wire
// contract protocol documents.
package deviceauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// envelope mirrors hub's device.go/session_token.go response shape —
// {"data"}/{"error"}, the same envelope cmd/api uses, but hand-decoded
// here rather than shared with internal/apiclient: these aren't protocol
// operations.
type envelope struct {
	Data  json.RawMessage `json:"data"`
	Error *errorBody      `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error is returned for any device-code endpoint failure. Code lets
// callers branch on the specific RFC 8628-flavored cases hub's device.go
// returns — "authorization_pending" (keep polling, not a real error),
// "expired_token" (the pairing is dead, start over), "session_invalid"
// (a stored credential no longer works, re-login).
type Error struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// Pending is DeviceCode's return value — the two codes and everything
// AccessToken/PollToken need to complete the pairing.
type Pending struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// Token is the successful result of PollToken or Refresh — a bearer
// access token plus the sealed session that minted it, rotated on every
// call. SealedSession is what gets persisted to disk (see credentials.go);
// AccessToken is short-lived and never written there.
type Token struct {
	AccessToken   string `json:"access_token"`
	SealedSession string `json:"sealed_session"`
}

// DeviceCode calls POST hubURL/device/code — starts a pairing.
func DeviceCode(ctx context.Context, hubURL string) (*Pending, error) {
	var p Pending
	if err := postJSON(ctx, hubURL+"/device/code", nil, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// PollToken calls POST hubURL/device/token once. A pending (not-yet-
// approved) pairing returns *Error with Code == "authorization_pending" —
// the caller's poll loop, not this function, decides how long to keep
// retrying.
func PollToken(ctx context.Context, hubURL, deviceCode string) (*Token, error) {
	var tok Token
	if err := postJSON(ctx, hubURL+"/device/token", map[string]string{"device_code": deviceCode}, &tok); err != nil {
		return nil, err
	}
	return &tok, nil
}

// Refresh calls POST hubURL/device/refresh with a previously-persisted
// sealed session — every `driftmapper` invocation after the first
// `driftmapper login`, no browser step involved.
func Refresh(ctx context.Context, hubURL, sealedSession string) (*Token, error) {
	var tok Token
	if err := postJSON(ctx, hubURL+"/device/refresh", map[string]string{"sealed_session": sealedSession}, &tok); err != nil {
		return nil, err
	}
	return &tok, nil
}

func postJSON(ctx context.Context, url string, body any, dst any) error {
	var reqBody io.Reader
	if body != nil {
		marshaled, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(marshaled)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, reqBody)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("request %s: %w", url, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("decode response (status %d): %w", resp.StatusCode, err)
	}

	if resp.StatusCode != http.StatusOK {
		apiErr := &Error{StatusCode: resp.StatusCode}
		if env.Error != nil {
			apiErr.Code = env.Error.Code
			apiErr.Message = env.Error.Message
		} else {
			apiErr.Code = "unknown"
			apiErr.Message = fmt.Sprintf("unexpected status %d", resp.StatusCode)
		}
		return apiErr
	}

	if dst != nil {
		if err := json.Unmarshal(env.Data, dst); err != nil {
			return fmt.Errorf("decode data: %w", err)
		}
	}
	return nil
}
