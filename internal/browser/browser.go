// Package browser launches the user's default web browser at a URL, for
// `driftmapper compare -open` (DRFT-36). A failed launch is never fatal to
// callers — they're expected to fall back to printing the URL so the user
// can open it by hand.
package browser

import (
	"fmt"
	"os/exec"
	"runtime"
)

// Open launches url in the default browser for the current OS.
func Open(url string) error {
	name, args := commandFor(runtime.GOOS, url)
	if err := exec.Command(name, args...).Start(); err != nil {
		return fmt.Errorf("open %s: %w", url, err)
	}
	return nil
}

// commandFor returns the OS-specific command that opens url in the default
// browser. Split out from Open so the dispatch logic is testable without
// actually spawning a process.
func commandFor(goos, url string) (name string, args []string) {
	switch goos {
	case "darwin":
		return "open", []string{url}
	case "windows":
		// The empty string is a required placeholder: cmd's "start" takes an
		// optional window-title argument first, and a bare URL there gets
		// misread as the title if the URL contains characters like "&".
		return "cmd", []string{"/c", "start", "", url}
	default:
		return "xdg-open", []string{url}
	}
}
