package kit

import (
	"gioui.org/layout"
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
