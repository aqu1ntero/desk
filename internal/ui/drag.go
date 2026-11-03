package ui

import (
	"image"
	"image/color"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"

	"github.com/lfdominguez/desk/internal/ui/kit"
)

// dragState is a press on a card: a click when released in place, a drag
// once it moves past a few dp.
type dragState struct {
	press     string      // the card pressed, "" none
	start, at image.Point // where the press began; the pointer now
	off       image.Point // pointer minus the card's corner, so it doesn't jump
	w         int         // the card's width
	on        bool        // dragging
}

func (l boardLayout) cardAt(p image.Point) (hit, bool) {
	for _, h := range l.cards {
		if p.In(h.r) && p.In(l.cols[h.col]) {
			return h, true
		}
	}
	return hit{}, false
}

// colNear is the column under p's x, or the nearest one.
func (l boardLayout) colNear(p image.Point) int {
	best, bd := -1, 0
	for i, r := range l.cols {
		d := max(r.Min.X-p.X, p.X-r.Max.X, 0)
		if best < 0 || d < bd {
			best, bd = i, d
		}
	}
	return best
}

// dropAt is where card dragged lands if dropped at p: a column, and the card
// it goes before ("" the end). The dragged card itself doesn't count.
func (l boardLayout) dropAt(p image.Point, dragged string) (col int, before string) {
	col = l.colNear(p)
	for _, h := range l.cards {
		if h.col == col && h.id != dragged && p.Y < (h.r.Min.Y+h.r.Max.Y)/2 {
			return col, h.id
		}
	}
	return col, ""
}

// pointer takes the board's pointer events: hover, wheel scroll, click and
// drag on cards. One handler for the whole board, hit-tested against the
// last frame's layout, so positions are all in board coordinates.
func (u *ui) pointer(gtx layout.Context) {
	tag := &u.drag
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: tag, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel | pointer.Move | pointer.Leave | pointer.Scroll,
			ScrollY: pointer.ScrollRange{Min: -1 << 20, Max: 1 << 20}})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		p := e.Position.Round()
		switch e.Kind {
		case pointer.Move:
			h, _ := u.lay.cardAt(p)
			u.hover = h.id
		case pointer.Leave:
			u.hover = ""
		case pointer.Scroll:
			if c := u.lay.colNear(p); c >= 0 {
				u.scroll[c] += int(e.Scroll.Y)
			}
		case pointer.Press:
			if u.menu != nil {
				if p.In(u.menu.r) { // its items take it
					break
				}
				u.menu = nil
			}
			if e.Buttons == pointer.ButtonSecondary { // a card's menu
				if h, ok := u.lay.cardAt(p); ok {
					u.menu, u.sel = &cardMenu{id: h.id, at: p}, h.id
				}
				break
			}
			if e.Buttons != pointer.ButtonPrimary {
				break
			}
			u.drag = dragState{start: p, at: p}
			if h, ok := u.lay.cardAt(p); ok {
				u.drag.press, u.drag.off, u.drag.w = h.id, p.Sub(h.r.Min), h.r.Dx()
			}
		case pointer.Drag:
			u.drag.at = p
			if d := p.Sub(u.drag.start); u.drag.press != "" && !u.drag.on && d.X*d.X+d.Y*d.Y > gtx.Dp(5)*gtx.Dp(5) {
				u.drag.on, u.hover = true, ""
				gtx.Execute(pointer.GrabCmd{Tag: tag, ID: e.PointerID})
			}
		case pointer.Release:
			id := u.drag.press
			switch {
			case u.drag.on:
				col, before := u.lay.dropAt(p, id)
				u.b.MoveBefore(id, col, before)
				u.sel = id
				u.save()
			case id != "":
				u.sel = id
				u.openPanel(id)
			}
			u.drag = dragState{}
		case pointer.Cancel:
			u.drag = dragState{}
		}
	}
	event.Op(gtx.Ops, tag)
}

// dragOverlay draws, over the board, where the card will land and the card
// itself under the pointer.
func (u *ui) dragOverlay(gtx layout.Context) {
	l := u.lay
	col, before := l.dropAt(u.drag.at, u.drag.press)
	if col < 0 {
		return
	}
	body, gap := l.cols[col], gtx.Dp(8)
	y := body.Min.Y - gap/2
	for _, h := range l.cards {
		if h.col != col || h.id == u.drag.press {
			continue
		}
		if h.id == before {
			y = h.r.Min.Y - gap/2
			break
		}
		y = h.r.Max.Y + gap/2
	}
	y = min(max(y, body.Min.Y+gtx.Dp(1)), body.Max.Y-gtx.Dp(2))
	in := gtx.Dp(10)
	kit.FillRRect(gtx.Ops, image.Rect(body.Min.X+in-2, y-gtx.Dp(1), body.Max.X-in+2, y+gtx.Dp(2)), gtx.Dp(2), kit.CAcc)

	b := u.b
	ci, j := b.Find(u.drag.press)
	if ci < 0 {
		return
	}
	s := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops) // the whole board shows the grab
	pointer.CursorGrabbing.Add(gtx.Ops)
	s.Pop()

	c := b.Columns[ci].Cards[j]
	st := op.Offset(u.drag.at.Sub(u.drag.off)).Push(gtx.Ops)
	defer st.Pop()
	gtx.Constraints = layout.Constraints{Min: image.Pt(u.drag.w, 0), Max: image.Pt(u.drag.w, 1<<20)}
	m := op.Record(gtx.Ops)
	d := u.cardBody(gtx, c, true, false, ci == b.Column("Done"))
	call := m.Stop()
	for _, o := range []int{gtx.Dp(3), gtx.Dp(7)} { // a soft shadow: two dark, offset layers
		kit.FillRRect(gtx.Ops, image.Rect(-o/3, o/2, d.Size.X+o/3, d.Size.Y+o), gtx.Dp(kit.RCard)+o/2, color.NRGBA{A: 0x38})
	}
	call.Add(gtx.Ops)
}
