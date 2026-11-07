package clip

import (
	"bytes"
	"image/color"
	"testing"
	"unsafe"
)

var (
	emptyClip   = user32.NewProc("EmptyClipboard")
	setData     = user32.NewProc("SetClipboardData")
	globalAlloc = kernel32.NewProc("GlobalAlloc")
)

// setClip puts data on the clipboard as each format f (0: none, emptied).
func setClip(t *testing.T, data map[uintptr][]byte) {
	t.Helper()
	if r, _, err := openClip.Call(0); r == 0 {
		t.Fatal("OpenClipboard:", err)
	}
	defer closeClip.Call()
	emptyClip.Call()
	for f, b := range data {
		h, _, _ := globalAlloc.Call(2, uintptr(len(b))) // GMEM_MOVEABLE
		p, _, _ := globalLock.Call(h)
		ptr := *(*unsafe.Pointer)(unsafe.Pointer(&p))
		copy(unsafe.Slice((*byte)(ptr), len(b)), b)
		globalUnlock.Call(h)
		if r, _, err := setData.Call(f, h); r == 0 {
			t.Fatal("SetClipboardData:", err)
		}
	}
}

// TestImageWindows: the "PNG" format as it is, else CF_DIB as PNG; neither
// is no image.
func TestImageWindows(t *testing.T) {
	setClip(t, map[uintptr][]byte{pngFormat():[]byte("\x89PNG fake")})
	if b, typ, err := Image(); string(b) != "\x89PNG fake" || typ != "image/png" || err != nil {
		t.Fatalf("PNG: %q %q %v", b, typ, err)
	}
	setClip(t, map[uintptr][]byte{cfDIB: mkDIB(1, 1, 24, 0, 0, nil, []byte{3, 2, 1, 0})})
	b, typ, err := Image()
	if err != nil || typ != "image/png" || !bytes.HasPrefix(b, []byte("\x89PNG")) || at(t, b, 0, 0) != (color.NRGBA{1, 2, 3, 255}) {
		t.Fatalf("DIB: %d bytes %q %v", len(b), typ, err)
	}
	setClip(t, map[uintptr][]byte{cfDIB: make([]byte, 10)})
	if b, _, err := Image(); b != nil || err == nil {
		t.Fatalf("bad DIB: %d bytes %v", len(b), err)
	}
	setClip(t, nil)
	if b, typ, err := Image(); b != nil || typ != "" || err != nil {
		t.Fatalf("empty: %q %q %v", b, typ, err)
	}
}
