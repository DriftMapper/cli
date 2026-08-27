package deviceauth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCredentials_LoadWithNoFile_ReturnsNilNil(t *testing.T) {
	t.Setenv("DRIFTMAPPER_CONFIG_DIR", t.TempDir())

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c != nil {
		t.Errorf("c = %+v, want nil (never logged in)", c)
	}
}

func TestCredentials_SaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DRIFTMAPPER_CONFIG_DIR", dir)

	if err := Save(&Credentials{SealedSession: "sealed-value"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c == nil || c.SealedSession != "sealed-value" {
		t.Fatalf("c = %+v, want SealedSession=sealed-value", c)
	}

	info, err := os.Stat(filepath.Join(dir, "credentials.json"))
	if err != nil {
		t.Fatalf("stat credentials.json: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode = %o, want 0600", perm)
	}
}

func TestCredentials_Clear(t *testing.T) {
	t.Setenv("DRIFTMAPPER_CONFIG_DIR", t.TempDir())

	if err := Save(&Credentials{SealedSession: "sealed-value"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	c, err := Load()
	if err != nil {
		t.Fatalf("Load after Clear: %v", err)
	}
	if c != nil {
		t.Errorf("c = %+v, want nil after Clear", c)
	}

	// Clearing again (nothing to clear) is not an error.
	if err := Clear(); err != nil {
		t.Errorf("second Clear: %v, want nil", err)
	}
}
