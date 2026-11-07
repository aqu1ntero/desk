package clip

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image/png"

	"golang.org/x/image/bmp"
)

// dibPNG turns a packed DIB (the Windows clipboard's CF_DIB: a
// BITMAPINFOHEADER, its masks or palette, the pixels) into PNG, through a
// BMP file header and x/image/bmp. A 32-bit BI_BITFIELDS DIB with the
// default masks (what screenshots copy) is read as the BI_RGB it equals;
// other masks (16-bit) are unsupported. Platform-neutral so Linux tests it.
func dibPNG(dib []byte) ([]byte, error) {
	le := binary.LittleEndian
	if len(dib) < 40 || le.Uint32(dib) < 40 || int(le.Uint32(dib)) > len(dib) {
		return nil, errors.New("clip: short DIB")
	}
	info := int(le.Uint32(dib))
	bpp, comp := le.Uint16(dib[14:]), le.Uint32(dib[16:])
	if info == 40 && comp == 3 && len(dib) >= 52 &&
		le.Uint32(dib[40:]) == 0xff0000 && le.Uint32(dib[44:]) == 0xff00 && le.Uint32(dib[48:]) == 0xff {
		dib = append(bytes.Clone(dib[:40]), dib[52:]...)
		le.PutUint32(dib[16:], 0)
	}
	off := 14 + info
	if bpp <= 8 {
		n := int(le.Uint32(dib[32:]))
		if n == 0 {
			n = 1 << bpp
		}
		off += 4 * n
	}
	f := make([]byte, 14, 14+len(dib))
	f[0], f[1] = 'B', 'M' // the file size field stays 0: x/image/bmp doesn't read it
	le.PutUint32(f[10:], uint32(off))
	img, err := bmp.Decode(bytes.NewReader(append(f, dib...)))
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
