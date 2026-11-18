package ui

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/lfdominguez/desk/internal/kanban"
	"github.com/lfdominguez/desk/internal/links"
	"github.com/lfdominguez/desk/internal/orgs"
	"github.com/lfdominguez/desk/internal/vault"
)

const acmeWorkflow = `[workflow]
rules = "Test means deployed to the testing environment."

[workflow.status]
"In progress" = "In Progress"

[workflow.follow]
Test = "Review"
`

func wfTicket(n string) kanban.Link {
	return kanban.Link{URL: "https://globex.atlassian.net/browse/ABC-" + n, Kind: links.KindJira, Ref: "ABC-" + n}
}

// TestTicketFollows: the background check's ticket someone moved leads its
// card to the workflow's column, which then counts as pushed: no move back.
// One not moved by someone stays, its status kept as seen.
func TestTicketFollows(t *testing.T) {
	h := newHarness(t)
	h.orgToml("Acme", acmeWorkflow)
	pushed := 0
	pushTicket = func(context.Context, kanban.Link, string, orgs.Config) (string, error) { pushed++; return "", nil }
	synced := links.Synced{Seen: "In Progress"}
	syncTicket = func(context.Context, kanban.Link, string, orgs.Config) (links.Synced, error) { return synced, nil }
	c := h.u.b.Add(h.u.b.Column("In progress"), kanban.Card{Title: "Fix it", Org: "Acme", Links: []kanban.Link{wfTicket("1")}, Pushed: "In progress"})
	h.u.syncTickets(true)
	h.pumpUntil("the check", func() bool { return !h.u.pushing[c.ID] })
	if got := h.u.b.Card(c.ID); got.Links[0].Seen != "In Progress" || h.u.columnOf(c.ID) != "In progress" {
		t.Fatalf("seen %q, in %q", got.Links[0].Seen, h.u.columnOf(c.ID))
	}
	synced = links.Synced{Seen: "Test", Moved: true}
	h.u.syncTickets(true)
	h.pumpUntil("the follow", func() bool { return h.u.columnOf(c.ID) == "Review" && !h.u.pushing[c.ID] })
	if got := h.u.b.Card(c.ID); got.Pushed != "Review" || got.Links[0].Seen != "Test" || !strings.Contains(h.u.toast, "follows to Review") { // State: the links poll's too
		t.Fatalf("pushed %q, seen %q, toast %q", got.Pushed, got.Links[0].Seen, h.u.toast)
	}
	h.u.save()
	h.frame()
	if pushed != 0 {
		t.Fatalf("moved back: %d pushes", pushed)
	}
	if h.u.followTicket(c.ID, "ABC-1", wfTicket("1").URL, "Done", "In progress", "Done") != "" || h.u.columnOf(c.ID) != "Review" {
		t.Fatal("a card that moved on meanwhile stays")
	}
	if h.u.b.Card(c.ID).Links[0].State != "Done" {
		t.Fatal("its chip says the ticket's status still")
	}
	if h.u.columnOf("gone") != "" || h.u.followTicket("gone", "", "", "", "", "") != "" || h.u.followTicket(c.ID, "", "", "", "Review", "Nowhere") != "" {
		t.Fatal("a gone card or column")
	}
}

// TestFollowColumn: the workflow's column, the card's own when it shares
// the status, else the board's first; a GitHub issue without one closes
// to Done and reopens out of it; nothing else moves.
func TestFollowColumn(t *testing.T) {
	h := newHarness(t)
	w := orgs.Workflow{Status: map[string]string{"Blocked": "In Progress", "In progress": "In Progress"}}
	gh := kanban.Link{Kind: links.KindGitHubIssue}
	for _, c := range []struct {
		l                  kanban.Link
		status, from, want string
	}{
		{wfTicket("1"), "In Progress", "Blocked", "Blocked"},
		{wfTicket("1"), "In Progress", "Review", "In progress"},
		{wfTicket("1"), "Deployed", "Review", ""},
		{gh, "closed", "In progress", "Done"},
		{gh, "open", "Done", "In progress"},
		{gh, "open", "Review", ""},
		{gh, "other", "Review", ""},
	} {
		if got := h.u.followColumn(w, c.l, c.status, c.from); got != c.want {
			t.Errorf("%+v: %q", c, got)
		}
	}
}

// TestTicketTool: ticket_status moves the card's ticket and the card
// follows; it refuses without a workflow, a status or one ticket to pick,
// and says why a move failed; the workspace Claude hasn't it. The card's
// Claude is told the rules.
func TestTicketTool(t *testing.T) {
	h := newHarness(t)
	var asked []string
	transitionTicket = func(_ context.Context, l kanban.Link, status string, _ orgs.Config) (string, error) {
		asked = append(asked, l.Ref+">"+status)
		switch status {
		case "Locked":
			return "", &vault.LockedError{Vault: orgs.Vault{Name: "pw", Kind: "bitwarden"}}
		case "Nope":
			return "", errors.New("ABC-1 can't go there")
		}
		return status, nil
	}
	c := h.u.b.Add(h.u.b.Column("In progress"), kanban.Card{Title: "Fix it", Org: "Acme", Links: []kanban.Link{wfTicket("1")}})
	call := func(id string, args map[string]string) (text string, ok bool) {
		raw, _ := json.Marshal(args)
		h.off(func() { text, ok = h.u.cardTool(id, "ticket_status", raw) })
		return text, ok
	}
	if text, ok := call(c.ID, map[string]string{"status": "Test"}); ok || !strings.Contains(text, "no ticket workflow") {
		t.Fatalf("no workflow: %q", text)
	}
	h.orgToml("Acme", acmeWorkflow)
	for _, tc := range []struct {
		id     string
		args   map[string]string
		want   string
		wantOK bool
	}{
		{"ws:" + c.ID, map[string]string{"status": "Test"}, "no tool ticket_status", false},
		{"gone", map[string]string{"status": "Test"}, "gone from the board", false},
		{c.ID, map[string]string{}, "which status?", false},
		{c.ID, map[string]string{"status": "Nope"}, "can't go there", false},
		{c.ID, map[string]string{"status": "Locked"}, "unlock", false},
		{c.ID, map[string]string{"status": "Waiting"}, "ABC-1 is Waiting now.", true},
		{c.ID, map[string]string{"status": "Test", "url": "ABC-1"}, "The card moved to Review.", true},
	} {
		if text, ok := call(tc.id, tc.args); ok != tc.wantOK || !strings.Contains(text, tc.want) {
			t.Fatalf("%v: %q %v", tc.args, text, ok)
		}
	}
	if h.u.columnOf(c.ID) != "Review" || h.u.b.Card(c.ID).Pushed != "Review" || len(h.u.locked) != 1 {
		t.Fatalf("card in %q, locked %d", h.u.columnOf(c.ID), len(h.u.locked))
	}
	h.u.b.Card(c.ID).Links = append(h.u.b.Card(c.ID).Links, wfTicket("2"))
	if text, _ := call(c.ID, map[string]string{"status": "Test"}); !strings.Contains(text, "which ticket?") || !strings.Contains(text, "ABC-2") {
		t.Fatalf("two tickets: %q", text)
	}
	h.u.b.Card(c.ID).Links = nil
	if text, _ := call(c.ID, map[string]string{"status": "Test"}); !strings.Contains(text, "no ticket") {
		t.Fatalf("none: %q", text)
	}
	h.orgToml("Acme", "not toml [")
	if _, ok := call(c.ID, map[string]string{"status": "Test"}); ok {
		t.Fatal("a desk.toml that doesn't read")
	}

	h.orgToml("Acme", acmeWorkflow)
	d := h.u.b.Add(0, kanban.Card{Title: "Other", Org: "Acme"})
	h.u.send(d.ID, "go")
	if !strings.Contains(h.claudes[len(h.claudes)-1].opts.System, "Test means deployed to the testing environment.") {
		t.Fatal("the card's Claude isn't told the workflow")
	}
	h.orgToml("Acme", "not toml [")
	if h.u.workflowPrompt(&d) != "" {
		t.Fatal("a broken desk.toml: no workflow")
	}
}
