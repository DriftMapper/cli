package deviceauth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Credentials is the on-disk shape of ~/.config/driftmapper/credentials.json
// (XDG-aware via os.UserConfigDir, override via DRIFTMAPPER_CONFIG_DIR).
// SealedSession is the only field: everything else (a bearer access token)
// is minted on demand from it via Refresh, never persisted itself — it's
// short-lived and there's no reason to write an already-stale credential
// to disk. No OS keyring: a plain file, mode 0600, keeps `make cross`'s
// reproducible builds free of cgo (see driftmapper-spec.md's Phase C plan).
type Credentials struct {
	SealedSession string `json:"sealed_session"`
}

// path returns the credentials file location, creating its parent
// directory (mode 0700) if needed.
func path() (string, error) {
	dir := os.Getenv("DRIFTMAPPER_CONFIG_DIR")
	if dir == "" {
		configDir, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(configDir, "driftmapper")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "credentials.json"), nil
}

// Load reads the stored credentials. Returns (nil, nil) — not an error —
// when no credentials file exists yet, since "never logged in" is an
// ordinary, expected state, not a failure.
func Load() (*Credentials, error) {
	p, err := path()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var c Credentials
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	if c.SealedSession == "" {
		return nil, nil
	}
	return &c, nil
}

// Save writes c to disk, mode 0600 — this is a bearer-secret-equivalent
// value (see hub's device.go doc comment: it's the exact payload
// server-side session refresh uses), so it gets the same permissions
// treatment DRIFTMAPPER_CHALLENGE's own doc comment calls for, just at
// rest instead of in a request.
func Save(c *Credentials) error {
	p, err := path()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(p, raw, 0o600)
}

// Clear removes the credentials file (`driftmapper logout`). Removing an
// already-absent file is not an error — logging out twice, or logging out
// after never having logged in, are both just "nothing to do."
func Clear() error {
	p, err := path()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
