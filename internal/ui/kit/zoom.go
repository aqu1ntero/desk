package kit

import (
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/layout"
)

// ZoomKey is a zoom shortcut's steps: Ctrl+= (or Ctrl++) +1, Ctrl+- -1,
// Ctrl+0 0 (back to 100%). ok is false for any other key or a release.
func ZoomKey(e key.Event) (steps int, ok bool) {
	if e.State != key.Press || !e.Modifiers.Contain(key.ModShortcut) || e.Modifiers.Contain(key.ModAlt) {
		return 0, false
	}
	switch e.Name {
	case "=", "+":
		return 1, true
	case "-":
		return -1, true
	case "0":
		return 0, true
	}
	return 0, false
}

// ZoomFilters are ZoomKey's keys, for a focus-less listener.
func ZoomFilters() []event.Filter {
	var fs []event.Filter
	for _, n := range []key.Name{"=", "+", "-", "0"} {
		fs = append(fs, key.Filter{Name: n, Required: key.ModShortcut, Optional: key.ModShift})
	}
	return fs
}

// Zoomed is gtx with its text at pct percent of its size: sizes in sp
// scale, sizes in dp don't.
func Zoomed(gtx layout.Context, pct int) layout.Context {
	gtx.Metric.PxPerSp *= float32(pct) / 100
	return gtx
}
