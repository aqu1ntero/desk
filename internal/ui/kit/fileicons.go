package kit

import (
	"image"
	"image/color"
	"strings"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// Seti is the Seti UI icon font (MIT, the one VS Code's default file icon
// theme draws): one glyph per kind of file, colored by seti_table.go.
var Seti = font.Font{Typeface: "Seti"}

type setiIcon struct {
	r rune
	c uint32
}

// fileIcon is name's Seti glyph: by the whole name first, then by its
// longest extension ("x.test.js" tries "test.js", then "js").
func fileIcon(name string) setiIcon {
	s := strings.ToLower(name)
	if i, ok := setiNames[s]; ok {
		return i
	}
	for {
		j := strings.IndexByte(s, '.')
		if j < 0 {
			return setiFile
		}
		s = s[j+1:]
		if i, ok := setiExts[s]; ok {
			return i
		}
	}
}

// FileIcon draws name's glyph in its color, centered in a box of side sz
// so the names after it line up.
func (k *Kit) FileIcon(name string, sz unit.Dp) layout.Widget {
	i := fileIcon(name)
	return IconBox(sz, k.Lbl(string(i.r), unit.Sp(sz)*.9, RGB(i.c), Seti))
}

// FolderIcon draws a filled folder, open with its front flap tilted.
func FolderIcon(sz unit.Dp, c color.NRGBA, open bool) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		s := float32(gtx.Dp(sz))
		var p clip.Path
		p.Begin(gtx.Ops)
		// The back with its tab.
		p.MoveTo(f32.Pt(s*.08, s*.22))
		p.LineTo(f32.Pt(s*.38, s*.22))
		p.LineTo(f32.Pt(s*.47, s*.31))
		p.LineTo(f32.Pt(s*.92, s*.31))
		p.LineTo(f32.Pt(s*.92, s*.80))
		p.LineTo(f32.Pt(s*.08, s*.80))
		p.Close()
		back := c
		if open {
			back.A = c.A / 2 // the front flap reads over it
		}
		paint.FillShape(gtx.Ops, back, clip.Outline{Path: p.End()}.Op())
		if open {
			p.Begin(gtx.Ops)
			p.MoveTo(f32.Pt(s*.20, s*.44))
			p.LineTo(f32.Pt(s*1.0, s*.44))
			p.LineTo(f32.Pt(s*.88, s*.80))
			p.LineTo(f32.Pt(s*.08, s*.80))
			p.Close()
			paint.FillShape(gtx.Ops, c, clip.Outline{Path: p.End()}.Op())
		}
		px := int(s)
		return layout.Dimensions{Size: image.Pt(px, px)}
	}
}

// IconBox centers w in a square of side sz. w may be wider (Seti's "GO"
// is): squeezed to sz, a label would truncate it to "…".
func IconBox(sz unit.Dp, w layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		px := gtx.Dp(sz)
		gtx.Constraints = layout.Constraints{Max: image.Pt(px*2, px*2)}
		m := op.Record(gtx.Ops)
		d := w(gtx)
		c := m.Stop()
		defer op.Offset(image.Pt((px-d.Size.X)/2, (px-d.Size.Y)/2)).Push(gtx.Ops).Pop()
		c.Add(gtx.Ops)
		return layout.Dimensions{Size: image.Pt(px, px)}
	}
}
