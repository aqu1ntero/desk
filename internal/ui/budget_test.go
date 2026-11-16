package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lfdominguez/desk/internal/claude"
	"github.com/lfdominguez/desk/internal/company"
	"github.com/lfdominguez/desk/internal/kanban"
)

// TestBudget: a turn's cost goes to the organization's day; a session
// over its budget pauses, what other Claudes send it waits, and the
// user's next message continues it with what waited.
func TestBudget(t *testing.T) {
	h := newHarness(t)
	org := filepath.Join(h.u.root, "Acme")
	writeAt(filepath.Join(org, ".desk", "desk.toml"), []byte("[company]\ncard_budget = 1\n"), 0o644)
	id := acmeCard(h).ID
	h.u.send(id, "start")
	f := h.mainClaudes()[0]
	f.on(claude.Event{Kind: claude.KindResult, Cost: 0.4})
	h.frame()
	if c := h.u.b.Card(id); c.Status != kanban.StatusWaiting || c.Note != "" {
		t.Fatalf("under budget: %q %q", c.Status, c.Note)
	}
	f.on(claude.Event{Kind: claude.KindResult, Cost: 1.2})
	h.frame()
	c := h.u.b.Card(id)
	if !strings.HasPrefix(c.Note, "Paused: over its $1.00 budget") || company.Spent(org, time.Now()) != 1.2 {
		t.Fatalf("over: %q, spent %v", c.Note, company.Spent(org, time.Now()))
	}
	sent := len(f.prompts)
	h.u.wsTell(id, "Look at main.go.")
	h.u.deliver(id, cameIn, true)
	if len(f.prompts) != sent || !strings.Contains(h.u.peerOut[id], "Look at main.go.") || strings.Contains(h.u.peerOut[id], cameIn) {
		t.Fatalf("held: prompts %q, out %q", f.prompts[sent:], h.u.peerOut[id])
	}
	h.u.send(id, "Go on.")
	if len(f.prompts) != sent+1 || !strings.Contains(f.prompts[sent], "Look at main.go.") || h.u.b.Card(id).Allowed != 1.2 {
		t.Fatalf("the user's go-ahead: %q, allowed %v", f.prompts[sent:], h.u.b.Card(id).Allowed)
	}
	os.RemoveAll(company.Dir(org))
	os.WriteFile(company.Dir(org), nil, 0o644) // the spend can't be kept: said, the turn goes on
	f.on(claude.Event{Kind: claude.KindResult, Cost: 1.5})
	h.frame()
	if !strings.HasPrefix(h.u.toast, "Company spend: ") || h.u.b.Card(id).Session.Cost != 1.5 { // the process reports its total so far
		t.Errorf("toast %q, cost %v", h.u.toast, h.u.b.Card(id).Session.Cost)
	}
}
