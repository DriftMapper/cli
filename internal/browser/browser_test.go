package browser

import (
	"reflect"
	"testing"
)

func TestCommandFor(t *testing.T) {
	tests := []struct {
		goos     string
		wantName string
		wantArgs []string
	}{
		{"darwin", "open", []string{"https://example.test/compare"}},
		{"windows", "cmd", []string{"/c", "start", "", "https://example.test/compare"}},
		{"linux", "xdg-open", []string{"https://example.test/compare"}},
		{"freebsd", "xdg-open", []string{"https://example.test/compare"}},
	}
	for _, tt := range tests {
		name, args := commandFor(tt.goos, "https://example.test/compare")
		if name != tt.wantName || !reflect.DeepEqual(args, tt.wantArgs) {
			t.Errorf("commandFor(%q) = %q, %v; want %q, %v", tt.goos, name, args, tt.wantName, tt.wantArgs)
		}
	}
}
