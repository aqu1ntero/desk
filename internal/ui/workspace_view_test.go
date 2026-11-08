package ui

import (
	"image"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lfdominguez/desk/internal/kanban"
)

// label is a text the last frame drew, with where.
type label struct {
	text string
	at   image.Rectangle
}

// labels are the texts the last frame drew (each widget.Label says its text
// to the semantic tree).
func (h *harness) labels() (out []label) {
	for _, n := range h.r.AppendSemantics(nil) {
		if n.Desc.Label != "" {
			out = append(out, label{n.Desc.Label, n.Desc.Bounds})
		}
	}
	return out
}

// count is how many labels drawn read exactly s, inside area.
func (h *harness) count(s string, area image.Rectangle) (n int) {
	for _, l := range h.labels() {
		if l.text == s && l.at.Min.In(area) {
			n++
		}
	}
	return n
}

// treeArea is the workspace's tree: right of the sidebar, 260dp wide.
var treeArea = image.Rect(233, 0, 233+260, 900)

// TestWorkspaceTreeDraws: folders say open or closed, files nothing; rows
// indent by depth; only an added project has a remove button.
func TestWorkspaceTreeDraws(t *testing.T) {
	h := newHarness(t)
	c, dir := wsCard(h)
	os.MkdirAll(filepath.Join(dir, "pkg"), 0o755)
	os.WriteFile(filepath.Join(dir, "pkg", "a.go"), []byte("package pkg\n"), 0o644)
	h.u.openWS(c.ID)
	h.frame()
	h.clickButton("wsRow" + filepath.Join(dir, "pkg"))
	// api ▾, pkg ▾, a.go, v2 ▸, main.go
	if o, cl := h.count("▾", treeArea), h.count("▸", treeArea); o != 2 || cl != 1 { // files have none
		t.Fatalf("marks: open %d closed %d", o, cl)
	}
	x := func(name string) int {
		for _, l := range h.labels() {
			if l.text == name && l.at.Min.In(treeArea) {
				return l.at.Min.X
			}
		}
		t.Fatalf("%s not in the tree", name)
		return 0
	}
	if d0, d1 := x("pkg")-x("api"), x("a.go")-x("main.go"); d0 != 12 || d1 != 12 { // a level deeper, same mark
		t.Errorf("indents %d %d", d0, d1)
	}
	extra := t.TempDir()
	h.u.addProject(c.ID, extra)
	h.frame()
	if _, ok := h.seek("wsDrop"+extra, treeArea, h.frame); !ok {
		t.Fatal("an added project has no remove button")
	}
	if _, ok := h.seek("wsDrop"+dir, treeArea, h.frame); ok {
		t.Error("the card's own folder can be removed")
	}
}

// TestWorkspaceCenterDraws: with no file a hint; a file's tab (● with edits),
// its caret's line and column; the change bar's text or its note.
func TestWorkspaceCenterDraws(t *testing.T) {
	h := newHarness(t)
	c, dir := wsCard(h)
	h.u.openWS(c.ID)
	h.frame()
	hint := "Open a file from the tree on the left."
	if _, ok := h.shown(hint); !ok {
		t.Fatal("no hint with nothing open")
	}
	st := h.u.wsOf(c.ID)
	tb := h.u.wsOpen(st, filepath.Join(dir, "main.go"))
	h.frame()
	if _, ok := h.shown(hint); ok {
		t.Error("the hint over an open file")
	}
	tb.ed.SetCaret(16, 16) // line 3, past "fu"
	h.frame()
	if _, ok := h.shown("Ln 3, Col 3"); !ok {
		t.Errorf("caret: %v", h.labels())
	}
	if h.count("main.go", image.Rect(0, 0, 1700, 900)) != 2 { // the tree's row and the tab
		t.Error("the tab's name")
	}
	tb.dirty = true
	h.frame()
	if _, ok := h.shown("main.go ●"); !ok {
		t.Error("an edited tab not marked")
	}
	tb.edit = &kanban.Edit{Old: "a", New: "b"}
	h.frame()
	if _, ok := h.shown("The change to main.go from the chat, selected"); !ok {
		t.Error("the change bar")
	}
	tb.note = "Not found in main.go any more."
	h.frame()
	if l, ok := h.shown("Not found"); !ok || l.text != tb.note {
		t.Errorf("the note: %+v", l)
	}
}

// TestWorkspaceChatDraws: the head says whether it works; an empty chat
// explains itself; the composer's foot says what goes with the message; a
// run of one speaker names it once.
func TestWorkspaceChatDraws(t *testing.T) {
	h := newHarness(t)
	c, dir := wsCard(h)
	h.u.openWS(c.ID)
	h.frame()
	st := h.u.wsOf(c.ID)
	for _, s := range []string{"Reads the projects with you; never writes", "Ask about the code", "↵ sends with nothing open: just your words"} {
		if _, ok := h.shown(s); !ok {
			t.Errorf("%q not shown", s)
		}
	}
	st.from = []string{"you"}
	h.frame()
	if _, ok := h.shown("Working…"); !ok {
		t.Error("not working")
	}
	st.from = nil
	tb := h.u.wsOpen(st, filepath.Join(dir, "main.go"))
	h.frame()
	if l, ok := h.shown("↵ sends with"); !ok || l.text != "↵ sends with main.go" {
		t.Errorf("file: %q", l.text)
	}
	tb.ed.SetCaret(14, 20)
	h.frame()
	if l, _ := h.shown("↵ sends with"); l.text != "↵ sends with the selection in main.go" {
		t.Errorf("selection: %q", l.text)
	}
	tb.edit = &kanban.Edit{Old: "a", New: "b"}
	h.frame()
	if l, _ := h.shown("↵ sends with"); l.text != "↵ sends with the change to main.go" {
		t.Errorf("change: %q", l.text)
	}
	h.u.say(wsKey(c.ID), "you", "one")
	h.u.say(wsKey(c.ID), "you", "two")
	h.u.say(wsKey(c.ID), "claude", "three")
	h.frame()
	all := image.Rect(0, 0, 1700, 900)
	if _, ok := h.shown("Ask about the code"); ok {
		t.Error("the empty chat's text over messages")
	}
	if y, cl := h.count("You", all), h.count("Claude", all); y != 1 || cl != 1 {
		t.Errorf("names: You %d Claude %d", y, cl)
	}
}

// selected are the labels drawn inside areas marked selected.
func (h *harness) selected() (out []string) {
	nodes := h.r.AppendSemantics(nil)
	for _, n := range nodes {
		if !n.Desc.Selected {
			continue
		}
		for _, l := range h.labels() {
			if l.at.Min.In(n.Desc.Bounds) {
				out = append(out, l.text)
			}
		}
	}
	return out
}

// TestWorkspaceSelected: the file shown is the selected row of the tree
// and the selected tab, for screen readers.
func TestWorkspaceSelected(t *testing.T) {
	h := newHarness(t)
	c, dir := wsCard(h)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b\n"), 0o644)
	st := h.u.wsOf(c.ID)
	h.u.openWS(c.ID)
	h.u.wsOpen(st, filepath.Join(dir, "main.go"))
	h.u.wsOpen(st, filepath.Join(dir, "b.txt"))
	h.frame()
	if got := h.selected(); slices.Contains(got, "main.go") || len(slices.DeleteFunc(slices.Clone(got), func(s string) bool { return s != "b.txt" })) != 2 {
		t.Fatalf("selected %q", got)
	}
}

// TestWorkspaceTips: the tips of buttons near a right edge end at the
// button, inside the window and the tree.
func TestWorkspaceTips(t *testing.T) {
	h := newHarness(t)
	c, _ := wsCard(h)
	extra := t.TempDir()
	h.u.addProject(c.ID, extra)
	h.u.openWS(c.ID)
	h.frame()
	for id, tip := range map[string]string{"wsBack": "Back to the session (Esc)", "wsDrop" + extra: "Remove from the workspace"} {
		at, ok := h.seek(id, image.Rectangle{Max: h.size}, h.frame)
		if !ok {
			t.Fatalf("%s not on screen", id)
		}
		h.frame()
		if l, ok := h.shown(tip); !ok || l.at.Min.X > int(at.X)-40 {
			t.Errorf("%s's tip at %v, the button at %v", id, l.at, at)
		}
	}
}

// TestWorkspaceBackBlurs: back to the session, the composer lets go of the keyboard.
func TestWorkspaceBackBlurs(t *testing.T) {
	h := newHarness(t)
	c, _ := wsCard(h)
	st := h.u.wsOf(c.ID)
	h.u.openWS(c.ID)
	h.u.focusLater = &st.in
	h.frame()
	if !h.r.Source().Focused(&st.in) {
		t.Fatal("the composer not focused")
	}
	h.clickButton("wsBack")
	if h.u.view != "sess:"+c.ID || h.r.Source().Focused(&st.in) {
		t.Errorf("view %q, composer focused %v", h.u.view, h.r.Source().Focused(&st.in))
	}
}

// shown is the first label drawn that contains s.
func (h *harness) shown(s string) (label, bool) {
	for _, l := range h.labels() {
		if strings.Contains(l.text, s) {
			return l, true
		}
	}
	return label{}, false
}
