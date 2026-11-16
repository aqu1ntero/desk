package ui

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestWalls: a session sees neither the other organizations' folders nor
// desk's board and state, but keeps the pasted images.
func TestWalls(t *testing.T) {
	h := newHarness(t)
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	for _, o := range []string{"Acme", "Globex"} {
		os.MkdirAll(filepath.Join(h.u.root, o), 0o755)
	}
	hide, keep := h.u.walls(filepath.Join(h.u.root, "Acme", "api"))
	if !slices.Contains(hide, filepath.Join(h.u.root, "Globex")) || slices.Contains(hide, filepath.Join(h.u.root, "Acme")) {
		t.Errorf("organizations: %v", hide)
	}
	if !slices.Contains(hide, filepath.Dir(h.u.path)) || !slices.Contains(hide, filepath.Join(state, "desk")) {
		t.Errorf("desk's own: %v", hide)
	}
	if !slices.Equal(keep, []string{h.u.imagesDir()}) {
		t.Errorf("keep: %v", keep)
	}
}
