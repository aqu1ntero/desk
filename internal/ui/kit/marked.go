package kit

import (
	"image"
	"image/color"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"
	"gioui.org/x/styledtext"
)

// Marked is a one-line label whose runes at marks (a search's matches)
// draw bold in the accent text color.
// ponytail: past the width it is cut at the line's end, no ellipsis;
// measure and trim the spans if long titles need one.
func (k *Kit) Marked(s string, marks map[int]bool, sp unit.Sp, c color.NRGBA, f font.Font) layout.Widget {
	var spans []styledtext.SpanStyle
	for i, r := range []rune(s) {
		st := styledtext.SpanStyle{Font: f, Size: sp, Color: c}
		if marks[i] {
			st.Font, st.Color = Bold(f), CAccText
		}
		if n := len(spans); n > 0 && spans[n-1].Font == st.Font && spans[n-1].Color == st.Color {
			spans[n-1].Content += string(r)
			continue
		}
		st.Content = string(r)
		spans = append(spans, st)
	}
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min = image.Point{}
		m := op.Record(gtx.Ops) // one line's height, from a plain label
		one := k.Label(gtx, "Ag", sp, c, f, 1)
		m.Stop()
		h := one.Size.Y
		if len(spans) == 0 {
			return layout.Dimensions{Size: image.Pt(0, h)}
		}
		m = op.Record(gtx.Ops)
		d := styledtext.Text(k.Sh, spans...).Layout(gtx, nil)
		call := m.Stop()
		sz := image.Pt(min(d.Size.X, gtx.Constraints.Max.X), h)
		defer clip.Rect{Max: sz}.Push(gtx.Ops).Pop()
		call.Add(gtx.Ops)
		return layout.Dimensions{Size: sz, Baseline: one.Baseline}
	}
}
