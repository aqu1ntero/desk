package infra

import (
	"testing"

	"gioui.org/layout"

	deskorgs "github.com/lfdominguez/desk/internal/orgs"
)

// TestPaneZoom: a bigger desk text size gives a pane fewer columns. The
// keys are desk's (ui's keys, polled before any pane).
func TestPaneZoom(t *testing.T) {
	h := newGM(t)
	u := h.u
	st, _, _ := gmTermState(t)
	h.set(st)
	u.org = "Acme"
	w := func(gtx layout.Context) { u.terminals(gtx) }
	h.draw(w)
	v := u.view(st.Tabs[0].Panes[0].Term)
	cols := v.cols
	if err := deskorgs.SetZoom(120); err != nil {
		t.Fatal(err)
	}
	h.draw(w)
	if v.cols >= cols {
		t.Fatalf("columns %d → %d", cols, v.cols)
	}
}
