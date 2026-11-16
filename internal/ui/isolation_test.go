package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/lfdominguez/desk/internal/testwait"

	"github.com/lfdominguez/desk/internal/infra"
)

// TestDrawNameStaysInOrg: a drawing's name can't lead out of its
// organization's folder into another's.
func TestDrawNameStaysInOrg(t *testing.T) {
	h := newHarness(t)
	c := acmeCard(h)
	other := h.u.drawingsDir("Globex")
	os.MkdirAll(other, 0o755)
	os.WriteFile(filepath.Join(other, "Secret.excalidraw"), []byte(`{"type":"excalidraw","elements":[]}`), 0o644)
	rel, _ := filepath.Rel(h.u.cardDrawingsDir("Acme", c.ID), filepath.Join(other, "Secret"))
	if text, ok := h.tool(c.ID, "drawing_read", `{"drawing":"`+filepath.ToSlash(rel)+`"}`); ok {
		t.Errorf("another organization's drawing was read: %q", text)
	}
}

// pidSession is a session with a process, as claude.Proc's.
type pidSession struct {
	session
	pid int
}

func (p pidSession) Pid() int { return p.pid }

// TestOwnAndCardOrg: a started Claude is recorded as its card's; a card's
// organization is found by its key.
func TestOwnAndCardOrg(t *testing.T) {
	h := newHarness(t)
	c := acmeCard(h)
	own(nil, infra.Owner{Card: "x"})                                    // a fake without a process: nothing to record
	own(pidSession{pid: 1 << 30}, infra.Owner{Card: c.ID, Org: "Acme"}) // past any pid: no process of the test's
	cmd := exec.Command("true")
	if pidOf(cmd) != 0 {
		t.Error("not started: no process")
	}
	if cmd.Start() == nil {
		cmd.Wait()
		if pidOf(cmd) <= 0 {
			t.Error("started: its pid")
		}
	}
	got := make(chan string, 1)
	go func() { got <- h.u.cardOrgAsk(c.Key()) }()
	deadline := time.Now().Add(testwait.Long)
	for g := ""; g == ""; {
		select {
		case g = <-got:
			if g != "Acme" {
				t.Errorf("asked from another goroutine: %q", g)
			}
		case e := <-h.u.events:
			e()
		case <-time.After(time.Until(deadline)):
			t.Fatal("the board didn't answer")
		}
	}
	if h.u.cardOrg(c.Key()) != "Acme" || h.u.cardOrg("DESK-999") != "" {
		t.Errorf("cardOrg: %q", h.u.cardOrg(c.Key()))
	}
}
