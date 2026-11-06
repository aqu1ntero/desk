package infra

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/widget"

	"github.com/lfdominguez/desk/internal/sshc"
)

// TestHostKeyChangeOf: the box's view of a changed-key refusal, wrapped or
// not; anything else is none.
func TestHostKeyChangeOf(t *testing.T) {
	e := &sshc.HostKeyChangedError{Host: "[192.0.2.12]:2222", Type: "ED25519", Fingerprint: "SHA256:new",
		Known: []sshc.KnownLine{{File: "/home/me/.ssh/known_hosts", Line: 3, Type: "ECDSA", Fingerprint: "SHA256:old"}, {Line: 1, Type: "RSA", Fingerprint: "SHA256:cmd"}}}
	c := hostKeyChangeOf(fmt.Errorf("dial: %w", e))
	if c == nil || c.Host != e.Host || c.Type != "ED25519" || c.Remote != "SHA256:new" || len(c.Old) != 2 ||
		c.Old[0] != (oldKey{"/home/me/.ssh/known_hosts", 3, "ECDSA", "SHA256:old"}) || c.Old[1] != (oldKey{"", 1, "RSA", "SHA256:cmd"}) {
		t.Fatalf("%+v", c)
	}
	if hostKeyChangeOf(sshc.ErrHostKey) != nil || hostKeyChangeOf(errors.New("x")) != nil || hostKeyChangeOf(nil) != nil {
		t.Error("not a changed key")
	}
	if home, _ := os.UserHomeDir(); tildeHome(filepath.Join(home, ".ssh", "known_hosts")) != "~/.ssh/known_hosts" || tildeHome("/etc/ssh/ssh_known_hosts") != "/etc/ssh/ssh_known_hosts" {
		t.Error("tildeHome")
	}
}

// TestPaletteOverConnBox: with the palette open over a pane's connection
// box, keys are the palette's (esc closes it, not the pane; enter opens its
// pick), as when no box is up.
func TestPaletteOverConnBox(t *testing.T) {
	failed, _ := gmTerm(t, "web-02", 80, 24)
	failed.exited = true
	tb := &Tab{org: "Acme", name: "web-02", pinned: map[int]bool{}, panes: []*Pane{{id: 1, term: failed, name: "web-02"}}}
	tb.term = failed
	m := model{tabs: []*Tab{tb}, pal: true}
	if tb.connState() != "failed" {
		t.Fatalf("state %q", tb.connState())
	}
	r, _ := m.key(keyOf("esc"))
	if mm := r.(model); mm.pal || len(tb.panes) != 1 || tb.panes[0].closed {
		t.Fatalf("esc: palette open %v, panes %d", mm.pal, len(tb.panes))
	}
	r, _ = r.(model).key(keyOf("esc")) // the palette gone: the box takes it
	if len(tb.panes) == 1 && !tb.panes[0].closed {
		t.Error("esc on the box closes the pane")
	}
}

// TestGMHostKeyBox: a pane whose host key changed shows both keys, says
// what accepting does, and has Accept new key (a) when a known_hosts line
// holds the old one. (Accepting itself: TestSSHHostKeyChanged.)
func TestGMHostKeyBox(t *testing.T) {
	failed, _ := gmTerm(t, "web-02", 200, 24)
	failed.err = &sshc.HostKeyChangedError{Host: "web-02", Type: "ED25519", Fingerprint: "SHA256:new",
		Known: []sshc.KnownLine{{File: "/home/me/.ssh/known_hosts", Line: 3, Type: "ECDSA", Fingerprint: "SHA256:old"}}}
	failed.exited = true
	tb := &Tab{org: "Acme", name: "web-02", pinned: map[int]bool{}, panes: []*Pane{{id: 1, term: failed, name: "web-02"}}}
	gp := (&driver{}).snapshot(&model{tabs: []*Tab{tb}}).Tabs[0].Panes[0]
	if gp.Conn != "failed" || gp.KeyChange == nil || gp.KeyChange.Old[0].Print != "SHA256:old" {
		t.Fatalf("snapshot %+v", gp)
	}
	title, _, guide := connText(gp, time.Now())
	if title != "web-02's host key changed" || !strings.Contains(guide, "drops the old line from known_hosts") {
		t.Errorf("%q %q", title, guide)
	}
	h := newGM(t)
	u := h.u
	st, _, _ := gmTermState(t)
	h.set(st)
	u.org = "Acme"
	st.Tabs[0].Panes[0].Conn, st.Tabs[0].Panes[0].KeyChange = "failed", gp.KeyChange
	u.clicks = map[string]*widget.Clickable{}
	w := func(gtx layout.Context) { u.terminals(gtx) }
	h.draw(w)
	if u.clicks["connA"] == nil || u.clicks["connR"] == nil {
		t.Fatal("Accept new key and Reconnect")
	}
	u.click("connA").Click()
	h.draw(w)
	if len(h.acts()) != 1 {
		t.Error("Accept presses a key")
	}
	v := u.view(st.Tabs[0].Panes[0].Term) // typed on the pane: the box's keys, not the dead shell's
	h.draw(func(gtx layout.Context) { gtx.Execute(key.FocusCmd{Tag: v}); u.terminals(gtx) })
	h.r.Queue(key.EditEvent{Text: "R"}, key.Event{Name: key.NameEscape, State: key.Press}, key.Event{Name: key.NameUpArrow, State: key.Press})
	h.draw(w)
	if n := len(h.acts()); n != 2 {
		t.Errorf("R and Esc on the box: %d presses", n)
	}
	st.Tabs[0].Panes[0].KeyChange = &hostKeyChange{Type: "ED25519", Remote: "SHA256:x"} // no line to drop: nothing to accept
	u.clicks = map[string]*widget.Clickable{}
	h.draw(w)
	if u.clicks["connA"] != nil {
		t.Error("Accept with no known_hosts line")
	}
}
