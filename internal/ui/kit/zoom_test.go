package kit

import (
	"testing"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/unit"
)

// TestZoomKey: Ctrl with =, + (Shift), - and 0 zoom; a release, no Ctrl,
// Ctrl+Alt or another key don't.
func TestZoomKey(t *testing.T) {
	ev := func(n key.Name, m key.Modifiers) key.Event { return key.Event{Name: n, Modifiers: m, State: key.Press} }
	for _, c := range []struct {
		e     key.Event
		steps int
		ok    bool
	}{
		{ev("=", key.ModShortcut), 1, true},
		{ev("+", key.ModShortcut|key.ModShift), 1, true},
		{ev("-", key.ModShortcut), -1, true},
		{ev("0", key.ModShortcut), 0, true},
		{key.Event{Name: "=", Modifiers: key.ModShortcut, State: key.Release}, 0, false},
		{ev("=", 0), 0, false},
		{ev("-", key.ModShift), 0, false},
		{ev("=", key.ModShortcut|key.ModAlt), 0, false},
		{ev("A", key.ModShortcut), 0, false},
	} {
		if s, ok := ZoomKey(c.e); s != c.steps || ok != c.ok {
			t.Errorf("%v %v: %d %v", c.e.Name, c.e.Modifiers, s, ok)
		}
	}
	if fs := ZoomFilters(); len(fs) != 4 {
		t.Errorf("%d filters", len(fs))
	} else if f := fs[1].(key.Filter); f.Name != "+" || f.Required != key.ModShortcut || f.Optional != key.ModShift {
		t.Errorf("filter %+v", f)
	}
}

// TestZoomed: sp scale by the percentage, dp stay.
func TestZoomed(t *testing.T) {
	gtx := layout.Context{Metric: unit.Metric{PxPerDp: 2, PxPerSp: 2}}
	z := Zoomed(gtx, 150)
	if z.Metric.PxPerSp != 3 || z.Metric.PxPerDp != 2 || gtx.Metric.PxPerSp != 2 {
		t.Errorf("zoomed %+v, original %+v", z.Metric, gtx.Metric)
	}
}
