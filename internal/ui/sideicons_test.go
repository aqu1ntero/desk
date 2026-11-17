package ui

import (
	"testing"

	"github.com/lfdominguez/desk/internal/kanban"
)

// TestSideSpinner: a working session's sidebar row spins, and so does
// Working while any card works; its terminal or a question stops it, as
// do the other statuses. The frame that drew one asks for the next.
func TestSideSpinner(t *testing.T) {
	h := newHarness(t)
	c := h.u.b.Add(0, kanban.Card{Title: "x", Org: "Acme", Status: kanban.StatusWorking, Session: kanban.Session{ID: "s1"}})
	h.frame()
	if !h.u.spinning("sess:"+c.ID, 0) || !h.u.spinning("st:working", 1) || !h.u.spin {
		t.Fatal("a working session doesn't spin")
	}
	h.u.asks[c.ID] = &askState{}
	if h.u.spinning("sess:"+c.ID, 0) || h.u.spinning("st:working", 0) || h.u.spinning("sess:gone", 0) || h.u.spinning("st:waiting", 1) {
		t.Fatal("spins while asking, with nothing working, or for another status")
	}
	delete(h.u.asks, c.ID)
	h.u.b.Card(c.ID).Status = kanban.StatusWaiting
	h.u.save()
	h.frame()
	if h.u.spin {
		t.Fatal("still asks for frames with nothing working")
	}
	if statusIcon[kanban.StatusWaiting] == nil || statusIcon[kanban.StatusBlocked] == nil {
		t.Fatal("a status without its icon")
	}
}
