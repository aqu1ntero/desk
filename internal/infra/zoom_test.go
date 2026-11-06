package infra

import (
	"testing"

	"gioui.org/io/key"
	"gioui.org/layout"

	deskorgs "github.com/lfdominguez/desk/internal/orgs"
)

// TestPaneZoom: desk's zoom keys reach a pane before its shell (no key
// pressed for it), and the bigger text gives it fewer columns.
func TestPaneZoom(t *testing.T) {
	h := newGM(t)
	u := h.u
	st, _, _ := gmTermState(t)
	h.set(st)
	u.org = "Acme"
	w := func(gtx layout.Context) { u.terminals(gtx) }
	h.draw(w)
	v := u.view(st.Tabs[0].Panes[0].Term)
	h.draw(func(gtx layout.Context) { gtx.Execute(key.FocusCmd{Tag: v}); u.terminals(gtx) })
	cols := v.cols
	h.acts()
	h.key("=", key.ModShortcut)
	h.key("=", key.ModShortcut)
	h.draw(w)
	h.draw(w)
	if deskorgs.Zoom() != 120 || v.cols >= cols || len(h.acts()) != 0 {
		t.Fatalf("zoom %d, columns %d → %d", deskorgs.Zoom(), cols, v.cols)
	}
}
