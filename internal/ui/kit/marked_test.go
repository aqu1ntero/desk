package kit

import (
	"image"
	"testing"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// TestMarked: a marked label keeps one line's height however long, and is
// as wide as its text up to the width given.
func TestMarked(t *testing.T) {
	k := New()
	gtx := func(w int) layout.Context {
		return layout.Context{Ops: new(op.Ops), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Constraints: layout.Constraints{Max: image.Pt(w, 400)}}
	}
	one := k.Lbl("web-01", 13, CText, Sans)(gtx(400)).Size
	d := k.Marked("web-01", map[int]bool{0: true, 1: true}, 13, CText, Sans)(gtx(400)).Size
	if d.Y != one.Y || d.X < one.X-2 || d.X > one.X+8 {
		t.Fatalf("marked %v, plain %v", d, one)
	}
	long := k.Marked("renew the certificate on every web server of the api group", nil, 13, CText, Sans)(gtx(60)).Size
	if long.Y != one.Y || long.X > 60 {
		t.Fatalf("a long one wraps or overflows: %v", long)
	}
	if e := k.Marked("", nil, 13, CText, Sans)(gtx(60)).Size; e != image.Pt(0, one.Y) {
		t.Fatalf("empty: %v", e)
	}
}
