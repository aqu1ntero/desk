package kit

import (
	"image"
	"testing"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/widget"
)

func TestScrollList(t *testing.T) {
	run := func(n int) (layout.Dimensions, int) {
		var l widget.List
		l.Axis = layout.Vertical
		laid := 0
		d := ScrollList(ctx(new(op.Ops), 200, 100), &l, n, func(gtx layout.Context, i int) layout.Dimensions {
			laid++
			return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, 20)}
		})
		return d, laid
	}
	if d, laid := run(3); d.Size.Y != 60 || laid != 3 {
		t.Errorf("3 rows: %v tall, %d laid out; want 60, 3", d.Size.Y, laid)
	}
	if d, laid := run(50); d.Size.Y != 100 || laid >= 50 {
		t.Errorf("50 rows: %v tall, %d laid out; want the 100 it has, only the visible ones", d.Size.Y, laid)
	}
}

// TestPinned: a dialog's foot stays in view; its body scrolls in what's
// left when both don't fit, and the dialog is as tall as both when they do.
func TestPinned(t *testing.T) {
	run := func(bodyH int) layout.Dimensions {
		var l widget.List
		foot := func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: image.Pt(50, 30)} }
		body := func(gtx layout.Context) layout.Dimensions {
			return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, bodyH)}
		}
		return Pinned(ctx(new(op.Ops), 200, 100), &l, body, foot)
	}
	if d := run(40); d.Size != image.Pt(200, 70) {
		t.Errorf("fits: %v, want 200x70", d.Size)
	}
	if d := run(5000); d.Size != image.Pt(200, 100) {
		t.Errorf("too tall: %v, want the 200x100 it has, the foot inside", d.Size)
	}
}
