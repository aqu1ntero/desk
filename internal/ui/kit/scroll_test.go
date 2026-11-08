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
