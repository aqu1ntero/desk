package orgs

import (
	"os"
	"path/filepath"
	"testing"
)

// TestZoom: 100 unset; SetZoom clamps and keeps it (100 not written); the
// other settings keep it; steps move by ten, 0 goes back to 100.
func TestZoom(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if z := Zoom(); z != 100 {
		t.Fatalf("unset: %d", z)
	}
	for in, want := range map[int]int{120: 120, 10: 50, 49: 50, 50: 50, 300: 300, 301: 300, 999: 300} {
		if err := SetZoom(in); err != nil {
			t.Fatal(err)
		}
		if z, s := Zoom(), loadSettings().Zoom; z != want || s != want {
			t.Errorf("SetZoom(%d): Zoom %d, file %d, want %d", in, z, s, want)
		}
	}
	SetZoom(100)
	if z, s := Zoom(), loadSettings().Zoom; z != 100 || s != 0 {
		t.Errorf("100: Zoom %d, file %d", z, s)
	}
	SetZoom(130)
	if err := SaveLangs("Spanish", "Spanish"); err != nil || loadSettings().Zoom != 130 {
		t.Errorf("SaveLangs lost the zoom: %v %d", err, loadSettings().Zoom)
	}
	for _, c := range []struct{ steps, want int }{{1, 140}, {-2, 120}, {0, 100}, {-1, 90}} {
		if err := StepZoom(c.steps); err != nil || Zoom() != c.want {
			t.Errorf("StepZoom(%d): %d %v, want %d", c.steps, Zoom(), err, c.want)
		}
	}
}

// TestZoomFile: Zoom reads the file once per settings file: an edit by hand
// counts at the next start, another settings folder reads its own.
func TestZoomFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	write := func(s string) {
		os.MkdirAll(filepath.Join(dir, "desk"), 0o755)
		os.WriteFile(filepath.Join(dir, "desk", "settings.toml"), []byte(s), 0o644)
	}
	write("zoom = 150\n")
	if z := Zoom(); z != 150 {
		t.Fatalf("from the file: %d", z)
	}
	write("zoom = 170\n")
	if z := Zoom(); z != 150 {
		t.Errorf("read again: %d", z)
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if z := Zoom(); z != 100 {
		t.Errorf("another folder: %d", z)
	}
	dir = t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	write("zoom = 5\n")
	if z := Zoom(); z != 50 {
		t.Errorf("out of bounds in the file: %d", z)
	}
}

// TestZoomUnwritable: a zoom the file can't keep still counts until desk quits.
func TestZoomUnwritable(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	os.WriteFile(file, nil, 0o644)
	t.Setenv("XDG_CONFIG_HOME", file)
	if err := SetZoom(160); err == nil || Zoom() != 160 {
		t.Errorf("err %v, zoom %d", err, Zoom())
	}
}
