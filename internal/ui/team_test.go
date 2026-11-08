package ui

import (
	"image"
	"strings"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"

	"github.com/lfdominguez/desk/internal/claude"
	"github.com/lfdominguez/desk/internal/kanban"
	"github.com/lfdominguez/desk/internal/ui/kit"
)

// TestTeamMembers: one member per kind in the order they came, each its
// pastel; a kind run again keeps its running run; a member's running shell
// is its own, not another's, and a done or unowned shell is nobody's.
func TestTeamMembers(t *testing.T) {
	h := newHarness(t)
	c := teamCard(h)
	h.u.onTask(c.ID, &claude.Task{ID: "a3", ToolUse: "tu4", Name: "Parse again", Agent: "api", Status: "completed"})
	h.u.onTask(c.ID, &claude.Task{ID: "a5", ToolUse: "tu7", Name: "Untyped", Status: "completed"}) // no kind: its name
	h.u.toolBy["tu8"], h.u.toolBy["tu9"] = "tu1", "nobody"
	h.u.onTask(c.ID, &claude.Task{ID: "s3", ToolUse: "tu8", Shell: true, Status: "completed"}) // api's, done
	h.u.onTask(c.ID, &claude.Task{ID: "s4", ToolUse: "tu9", Shell: true, Status: "running"})   // by a task desk never saw
	h.u.onTask(c.ID, &claude.Task{ID: "s5", ToolUse: "tu10", Shell: true, Status: "running"})  // the session's
	ms := h.u.members(c.ID)
	var names []string
	for _, m := range ms {
		names = append(names, m.name)
	}
	if strings.Join(names, ",") != "api,web,Untyped" {
		t.Fatalf("members %v", names)
	}
	for i, m := range ms {
		if m.col != pastels[1+i] {
			t.Errorf("%s colored %v, want pastel %d", m.name, m.col, 1+i)
		}
	}
	if ms[0].runs != 2 || ms[0].last.ID != "a1" {
		t.Fatalf("api: %d runs, shown %s", ms[0].runs, ms[0].last.ID)
	}
	if ms[0].shell == nil || ms[0].shell.ID != "s1" || ms[1].shell != nil || ms[2].shell != nil {
		t.Fatalf("shells: %v %v %v", ms[0].shell, ms[1].shell, ms[2].shell)
	}
	h.u.onTask(c.ID, &claude.Task{ID: "a6", ToolUse: "tu11", Agent: "web", Status: "completed"})
	if ms := h.u.members(c.ID); ms[1].last.ID != "a6" {
		t.Fatalf("web, both done: shown %s, want the latest", ms[1].last.ID)
	}
	for _, k := range []string{"d", "e", "f", "g"} { // seven kinds: the pastels after the lead's go round again
		h.u.onTask(c.ID, &claude.Task{ID: "k" + k, ToolUse: "tk" + k, Agent: k, Status: "completed"})
	}
	if ms := h.u.members(c.ID); len(ms) != 7 || ms[6].col != pastels[1] || ms[5].col != pastels[6] {
		t.Fatalf("seven kinds: %d, the last in %v", len(ms), ms[len(ms)-1].col)
	}
	if got := teamFacts(&task{Task: claude.Task{Tools: 3, Tokens: 1500}, start: time.Now()}, time.Now()); got != "3 tools · "+tokens(1500)+" tokens · 0s" {
		t.Errorf("facts %q", got)
	}
	if got := teamFacts(&task{start: time.Now()}, time.Now()); got != "0s" {
		t.Errorf("no facts %q", got)
	}
}

// TestHandoffs: briefs and results in order, the lead in its own color
// and each agent in its; who the lead waits for, once each; a hovered
// handoff names whom it opens; none at all says so.
func TestHandoffs(t *testing.T) {
	h := newHarness(t)
	c := teamCard(h)
	h.u.findTask(c.ID, "a1", "").Summary = "Halfway" // running: no result yet
	h.u.onTask(c.ID, &claude.Task{ID: "a7", ToolUse: "tu12", Agent: "api", Status: "running"})
	h.u.onTask(c.ID, &claude.Task{ID: "a8", ToolUse: "tu13", Agent: "planner", Status: "completed"}) // an agent named as the lead: no brief, no result
	sz := image.Pt(380, 700)
	var act func()
	w := func(gtx layout.Context) layout.Dimensions { return h.u.handoffs(gtx, c, &act) }
	img, _ := h.shot(sz, w)
	all := image.Rectangle{Max: sz}
	for _, s := range []string{"Parse the new statement format; PUT upserts only.", "Upload screen with per-file progress.", "Upload screen done, es and en copy.", "planner is waiting for api…"} {
		if h.count(s, all) != 1 {
			t.Errorf("%q drawn %d times", s, h.count(s, all))
		}
	}
	if h.count("Halfway", all) != 0 {
		t.Error("a running agent's result")
	}
	semi := kit.Semi(kit.Sans)
	pl, ap := h.measure("planner", 11, semi), h.measure("api", 11, semi)
	if a, b, c, d := inks(img, h.labels(), "planner", pl, pastels[0]), inks(img, h.labels(), "api", ap, pastels[1]), inks(img, h.labels(), "api", ap, pastels[0]), inks(img, h.labels(), "planner", pl, pastels[3]); !a || !b || c || d {
		t.Errorf("handoff names in the wrong colors: %v %v %v %v", a, b, c, d)
	}
	hover := func(s string) { // the pointer over the handoff that reads s
		for _, l := range h.labels() {
			if l.text == s {
				h.r.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: f32.Pt(float32(l.at.Min.X+4), float32(l.at.Min.Y+4))})
			}
		}
		h.draw(sz, w)
		h.draw(sz, w)
	}
	hover("Parse the new statement format; PUT upserts only.")
	if h.count("Open api", all) != 1 {
		t.Error("a brief's tip doesn't name its agent")
	}
	brief := labelIn(t, h.labels(), "Parse the new statement format; PUT upserts only.", all)
	if tip := labelIn(t, h.labels(), "Open api", all); tip.Min.X != brief.Min.X-12+8 { // under the handoff's left edge
		t.Errorf("tip at %v, the brief at %v", tip.Min, brief.Min)
	}
	hover("Upload screen done, es and en copy.")
	if h.count("Open web", all) != 1 {
		t.Error("a result's tip doesn't name its agent")
	}
	empty := h.u.b.Card(h.u.b.Add(0, kanban.Card{Title: "y", Org: "Acme"}).ID)
	h.draw(sz, func(gtx layout.Context) layout.Dimensions { return h.u.handoffs(gtx, empty, &act) })
	if h.count("No handoffs yet.", all) != 1 || h.count("planner is waiting for api…", all) != 0 {
		t.Error("no handoffs: not said")
	}
}
