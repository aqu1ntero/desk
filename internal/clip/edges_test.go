//go:build linux

package clip

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestImageFailures: each way the compositor can fail Image is an error and
// no image.
func TestImageFailures(t *testing.T) {
	for _, tc := range []struct{ mode, want string }{
		{"error-registry", "wayland error 7: boom"},
		{"no-manager", "no clipboard data-control"},
		{"error-device", "wayland error 7: boom"},
		{"hangup", "pipe"},
		{"silent", "reading the clipboard"},
	} {
		fakeCompositor(t, t.TempDir(), []string{"image/png"}, "PNG", tc.mode)
		b, typ, err := Image()
		if err == nil || !strings.Contains(err.Error(), tc.want) || b != nil || typ != "" {
			t.Errorf("%s: %q %q %v, want error %q", tc.mode, b, typ, err, tc.want)
		}
	}
}

// TestImagePipeFails: no pipe for the owner to write into is an error.
func TestImagePipeFails(t *testing.T) {
	fakeCompositor(t, t.TempDir(), []string{"image/png"}, "PNG")
	old := pipe
	defer func() { pipe = old }()
	pipe = func() (*os.File, *os.File, error) { return nil, nil, errors.New("no fds") }
	if b, _, err := Image(); err == nil || err.Error() != "no fds" || b != nil {
		t.Fatalf("%q %v", b, err)
	}
}

// TestDial: no socket is an error naming it; WAYLAND_DISPLAY defaults to
// wayland-0 in XDG_RUNTIME_DIR and may be an absolute path.
func TestDial(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	for disp, want := range map[string]string{
		"":                          filepath.Join(dir, "wayland-0"),
		"wl-1":                      filepath.Join(dir, "wl-1"),
		filepath.Join(dir, "abs-1"): filepath.Join(dir, "abs-1"),
	} {
		t.Setenv("WAYLAND_DISPLAY", disp)
		_, err := dial()
		if err == nil || !strings.Contains(err.Error(), "no Wayland clipboard") || !strings.Contains(err.Error(), want) {
			t.Errorf("WAYLAND_DISPLAY=%q: %v, want %s", disp, err, want)
		}
	}
	t.Setenv("WAYLAND_DISPLAY", filepath.Join(dir, "none"))
	if b, typ, err := Image(); err == nil || b != nil || typ != "" {
		t.Fatalf("Image without a compositor: %q %q %v", b, typ, err)
	}
}

// TestPick: PNG first, else the first image type, else none.
func TestPick(t *testing.T) {
	for want, types := range map[string][]string{
		"image/png":  {"image/jpeg", "image/png"},
		"image/jpeg": {"text/plain", "image/jpeg", "image/gif"},
		"":           {"text/plain"},
	} {
		if got := pick(types); got != want {
			t.Errorf("pick(%v) = %q, want %q", types, got, want)
		}
	}
}

// pair is a connected client conn and the server's end.
func pair(t *testing.T) (*conn, *net.UnixConn) {
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(t.TempDir(), "s"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	c, err := net.DialUnix("unix", nil, l.Addr().(*net.UnixAddr))
	if err != nil {
		t.Fatal(err)
	}
	s, err := l.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(); s.Close() })
	return &conn{c: c, next: 2}, s
}

// TestRoundtripBroken: a closed connection, a peer that hangs up mid-header
// or mid-body, and a message shorter than its header end the roundtrip
// with an error.
func TestRoundtripBroken(t *testing.T) {
	on := func(uint32, uint32, body) { t.Error("event delivered") }

	w, _ := pair(t)
	w.c.Close()
	if err := w.roundtrip(on); err == nil {
		t.Error("closed conn: no error")
	}
	for name, msg := range map[string][]byte{
		"hangup":    nil,
		"short":     le.AppendUint32(le.AppendUint32(nil, 9), 4<<16),
		"cut body":  le.AppendUint32(le.AppendUint32(nil, 9), 16<<16),
		"half head": {1, 2},
	} {
		w, s := pair(t)
		s.Write(msg)
		s.CloseWrite() // still reads the client's sync
		err := w.roundtrip(on)
		if err == nil {
			t.Errorf("%s: no error", name)
		}
		if name == "short" && err.Error() != "wayland: bad message" {
			t.Errorf("short: %v", err)
		}
	}
}

// TestBodyShort: arguments past the end read as zero and "", and a string
// whose length runs past the body is "".
func TestBodyShort(t *testing.T) {
	b := body{1, 2}
	if b.u() != 0 || len(b) != 2 {
		t.Error("short uint")
	}
	b = body(args{}.u(0))
	if b.s() != "" {
		t.Error("empty string")
	}
	b = body(args{}.u(100).u(0x41414141))
	if b.s() != "" {
		t.Error("string past the end")
	}
}
