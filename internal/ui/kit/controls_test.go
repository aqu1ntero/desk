package kit

import (
	"image"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// press queues a click at x, y for r's next frame.
func press(r *input.Router, x, y float32) {
	p := f32.Pt(x, y)
	r.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: p},
		pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: p})
}

// pointerMove is the mouse moving to x, y.
func pointerMove(x, y float32) pointer.Event {
	return pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: f32.Pt(x, y)}
}

// routed is a 1px-per-dp context taking r's events, w×h at most.
func routed(ops *op.Ops, r *input.Router, w, h int) layout.Context {
	ops.Reset()
	return layout.Context{Ops: ops, Source: r.Source(), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Constraints: layout.Constraints{Max: image.Pt(w, h)}}
}

// TestSwitchClick: 30×18dp, and a click on it is reported once.
func TestSwitchClick(t *testing.T) {
	k := New()
	var r input.Router
	var ops op.Ops
	frame := func() (layout.Dimensions, bool) {
		d, ok := k.Switch(routed(&ops, &r, 200, 100), "sw", false)
		r.Frame(&ops)
		return d, ok
	}
	if d, ok := frame(); ok || d.Size != image.Pt(30, 18) {
		t.Fatalf("first frame: %v %v", d.Size, ok)
	}
	press(&r, 5, 5)
	if _, ok := frame(); !ok {
		t.Fatal("click not reported")
	}
	if _, ok := frame(); ok {
		t.Fatal("click reported twice")
	}
}

// TestSwitchLook: off is a grey track with the knob left; on, an accent
// track with the knob right.
func TestSwitchLook(t *testing.T) {
	look := func(on bool) *image.RGBA {
		var ops op.Ops
		New().Switch(ctx(&ops, 60, 36), "sw", on) // 2px a dp: 60×36, knob 28 across, 4 in
		return render(t, 60, 36, &ops)
	}
	off, on := look(false), look(true)
	if c := off.At(10, 18); !near(c, CText) {
		t.Errorf("off: left %v, want the knob", c)
	}
	// the knob is 14dp round: right of it and under it is track
	for _, p := range []image.Point{{48, 18}, {38, 18}, {18, 34}} {
		if c := off.At(p.X, p.Y); !near(c, RGB(0x3a3b44)) {
			t.Errorf("off: %v = %v, want the grey track", p, c)
		}
	}
	if c := on.At(10, 18); !near(c, CAcc) {
		t.Errorf("on: left %v, want the accent track", c)
	}
	if c := on.At(48, 18); !near(c, CText) {
		t.Errorf("on: right %v, want the knob", c)
	}
	if c := on.At(42, 2); !near(c, CAcc) {
		t.Errorf("on: above the knob %v, want the track (the knob is 2dp in)", c)
	}
}

// TestStepper: − reports -1, + reports +1, the value between reports 0,
// and a step is reported only on its frame.
func TestStepper(t *testing.T) {
	k := New()
	var r input.Router
	var ops op.Ops
	frame := func() (layout.Dimensions, int) {
		d, s := k.Stepper(routed(&ops, &r, 300, 100), "st", "3")
		r.Frame(&ops)
		return d, s
	}
	// 1 rim + 26 button + 28 value + 26 button + 1 rim; 26 + 2 high
	if d, s := frame(); s != 0 || d.Size != image.Pt(82, 28) {
		t.Fatalf("first frame: %v %d", d.Size, s)
	}
	for _, c := range []struct {
		x    float32
		want int
	}{{10, -1}, {41, 0}, {70, 1}} {
		press(&r, c.x, 14)
		if _, s := frame(); s != c.want {
			t.Errorf("click at %v: %d, want %d", c.x, s, c.want)
		}
		if _, s := frame(); s != 0 {
			t.Errorf("after the click at %v: %d", c.x, s)
		}
	}
}

// TestStepperWideValue: a value wider than its 28dp slot widens the box.
func TestStepperWideValue(t *testing.T) {
	var ops op.Ops
	k := New()
	gtx := ctx(&ops, 1000, 200)
	gtx.Metric = unit.Metric{PxPerDp: 1, PxPerSp: 1}
	v := "1234567890"
	lw := k.Lbl(v, 13, CText, Sans)(gtx).Size.X
	if lw <= 28 {
		t.Fatalf("label %d not wider than its slot", lw)
	}
	if d, _ := k.Stepper(gtx, "st", v); d.Size.X != 2+26+lw+26 {
		t.Fatalf("width %d, want %d", d.Size.X, 2+26+lw+26)
	}
}
