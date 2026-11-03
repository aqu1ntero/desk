package kit

import (
	"image"
	"image/color"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"
)

// The dialog look from the redesign: every action shows its key as a cap,
// buttons come in four kinds, and modals share one frame.

var (
	CKeyRim  = RGB(0x3b3c45) // a key cap's rim and the rim of menus and popovers
	CBtnRim  = RGB(0x383944) // a secondary button's rim
	CDanger  = RGB(0xc9302a) // a destructive button's fill
	CSel     = RGB(0x1d3554) // the selected row of a list, menu or palette
	CRedSoft = RGB(0x2a1a1c) // the background of a destructive banner
	CRedText = RGB(0xff8a84)
	CFoot    = RGB(0x1f2026) // a dialog footer's band
	NRGBA0   = color.NRGBA{} // no color: no rim, no fill
	// CDim is a modal's scrim. Gio blends in linear light, where black at
	// half alpha barely darkens a dark window: this much reads as the
	// mockups' rgba(5,6,8,.55).
	CDim = color.NRGBA{5, 6, 8, 0xb4}
)

// Kind is a button's role: Primary is the one action Enter runs, Danger is
// one that can't be undone; Off is one that can't be used now (dimmed, takes
// no clicks).
type Kind int

const (
	Secondary Kind = iota
	Primary
	Ghost
	Danger
	Off
)

// KeyCap draws key k (a letter, "esc", "↵", "Ctrl+S"…) as a raised cap; on
// a filled button it turns translucent white.
func (k *Kit) KeyCap(key string, onFill bool) layout.Widget {
	fg, bg, rim := CText2, CPanelCard, CKeyRim
	if onFill {
		fg, bg, rim = CWhite, color.NRGBA{0xff, 0xff, 0xff, 0x29}, color.NRGBA{0xff, 0xff, 0xff, 0x47}
	}
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min = image.Point{}
		return layout.Stack{}.Layout(gtx,
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				// a thicker bottom edge makes it read as a key
				r := image.Rectangle{Max: gtx.Constraints.Min}
				FillRRect(gtx.Ops, r, gtx.Dp(3), rim)
				in := image.Rect(r.Min.X+gtx.Dp(1), r.Min.Y+gtx.Dp(1), r.Max.X-gtx.Dp(1), r.Max.Y-gtx.Dp(2))
				FillRRect(gtx.Ops, in, gtx.Dp(2), bg)
				return layout.Dimensions{Size: gtx.Constraints.Min}
			}),
			layout.Stacked(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: 1, Bottom: 2, Left: 5, Right: 5}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = max(gtx.Constraints.Min.X, gtx.Dp(8))
					return layout.Center.Layout(gtx, k.Lbl(key, 11, fg, Mono))
				})
			}))
	}
}

// Hint is a key cap followed by what it does, for footers and hint rows.
func (k *Kit) Hint(key, what string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(k.KeyCap(key, false)), layout.Rigid(HGap(6)),
			layout.Rigid(k.Lbl(what, 12, CText3, Sans)))
	}
}

// KBtn is a dialog button: label, then its key cap when key isn't "".
func (k *Kit) KBtn(gtx layout.Context, id string, kind Kind, label, key string) (layout.Dimensions, bool) {
	fg, fill, hover, rim := CText, CPanelCard, CHover, CBtnRim
	switch kind {
	case Primary:
		fg, fill, hover, rim = CWhite, CAcc, Mix(CAcc, CWhite, 0.12), color.NRGBA{}
	case Danger:
		fg, fill, hover, rim = CWhite, CDanger, Mix(CDanger, CWhite, 0.12), color.NRGBA{}
	case Ghost:
		fg, fill, rim = CText2, color.NRGBA{}, color.NRGBA{}
	case Off:
		fg, hover = CText3, fill
		gtx = gtx.Disabled()
	}
	onFill := kind == Primary || kind == Danger
	return k.RimButton(gtx, id, fill, hover, rim, RCtl, layout.Inset{Top: 7, Bottom: 7, Left: 12, Right: 12}, func(gtx layout.Context) layout.Dimensions {
		ch := []layout.FlexChild{layout.Rigid(k.Lbl(label, 13, fg, Semi(Sans)))}
		if key != "" {
			ch = append(ch, layout.Rigid(HGap(8)), layout.Rigid(k.KeyCap(key, onFill)))
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx, ch...)
	})
}

// Modal dims the window, swallows pointer input outside the box (a press
// there calls dismiss, when given), and lays box out centered, top dp down,
// at most width dp wide, on the dialog surface.
func (k *Kit) Modal(gtx layout.Context, tag event.Tag, top, width unit.Dp, dismiss func(), box layout.Widget) layout.Dimensions {
	Fill(gtx, CDim)
	full := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
	event.Op(gtx.Ops, tag)
	full.Pop()
	pressed := false
	for {
		e, ok := gtx.Event(pointer.Filter{Target: tag, Kinds: pointer.Press | pointer.Scroll, ScrollY: pointer.ScrollRange{Min: -1 << 20, Max: 1 << 20}})
		if !ok {
			break
		}
		if p, ok := e.(pointer.Event); ok && p.Kind == pointer.Press {
			pressed = true
		}
	}
	if pressed && dismiss != nil {
		dismiss()
	}
	defer op.Offset(image.Pt(0, gtx.Dp(top))).Push(gtx.Ops).Pop()
	cgtx := gtx
	cgtx.Constraints.Min = image.Point{}
	cgtx.Constraints.Max.X = min(gtx.Constraints.Max.X-gtx.Dp(32), gtx.Dp(width))
	cgtx.Constraints.Max.Y = max(0, gtx.Constraints.Max.Y-gtx.Dp(top)-gtx.Dp(24))
	m := op.Record(gtx.Ops)
	d := Boxed(CPanel, CRim, RSheet, layout.Inset{}, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		m := op.Record(gtx.Ops)
		d := box(gtx)
		call := m.Stop()
		// the box takes the presses that land on it, under its own widgets
		st := clip.Rect{Max: d.Size}.Push(gtx.Ops)
		event.Op(gtx.Ops, &boxTag)
		st.Pop()
		call.Add(gtx.Ops)
		return d
	})(cgtx)
	call := m.Stop()
	defer op.Offset(image.Pt((gtx.Constraints.Max.X-d.Size.X)/2, 0)).Push(gtx.Ops).Pop()
	call.Add(gtx.Ops)
	return layout.Dimensions{Size: gtx.Constraints.Max}
}

var boxTag int // the tag of a modal box's press blocker; nothing reads it

// Title is a dialog's heading with a muted line under it.
func (k *Kit) Title(title, sub string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		ch := []layout.FlexChild{layout.Rigid(k.Lbl(title, 15, CText, Bold(Sans)))}
		if sub != "" {
			ch = append(ch, layout.Rigid(VGap(3)), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return k.Label(gtx, sub, 12, CText3, Sans, 0)
			}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
	}
}

// Caption is a small field label above an input.
func (k *Kit) Caption(s string) layout.Widget { return k.Lbl(s, 12, CText3, Semi(Sans)) }
