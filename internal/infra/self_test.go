package infra

import (
	"os"
	"testing"
)

// TestSelf: a launcher's $DESK_SELF wins over the executable.
func TestSelf(t *testing.T) {
	old := selfCmd
	defer func() { selfCmd = old }()
	selfCmd = "/tmp/app/desk"
	if got, err := Self(); got != "/tmp/app/desk" || err != nil {
		t.Errorf("with DESK_SELF: %q %v", got, err)
	}
	selfCmd = ""
	exe, _ := os.Executable()
	if got, err := Self(); got != exe || err != nil {
		t.Errorf("without: %q %v, want %q", got, err, exe)
	}
}
