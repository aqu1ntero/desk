package ui

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/pointer"

	"github.com/lfdominguez/desk/internal/claude"
	"github.com/lfdominguez/desk/internal/company"
	"github.com/lfdominguez/desk/internal/kanban"
)

// TestCoGeometry: sectors share the circle by planets (at least 3 each),
// planets spread on their rings within their sector, hits find the
// nearest drawn, and chords run from one planet to the other.
func TestCoGeometry(t *testing.T) {
	secs := coSectors([]string{"Acme", "Globex"}, map[string]int{"Acme": 9, "Globex": 1})
	if len(secs) != 2 || secs[0].a0 != 2 || secs[0].a1-secs[0].a0 != 352*9/12.0 || secs[1].a1 > 358.01 {
		t.Fatalf("%+v", secs)
	}
	ps := []coPlanet{{org: "Acme", ring: 2}, {org: "Acme", ring: 2}, {org: "Globex", ring: 0}}
	radii := coRadii(100)
	coPlace(ps, secs, radii)
	for i, p := range ps {
		d := p.x*p.x + p.y*p.y
		if want := radii[p.ring] * radii[p.ring]; d < want-0.5 || d > want+0.5 {
			t.Errorf("planet %d off its ring: %v", i, d)
		}
	}
	if ps[0].x == ps[1].x || ps[2].x >= 0 {
		t.Errorf("spread: %+v", ps)
	}
	if coHit(ps, ps[1].x+3, ps[1].y) != 1 || coHit(ps, 0, 0) != -1 {
		t.Error("hits")
	}
	if x, y := coChord(ps[0], ps[2], 0); x != ps[0].x || y != ps[0].y {
		t.Error("a chord starts at its first planet")
	}
	if x, y := coChord(ps[0], ps[2], 1); x != ps[2].x || y != ps[2].y {
		t.Error("and ends at the other")
	}
	if coShort("Rotate the staging certificates", 10) != "Rotate th…" || coShort("ok", 10) != "ok" {
		t.Error("coShort")
	}
}

// coAt is where planet id is in the window: the view knows its px, and
// where the pointer was in them.
func (h *harness) coAt(id string) f32.Point {
	h.t.Helper()
	probe := f32.Pt(float32(h.size.X)-200, float32(h.size.Y)-40)
	h.send(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: probe})
	off := probe.Sub(h.u.co.ptr)
	for _, p := range h.u.co.ps {
		if p.id == id {
			return off.Add(h.u.co.at).Add(f32.Pt(p.x, p.y).Mul(h.u.co.px))
		}
	}
	h.t.Fatalf("no planet %s", id)
	return f32.Point{}
}

// TestCompanyView: the sidebar's Company shows each organization's
// executives, leads and cards in their states; a lead on a brief and a
// role at work are busy; hand-offs of the last minutes fly; a click on a
// card's planet or note opens its session.
func TestCompanyView(t *testing.T) {
	h := newHarness(t)
	work := h.u.b.Card(h.u.b.Add(0, kanban.Card{Title: "Split the API", Org: "Acme", Status: kanban.StatusWorking, Session: kanban.Session{ID: "s1"}}).ID)
	paused := h.u.b.Card(h.u.b.Add(0, kanban.Card{Title: "Rotate keys", Org: "Globex", Status: kanban.StatusWaiting, Note: "Paused: over its budget", Session: kanban.Session{ID: "s2"}}).ID)
	h.u.b.Add(0, kanban.Card{Title: "No session yet", Org: "Acme"})
	for _, st := range []string{kanban.StatusWaiting, kanban.StatusBlocked, kanban.StatusReview, kanban.StatusDone} {
		h.u.b.Add(0, kanban.Card{Title: st, Org: "Acme", Status: st, Session: kanban.Session{ID: st}})
	}
	h.u.onTask(work.ID, &claude.Task{ID: "a1", Agent: "desk-cto", Status: "running"})
	h.u.leads = map[string]*leadState{leadKey("Acme", "security"): {org: "Acme", dept: company.Departments[1], from: []string{work.ID}}}
	acme := filepath.Join(h.u.root, "Acme")
	now := time.Now()
	company.Log(acme, company.Entry{At: now, Kind: "brief", From: work.Key(), To: "Security lead"})
	company.Log(acme, company.Entry{At: now, Kind: "brief", From: work.Key(), To: "desk-cto"})
	company.Log(acme, company.Entry{At: now.Add(-time.Hour), Kind: "brief", From: work.Key(), To: "desk-ciso"}) // long landed
	company.Log(acme, company.Entry{At: now, Kind: "brief", From: work.Key(), To: "desk-qa"})                   // not in the sky
	if h.u.count(viewCompany) != 1 {
		t.Errorf("the sidebar counts who needs the user: %d", h.u.count(viewCompany))
	}
	h.clickButton("side" + viewCompany)
	if h.u.view != viewCompany || h.u.viewTitle() != "Company" || !strings.HasPrefix(h.u.sentence(), "1 busy · 1 need you · $") {
		t.Fatalf("view %q %q %q", h.u.view, h.u.viewTitle(), h.u.sentence())
	}
	h.frame()
	_, ps, links := h.u.coModel(time.Now())
	state := map[string]string{}
	for _, p := range ps {
		state[p.org+" "+p.label] = p.state
	}
	if state["Acme CTO"] != "work" || state["Globex CTO"] != "" || state["Acme Security lead"] != "work" || state["Acme "+work.Key()] != "work" ||
		state["Globex "+paused.Key()] != "you" || len(links) != 2 {
		t.Fatalf("states %v, links %v", state, links)
	}
	for _, want := range []string{"turn", "stuck", "done"} {
		if !slices.Contains(slices.Collect(func(yield func(string) bool) {
			for _, s := range state {
				if !yield(s) {
					return
				}
			}
		}), want) {
			t.Errorf("no %s card", want)
		}
	}
	h.clickAt(h.coAt("card:" + work.ID))
	h.frame()
	if h.u.view != "sess:"+work.ID {
		t.Fatalf("a planet's click: view %q", h.u.view)
	}
	h.u.view = viewCompany
	h.frame()
	h.clickAt(h.coAt("lead:Acme:security")) // a lead has no session view of its own
	h.frame()
	if h.u.view != viewCompany {
		t.Fatalf("a lead's click: view %q", h.u.view)
	}
	note := h.u.co.notes[0]
	probe := f32.Pt(float32(h.size.X)-200, float32(h.size.Y)-40)
	h.send(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: probe})
	h.clickAt(probe.Sub(h.u.co.ptr).Add(f32.Pt(float32(note.r.Min.X+20), float32(note.r.Min.Y+20))))
	h.frame()
	if h.u.view != "sess:"+note.card {
		t.Fatalf("a note's click: view %q", h.u.view)
	}
}

// TestCompanyEmpty: no one working yet says so; a narrow window has no
// notes panel.
func TestCompanyEmpty(t *testing.T) {
	h := newHarness(t)
	h.u.view = viewCompany
	h.frame()
	if len(h.u.co.ps) != 0 || h.u.sentence() != "0 busy · 0 need you · $0.00 spent today" {
		t.Fatalf("%d planets, %q", len(h.u.co.ps), h.u.sentence())
	}
}
