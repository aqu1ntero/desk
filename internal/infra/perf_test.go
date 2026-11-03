package infra

import (
	"encoding/json"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/x/styledtext"
)

// The window's caches: each test pins that work is skipped while its input
// stays the same and done again once it changes.

// TestEmitSkipsUnchanged: output alone makes no snapshot; a finished
// command, a flash or an action does.
func TestEmitSkipsUnchanged(t *testing.T) {
	term, _ := gmTerm(t, "web1", 40, 6)
	tb := &Tab{org: "Acme", name: "web1", pinned: map[int]bool{}, term: term,
		panes: []*Pane{{id: 1, term: term, name: "web1", waitUntil: time.Now().Add(time.Hour)}}}
	m := &model{tabs: []*Tab{tb, {org: "Acme", name: "Acme", orgOnly: true, pinned: map[int]bool{}}}}
	d := &driver{in: make(chan any, 4)}
	redraws := 0
	d.redraw = func() { redraws++ }
	d.emit(m)
	s1 := d.state.Load()
	gmOut(term, "plain output\r\n")
	if d.emit(m); d.state.Load() != s1 || redraws != 1 {
		t.Fatalf("output alone made a snapshot: %d redraws", redraws)
	}
	gmOut(term, gmBlock("ls", "a\r\n", "0"))
	d.emit(m)
	s2 := d.state.Load()
	if s2 == s1 || len(s2.Tabs[0].Panes[0].Blocks) != 1 || redraws != 2 {
		t.Fatalf("a finished command shows no badge: %d redraws", redraws)
	}
	m.flash = "copied"
	if d.emit(m); d.state.Load().Flash != "copied" || redraws != 3 {
		t.Fatalf("a flash made no snapshot: %d", redraws)
	}
	d.dirty = true // an action ran: a snapshot even with the same signature
	if d.emit(m); redraws != 4 || d.dirty {
		t.Fatalf("an action made no snapshot: %d", redraws)
	}
	if d.emit(m); redraws != 4 {
		t.Fatal("a snapshot with nothing new")
	}
}

// TestLoopRedrawNoSnapshot: the redraw a pane's output sends makes no new
// snapshot when nothing it shows changed; an action does.
func TestLoopRedrawNoSnapshot(t *testing.T) {
	gmEnv(t)
	d := &driver{in: make(chan any, 16)}
	redraws := make(chan struct{}, 16)
	d.redraw, d.quit = func() { redraws <- struct{}{} }, func() {}
	go d.loop(model{})
	defer d.act(func(*model) { runtime.Goexit() })
	d.act(func(*model) {})
	<-redraws                         // the action's snapshot
	time.Sleep(60 * time.Millisecond) // past the burst wait
	d.in <- redrawMsg{}
	got := make(chan int)
	d.act(func(*model) { got <- len(redraws) }) // runs after the redraw, before its own snapshot
	if n := <-got; n != 0 {
		t.Error("a redraw with nothing new made a snapshot")
	}
}

// TestHostAlertIndex: the worst alert per host, matched by name, address,
// FQDN or its nagios name, from an index built once per change of alerts.
func TestHostAlertIndex(t *testing.T) {
	m := &model{}
	m.setAlerts(alertsMsg{src: "Acme/n", first: true, alerts: []nagiosAlert{
		{Org: "Acme", Host: "web-01.acme.lan", State: "CRITICAL"},
		{Org: "Acme", Host: "DB-01", State: "CRITICAL"},
		{Org: "Acme", Host: "10.0.0.2", State: "DOWN"},
		{Org: "Acme", Host: "app-01", State: "DOWN"},
		{Org: "Acme", Host: "app-01.acme.lan", State: "CRITICAL"},
		{Org: "Acme", Host: "MON-WEB", State: "UNREACHABLE"},
		{Org: "Acme", Host: "web-02", State: "DOWN"},
		{Org: "Acme", Host: "cache-01", State: "CRITICAL"},
		{Org: "Acme", Host: "cache-01", State: "DOWN"},
		{Org: "Acme", Host: "q-01.a.lan", State: "DOWN"},
		{Org: "Acme", Host: "q-01.b.lan", State: "CRITICAL"},
		{Org: "Globex", Host: "db-01", State: "DOWN"},
	}})
	for _, c := range []struct {
		n    node
		want string
	}{
		{node{org: "Acme", label: "web-01"}, "CRITICAL"},                       // its FQDN
		{node{org: "Acme", label: "db-01", addr: "10.0.0.2"}, "DOWN"},          // by address, worse than by name
		{node{org: "Acme", label: "app-01"}, "DOWN"},                           // by name, worse than the FQDN
		{node{org: "Acme", label: "web-02", nagios: "mon-web"}, "UNREACHABLE"}, // only its nagios name
		{node{org: "Acme", label: "web"}, ""},                                  // a name, not a prefix
		{node{org: "Acme", label: "cache-01"}, "DOWN"},
		{node{org: "Acme", label: "q-01"}, "DOWN"},
		{node{org: "Globex", label: "db-01"}, "DOWN"},
		{node{org: "Initech", label: "db-01"}, ""},
	} {
		if got := m.hostAlert(c.n); got != c.want {
			t.Errorf("%+v: %q, want %q", c.n, got, c.want)
		}
	}
	m.alerts["Acme/n"] = nil // not through setAlerts: the index stays
	if m.hostAlert(node{org: "Acme", label: "app-01"}) != "DOWN" {
		t.Error("the index is built again per host")
	}
	m.setAlerts(alertsMsg{src: "Acme/n", first: true})
	if got := m.hostAlert(node{org: "Acme", label: "app-01"}); got != "" {
		t.Errorf("a cleared alert stays: %q", got)
	}
}

// TestKeepReadsKeptLines: Keep carries the last keptLines lines.
func TestKeepReadsKeptLines(t *testing.T) {
	term, _ := gmTerm(t, "web1", 20, 5)
	var b strings.Builder
	for i := 1; i <= keptLines+100; i++ {
		fmt.Fprintf(&b, "%d\r\n", i)
	}
	gmOut(term, b.String())
	lines := strings.Split(term.Keep().Text, "\n")
	if len(lines) != keptLines || lines[0] != "101" || lines[len(lines)-1] != fmt.Sprint(keptLines+100) {
		t.Fatalf("%d lines, %q … %q", len(lines), lines[0], lines[len(lines)-1])
	}
}

// TestSessionKeepsByRev: a save Keeps again only the panes whose Rev moved,
// every pane on Close's forced save; a dropped pane saves what it kept.
func TestSessionKeepsByRev(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	term, _ := gmTerm(t, "web1", 40, 6)
	gone, _ := gmTerm(t, "db1", 40, 6)
	gmOut(term, "first\r\n")
	tab := &Tab{org: "Acme", name: "web1", panes: []*Pane{{id: 1, term: term, name: "web1"},
		{id: 2, term: gone, name: "db1", lost: time.Now(), kept: paneState{Text: "kept"}}}}
	m := model{restored: true, sessionSaved: "x", tabs: []*Tab{tab}}
	saved := func(i int) string {
		b, _ := os.ReadFile(sessionPath())
		var ss savedSession
		json.Unmarshal(b, &ss)
		return ss.Tabs[0].Panes[i].State.Text
	}
	if m.saveSession(); saved(0) != "first" || saved(1) != "kept" {
		t.Fatalf("saved %q, %q", saved(0), saved(1))
	}
	gmOut(term, "second\r\n")
	tab.chat = append(tab.chat, &chatEntry{who: "you", text: "hi"}) // a save, but not for the pane
	if m.saveSession(); saved(0) != "first" {
		t.Errorf("a pane whose Rev stayed was kept again: %q", saved(0))
	}
	gmOut(term, gmBlock("ls", "a\r\n", "0"))
	if m.saveSession(); !strings.Contains(saved(0), "second") {
		t.Errorf("a finished command didn't keep the pane again: %q", saved(0))
	}
	gmOut(term, "third\r\n")
	m.sessionSaved = "" // Close
	if m.saveSession(); !strings.Contains(saved(0), "third") {
		t.Errorf("Close's save missed the last output: %q", saved(0))
	}
	tab.panes = nil
	if m.saveSession(); len(m.kept) != 0 {
		t.Errorf("closed panes stay cached: %d", len(m.kept))
	}
}

// TestRunbooksCached: the list is read again only when the folder changes.
func TestRunbooksCached(t *testing.T) {
	org := t.TempDir()
	tab := &Tab{dir: filepath.Join(org, "web"), group: "web"}
	if rbs := runbooks(tab); rbs != nil {
		t.Fatalf("no folder: %+v", rbs)
	}
	p, _ := saveRunbook(tab, "disk", "# Disk check\n")
	dir, old := filepath.Dir(p), time.Now().Add(-time.Hour)
	os.Chtimes(dir, old, old)
	if rbs := runbooks(tab); len(rbs) != 1 || rbs[0].title != "Disk check" {
		t.Fatalf("runbooks %+v", rbs)
	}
	os.WriteFile(p, []byte("# Disk usage\n"), 0o644)
	os.Chtimes(dir, old, old)
	if rbs := runbooks(tab); rbs[0].title != "Disk check" {
		t.Errorf("read again with the folder unchanged: %+v", rbs)
	}
	saveRunbook(tab, "logs", "# Logs\n")
	if rbs := runbooks(tab); len(rbs) != 2 || rbs[0].title != "Disk usage" {
		t.Errorf("a new runbook: %+v", rbs)
	}
}

// TestTasksCached: Tasks answers from its last list until the tasks change.
func TestTasksCached(t *testing.T) {
	w := &Workspace{tasksGen: -1}
	tab := &Tab{org: "Acme"}
	put := func(id int, bump bool) {
		hub.Lock()
		hub.tasks[id] = &peerTask{id: id, from: "DESK-1", tab: tab}
		if bump {
			hub.taskGen++
		}
		hub.Unlock()
	}
	defer func() { hub.Lock(); delete(hub.tasks, 910001); delete(hub.tasks, 910002); hub.taskGen++; hub.Unlock() }()
	mine := func() (n int) {
		for _, k := range w.Tasks() {
			if k.ID >= 910001 {
				n++
			}
		}
		return n
	}
	put(910001, true)
	if mine() != 1 {
		t.Fatal("the task is missing")
	}
	put(910002, false)
	if mine() != 1 {
		t.Error("the list was made again with no change")
	}
	put(910002, true)
	if mine() != 2 {
		t.Error("a new task is missing")
	}
}

// TestTreeCached: Tree is made again for a new snapshot or a pin, not per call.
func TestTreeCached(t *testing.T) {
	h := newGM(t)
	w := &Workspace{u: h.u}
	tree := []guiNode{{Depth: 0, Org: "Acme", Label: "Acme"}, {Depth: 1, Label: "web"}, {Depth: 2, Org: "Acme", Group: "web", Label: "web1", Host: "web1"}}
	h.d.state.Store(&guiState{Tree: tree})
	a, b := w.Tree(), w.Tree()
	if &a[0] != &b[0] {
		t.Error("the same snapshot made the tree again")
	}
	w.TogglePin("Acme", "web", "web1")
	defer w.TogglePin("Acme", "web", "web1")
	if c := w.Tree(); !c[0].Groups[0].Hosts[0].Pinned {
		t.Error("a pin doesn't show")
	}
	h.d.state.Store(&guiState{Tree: tree[:1]})
	if c := w.Tree(); len(c[0].Groups) != 0 {
		t.Error("a new snapshot doesn't show")
	}
}

// TestSetShown: hidden, terminal output asks for no frame; snapshots still do.
func TestSetShown(t *testing.T) {
	n := 0
	u := newUI(&driver{}, guiLayout{}, func() { n++ })
	w := &Workspace{u: u}
	if u.termRedraw(); n != 1 {
		t.Fatal("shown: output draws")
	}
	w.SetShown(false)
	if u.termRedraw(); n != 1 {
		t.Error("hidden: output asked for a frame")
	}
	if u.redraw(); n != 2 {
		t.Error("hidden: a snapshot asked for no frame")
	}
	w.SetShown(true)
	if u.termRedraw(); n != 3 {
		t.Error("shown again: output draws")
	}
}

// TestGridCachesScreen: a pane's screen is read and its cells recorded again
// only after output or a scroll; the selection draws apart.
func TestGridCachesScreen(t *testing.T) {
	h := newGM(t)
	u := h.u
	st, ts, _ := gmTermState(t)
	h.set(st)
	u.org = "Acme"
	w := func(gtx layout.Context) { u.terminals(gtx) }
	h.draw(w)
	h.draw(w) // sized
	v := u.terms[ts[0]]
	gen, rec := v.gridGen, v.recorded
	h.draw(w)
	if v.gridGen != gen || v.recorded != rec || rec == 0 {
		t.Fatalf("an unchanged screen was read %d or drawn %d times again", v.gridGen-gen, v.recorded-rec)
	}
	gmOut(ts[0], "hello   \r\n")
	h.draw(w)
	if v.gridGen != gen+1 || v.recorded != rec+1 || string(v.line(0)) != "hello" {
		t.Fatalf("new output: read %d, drawn %d, line %q", v.gridGen-gen, v.recorded-rec, string(v.line(0)))
	}
	v.sel = [2]image.Point{{0, 0}, {3, 0}}
	if h.draw(w); v.recorded != rec+1 {
		t.Error("a selection recorded the cells again")
	}
	v.scroll = 1
	if h.draw(w); v.gridGen != gen+2 {
		t.Error("a scroll didn't read the screen again")
	}
	v.scroll = 0
	gmOut(ts[0], "[sudo] password for deploy: ")
	if u, ok := v.sudo(ts[0]); !ok || u != "deploy" {
		t.Errorf("sudo %q %v", u, ok)
	}
	ts[0].mu.Lock()
	ts[0].emu.WriteString("\r\n") // no Version bump: the answer stays
	ts[0].mu.Unlock()
	if _, ok := v.sudo(ts[0]); !ok {
		t.Error("asked the terminal again with no new output")
	}
}

// TestSelTextKeepsLayout: the same spans at the same width keep their layout;
// a new text, font, size, width or scale lays out again, a color doesn't.
func TestSelTextKeepsLayout(t *testing.T) {
	h := newGM(t)
	u := h.u
	metric := unit.Metric{PxPerDp: 1, PxPerSp: 1}
	sp := styledtext.SpanStyle{Content: "hello world", Font: sansFont, Size: 13, Color: cText}
	var size image.Point
	draw := func(maxW int, sp styledtext.SpanStyle) *int {
		h.draw(func(gtx layout.Context) {
			gtx.Metric = metric
			gtx.Constraints = layout.Constraints{Max: image.Pt(maxW, 500)}
			size = u.selRich(gtx, "x", sp).Size
		})
		return &u.sels["x"].pieces[0].xs[0]
	}
	first := draw(400, sp)
	if size.X == 0 || size.Y == 0 {
		t.Fatalf("size %v", size)
	}
	same := size
	sp.Color = cRed
	if draw(400, sp) != first || size != same {
		t.Error("the same spans were laid out again")
	}
	for name, change := range map[string]func(){
		"text":  func() { sp.Content = "hello there" },
		"font":  func() { sp.Font = fBold(sansFont) },
		"size":  func() { sp.Size = 20 },
		"scale": func() { metric.PxPerSp = 2 },
	} {
		before := draw(400, sp)
		change()
		if draw(400, sp) == before {
			t.Errorf("a new %s kept the layout", name)
		}
	}
	before := draw(400, sp)
	if draw(30, sp) == before {
		t.Error("a narrower width kept the layout")
	}
}

// TestBound: a cache drops half its entries at its limit.
func TestBound(t *testing.T) {
	c := map[string]int{}
	for i := range 9 {
		c[fmt.Sprint(i)] = i
	}
	if bound(c, 10); len(c) != 9 {
		t.Fatalf("under the limit: %d", len(c))
	}
	c["9"] = 9
	if bound(c, 10); len(c) != 5 {
		t.Errorf("at the limit: %d left", len(c))
	}
	oldMd, oldHl := mdBlocks, hlCache
	t.Cleanup(func() { mdBlocks, hlCache = oldMd, oldHl })
	mdBlocks, hlCache = map[string][]mdBlock{}, map[string][]styledtext.SpanStyle{}
	for i := range cacheMax {
		mdBlocks[fmt.Sprint(i)], hlCache[fmt.Sprint(i)] = nil, nil
	}
	parseMarkdown("new **text**")
	newGM(t).u.highlight("echo hi", "sh")
	if len(mdBlocks) > cacheMax/2+1 || len(hlCache) > cacheMax/2+1 {
		t.Errorf("unbounded: %d markdown, %d highlights", len(mdBlocks), len(hlCache))
	}
}
