package kit

import (
	"bytes"
	"image"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// size is what w lays out to, at 2px a dp, on its own ops.
func size(w layout.Widget) image.Point {
	var ops op.Ops
	return w(ctx(&ops, 2000, 500)).Size
}

// TestKeyCapSize: the label plus 5dp a side and 1dp over, 2dp under, never
// narrower than 18dp.
func TestKeyCapSize(t *testing.T) {
	k := New()
	for _, key := range []string{".", "Ctrl+Shift+S"} {
		l := size(k.Lbl(key, 11, CText2, Mono))
		want := image.Pt(max(l.X, 16)+20, l.Y+6)
		if got := size(k.KeyCap(key, false)); got != want {
			t.Errorf("%q: %v, want %v", key, got, want)
		}
	}
}

// TestKeyCapLook: a rim all round, thicker at the bottom, over a panel fill;
// on a filled button the fill is translucent.
func TestKeyCapLook(t *testing.T) {
	k := New()
	draw := func(onFill bool) (*image.RGBA, image.Point) {
		var ops op.Ops
		d := k.KeyCap("W", onFill)(ctx(&ops, 200, 100))
		return render(t, 200, 100, &ops), d.Size
	}
	img, sz := draw(false)
	w, h := sz.X, sz.Y
	for _, p := range []image.Point{{0, h / 2}, {w - 1, h / 2}, {w / 2, 0}, {w / 2, h - 1}, {w / 2, h - 3}} {
		if c := img.At(p.X, p.Y); !near(c, CKeyRim) {
			t.Errorf("%v = %v, want the rim", p, c)
		}
	}
	if c := img.At(3, h/2); !near(c, CPanelCard) {
		t.Errorf("inside %v, want the fill", c)
	}
	on, _ := draw(true)
	if c := on.RGBAAt(3, h/2); c.A == 0xff || c.A == 0 {
		t.Errorf("on a fill, inside %v, want translucent", c)
	}
}

// TestHint: the cap, 6dp, then what it does.
func TestHint(t *testing.T) {
	k := New()
	c, l := size(k.KeyCap("esc", false)), size(k.Lbl("close", 12, CText3, Sans))
	got := size(k.Hint("esc", "close"))
	if got.X != c.X+12+l.X || got.Y != max(c.Y, l.Y) {
		t.Fatalf("%v, want %d×%d", got, c.X+12+l.X, max(c.Y, l.Y))
	}
	var ops op.Ops
	k.Hint("esc", "close")(ctx(&ops, 400, 100))
	if px := render(t, 400, 100, &ops).At(3, got.Y/2); !near(px, CPanelCard) {
		t.Fatalf("cap %v, want the plain cap's fill", px)
	}
}

// TestKBtnKey: a key adds 8dp and its cap after the label.
func TestKBtnKey(t *testing.T) {
	k := New()
	btn := func(key string) image.Point {
		var ops op.Ops
		d, _ := k.KBtn(ctx(&ops, 2000, 500), "b", Secondary, "Save", key)
		return d.Size
	}
	bare, keyed, cp := btn(""), btn("S"), size(k.KeyCap("S", false))
	if keyed.X != bare.X+16+cp.X {
		t.Fatalf("with key %d, without %d, cap %d", keyed.X, bare.X, cp.X)
	}
	if l := size(k.Lbl("Save", 13, CText, Semi(Sans))); bare.X != l.X+48 {
		t.Fatalf("bare %d, label %d + 12dp a side", bare.X, l.X)
	}
}

// TestKBtnKinds: each kind's fill and rim, and the key cap translucent on
// the filled ones.
func TestKBtnKinds(t *testing.T) {
	k := New()
	cp := size(k.KeyCap("S", true))
	for _, c := range []struct {
		name      string
		kind      Kind
		fill, rim [4]uint8 // RGBA, alpha 0 for none
		capOnFill bool
	}{
		{"secondary", Secondary, rgba(CPanelCard), rgba(CBtnRim), false},
		{"primary", Primary, rgba(CAcc), rgba(CAcc), true},
		{"danger", Danger, rgba(CDanger), rgba(CDanger), true},
		{"ghost", Ghost, [4]uint8{}, [4]uint8{}, false},
		{"off", Off, rgba(CPanelCard), rgba(CBtnRim), false},
	} {
		var ops op.Ops
		d, _ := k.KBtn(ctx(&ops, 400, 100), "b"+c.name, c.kind, "Go", "S")
		img := render(t, 400, 100, &ops)
		y := d.Size.Y / 2
		at := func(x int) [4]uint8 { p := img.RGBAAt(x, y); return [4]uint8{p.R, p.G, p.B, p.A} }
		if !close4(at(6), c.fill) {
			t.Errorf("%s: fill %v, want %v", c.name, at(6), c.fill)
		}
		if !close4(at(0), c.rim) {
			t.Errorf("%s: rim %v, want %v", c.name, at(0), c.rim)
		}
		capIn := at(d.Size.X - 24 - cp.X + 3) // inside the cap, 12dp from the right edge
		if onFill := !close4(capIn, rgba(CPanelCard)) && capIn[3] != 0; onFill != c.capOnFill {
			t.Errorf("%s: cap fill %v, translucent white %v, want %v", c.name, capIn, onFill, c.capOnFill)
		}
	}
}

func rgba(c interface{ RGBA() (r, g, b, a uint32) }) [4]uint8 {
	r, g, b, a := c.RGBA()
	return [4]uint8{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}
}

func close4(a, b [4]uint8) bool {
	for i := range a {
		if int(a[i])-int(b[i]) > 2 || int(b[i])-int(a[i]) > 2 {
			return false
		}
	}
	return true
}

// TestKBtnClicks: a button reports its click; an Off one takes none.
func TestKBtnClicks(t *testing.T) {
	for _, c := range []struct {
		kind Kind
		want bool
	}{{Primary, true}, {Secondary, true}, {Off, false}} {
		k := New()
		var r input.Router
		var ops op.Ops
		frame := func() bool {
			_, ok := k.KBtn(routed(&ops, &r, 300, 100), "b", c.kind, "Go", "")
			r.Frame(&ops)
			return ok
		}
		frame()
		press(&r, 5, 5)
		if got := frame(); got != c.want {
			t.Errorf("kind %d: clicked %v, want %v", c.kind, got, c.want)
		}
	}
}

// TestKBtnHover: the filled kinds lighten under the pointer, the others
// take the hover grey, and Off keeps its fill.
func TestKBtnHover(t *testing.T) {
	for _, c := range []struct {
		name string
		kind Kind
		want [4]uint8
	}{
		{"primary", Primary, rgba(Mix(CAcc, CWhite, 0.12))},
		{"danger", Danger, rgba(Mix(CDanger, CWhite, 0.12))},
		{"secondary", Secondary, rgba(CHover)},
		{"ghost", Ghost, rgba(CHover)},
		{"off", Off, rgba(CPanelCard)},
	} {
		k := New()
		var r input.Router
		var ops op.Ops
		var d layout.Dimensions
		frame := func() *image.RGBA {
			ops.Reset()
			gtx := ctx(&ops, 300, 100)
			gtx.Source = r.Source()
			d, _ = k.KBtn(gtx, "b", c.kind, "Go", "")
			r.Frame(&ops)
			return render(t, 300, 100, &ops)
		}
		frame()
		r.Queue(pointerMove(6, 10))
		frame()
		p := frame().RGBAAt(6, d.Size.Y/2)
		if got := [4]uint8{p.R, p.G, p.B, p.A}; !close4(got, c.want) {
			t.Errorf("%s hovered: %v, want %v", c.name, got, c.want)
		}
	}
}

// modalRun lays a modal out on a 400×300 window at 1px a dp, its box 100
// high, and keeps what the box was given and how often it was dismissed.
type modalRun struct {
	k         *Kit
	r         input.Router
	ops       op.Ops
	tag       int
	dismissed int
	box       layout.Constraints
}

func (m *modalRun) frame(top, width unit.Dp, dismiss bool) layout.Dimensions {
	gtx := routed(&m.ops, &m.r, 400, 300)
	var f func()
	if dismiss {
		f = func() { m.dismissed++ }
	}
	d := m.k.Modal(gtx, &m.tag, top, width, f, func(gtx layout.Context) layout.Dimensions {
		m.box = gtx.Constraints
		return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, 100)}
	})
	m.r.Frame(&m.ops)
	return d
}

// TestModalDismiss: a press on the scrim dismisses, one on the box doesn't,
// and without a dismiss a press is just swallowed.
func TestModalDismiss(t *testing.T) {
	m := &modalRun{k: New()}
	if d := m.frame(40, 200, true); d.Size != image.Pt(400, 300) {
		t.Fatalf("modal %v, want the window", d.Size)
	}
	press(&m.r, 200, 90) // on the box: x 100–300, y 40–140
	m.frame(40, 200, true)
	if m.dismissed != 0 {
		t.Fatal("a press on the box dismissed")
	}
	m.r.Queue(pointer.Event{Kind: pointer.Scroll, Source: pointer.Mouse, Position: f32.Pt(10, 10), Scroll: f32.Pt(0, 30)})
	m.frame(40, 200, true)
	if m.dismissed != 0 {
		t.Fatal("a scroll on the scrim dismissed")
	}
	press(&m.r, 10, 10)
	m.frame(40, 200, true)
	if m.dismissed != 1 {
		t.Fatalf("a press on the scrim: %d dismissals", m.dismissed)
	}
	press(&m.r, 10, 10)
	m.frame(40, 200, false)
	m.frame(40, 200, false)
	if m.dismissed != 1 {
		t.Fatalf("no dismiss given: %d dismissals", m.dismissed)
	}
}

// TestModalBoxSize: the box gets its width, at most the window less 32dp,
// all of it, and the height below top less 24dp, never below 0.
func TestModalBoxSize(t *testing.T) {
	m := &modalRun{k: New()}
	for _, c := range []struct {
		top, width unit.Dp
		w, h       int
	}{{40, 200, 200, 236}, {40, 1000, 368, 236}, {290, 200, 200, 0}} {
		m.frame(c.top, c.width, false)
		if m.box.Max != image.Pt(c.w, c.h) || m.box.Min != image.Pt(c.w, 0) {
			t.Errorf("top %v width %v: box %+v, want %d×%d", c.top, c.width, m.box, c.w, c.h)
		}
	}
}

// TestModalPlace: the box sits centered, top dp down, on the panel, the
// scrim around it.
func TestModalPlace(t *testing.T) {
	var ops op.Ops
	gtx := ctx(&ops, 400, 300)
	gtx.Metric = unit.Metric{PxPerDp: 1, PxPerSp: 1}
	var tag int
	New().Modal(gtx, &tag, 40, 200, nil, func(gtx layout.Context) layout.Dimensions {
		return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, 100)}
	})
	img := render(t, 400, 300, &ops)
	for _, p := range []image.Point{{102, 90}, {297, 90}, {200, 43}, {200, 137}} {
		if c := img.At(p.X, p.Y); !near(c, CPanel) {
			t.Errorf("%v = %v, want the box", p, c)
		}
	}
	for _, p := range []image.Point{{97, 90}, {303, 90}, {200, 37}, {200, 143}} {
		if c := img.At(p.X, p.Y); near(c, CPanel) || near(c, CRim) {
			t.Errorf("%v = %v, want the scrim", p, c)
		}
	}
}

// TestTitle: the heading alone, or over 3dp and the line under it.
func TestTitle(t *testing.T) {
	k := New()
	h, s := size(k.Lbl("Settings", 15, CText, Bold(Sans))), size(k.Lbl("Where desk looks", 12, CText3, Sans))
	if got := size(k.Title("Settings", "")); got != h {
		t.Errorf("alone %v, want %v", got, h)
	}
	if got := size(k.Title("Settings", "Where desk looks")); got.Y != h.Y+6+s.Y || got.X != max(h.X, s.X) {
		t.Errorf("with a line %v, want %d×%d", got, max(h.X, s.X), h.Y+6+s.Y)
	}
}

// TestTitleWraps: the line under the heading wraps to the width it gets.
func TestTitleWraps(t *testing.T) {
	k := New()
	sub := "a muted line long enough to need a second one at this width"
	var ops op.Ops
	one := size(k.Title("T", sub))
	gtx := ctx(&ops, 200, 500)
	if d := k.Title("T", sub)(gtx); d.Size.Y <= one.Y || d.Size.X > 200 {
		t.Fatalf("narrow %v, wide %v", d.Size, one)
	}
}

// TestCaption: a small semibold muted label.
func TestCaption(t *testing.T) {
	k := New()
	var a, b op.Ops
	k.Caption("Name")(ctx(&a, 400, 100))
	k.Lbl("Name", 12, CText3, Semi(Sans))(ctx(&b, 400, 100))
	if !bytes.Equal(opBytes(&a), opBytes(&b)) {
		t.Fatal("Caption isn't a 12sp semibold CText3 label")
	}
}
