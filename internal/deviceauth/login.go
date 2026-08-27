package deviceauth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

// ErrNotLoggedIn is AccessToken's error when no credentials are stored at
// all — distinct from a stored-but-dead session (Error{Code:
// "session_invalid"} from Refresh), so callers can phrase the two
// differently ("run `driftmapper login`" vs. "your login expired, run
// `driftmapper login` again").
var ErrNotLoggedIn = errors.New("not logged in — run `driftmapper login`")

// pollDelay computes the wait between polls from the server's advertised
// Interval (RFC 8628-style, whole seconds; hub's DeviceCode handler always
// sends 5). A non-positive value (a server that omits it) falls back to a
// safe default rather than hammering the endpoint. Swapped out in tests to
// avoid multi-second sleeps — production always goes through this var.
var pollDelay = func(serverInterval int) time.Duration {
	if serverInterval <= 0 {
		return 5 * time.Second
	}
	return time.Duration(serverInterval) * time.Second
}

// Login runs the full device-code pairing against hubURL: starts it,
// prints the code and opens openBrowser at the verification URL, then
// polls at the server-specified interval until the human approves (or the
// pairing expires). On success, persists the resulting sealed session via
// Save — every later invocation refreshes from that, no browser step
// involved (see AccessToken).
func Login(ctx context.Context, hubURL string, openBrowser func(string) error, stdout io.Writer) error {
	pending, err := DeviceCode(ctx, hubURL)
	if err != nil {
		return fmt.Errorf("start device login: %w", err)
	}

	fmt.Fprintf(stdout, "First, confirm this code: %s\n", pending.UserCode)
	fmt.Fprintf(stdout, "Opening %s in your browser...\n", pending.VerificationURIComplete)
	if err := openBrowser(pending.VerificationURIComplete); err != nil {
		fmt.Fprintf(stdout, "Could not open a browser automatically — open this URL yourself:\n%s\n", pending.VerificationURIComplete)
	}

	interval := pollDelay(pending.Interval)
	deadline := time.Now().Add(time.Duration(pending.ExpiresIn) * time.Second)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("device code expired before it was approved — run `driftmapper login` again")
		}

		tok, err := PollToken(ctx, hubURL, pending.DeviceCode)
		if err != nil {
			var apiErr *Error
			if errors.As(err, &apiErr) && apiErr.Code == "authorization_pending" {
				continue
			}
			return fmt.Errorf("poll device login: %w", err)
		}

		if err := Save(&Credentials{SealedSession: tok.SealedSession}); err != nil {
			return fmt.Errorf("save credentials: %w", err)
		}
		fmt.Fprintln(stdout, "Logged in.")
		return nil
	}
}

// AccessToken returns a fresh bearer access token for the declared-build
// producer (DRFT-129): loads the stored sealed session and refreshes it
// against hubURL, persisting the newly-rotated value in its place —
// exactly SessionAPIToken's rotate-on-every-call behavior (DRFT-114), just
// driven by the CLI's own stored credential instead of a browser cookie.
// Returns ErrNotLoggedIn when nothing is stored at all.
func AccessToken(ctx context.Context, hubURL string) (string, error) {
	creds, err := Load()
	if err != nil {
		return "", fmt.Errorf("load credentials: %w", err)
	}
	if creds == nil {
		return "", ErrNotLoggedIn
	}

	tok, err := Refresh(ctx, hubURL, creds.SealedSession)
	if err != nil {
		var apiErr *Error
		if errors.As(err, &apiErr) && apiErr.Code == "session_invalid" {
			return "", fmt.Errorf("your login has expired — run `driftmapper login` again: %w", err)
		}
		return "", fmt.Errorf("refresh login: %w", err)
	}

	if err := Save(&Credentials{SealedSession: tok.SealedSession}); err != nil {
		return "", fmt.Errorf("save refreshed credentials: %w", err)
	}
	return tok.AccessToken, nil
}
