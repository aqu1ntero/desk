package kit

import (
	"image"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// ScrollList lays out l with a thin scrollbar on its right edge in our
// colors, drawn only when the rows don't all fit (material.List decides).
// The bar can be dragged and its track clicked.
func ScrollList(gtx layout.Context, l *widget.List, n int, el layout.ListElement) layout.Dimensions {
	// barTheme: material.List reads only FingerSize (the thumb's least length)
	// and the colors we set right after.
	s := material.List(&barTheme, l)
	s.Indicator.Color, s.Indicator.HoverColor = Mix(CRim, CText3, .35), CText3
	s.Indicator.MinorWidth, s.Indicator.CornerRadius = 4, 2
	return s.Layout(gtx, n, el)
}

var barTheme = material.Theme{FingerSize: 32}

// Pinned is a dialog's body over its foot (its buttons) in gtx's height:
// the foot always shows, and the body scrolls in l when the two don't fit
// (a long command once pushed an approval's buttons out of the window). It
// is as tall as both when they fit.
func Pinned(gtx layout.Context, l *widget.List, body, foot layout.Widget) layout.Dimensions {
	gtx.Constraints.Min.Y = 0
	m := op.Record(gtx.Ops)
	fd := foot(gtx)
	fc := m.Stop()
	bg := gtx
	bg.Constraints.Max.Y = max(0, gtx.Constraints.Max.Y-fd.Size.Y)
	l.Axis = layout.Vertical
	bd := ScrollList(bg, l, 1, func(gtx layout.Context, _ int) layout.Dimensions { return body(gtx) })
	defer op.Offset(image.Pt(0, bd.Size.Y)).Push(gtx.Ops).Pop()
	fc.Add(gtx.Ops)
	return layout.Dimensions{Size: image.Pt(max(bd.Size.X, fd.Size.X), bd.Size.Y+fd.Size.Y)}
}
