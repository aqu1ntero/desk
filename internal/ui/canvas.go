package ui

import (
	"fmt"
	"image"
	"image/color"
	"strings"
	"time"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/lfdominguez/desk/internal/kanban"
	"github.com/lfdominguez/desk/internal/ui/kit"
)

// The canvas: a card's process (cardGraph) drawn as a node graph in its
// full window, as Blender draws nodes: a grid, frames, nodes with a header
// colored by family, white control wires and data wires colored by type,
// and on each Claude's node its avatar and what it's on. It pans (drag the
// empty canvas, or scroll) and zooms (Ctrl/⌘+scroll, around the pointer); a
// click on a node opens what it is. One pointer handler for the whole
// canvas, hit-tested against last frame's nodes, as the board's (drag.go).
// Read-only: what it draws is what desk knows; nothing runs from it.

// The canvas's own colors, on desk's dark theme.
var (
	cvBG      = kit.CColumn
	cvNodeBG  = kit.RGB(0x2a2b31)
	cvParamBG = kit.RGB(0x1f2025)
	cvShadow  = color.NRGBA{A: 0x60}
)

// canvasOn is whether card c's full window shows its canvas rather than its
// conversation: as picked in the header, else the canvas for a card that
// runs as an agent (its team's place before).
func (u *ui) canvasOn(c *kanban.Card) bool {
	if on, ok := u.canvas[c.ID]; ok {
		return on
	}
	return c.Agent != ""
}

// started is whether c's session has begun: before, the pane offers to start it.
func (u *ui) started(c *kanban.Card) bool { return len(u.chat(c.ID)) > 0 || len(u.tasks[c.ID]) > 0 }

// canvasMode is whether card c's pane draws its canvas now: the full
// window, Canvas picked, the session begun, nothing opened in its place.
func (u *ui) canvasMode(c *kanban.Card, full bool) bool {
	return full && u.canvasOn(c) && u.sub == "" && u.started(c)
}

// canvasBody is the canvas, in place of the conversation, and beside it the
// handoffs once asked for (its Handoffs button).
func (u *ui) canvasBody(gtx layout.Context, c *kanban.Card, act *func()) layout.Dimensions {
	gtx.Constraints.Min = gtx.Constraints.Max
	if !u.handoffsOn || gtx.Constraints.Max.X < gtx.Dp(900) { // too narrow for both: the canvas alone
		return u.canvasView(gtx, c, act)
	}
	return layout.Flex{}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return u.canvasView(gtx, c, act) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			paint.FillShape(gtx.Ops, kit.CLine, clip.Rect(image.Rect(0, 0, gtx.Dp(1), gtx.Constraints.Max.Y)).Op())
			return layout.Dimensions{Size: image.Pt(gtx.Dp(1), gtx.Constraints.Max.Y)}
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return u.handoffs(gtx, c, act) }))
}

// cvOf is card id's view, made fitted on first use.
func (u *ui) cvOf(id string) *cvView {
	v := u.cv[id]
	if v == nil {
		v = &cvView{zoom: 1, auto: true}
		u.cv[id] = v
	}
	return v
}

// canvasView draws card c's canvas over all of gtx's max.
func (u *ui) canvasView(gtx layout.Context, c *kanban.Card, act *func()) layout.Dimensions {
	size := gtx.Constraints.Max
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	paint.FillShape(gtx.Ops, cvBG, clip.Rect{Max: size}.Op())
	px := gtx.Metric.PxPerDp
	v := u.cvOf(c.ID)
	g := u.cardGraph(c, gtx.Now)
	cvLayout(g.nodes)
	if b := cvBounds(g.nodes); v.auto && (b != v.fitted || size != v.size) { // fitted and untouched: keep it fitted as the graph grows
		v.fit(b, size, px)
	}
	u.cvPointer(gtx, c.ID, v, px, act)
	v.nodes = g.nodes
	s := v.zoom * px
	cvGrid(gtx, v.pan, s, size)

	st := op.Affine(f32.Affine2D{}.Scale(f32.Point{}, f32.Pt(s, s)).Offset(v.pan)).Push(gtx.Ops)
	cg := gtx // canvas units from here: a dp is a unit, the transform scales
	cg.Metric = unit.Metric{PxPerDp: 1, PxPerSp: 1}
	cg.Constraints = layout.Constraints{Max: image.Pt(1<<20, 1<<20)}
	names, frames := cvFrames(g.nodes)
	for i, r := range frames {
		col := famColors[famClaude]
		if names[i] == "Delivery" {
			col = famColors[famCheck]
		}
		kit.FillRRect(cg.Ops, r, 10, kit.Mix(cvBG, col, .22))
		kit.FillRRect(cg.Ops, r.Inset(1), 9, kit.Mix(cvBG, col, .07))
		u.cvText(cg, image.Pt(r.Min.X+12, r.Min.Y+8), strings.ToUpper(names[i]), 10, kit.Mix(col, kit.CWhite, .35), kit.Bold(kit.Sans), 300, text.Start)
	}
	for _, w := range g.wires {
		segs, typ, ok := cvRoute(&g, w)
		if !ok {
			continue
		}
		col, width := sockColors[typ], float32(2.2)
		if typ == sockControl {
			width = 3.4
		}
		lit := v.hover == w.from || v.hover == w.to
		if to := g.node(w.to); !lit && (to.dim || g.node(w.from).dim) {
			col.A = 0x70
		}
		if lit {
			width += 1.2
		}
		var p clip.Path
		p.Begin(cg.Ops)
		p.MoveTo(segs[0][0])
		for _, sg := range segs {
			p.CubeTo(sg[1], sg[2], sg[3])
		}
		paint.FillShape(cg.Ops, col, clip.Stroke{Path: p.End(), Width: width}.Op())
	}
	busy := false
	for i := range g.nodes {
		n := &g.nodes[i]
		u.cvNode(cg, n, v.hover == n.id)
		busy = busy || n.who != nil && n.who.busy
	}
	st.Pop()

	// the handler, under the controls drawn next
	hand := clip.Rect{Max: size}.Push(gtx.Ops)
	switch {
	case v.panning:
		pointer.CursorGrabbing.Add(gtx.Ops)
	case v.hover != "" && g.node(v.hover) != nil && g.node(v.hover).open != "":
		pointer.CursorPointer.Add(gtx.Ops)
	}
	event.Op(gtx.Ops, v)
	hand.Pop()
	u.cvControls(gtx, c, v, &g, act)
	u.tick(gtx, c.ID)
	if busy { // every frame draws the whole window: ~12 a second is enough for a 1.8 s breath, and slow when not looked at
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(map[bool]time.Duration{true: 80 * time.Millisecond, false: 500 * time.Millisecond}[u.focused])})
	}
	return layout.Dimensions{Size: size}
}

// cvPointer takes the canvas's pointer events: hover, scroll pans, Ctrl/⌘
// scroll zooms around the pointer, a drag on it pans, a click on a node
// opens it (run by act, after the pane is drawn).
func (u *ui) cvPointer(gtx layout.Context, id string, v *cvView, px float32, act *func()) {
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: v, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel | pointer.Move | pointer.Leave | pointer.Scroll,
			ScrollX: pointer.ScrollRange{Min: -1 << 20, Max: 1 << 20}, ScrollY: pointer.ScrollRange{Min: -1 << 20, Max: 1 << 20}})
		if !ok {
			return
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		p := e.Position
		switch e.Kind {
		case pointer.Move:
			v.hover, v.ptr = cvHit(v.nodes, v.toCanvas(p, px)), p
		case pointer.Leave:
			v.hover = ""
		case pointer.Scroll:
			if e.Modifiers.Contain(key.ModShortcut) {
				v.zoomAt(p, wheelZoom(e.Scroll.Y))
			} else {
				v.pan, v.auto = v.pan.Sub(e.Scroll), false
			}
		case pointer.Press:
			if e.Buttons != pointer.ButtonPrimary {
				break
			}
			v.down, v.panning, v.start, v.last = true, false, p, p
			v.press = cvHit(v.nodes, v.toCanvas(p, px))
		case pointer.Drag:
			if !v.down {
				break
			}
			if d := p.Sub(v.start); !v.panning && d.X*d.X+d.Y*d.Y > 25*px*px {
				v.panning, v.hover = true, ""
				gtx.Execute(pointer.GrabCmd{Tag: v, ID: e.PointerID})
			}
			if v.panning {
				v.pan, v.auto = v.pan.Add(p.Sub(v.last)), false
			}
			v.last = p
		case pointer.Release, pointer.Cancel:
			if e.Kind == pointer.Release && v.down && !v.panning && v.press != "" && v.press == cvHit(v.nodes, v.toCanvas(p, px)) {
				for _, n := range v.nodes {
					if n.id == v.press && n.open != "" {
						open := n.open
						*act = func() { u.cvOpen(id, open) }
					}
				}
			}
			v.down, v.panning, v.press = false, false, ""
		}
	}
}

// cvOpen does what a click on a node does: its conversation, a subagent's
// or shell's, the workspace, the card's editor, a link in the browser.
func (u *ui) cvOpen(id, open string) {
	what, arg, _ := strings.Cut(open, ":")
	switch what {
	case "convo":
		u.canvas[id] = false
	case "sub":
		u.sub = arg
	case "ws":
		u.openWS(id)
	case "edit":
		u.open(id)
	case "url":
		openURL(arg)
	}
}

// cvControls are the canvas's own buttons over it: the earlier agents'
// chip, the hint, the zoom and Fit.
func (u *ui) cvControls(gtx layout.Context, c *kanban.Card, v *cvView, g *cvGraph, act *func()) {
	k, size := u.k, gtx.Constraints.Max
	place := func(at image.Point, call op.CallOp) {
		st := op.Offset(at).Push(gtx.Ops)
		call.Add(gtx.Ops)
		st.Pop()
	}
	g2 := gtx
	g2.Constraints = layout.Constraints{Max: image.Pt(gtx.Dp(420), gtx.Dp(40))}
	m := op.Record(gtx.Ops)
	d := k.Label(g2, "Drag to pan · "+kit.ModText("scroll")+" to zoom · click a node to open it", 11, kit.CText3, kit.Sans, 1)
	place(image.Pt(gtx.Dp(14), size.Y-d.Size.Y-gtx.Dp(14)), m.Stop())

	m = op.Record(gtx.Ops)
	d, fit := k.RimButton(g2, "cvFit"+c.ID, kit.CPanel, kit.CHover, kit.CRim, kit.RCtl, layout.Inset{Top: 4, Bottom: 4, Left: 10, Right: 10},
		k.Lbl("Fit", 12, kit.CText2, kit.Medium(kit.Sans)))
	call := m.Stop()
	fitAt := image.Pt(size.X-d.Size.X-gtx.Dp(14), size.Y-d.Size.Y-gtx.Dp(12))
	place(fitAt, call)
	if fit {
		v.auto, v.fitted = true, image.Rectangle{} // fits on the next frame, which the click asks for
	}
	label := "Handoffs"
	if u.handoffsOn {
		label = "Hide handoffs"
	}
	m = op.Record(gtx.Ops)
	hd, hand := k.RimButton(g2, "cvHand", kit.CPanel, kit.CHover, kit.CRim, kit.RCtl, layout.Inset{Top: 4, Bottom: 4, Left: 10, Right: 10},
		k.Lbl(label, 12, kit.CText2, kit.Medium(kit.Sans)))
	fitAt.X -= hd.Size.X + gtx.Dp(8)
	place(fitAt, m.Stop())
	if hand {
		*act = func() { u.handoffsOn = !u.handoffsOn }
	}
	m = op.Record(gtx.Ops)
	zd := k.Label(g2, fmt.Sprintf("%.0f%%", v.zoom*100), 11, kit.CText3, kit.Mono, 1)
	place(image.Pt(fitAt.X-zd.Size.X-gtx.Dp(10), fitAt.Y+(d.Size.Y-zd.Size.Y)/2), m.Stop())

	if g.earlier > 0 { // the long-done agents, off the canvas until asked for
		label := plural(g.earlier, "earlier agent", "earlier agents")
		if u.earlier[c.ID] {
			label = "Hide " + label
		}
		m := op.Record(gtx.Ops)
		d, ok := k.RimButton(g2, "cvEarlier"+c.ID, kit.CPanel, kit.CHover, kit.CRim, kit.RSheet, layout.Inset{Top: 5, Bottom: 5, Left: 12, Right: 12}, k.Lbl(label, 12, kit.CText2, kit.Sans))
		place(image.Pt((size.X-d.Size.X)/2, size.Y-d.Size.Y-gtx.Dp(12)), m.Stop())
		if ok {
			id := c.ID
			*act = func() {
				if u.earlier[id] {
					delete(u.earlier, id)
				} else {
					u.earlier[id] = true
				}
			}
		}
	}
}

// cvGrid is the canvas's grid, in the pane's px: a line every 24 units, a
// stronger one every 120.
func cvGrid(gtx layout.Context, pan f32.Point, s float32, size image.Point) {
	minor, major := kit.Mix(cvBG, kit.CWhite, .03), kit.Mix(cvBG, kit.CWhite, .065)
	step := 24 * s
	line := func(r image.Rectangle, i int) {
		c := minor
		if i%5 == 0 {
			c = major
		}
		paint.FillShape(gtx.Ops, c, clip.Rect(r).Op())
	}
	first := func(o float32) (float32, int) { // the first line at or past 0, and its index
		i := int(-o / step)
		if x := o + float32(i)*step; x < 0 {
			i++
		}
		return o + float32(i)*step, i
	}
	for x, i := first(pan.X); x < float32(size.X); x, i = x+step, i+1 {
		line(image.Rect(int(x), 0, int(x)+1, size.Y), i)
	}
	for y, i := first(pan.Y); y < float32(size.Y); y, i = y+step, i+1 {
		line(image.Rect(0, int(y), size.X, int(y)+1), i)
	}
}

// cvNode draws node n in canvas units: shadow, body, header in its
// family's color, rows and sockets; a Claude's avatar in its header and its
// status above it.
func (u *ui) cvNode(gtx layout.Context, n *cvNode, hover bool) {
	ops, r, rad := gtx.Ops, n.r, 6
	kit.FillRRect(ops, r.Add(image.Pt(0, 3)), rad+1, cvShadow)
	if hover {
		kit.FillRRect(ops, r.Inset(-2), rad+2, kit.Mix(cvNodeBG, kit.CWhite, .55))
	}
	kit.FillRRect(ops, r, rad, cvNodeBG)
	hc := famColors[n.fam]
	if n.dim {
		hc = kit.Mix(hc, cvNodeBG, .5)
	}
	head := image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+cvHeadH)
	kit.FillRRect(ops, head, rad, hc)
	paint.FillShape(ops, hc, clip.Rect(image.Rect(head.Min.X, head.Max.Y-rad, head.Max.X, head.Max.Y)).Op())
	x, right := r.Min.X+10, r.Max.X-8
	if n.tone.A != 0 { // a check's outcome, a dot before its name
		circle(ops, image.Pt(x+3, r.Min.Y+cvHeadH/2), 4, n.tone, 0)
		x += 12
	}
	if w := n.who; w != nil {
		at := image.Pt(r.Max.X-16, r.Min.Y+cvHeadH/2)
		if w.busy {
			ph := float64(gtx.Now.UnixMilli()%1800) / 1800
			ring := w.col
			ring.A = uint8((1 - ph) * 150)
			circle(ops, at, 10+int(ph*9), ring, 2)
		}
		circle(ops, at, 10, kit.Mix(w.col, kit.CWhite, .15), 0)
		circle(ops, at, 9, w.col, 0)
		u.cvText(gtx, image.Pt(at.X-12, at.Y-6), w.initials, 9, kit.RGB(0x16171b), kit.Bold(kit.Sans), 24, text.Middle)
		right = at.X - 14
		sc := kit.Mix(w.col, kit.CWhite, .25)
		if !w.busy {
			sc = kit.Mix(w.col, kit.CText3, .45)
		}
		u.cvText(gtx, image.Pt(r.Min.X+2, r.Min.Y-18), w.initials+" · "+w.status, 11, sc, kit.Medium(kit.Sans), cvNodeW+60, text.Start)
	}
	if n.badge != "" {
		d := u.cvText(gtx, image.Pt(right-110, r.Min.Y+7), n.badge, 10, kit.Mix(hc, kit.CWhite, .7), kit.Semi(kit.Sans), 110, text.End)
		right -= d + 6
	}
	tc := kit.CWhite
	if n.dim {
		tc = kit.CText2
	}
	u.cvText(gtx, image.Pt(x, r.Min.Y+6), n.title, 12, tc, kit.Semi(kit.Sans), right-x, text.Start)
	for i, row := range n.rows {
		y := int(n.rowY(i))
		switch {
		case row.in:
			u.cvText(gtx, image.Pt(r.Min.X+12, y-8), row.name, 11, kit.CText2, kit.Sans, cvNodeW-24, text.Start)
		case row.out:
			u.cvText(gtx, image.Pt(r.Min.X+12, y-8), row.name, 11, kit.CText2, kit.Sans, cvNodeW-24, text.End)
		default:
			box := image.Rect(r.Min.X+8, y-cvRowH/2+2, r.Max.X-8, y+cvRowH/2-1)
			kit.FillRRect(ops, box, 3, cvParamBG)
			nw := u.cvText(gtx, image.Pt(box.Min.X+6, y-7), row.name, 10, kit.CText3, kit.Sans, 90, text.Start)
			vc, f := row.tone, kit.Sans
			if vc.A == 0 {
				vc = kit.CText
			}
			if row.mono {
				f = kit.Mono
			}
			vx := box.Min.X + 6 + nw + 10
			u.cvText(gtx, image.Pt(vx, y-7), row.value, 10, vc, f, box.Max.X-6-vx, text.End)
		}
	}
	for i, row := range n.rows {
		if !row.in && !row.out {
			continue
		}
		x := r.Min.X
		if row.out {
			x = r.Max.X
		}
		cvSocket(ops, f32.Pt(float32(x), n.rowY(i)), row.sock)
	}
}

// cvSocket is a socket: a white diamond for control, a disc in its type's
// color for data, both rimmed dark.
func cvSocket(ops *op.Ops, c f32.Point, t cvSock) {
	if t != sockControl {
		at := image.Pt(int(c.X+.5), int(c.Y+.5))
		circle(ops, at, 6, kit.RGB(0x141418), 0)
		circle(ops, at, 4, sockColors[t], 0)
		return
	}
	diamond := func(h float32, col color.NRGBA) {
		var p clip.Path
		p.Begin(ops)
		p.MoveTo(c.Add(f32.Pt(0, -h)))
		p.LineTo(c.Add(f32.Pt(h, 0)))
		p.LineTo(c.Add(f32.Pt(0, h)))
		p.LineTo(c.Add(f32.Pt(-h, 0)))
		p.Close()
		paint.FillShape(ops, col, clip.Outline{Path: p.End()}.Op())
	}
	diamond(7, kit.RGB(0x141418))
	diamond(5, sockColors[sockControl])
}

// cvText draws s from at, one line cut to w and aligned in it, and says
// how wide it was.
func (u *ui) cvText(gtx layout.Context, at image.Point, s string, sp unit.Sp, c color.NRGBA, f font.Font, w int, al text.Alignment) int {
	if w <= 0 {
		return 0
	}
	g := gtx
	g.Constraints = layout.Constraints{Max: image.Pt(w, 1<<10)}
	m := op.Record(gtx.Ops)
	d := widget.Label{MaxLines: 1}.Layout(g, u.k.Sh, f, sp, s, kit.Mat(g, c))
	call := m.Stop()
	switch al {
	case text.End:
		at.X += w - d.Size.X
	case text.Middle:
		at.X += (w - d.Size.X) / 2
	}
	st := op.Offset(at).Push(gtx.Ops)
	call.Add(gtx.Ops)
	st.Pop()
	return d.Size.X
}
