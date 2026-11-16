package infra

import (
	"encoding/json"
	"net"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lfdominguez/desk/internal/company"
	"github.com/lfdominguez/desk/internal/testwait"
)

// ownAs records pid as o's for the test.
func ownAs(t *testing.T, pid int, o Owner) {
	t.Helper()
	Own(pid, o)
	t.Cleanup(func() { owners.Delete(pid) })
}

func linuxOnly(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("only Linux says who connected")
	}
}

// TestWhose: a connection is its process's, or the nearest parent desk
// started; a pid reused by another process isn't the old owner's.
func TestWhose(t *testing.T) {
	linuxOnly(t)
	sock := t.TempDir() + "/s.sock"
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	who := func() (Owner, bool, bool) {
		c, err := net.Dial("unix", sock)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		s, err := ln.Accept()
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		return whose(s)
	}
	if _, ok, sure := who(); ok || !sure {
		t.Fatalf("no owner yet: ok %v sure %v", ok, sure)
	}
	ownAs(t, os.Getppid(), Owner{Card: "c1", Org: "Acme"}) // the test runs under go test: a parent's
	if o, ok, _ := who(); !ok || o.Card != "c1" {
		t.Fatalf("the parent's owner: %+v %v", o, ok)
	}
	owners.Store(os.Getpid(), owned{Owner{Card: "old"}, "1"}) // an ended process's pid, now the test's
	if o, _, _ := who(); o.Card != "c1" {
		t.Errorf("a reused pid must not count: %+v", o)
	}
	if _, found := owners.Load(os.Getpid()); found {
		t.Error("the stale record is dropped")
	}
	Own(0, Owner{Card: "none"})
	if _, found := owners.Load(0); found {
		t.Error("pid 0 is no process")
	}
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if _, _, sure := whose(a); sure {
		t.Error("a pipe has no peer process")
	}
	if parentPID(-1) != 0 || procStart(-1) != "" {
		t.Error("no such process: nothing")
	}
}

// TestCardsCallAsThemselves: a card session's calls act as its own card,
// whatever they claim; an infra tab's Claude gets none of them.
func TestCardsCallAsThemselves(t *testing.T) {
	linuxOnly(t)
	runtimeA(t)
	got := make(chan string, 1)
	sock, err := ServeCards(func(card, name string, args json.RawMessage) (string, bool) {
		got <- card
		return "ok", true
	})
	if err != nil {
		t.Fatal(err)
	}
	ownAs(t, os.Getpid(), Owner{Card: "c1", Org: "Acme"})
	if r := forward(sock, toolCall{Name: "alerts", Card: "infra:Globex"}); r.IsError || <-got != "c1" {
		t.Errorf("a claimed identity must give way to the caller's: %+v", r)
	}
	bad, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	bad.Write([]byte("not json\n"))
	bad.SetReadDeadline(time.Now().Add(2 * time.Second))
	if n, _ := bad.Read(make([]byte, 8)); n != 0 {
		t.Error("a bad call gets no answer")
	}
	bad.Close()
	ownAs(t, os.Getpid(), Owner{Org: "Acme", Tab: &Tab{}})
	if r := forward(sock, toolCall{Name: "alerts", Card: "c1"}); !r.IsError || !strings.Contains(r.Text, "card sessions") {
		t.Errorf("a tab's Claude has no card tools: %+v", r)
	}
}

// TestTabToolsAreItsOwn: an infra tab's tools answer its own Claude, not
// another of desk's.
func TestTabToolsAreItsOwn(t *testing.T) {
	linuxOnly(t)
	runtimeA(t)
	tab := &Tab{org: "Acme"}
	sock, ln, err := serveTools(tab)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	ownAs(t, os.Getpid(), Owner{Card: "c1", Peer: "DESK-1", Org: "Globex"})
	if r := forward(sock, toolCall{Name: "hosts"}); !r.IsError || !strings.Contains(r.Text, "another tab's") {
		t.Errorf("a card's Claude must not reach a tab's tools: %+v", r)
	}
	ownAs(t, os.Getpid(), Owner{Org: "Acme", Tab: tab})
	if r := forward(sock, toolCall{Name: "hosts"}); strings.Contains(r.Text, "another tab's") {
		t.Errorf("the tab's own Claude: %+v", r)
	}
}

// TestPeerAsCard: a card's Claude speaks as its card, to its own
// organization's Claude only, and sees only that organization.
func TestPeerAsCard(t *testing.T) {
	o := Owner{Card: "c1", Peer: "DESK-1", Org: "Acme"}
	r := peerReq{Op: "tell", Org: "acme", From: "DESK-9"}
	if err := o.as(&r); err != nil || r.From != "DESK-1" || r.Org != "Acme" || r.only != "Acme" {
		t.Errorf("its own org: %+v %v", r, err)
	}
	if err := o.as(&peerReq{Op: "do", Org: "Globex"}); err == nil {
		t.Error("another organization's Claude must refuse")
	}
	if err := o.as(&peerReq{Op: "list"}); err != nil {
		t.Errorf("list needs no org: %v", err)
	}
	if err := (Owner{Card: "ws:c1", Org: "Acme"}).as(&peerReq{Op: "list"}); err == nil {
		t.Error("only a card's Claude uses desk peer")
	}
	m := model{tree: []node{{depth: 0, org: "Acme", dir: t.TempDir()}, {depth: 0, org: "Globex", dir: t.TempDir()}}}
	if l := m.peerList("Acme"); !strings.Contains(l, "org Acme") || strings.Contains(l, "Globex") {
		t.Errorf("list shows its org only: %q", l)
	}
	if r := m.peer(peerReq{Op: "tell", Org: "globex", From: "dev", Text: "x"}); r.Err != "" {
		t.Fatal(r.Err)
	}
	tab := m.tab()
	go tab.askPeer("", "which tables?")
	evs := waitEvents("dev", 5*time.Second)
	if len(evs) != 1 {
		t.Fatalf("question: %+v", evs)
	}
	if r := m.peer(peerReq{Op: "answer", ID: evs[0].ID, Text: "mine", only: "Acme"}); r.Err == "" {
		t.Error("another organization's question must look missing")
	}
	if r := m.peer(peerReq{Op: "answer", ID: evs[0].ID, Text: "events"}); r.Err != "" {
		t.Errorf("still open for its own: %v", r.Err)
	}
}

// TestPeerSocketHoldsCards: desk peer from a card's Claude can't reach
// another organization.
func TestPeerSocketHoldsCards(t *testing.T) {
	linuxOnly(t)
	runtimeA(t)
	sock, err := servePeers()
	if err != nil {
		t.Fatal(err)
	}
	ownAs(t, os.Getpid(), Owner{Card: "c1", Peer: "DESK-1", Org: "Acme"})
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	json.NewEncoder(c).Encode(peerReq{Op: "tell", Org: "Globex", From: "DESK-1", Text: "x"})
	var resp peerResp
	if err := json.NewDecoder(c).Decode(&resp); err != nil || !strings.Contains(resp.Err, "own organization") {
		t.Errorf("refused: %+v %v", resp, err)
	}
}

// TestPeerNotes: what a card hands the infra Claude, and what comes back
// to the card, go to the organization's company log through Cards.Note.
func TestPeerNotes(t *testing.T) {
	notes := make(chan company.Entry, 8)
	var w Workspace
	w.SetCards(Cards{Is: func(n string) bool { return strings.HasPrefix(n, "DESK-") },
		OrgOf: func(string) string { return "Acme" },
		Tell:  func(key, text string) error { return nil },
		Note:  func(org string, e company.Entry) { e.Text = org + ": " + e.Text; notes <- e }})
	defer w.SetCards(Cards{})
	m := model{tree: []node{{depth: 0, org: "Acme", dir: t.TempDir()}}}
	m.peer(peerReq{Op: "tell", Org: "acme", From: "DESK-3", Text: "db is postgres"})
	m.peer(peerReq{Op: "bogus", Org: "acme", From: "DESK-3"})
	tab := m.tab()
	tab.tellCard("DESK-3", "disk is full")
	tab.taskDone(7, "DESK-3", "done", "rotated")
	var got []company.Entry
	for len(got) < 3 {
		select {
		case e := <-notes:
			got = append(got, e)
		case <-time.After(testwait.Long):
			t.Fatalf("notes %+v", got)
		}
	}
	want := []company.Entry{
		{Kind: "message", From: "DESK-3", To: "Infra lead", Card: "DESK-3", Text: "Acme: db is postgres"},
		{Kind: "message", From: "Infra lead", To: "DESK-3", Card: "DESK-3", Text: "Acme: disk is full"},
		{Kind: "result", From: "Infra lead", To: "DESK-3", Card: "DESK-3", Text: "Acme: done\nrotated"},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("note %d: %+v, want %+v", i, got[i], want[i])
		}
	}
	w.SetCards(Cards{})
	note("Acme", company.Entry{}) // no board: nothing to note to
}

// TestTellCardOwnOrg: message_card writes only to the tab's organization's cards.
func TestTellCardOwnOrg(t *testing.T) {
	var w Workspace
	w.SetCards(Cards{Is: func(n string) bool { return strings.HasPrefix(n, "DESK-") },
		OrgOf: func(key string) string { return map[string]string{"DESK-1": "Acme", "DESK-2": "Globex"}[key] },
		Tell:  func(key, text string) error { return nil }})
	defer w.SetCards(Cards{})
	tab := &Tab{org: "Acme"}
	if r := tab.tellCard("DESK-1", "x"); r.IsError {
		t.Errorf("its own org's card: %+v", r)
	}
	if r := tab.tellCard("DESK-2", "x"); !r.IsError || !strings.Contains(r.Text, "not one of Acme's") {
		t.Errorf("another org's card: %+v", r)
	}
}
