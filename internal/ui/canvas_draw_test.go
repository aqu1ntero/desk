package ui

import (
	"image"
	"image/color"
	"slices"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/lfdominguez/desk/internal/claude"
	"github.com/lfdominguez/desk/internal/kanban"
	"github.com/lfdominguez/desk/internal/ui/kit"
)

// measure is how big s is drawn on one line: a label's semantic bounds are
// its constraints, not its text.
func (h *harness) measure(s string, sp unit.Sp, f font.Font) image.Point {
	gtx := layout.Context{Ops: new(op.Ops), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Constraints: layout.Constraints{Max: image.Pt(1<<16, 1<<10)}}
	return widget.Label{MaxLines: 1}.Layout(gtx, h.u.k.Sh, f, sp, s, kit.Mat(gtx, kit.CText)).Size
}

// inks is whether a label reading s, as big as sz, is drawn with col
// somewhere in it.
func inks(img *image.RGBA, ls []label, s string, sz image.Point, col color.NRGBA) bool {
	for _, l := range ls {
		if l.text != s {
			continue
		}
		for y := l.at.Min.Y; y < l.at.Min.Y+sz.Y; y++ {
			for x := l.at.Min.X; x < l.at.Min.X+sz.X; x++ {
				if near(img.At(x, y), col, 18) {
					return true
				}
			}
		}
	}
	return false
}

// buttons are the clickable areas the last frame drew inside r, left to right.
func (h *harness) buttons(r image.Rectangle) (out []image.Rectangle) {
	for _, n := range h.r.AppendSemantics(nil) {
		if n.Desc.Gestures&input.ClickGesture != 0 && n.Desc.Bounds.Min.In(r) {
			out = append(out, n.Desc.Bounds)
		}
	}
	slices.SortFunc(out, func(a, b image.Rectangle) int { return a.Min.X - b.Min.X })
	return out
}

// labelIn is the label reading s whose top left is inside r.
func labelIn(t *testing.T, ls []label, s string, r image.Rectangle) image.Rectangle {
	t.Helper()
	for _, l := range ls {
		if l.text == s && l.at.Min.In(r) {
			return l.at
		}
	}
	t.Fatalf("no label %q in %v", s, r)
	return image.Rectangle{}
}

func lum(c color.Color) int {
	r, g, b, _ := c.RGBA()
	return int(r>>8 + g>>8 + b>>8)
}

// TestCanvasDraws: at 1:1, panned, everything lands where the layout says:
// the grid, frames, headers in their family's color, bodies, shadows,
// sockets in their type's color, parameter boxes, a check's dot, avatars,
// the wires (dimmed toward done nodes, lit by hover) and each text.
func TestCanvasDraws(t *testing.T) {
	h := newHarness(t)
	c := canvasCard(h)
	h.u.onTask(c.ID, &claude.Task{ID: "a9", ToolUse: "tu9", Agent: "a-very-long-agent-name-for-the-header", Model: "claude-opus-5-5", Status: "running"})
	pan := image.Pt(40, 80)
	h.u.cv[c.ID] = &cvView{zoom: 1, pan: f32.Pt(40, 80)}
	sz := image.Pt(1700, 1300)
	var act func()
	w := func(gtx layout.Context) layout.Dimensions { return h.u.canvasView(gtx, c, &act) }
	img, _ := h.shot(sz, w)
	ls := h.labels()
	v := h.u.cv[c.ID]
	at := func(x, y int) color.Color { return img.At(x+pan.X, y+pan.Y) }
	node := func(id string) cvNode {
		for _, n := range v.nodes {
			if n.id == id {
				return n
			}
		}
		t.Fatalf("no node %s", id)
		return cvNode{}
	}
	px := func(r image.Rectangle) image.Rectangle { return r.Add(pan) }

	// the grid, in px: lines every 24 from the pan, every fifth stronger
	minor, major := kit.Mix(cvBG, kit.CWhite, .03), kit.Mix(cvBG, kit.CWhite, .065)
	for _, c := range []struct {
		x, y int
		want color.NRGBA
	}{{5, 8, minor}, {5, 80, major}, {5, 20, cvBG}, {16, 5, minor}, {40, 5, major}, {28, 5, cvBG}, {5, 1040, major}, {5, 1064, minor}, {1696, 5, minor}, {1600, 5, major}} {
		if got := img.At(c.x, c.y); !near(got, c.want, 2) {
			t.Errorf("grid at %d,%d: %v, want %v", c.x, c.y, got, c.want)
		}
	}

	for _, n := range v.nodes {
		hc := famColors[n.fam]
		if n.dim {
			hc = kit.Mix(hc, cvNodeBG, .5)
		}
		if got := at(n.r.Min.X+3, n.r.Min.Y+cvHeadH-2); !near(got, hc, 3) {
			t.Errorf("%s header %v, want %v", n.id, got, hc)
		}
		if got := at(n.r.Min.X+3, n.r.Max.Y-3); !near(got, cvNodeBG, 3) {
			t.Errorf("%s body %v", n.id, got)
		}
		if got := at(n.r.Min.X, n.r.Min.Y+cvHeadH-1); !near(got, hc, 3) { // square at the foot, only the top rounds
			t.Errorf("%s header's foot corner %v, want %v", n.id, got, hc)
		}
		if got := at(n.r.Min.X+3, n.r.Min.Y+cvHeadH+2); !near(got, cvNodeBG, 3) {
			t.Errorf("%s under its header %v", n.id, got)
		}
		x := n.r.Min.X + n.r.Dx()/2 + 5
		if lum(at(x, n.r.Max.Y+1)) >= lum(at(x, n.r.Max.Y+7))-4 && lum(at(x, n.r.Max.Y+1)) >= lum(at(x, n.r.Max.Y+8))-4 {
			t.Errorf("%s has no shadow", n.id)
		}
		for i, r := range n.rows {
			y := int(n.rowY(i))
			switch {
			case r.in || r.out:
				sx := n.r.Min.X
				if r.out {
					sx = n.r.Max.X
				}
				if got := at(sx, y); !near(got, sockColors[r.sock], 6) {
					t.Errorf("%s %s socket %v, want %v", n.id, r.name, got, sockColors[r.sock])
				}
				if got := at(sx, y+5); !near(got, kit.RGB(0x141418), 10) {
					t.Errorf("%s %s socket's rim %v", n.id, r.name, got)
				}
				if got := at(sx+3, y); r.sock != sockControl && !near(got, sockColors[r.sock], 6) { // centered on its point
					t.Errorf("%s %s socket off center: %v", n.id, r.name, got)
				}
			default:
				for _, p := range []image.Point{{n.r.Min.X + 9, y}, {n.r.Min.X + 20, y - 8}, {n.r.Min.X + 20, y + 8}, {n.r.Max.X - 9, y}} {
					if got := at(p.X, p.Y); !near(got, cvParamBG, 3) {
						t.Errorf("%s %s box at %v: %v", n.id, r.name, p, got)
					}
				}
				for _, p := range []image.Point{{n.r.Min.X + 20, y - 9}, {n.r.Min.X + 20, y + 9}, {n.r.Max.X - 7, y}} {
					if got := at(p.X, p.Y); near(got, cvParamBG, 3) {
						t.Errorf("%s %s box spills to %v", n.id, r.name, p)
					}
				}
			}
		}
		if n.who != nil {
			a := image.Pt(n.r.Max.X-16, n.r.Min.Y+cvHeadH/2)
			if got := at(a.X, a.Y+7); !near(got, n.who.col, 10) {
				t.Errorf("%s avatar %v, want %v", n.id, got, n.who.col)
			}
		}
	}
	ci := node("ci:https://github.com/acme-org/api/pull/7")
	if got := at(ci.r.Min.X+13, ci.r.Min.Y+13); !near(got, kit.CRed, 12) {
		t.Errorf("CI's dot %v", got)
	}

	// frames
	names, frames := cvFrames(v.nodes)
	for i, fr := range frames {
		col := famColors[famClaude]
		if names[i] == "Delivery" {
			col = famColors[famCheck]
		}
		if got := at(fr.Max.X-6, fr.Max.Y-6); !near(got, kit.Mix(cvBG, col, .07), 3) {
			t.Errorf("%s frame inside %v", names[i], got)
		}
		if got := at(fr.Max.X-20, fr.Max.Y-1); !near(got, kit.Mix(cvBG, col, .22), 4) {
			t.Errorf("%s frame rim %v", names[i], got)
		}
		l := labelIn(t, ls, map[string]string{"Session": "SESSION", "Delivery": "DELIVERY"}[names[i]], px(fr))
		if l.Min != px(fr).Min.Add(image.Pt(12, 8)) {
			t.Errorf("%s label at %v", names[i], l.Min)
		}
	}

	// wires: a control and a context one in their colors, one to a done node dimmed
	g := cvGraph{nodes: v.nodes}
	mid := func(w cvWire) image.Point {
		segs, _, ok := cvRoute(&g, w)
		if !ok {
			t.Fatalf("no route %v", w)
		}
		return segs[0].at(.5).Round()
	}
	start, ctx, web := mid(cvWire{"card", "Start", "main", "Run"}), mid(cvWire{"card", "Context", "main", "Context"}), mid(cvWire{"main", "Agents", "agent:web", "Run"})
	if got := at(start.X, start.Y); !near(got, sockColors[sockControl], 12) {
		t.Errorf("control wire %v", got)
	}
	if got := at(ctx.X, ctx.Y); !near(got, sockColors[sockContext], 16) {
		t.Errorf("context wire %v", got)
	}
	if got := at(web.X, web.Y); near(got, sockColors[sockControl], 40) || lum(got) < lum(kit.Mix(cvBG, famColors[famClaude], .07))+60 {
		t.Errorf("wire to a done node %v: not dimmed", got)
	}

	// text, each where it goes
	m := node("main")
	if l := labelIn(t, ls, "Claude · planner", px(m.r)); l.Min != px(m.r).Min.Add(image.Pt(10, 6)) {
		t.Errorf("title at %v", l.Min)
	}
	if l := labelIn(t, ls, "PL · Waiting on api", px(m.r).Add(image.Pt(0, -20)).Inset(-4)); l.Min != px(m.r).Min.Add(image.Pt(2, -18)) {
		t.Errorf("status at %v", l.Min)
	}
	ac := image.Pt(m.r.Max.X-16, m.r.Min.Y+cvHeadH/2)
	av := px(image.Rectangle{Min: ac, Max: ac})
	if l := labelIn(t, ls, "PL", av.Inset(-14)); l.Min != av.Min.Add(image.Pt(-12+(24-h.measure("PL", 9, kit.Bold(kit.Sans)).X)/2, -6)) {
		t.Errorf("initials at %v, avatar at %v", l, av.Min)
	}
	row := func(n cvNode, name string) int {
		for i, r := range n.rows {
			if r.name == name {
				return int(n.rowY(i)) + pan.Y
			}
		}
		t.Fatalf("%s has no row %s", n.id, name)
		return 0
	}
	if l := labelIn(t, ls, "Run", px(m.r)); l.Min != image.Pt(px(m.r).Min.X+12, row(m, "Run")-8) {
		t.Errorf("an input's name at %v", l.Min)
	}
	if l := labelIn(t, ls, "Finish", px(m.r)); l.Min.X+h.measure("Finish", 11, kit.Sans).X != px(m.r).Max.X-12 || l.Min.Y != row(m, "Finish")-8 {
		t.Errorf("an output's name at %v", l)
	}
	if l := labelIn(t, ls, "usage", px(m.r)); l.Min != image.Pt(px(m.r).Min.X+14, row(m, "usage")-7) {
		t.Errorf("a parameter's name at %v", l.Min)
	}
	usage := "3 turns · 96k tokens · $1.27"
	if l := labelIn(t, ls, usage, px(m.r)); l.Min.X+h.measure(usage, 10, kit.Sans).X != px(m.r).Max.X-14 || l.Min.Y != row(m, "usage")-7 {
		t.Errorf("a parameter's value at %v", l)
	}
	// a label's bounds are its width given, give or take its glyphs' overhang
	off := func(got, want int) bool { return got-want > 2 || want-got > 2 }
	if l := labelIn(t, ls, usage, px(m.r)); off(l.Dx(), (m.r.Max.X-8-6)-(m.r.Min.X+8+6+h.measure("usage", 10, kit.Sans).X+10)) {
		t.Errorf("a parameter's value is %d wide", l.Dx())
	}
	if l := labelIn(t, ls, "Run", px(m.r)); off(l.Dx(), cvNodeW-24) {
		t.Errorf("an input's name is %d wide", l.Dx())
	}
	if l := labelIn(t, ls, "PL · Waiting on api", px(m.r).Add(image.Pt(0, -20)).Inset(-4)); off(l.Dx(), cvNodeW+60) {
		t.Errorf("a status line is %d wide", l.Dx())
	}
	card := node("card")
	if l := labelIn(t, ls, "Card · "+c.Key(), px(card.r)); off(l.Max.X, px(card.r).Max.X-8) {
		t.Errorf("a plain title ends at %d", l.Max.X)
	}
	med := kit.Medium(kit.Sans)
	st, ws := "PL · Waiting on api", "AP · Working"
	if !inks(img, ls, st, h.measure(st, 11, med), kit.Mix(pastels[0], kit.CText3, .45)) || !inks(img, ls, ws, h.measure(ws, 11, med), kit.Mix(pastels[1], kit.CWhite, .25)) {
		t.Error("status lines: idle not dimmed, or working not bright")
	}
	if !inks(img, ls, "Your turn", h.measure("Your turn", 10, kit.Sans), statusColor[kanban.StatusWaiting]) || !inks(img, ls, usage, h.measure(usage, 10, kit.Sans), kit.CText) {
		t.Error("a value in its tone, or the default")
	}
	if l := labelIn(t, ls, "CI failed", px(ci.r)); l.Min.X != px(ci.r).Min.X+22 {
		t.Errorf("a check's title past its dot: %v", l.Min)
	}
	api := node("agent:api")
	right := px(api.r).Max.X - 16 - 14
	b := labelIn(t, ls, "Sonnet 5.5", px(api.r))
	bw := h.measure("Sonnet 5.5", 10, kit.Semi(kit.Sans)).X
	if b.Min.X+bw != right || b.Min.Y != px(api.r).Min.Y+7 {
		t.Errorf("badge at %v (%d wide), its right %d", b, bw, right)
	}
	long := node("agent:a-very-long-agent-name-for-the-header")
	lb := labelIn(t, ls, "Opus 5.5", px(long.r))
	title := "Claude · a-very-long-agent-name-for-the-header"
	if lt := labelIn(t, ls, title, px(long.r)); lt.Max.X != lb.Min.X-6 { // cut where the badge starts
		t.Errorf("a long title %v beside its badge %v", lt, lb)
	}

	// the controls, in the pane's px
	hintS := "Drag to pan · " + kit.ModText("scroll") + " to zoom · click a node to open it"
	hint := labelIn(t, ls, hintS, image.Rectangle{Max: sz})
	if hint.Min != image.Pt(14, sz.Y-14-h.measure(hintS, 11, kit.Sans).Y) {
		t.Errorf("hint at %v", hint)
	}
	bs := h.buttons(image.Rect(sz.X-400, sz.Y-60, sz.X, sz.Y))
	if len(bs) != 2 {
		t.Fatalf("buttons %v", bs)
	}
	hand, fit := bs[0], bs[1]
	if fit.Max != sz.Sub(image.Pt(14, 12)) || hand.Max.X != fit.Min.X-8 || hand.Max.Y != fit.Max.Y {
		t.Errorf("Handoffs at %v, Fit at %v", hand, fit)
	}
	zs := h.measure("100%", 11, kit.Mono)
	if zoom := labelIn(t, ls, "100%", image.Rect(sz.X-500, sz.Y-60, sz.X, sz.Y)); zoom.Min != image.Pt(hand.Min.X-10-zs.X, fit.Min.Y+(fit.Dy()-zs.Y)/2) {
		t.Errorf("zoom at %v, Handoffs at %v", zoom, hand)
	}

	// hover lights a node's rim and its wires, done or not
	v.hover = "agent:web"
	img, _ = h.shot(sz, w)
	wn := node("agent:web")
	if got := at(wn.r.Min.X-1, wn.r.Min.Y+wn.r.Dy()/2); !near(got, kit.Mix(cvNodeBG, kit.CWhite, .55), 8) {
		t.Errorf("hovered rim %v", got)
	}
	if got := at(m.r.Min.X-1, m.r.Min.Y+m.r.Dy()/2); near(got, kit.Mix(cvNodeBG, kit.CWhite, .55), 30) {
		t.Errorf("an unhovered node's rim lit: %v", got)
	}
	if got := at(web.X, web.Y); !near(got, sockColors[sockControl], 12) {
		t.Errorf("a hovered node's wire %v: not lit", got)
	}
}

// TestCanvasChip: the earlier agents' chip sits centered at the foot.
func TestCanvasChip(t *testing.T) {
	h := newHarness(t)
	c := teamCard(h)
	h.u.findTask(c.ID, "a2", "").end = time.Now().Add(-settled - time.Minute)
	var act func()
	sz := image.Pt(1200, 800)
	h.draw(sz, func(gtx layout.Context) layout.Dimensions { return h.u.canvasView(gtx, c, &act) })
	bs := h.buttons(image.Rect(300, sz.Y-60, 900, sz.Y))
	if len(bs) != 1 {
		t.Fatalf("chip buttons %v", bs)
	}
	if ch := bs[0]; ch.Max.Y != sz.Y-12 || ch.Min.X != (sz.X-ch.Dx())/2 {
		t.Errorf("chip at %v", ch)
	}
}

// TestCanvasFitsAgain: fitted and untouched, the canvas fits again when the
// graph or the pane changes; once moved, it stays; an idle canvas asks for
// no frames.
func TestCanvasFitsAgain(t *testing.T) {
	h := newHarness(t)
	c := h.u.b.Card(h.u.b.Add(1, kanban.Card{Title: "Idle", Org: "Acme", Status: kanban.StatusWaiting}).ID)
	var act func()
	w := func(gtx layout.Context) layout.Dimensions { return h.u.canvasView(gtx, c, &act) }
	h.draw(image.Pt(1200, 800), w)
	if _, ok := h.r.WakeupTime(); ok {
		t.Error("an idle canvas asks for frames")
	}
	c.Status = kanban.StatusWorking // the session breathes, nothing else asks
	h.draw(image.Pt(1200, 800), w)
	if at, ok := h.r.WakeupTime(); !ok || at.After(time.Now().Add(600*time.Millisecond)) {
		t.Errorf("a working session doesn't breathe: %v %v", at, ok)
	}
	c.Status = kanban.StatusWaiting
	h.draw(image.Pt(1200, 800), w)
	v := h.u.cv[c.ID]
	b := v.fitted
	h.u.onTask(c.ID, &claude.Task{ID: "x", ToolUse: "t", Agent: "api", Status: "completed"})
	h.u.findTask(c.ID, "x", "").end = time.Now()
	h.draw(image.Pt(1200, 800), w)
	if v.fitted == b || !v.auto {
		t.Fatal("a grown graph didn't fit again")
	}
	z := v.zoom
	h.draw(image.Pt(600, 400), w)
	if v.size != image.Pt(600, 400) || v.zoom >= z {
		t.Fatalf("a smaller pane didn't fit again: %v %v", v.size, v.zoom)
	}
	v.auto = false
	pan := v.pan
	h.draw(image.Pt(1200, 800), w)
	if v.pan != pan || v.size != image.Pt(600, 400) {
		t.Fatal("moved, yet fitted again")
	}
	h.r.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: f32.Pt(5, 5)})
	h.draw(image.Pt(1200, 800), w)
	if h.r.Cursor() != pointer.CursorDefault {
		t.Errorf("cursor over nothing: %v", h.r.Cursor())
	}
}
