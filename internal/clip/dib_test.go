package clip

import (
	"bytes"
	"encoding/binary"
	"image/color"
	"image/png"
	"testing"
)

// mkDIB is a packed DIB: a BITMAPINFOHEADER of w×h (negative h: top-down),
// bpp, compression comp and colors used, then extra (masks or palette) and
// the pixels.
func mkDIB(w, h int32, bpp uint16, comp, used uint32, extra, pix []byte) []byte {
	b := make([]byte, 40)
	le := binary.LittleEndian
	le.PutUint32(b, 40)
	le.PutUint32(b[4:], uint32(w))
	le.PutUint32(b[8:], uint32(h))
	le.PutUint16(b[12:], 1)
	le.PutUint16(b[14:], bpp)
	le.PutUint32(b[16:], comp)
	le.PutUint32(b[32:], used)
	return append(append(b, extra...), pix...)
}

// at decodes p's PNG and returns its pixel (x, y).
func at(t *testing.T, p []byte, x, y int) color.NRGBA {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(p))
	if err != nil {
		t.Fatal(err)
	}
	return color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
}

// TestDIBPNG: 24-bit bottom-up, 32-bit BI_BITFIELDS with the default masks
// (alpha ignored, as Windows leaves it 0) and an 8-bit palette all decode.
func TestDIBPNG(t *testing.T) {
	// 1×2 bottom-up 24-bit: rows padded to 4 bytes; the last row is the top
	p, err := dibPNG(mkDIB(1, 2, 24, 0, 0, nil, []byte{0, 0, 255, 0, 255, 0, 0, 0}))
	if err != nil {
		t.Fatal(err)
	}
	if c := at(t, p, 0, 0); c != (color.NRGBA{0, 0, 255, 255}) {
		t.Errorf("24-bit top = %v", c)
	}
	masks := []byte{0, 0, 0xff, 0, 0, 0xff, 0, 0, 0xff, 0, 0, 0}
	p, err = dibPNG(mkDIB(2, -1, 32, 3, 0, masks, []byte{1, 2, 3, 0, 40, 50, 60, 0}))
	if err != nil {
		t.Fatal(err)
	}
	if c := at(t, p, 1, 0); c != (color.NRGBA{60, 50, 40, 255}) {
		t.Errorf("32-bit bitfields = %v", c)
	}
	pal := []byte{0, 0, 0, 0, 9, 8, 7, 0}
	p, err = dibPNG(mkDIB(1, 1, 8, 0, 2, pal, []byte{1, 0, 0, 0}))
	if err != nil {
		t.Fatal(err)
	}
	if c := at(t, p, 0, 0); c != (color.NRGBA{7, 8, 9, 255}) {
		t.Errorf("8-bit palette = %v", c)
	}
	// colors used 0: the whole 1<<bpp palette (2 entries for 1 bit)
	if p, err = dibPNG(mkDIB(1, 1, 1, 0, 0, pal, []byte{0x80, 0, 0, 0})); err != nil || at(t, p, 0, 0) != (color.NRGBA{7, 8, 9, 255}) {
		t.Errorf("1-bit: %v", err)
	}
}

// TestDIBPNGBad: short, oversized headers, 16-bit masks, an empty image and
// short pixels are errors.
func TestDIBPNGBad(t *testing.T) {
	big := mkDIB(1, 1, 24, 0, 0, nil, nil)
	binary.LittleEndian.PutUint32(big, 400)
	small := mkDIB(1, 1, 24, 0, 0, nil, nil)
	binary.LittleEndian.PutUint32(small, 12)
	m565 := []byte{0, 0xf8, 0, 0, 0xe0, 7, 0, 0, 0x1f, 0, 0, 0}
	for name, d := range map[string][]byte{"short": make([]byte, 39), "header>": big, "header<": small} {
		if _, err := dibPNG(d); err == nil || err.Error() != "clip: short DIB" {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, d := range map[string][]byte{
		"565":       mkDIB(1, 1, 16, 3, 0, m565, []byte{0, 0, 0, 0}),
		"rgb masks": mkDIB(1, 1, 32, 3, 0, m565, []byte{0, 0, 0, 0}),
		"empty":     mkDIB(0, 0, 24, 0, 0, nil, nil),
		"no pixels": mkDIB(4, 4, 24, 0, 0, nil, []byte{1}),
	} {
		if p, err := dibPNG(d); err == nil || p != nil {
			t.Errorf("%s: %d bytes, %v", name, len(p), err)
		}
	}
}
