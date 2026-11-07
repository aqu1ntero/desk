package ui

import (
	"fmt"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/layout"

	"github.com/lfdominguez/desk/internal/orgs"
	"github.com/lfdominguez/desk/internal/ui/kit"
)

// zoomKeys reads only the zoom keys: where a terminal or the servers' panes
// take the rest, the text size still works when none of them has focus.
func (u *ui) zoomKeys(gtx layout.Context, filters []event.Filter) {
	for {
		ev, ok := gtx.Event(filters...)
		if !ok {
			return
		}
		if e, ok := ev.(key.Event); ok {
			u.zoomKey(e)
		}
	}
}

// zoomKey zooms the terminals' and session panel's text when e is a zoom
// key, and says whether it was.
func (u *ui) zoomKey(e key.Event) bool {
	steps, ok := kit.ZoomKey(e)
	if ok {
		u.zoom(steps)
	}
	return ok
}

// zoom moves the text size by steps (0: back to 100%) and says where it is.
func (u *ui) zoom(steps int) {
	if err := orgs.StepZoom(steps); err != nil {
		u.showToast("Couldn't keep the text size: "+err.Error(), true)
		return
	}
	u.showToast(fmt.Sprintf("Text size %d%%", orgs.Zoom()), false)
}
