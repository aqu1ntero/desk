package kit

import (
	"image"
	"image/color"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
)

// Small form controls of the settings redesign. They key their clickables
// by id like Button (widget.Bool would need state kept per row by the caller).

// Switch is an on/off toggle, 30×18dp; it reports a click (the caller flips).
func (k *Kit) Switch(gtx layout.Context, id string, on bool) (layout.Dimensions, bool) {
	return k.Button(gtx, id, color.NRGBA{}, color.NRGBA{}, 9, layout.Inset{}, func(gtx layout.Context) layout.Dimensions {
		w, h, pad := gtx.Dp(30), gtx.Dp(18), gtx.Dp(2)
		track, x := RGB(0x3a3b44), pad
		if on {
			track, x = CAcc, w-h+pad
		}
		FillRRect(gtx.Ops, image.Rect(0, 0, w, h), h/2, track)
		knob := image.Rect(x, pad, x+h-2*pad, h-pad)
		paint.FillShape(gtx.Ops, CText, clip.Ellipse(knob).Op(gtx.Ops))
		return layout.Dimensions{Size: image.Pt(w, h)}
	})
}

// Stepper is − value +, on a sunken box; it reports -1, +1 or 0.
func (k *Kit) Stepper(gtx layout.Context, id, value string) (layout.Dimensions, int) {
	step := 0
	btn := func(sfx, s string, d int) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			dim, ok := k.Button(gtx, id+sfx, color.NRGBA{}, CHover, RCtl, layout.Inset{}, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min = image.Pt(gtx.Dp(26), gtx.Dp(26))
				return layout.Center.Layout(gtx, k.Lbl(s, 14, CText2, Semi(Sans)))
			})
			if ok {
				step = d
			}
			return dim
		})
	}
	dim := Boxed(CPane, CLine, RCtl, layout.UniformInset(1), func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			btn("-", "−", -1),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min = image.Pt(gtx.Dp(28), 0)
				return layout.Center.Layout(gtx, k.Lbl(value, 13, CText, Sans))
			}),
			btn("+", "+", 1))
	})(gtx)
	return dim, step
}
