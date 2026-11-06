package ui

import (
	"fmt"

	"gioui.org/io/event"
	"gioui.org/io/key"

	"github.com/lfdominguez/desk/internal/orgs"
	"github.com/lfdominguez/desk/internal/ui/kit"
)

// zoomFilters are the zoom keys, heard everywhere but in a terminal, which
// hears its own (termEvents).
func zoomFilters() []event.Filter { return kit.ZoomFilters() }

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
