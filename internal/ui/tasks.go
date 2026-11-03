package ui

import (
	"cmp"
	"fmt"
	"image"
	"image/color"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"github.com/lfdominguez/desk/internal/claude"
	"github.com/lfdominguez/desk/internal/kanban"
	"github.com/lfdominguez/desk/internal/ui/kit"
)

// task is one of a session's subagents or background shells: what Claude
// Code reported of it, a subagent's own messages, a shell's command and
// the tail of its output. ponytail: in memory only, gone when desk closes;
// the subagent transcripts Claude keeps would bring them back.
type task struct {
	claude.Task
	uses       []string // later tool uses that resumed it (SendMessage): its messages name those too
	msgs       []kanban.ChatMsg
	cmd        string // a shell's command line (its Bash tool use's)
	out        string // the output's tail, as last read
	outSize    int64
	start, end time.Time       // end: when it stopped running, zero while it runs
	act        time.Time       // a subagent's last sign of life: a message, a tool use or result, progress
	pending    map[string]pend // a subagent's tool uses without a result yet, by id
}

// pend is a tool use waiting on its result.
type pend struct {
	line  string // "Bash  cargo test"
	since time.Time
}

// settle is how long a finished task stays with the running ones before
// it moves to the history.
const settle = time.Minute

// historyKey is what u.sub holds while the pane shows the task history.
const historyKey = "history"

// railW is the session view's rail of subagents and shells.
const railW unit.Dp = 330

// findTask is card's task with that id or, until its start names the id,
// the tool use that started it; nil if none.
func (u *ui) findTask(card, id, toolUse string) *task {
	for _, t := range u.tasks[card] {
		if id != "" && t.ID == id || toolUse != "" && (t.ToolUse == toolUse || slices.Contains(t.uses, toolUse)) {
			return t
		}
	}
	return nil
}

func (u *ui) newTask(card string, t claude.Task) *task {
	n := &task{Task: t, start: time.Now(), act: time.Now(), pending: map[string]pend{}}
	u.tasks[card] = append(u.tasks[card], n)
	return n
}

// onTask merges an update of one of card's tasks (zero fields keep what was known).
func (u *ui) onTask(card string, up *claude.Task) {
	t := u.findTask(card, up.ID, up.ToolUse)
	if t == nil {
		t = u.newTask(card, claude.Task{})
	}
	for _, f := range []struct {
		dst *string
		v   string
	}{
		{&t.ID, up.ID}, {&t.Name, up.Name}, {&t.Agent, up.Agent}, {&t.Model, up.Model}, {&t.Prompt, up.Prompt},
		{&t.Status, up.Status}, {&t.Note, up.Note}, {&t.Summary, up.Summary}, {&t.Output, up.Output},
	} {
		if f.v != "" {
			*f.dst = f.v
		}
	}
	if t.ToolUse == "" {
		t.ToolUse = up.ToolUse
	}
	t.Shell = t.Shell || up.Shell
	if up.Note != "" || up.Tokens > t.Tokens || up.Tools > t.Tools {
		t.act = time.Now()
	}
	t.Tokens, t.Tools = max(t.Tokens, up.Tokens), max(t.Tools, up.Tools)
	if t.Shell && t.cmd == "" {
		t.cmd = cmp.Or(u.toolCmds[t.ToolUse], strings.TrimPrefix(u.toolLines[t.ToolUse], "Bash  "))
	}
	t.ended()
}

// ended stamps when t stopped running (and clears it if it runs again).
func (t *task) ended() {
	switch {
	case t.Status == "running":
		t.end = time.Time{}
	case t.Status != "" && t.end.IsZero():
		t.end = time.Now()
	}
}

// resume ties tool use (a SendMessage) to subagent id, whose messages it
// carries from now on; a subagent desk never saw start (before it opened)
// gets a row of its own.
func (u *ui) resume(card, id, toolUse, line string) {
	t := u.findTask(card, id, "")
	if t == nil {
		t = u.newTask(card, claude.Task{ID: id, Name: "Resumed subagent"})
	}
	t.uses, t.Status = append(t.uses, toolUse), "running"
	t.ended()
	t.msgs = append(t.msgs, kanban.ChatMsg{Who: "tool", Text: line, At: time.Now()})
}

// key is what u.sub names t by: its id, or until it is known, its tool use.
func (t *task) key() string { return cmp.Or(t.ID, t.ToolUse) }

// subSay adds a message to the subagent that tool use parent started.
func (u *ui) subSay(card, parent string, m kanban.ChatMsg) {
	t := u.findTask(card, "", parent)
	if t == nil { // its messages beat its start, or it is one desk never saw start
		t = u.newTask(card, claude.Task{ToolUse: parent, Status: "running", Name: strings.Join(strings.Fields(u.toolLines[parent]), " ")})
	}
	m.At = time.Now()
	t.msgs = append(t.msgs, m)
	t.act = time.Now()
}

// wait is what a running subagent is stuck on, when it is: a badge.
type wait struct {
	text  string // "Waiting on Cargo tests"
	since time.Time
	c     color.NRGBA
}

// waitTools only wait: on a background task's output, a monitor, time.
var waitTools = []string{"TaskOutput", "BashOutput", "Monitor", "Sleep"}

// waiting is what subagent t waits on, if anything: its own background
// shell (polling it, or idle while it runs), a tool that has run long, or
// nothing at all for a while. So a quiet subagent says why.
func (u *ui) waiting(card string, t *task, now time.Time) (wait, bool) {
	if t.Shell || t.Status != "running" {
		return wait{}, false
	}
	var shell *task // its first running background shell
	for _, s := range u.tasks[card] {
		if by := u.toolBy[s.ToolUse]; s.Shell && s.Status == "running" && by != "" && (by == t.ToolUse || slices.Contains(t.uses, by)) {
			shell = s
			break
		}
	}
	on := "a background task"
	if shell != nil {
		on = shell.title()
	}
	var p *pend // its oldest tool use still out
	for _, x := range t.pending {
		if p == nil || x.since.Before(p.since) {
			p = &x
		}
	}
	if p != nil {
		name, args, _ := strings.Cut(p.line, "  ")
		polls := slices.Contains(waitTools, name) || name == "Bash" && (strings.Contains(args, "sleep") || strings.HasPrefix(args, "wait") || strings.Contains(args, "tail -f"))
		switch {
		case polls:
			return wait{"Waiting on " + on, p.since, kit.COrange}, true
		case now.Sub(p.since) > 30*time.Second:
			return wait{"Running " + strings.Join(strings.Fields(p.line), " "), p.since, kit.CAccText}, true
		}
		return wait{}, false
	}
	switch idle := now.Sub(t.act); {
	case shell != nil && idle > 20*time.Second:
		return wait{"Waiting on " + on, t.act, kit.COrange}, true
	case idle > 2*time.Minute:
		return wait{"No activity", t.act, kit.CRed}, true
	}
	return wait{}, false
}

// held is how long w has held, short: "45s", "3m 10s", "1h 05m".
func (w wait) held(now time.Time) string {
	x := task{start: w.since, end: now}
	return x.took(now)
}

// badge is w as a pill: a dot, what it waits on, for how long.
func (u *ui) badge(w wait) layout.Widget {
	k := u.k
	return func(gtx layout.Context) layout.Dimensions {
		return kit.Boxed(kit.Mix(kit.CPanel, w.c, .16), color.NRGBA{}, kit.RCtl, layout.Inset{Top: 3, Bottom: 3, Left: 7, Right: 8}, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					sz := gtx.Dp(6)
					kit.FillRRect(gtx.Ops, image.Rect(0, 0, sz, sz), sz/2, w.c)
					return layout.Dimensions{Size: image.Pt(sz, sz)}
				}),
				layout.Rigid(kit.HGap(6)),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return k.Label(gtx, w.text+" · "+w.held(gtx.Now), 11, kit.Mix(w.c, kit.CWhite, .35), kit.Semi(kit.Sans), 1)
				}))
		})(gtx)
	}
}

// backgrounded is what card's session still runs once a turn ends, as a
// status note ("Waiting on its subagent: Run e2e"), or "" when nothing.
func (u *ui) backgrounded(card string) string {
	var names []string
	agents := 0
	for _, t := range u.tasks[card] {
		if t.Status == "running" {
			names = append(names, t.title())
			if !t.Shell {
				agents++
			}
		}
	}
	switch {
	case len(names) == 0:
		return ""
	case len(names) > 1:
		return fmt.Sprintf("Waiting on %d background tasks: %s", len(names), strings.Join(names, ", "))
	case agents == 1:
		return "Waiting on its subagent: " + names[0]
	}
	return "Waiting on its background shell: " + names[0]
}

// stopTasks marks card's running tasks stopped: their process ended.
func (u *ui) stopTasks(card string) {
	for _, t := range u.tasks[card] {
		if t.Status == "running" {
			t.Status = "stopped"
			t.ended()
		}
	}
}

// split is card's tasks, newest first: the running ones and those that
// ended under a minute ago (now), and the rest (history).
func (u *ui) split(card string, at time.Time) (now, history []*task) {
	ts := u.tasks[card]
	for i := len(ts) - 1; i >= 0; i-- {
		if t := ts[i]; t.end.IsZero() || at.Sub(t.end) < settle {
			now = append(now, t)
		} else {
			history = append(history, t)
		}
	}
	return now, history
}

// tick asks for the frames card's tasks need: one a second while one runs
// (its time counts), one when a finished one moves to the history.
func (u *ui) tick(gtx layout.Context, card string) {
	var next time.Time
	for _, t := range u.tasks[card] {
		at := gtx.Now.Add(time.Second)
		if !t.end.IsZero() {
			if at = t.end.Add(settle); !at.After(gtx.Now) {
				continue
			}
		}
		if next.IsZero() || at.Before(next) {
			next = at
		}
	}
	if !next.IsZero() {
		gtx.Execute(op.InvalidateCmd{At: next})
	}
}

var taskColor = map[string]color.NRGBA{"running": kit.CPurple, "completed": kit.CGreen}

func (t *task) color() color.NRGBA {
	if c, ok := taskColor[t.Status]; ok {
		return c
	}
	return kit.CRed // failed, killed, stopped
}

func (t *task) title() string {
	switch {
	case t.Name != "":
		return t.Name
	case t.Shell:
		return "Background shell"
	}
	return "Subagent"
}

// modelName is "Opus 5.5" for claude-opus-5-5 (a dated id loses its date).
func modelName(id string) string {
	p := strings.Split(strings.TrimPrefix(id, "claude-"), "-")
	if len(p) > 1 && len(p[len(p)-1]) == 8 { // -20260101
		p = p[:len(p)-1]
	}
	if len(p) == 0 || p[0] == "" {
		return id
	}
	return strings.ToUpper(p[0][:1]) + p[0][1:] + " " + strings.Join(p[1:], ".")
}

// took is how long t has run, or ran.
func (t *task) took(now time.Time) string {
	end := t.end
	if end.IsZero() {
		end = now
	}
	d := end.Sub(t.start).Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < 10*time.Minute:
		return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
}

// by is who ran shell t: the subagent whose tool use it was, or the
// session itself.
func (u *ui) by(card string, t *task) string {
	if p := u.toolBy[t.ToolUse]; p != "" {
		if a := u.findTask(card, "", p); a != nil {
			return "by " + a.title()
		}
		return "by a subagent"
	}
	return "by the session"
}

// facts is a subagent's kind, tools, tokens and time, or a shell's: who
// ran it, and its time.
func (u *ui) facts(card string, t *task, now time.Time) string {
	var p []string
	if t.Shell {
		p = append(p, u.by(card, t))
	}
	if t.Agent != "" {
		p = append(p, t.Agent)
	}
	if t.Tools > 0 {
		p = append(p, fmt.Sprintf("%d tools", t.Tools))
	}
	if t.Tokens > 0 {
		p = append(p, tokens(t.Tokens))
	}
	return strings.Join(append(p, t.took(now)), " · ")
}

// doing is what a subagent is on now, or how it ended.
func (t *task) doing() string {
	switch {
	case t.Status == "running":
		return cmp.Or(t.Note, "running")
	case t.Summary != "" && !t.Shell:
		return firstLine(t.Summary)
	}
	return t.Status
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// taskBar is the strip under a session's header while it has subagents
// or shells: what runs now, in words; a click shows the rail with their
// details (from the side panel, in the full window).
func (u *ui) taskBar(gtx layout.Context, card string, full bool, act *func()) layout.Dimensions {
	k := u.k
	now, history := u.split(card, gtx.Now)
	var agents, shells int
	var what []string
	for _, t := range now {
		if t.Status != "running" {
			continue
		}
		if t.Shell {
			shells++
			s := strings.ToLower(t.title()[:1]) + t.title()[1:]
			if p := u.toolBy[t.ToolUse]; p != "" {
				if a := u.findTask(card, "", p); a != nil {
					s += " (" + a.title() + ")"
				}
			}
			what = append(what, s+" "+t.took(gtx.Now))
			continue
		}
		agents++
		s := t.title()
		if t.Model != "" {
			s += " on " + modelName(t.Model)
		}
		if w, ok := u.waiting(card, t, gtx.Now); ok {
			s += " (" + strings.ToLower(w.text[:1]) + w.text[1:] + " " + w.held(gtx.Now) + ")"
		}
		what = append(what, s)
	}
	var head string
	switch {
	case agents > 0 && shells > 0:
		head = "You're running " + plural(agents, "subagent", "subagents") + " and " + plural(shells, "shell", "shells")
	case agents > 0:
		head = "You're running " + plural(agents, "subagent", "subagents")
	case shells > 0:
		head = "You're running " + plural(shells, "background shell", "background shells")
	}
	running := head != ""
	open := full && u.rail
	cta := "Show details"
	if open {
		cta = "Hide details"
	}
	fill := kit.Mix(kit.CPanel, kit.CPurple, .08)
	if !running {
		fill = kit.CPanel
	}
	d, ok := k.Button(gtx, "taskBar"+card, fill, kit.Mix(fill, kit.CWhite, .04), 0, layout.Inset{Top: 9, Bottom: 9, Left: 16, Right: 12}, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		var kids []layout.FlexChild
		if running {
			kids = append(kids,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					sz := gtx.Dp(6)
					for i, a := range []uint8{0xff, 0x99, 0x4d} {
						c := kit.CPurple
						c.A = a
						x := i * (sz + gtx.Dp(3))
						kit.FillRRect(gtx.Ops, image.Rect(x, 0, x+sz, sz), sz/2, c)
					}
					return layout.Dimensions{Size: image.Pt(3*sz+2*gtx.Dp(3), sz)}
				}),
				layout.Rigid(kit.HGap(10)),
				layout.Rigid(k.Lbl(head, 13, kit.CText, kit.Semi(kit.Sans))),
				layout.Rigid(kit.HGap(10)),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return k.Label(gtx, strings.Join(what, " · "), 12, kit.CText2, kit.Sans, 1)
				}))
		} else {
			kids = append(kids, layout.Flexed(1, k.LblFill(plural(len(history)+len(now), "finished", "finished")+": history", 12, kit.CText3, kit.Sans)))
		}
		if running && len(history) > 0 {
			kids = append(kids, layout.Rigid(kit.HGap(10)), layout.Rigid(k.Lbl(fmt.Sprintf("%d finished", len(history)), 12, kit.CText3, kit.Sans)))
		}
		kids = append(kids, layout.Rigid(kit.HGap(10)), layout.Rigid(kit.Boxed(kit.Mix(kit.CPanel, kit.CPurple, .2), color.NRGBA{}, kit.RCtl, layout.Inset{Top: 3, Bottom: 3, Left: 9, Right: 9},
			k.Lbl(cta, 12, kit.Mix(kit.CPurple, kit.CWhite, .55), kit.Semi(kit.Sans)))))
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx, kids...)
	})
	if ok {
		*act = func() {
			switch {
			case !running && !full:
				u.openSession(card)
				u.rail, u.sub = true, historyKey
			case !running && u.rail: // Hide details: the rail, and the history it opened
				u.rail = false
				if u.sub == historyKey {
					u.sub = ""
				}
			case !running:
				u.rail, u.sub = true, historyKey
			case !full:
				u.openSession(card)
				u.rail = true
			default:
				u.rail = !u.rail
			}
		}
	}
	return d
}

// rail is the session view's column of subagents and shells running now
// (or just finished), each a card; History at its foot.
func (u *ui) taskRail(gtx layout.Context, card string, act *func()) layout.Dimensions {
	k := u.k
	gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(railW), gtx.Constraints.Max.Y))
	kit.Fill(gtx, kit.CCard)
	now, history := u.split(card, gtx.Now)
	var agents, shells []*task
	for _, t := range now {
		if t.Shell {
			shells = append(shells, t)
		} else {
			agents = append(agents, t)
		}
	}
	gap := func(dp unit.Dp, w layout.Widget) layout.Widget { // w, then dp of space
		return func(gtx layout.Context) layout.Dimensions { return layout.Inset{Bottom: dp}.Layout(gtx, w) }
	}
	group := func(name string, ts []*task) []layout.Widget {
		if len(ts) == 0 {
			return nil
		}
		ws := []layout.Widget{gap(6, k.Lbl(fmt.Sprintf("%s · %d", strings.ToUpper(name), len(ts)), 11, kit.CText2, kit.Bold(kit.Sans)))}
		for i, t := range ts {
			sp := unit.Dp(6)
			if i == len(ts)-1 {
				sp = 14
			}
			ws = append(ws, gap(sp, func(gtx layout.Context) layout.Dimensions { return u.taskCard(gtx, card, t, act) }))
		}
		return ws
	}
	top := []layout.Widget{gap(12, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, k.LblFill("Running now", 14, kit.CText, kit.Semi(kit.Sans))),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				d, ok := k.IconBtn(gtx, "railClose", kit.IcoX, false)
				k.Tip(gtx, "railClose", d, "Hide the details (Esc)", true)
				if ok {
					*act = func() { u.rail = false }
				}
				return d
			}))
	})}
	if len(now) == 0 {
		top = append(top, k.Lbl("Nothing runs now.", 12, kit.CText3, kit.Sans))
	}
	top = append(top, group("Subagents", agents)...)
	top = append(top, group("Shells", shells)...)
	return layout.Inset{Top: 12, Bottom: 12, Left: 14, Right: 10}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return u.railList.Layout(gtx, len(top), func(gtx layout.Context, i int) layout.Dimensions { return top[i](gtx) })
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				d, ok := k.Button(gtx, "railHistory"+card, color.NRGBA{}, kit.CHover, kit.RCtl, layout.Inset{Top: 8, Bottom: 8, Left: 4, Right: 4}, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.Flex{}.Layout(gtx,
						layout.Flexed(1, k.LblFill("History", 13, kit.CText3, kit.Sans)),
						layout.Rigid(k.Lbl(fmt.Sprintf("%d finished ›", len(history)), 13, kit.CAccText, kit.Semi(kit.Sans))))
				})
				if ok {
					*act = func() { u.sub = historyKey }
				}
				return d
			}))
	})
}

// taskCard is one subagent or shell in the rail or the history: a click
// shows it in place of the conversation.
func (u *ui) taskCard(gtx layout.Context, card string, t *task, act *func()) layout.Dimensions {
	k := u.k
	bid := "taskCard" + t.key()
	fill := kit.CPanel
	if u.sub == t.key() {
		fill = kit.CPanelCard
	}
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	d, ok := k.Button(gtx, bid, fill, kit.CHover, kit.RCard, layout.UniformInset(10), func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		head := []layout.FlexChild{layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if t.Status == "running" {
				sz := gtx.Dp(8)
				kit.FillRRect(gtx.Ops, image.Rect(0, 0, sz, sz), sz/2, kit.CPurple)
				return layout.Dimensions{Size: image.Pt(sz, sz)}
			}
			ico := kit.IcoCheck
			if t.Status != "completed" {
				ico = kit.IcoX
			}
			return kit.Icon(12, t.color(), ico)(gtx)
		}), layout.Rigid(kit.HGap(8)),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return k.Label(gtx, t.title(), 13, kit.CText, kit.Semi(kit.Sans), 1)
			})}
		if t.Model != "" {
			head = append(head, layout.Rigid(kit.HGap(6)), layout.Rigid(kit.Boxed(kit.CHover, color.NRGBA{}, kit.RCtl, layout.Inset{Top: 2, Bottom: 2, Left: 6, Right: 6},
				k.Lbl(modelName(t.Model), 11, kit.CText, kit.Mono))))
		}
		rows := []layout.FlexChild{layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx, head...)
		}), layout.Rigid(kit.VGap(5))}
		if w, ok := u.waiting(card, t, gtx.Now); ok {
			rows = append(rows, layout.Rigid(u.badge(w)), layout.Rigid(kit.VGap(5)))
		}
		if t.Shell {
			if t.cmd != "" {
				rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return k.Label(gtx, "$ "+t.cmd, 11, kit.CText2, kit.Mono, 1)
				}))
			}
			if last := lastLine(t.tail()); last != "" {
				rows = append(rows, layout.Rigid(kit.VGap(5)), layout.Rigid(kit.Boxed(kit.CPane, color.NRGBA{}, kit.RCtl, layout.Inset{Top: 5, Bottom: 5, Left: 8, Right: 8}, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return k.Label(gtx, last, 11, kit.CText3, kit.Mono, 1)
				})))
			}
		} else if d := t.doing(); d != "running" {
			rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return k.Label(gtx, d, 12, kit.CText2, kit.Sans, 2)
			}))
		}
		rows = append(rows, layout.Rigid(kit.VGap(5)), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return k.Label(gtx, u.facts(card, t, gtx.Now), 11, kit.CText3, kit.Mono, 1)
		}))
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
	})
	k.Tip(gtx, bid, d, "Open it: its messages, or the shell's output", false)
	if ok {
		*act = func() { u.sub = t.key() }
	}
	return d
}

func lastLine(s string) string {
	s = strings.TrimRight(s, "\n")
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// paneBar is the strip over a task or the history in the pane: back to
// the conversation, what it is.
func (u *ui) paneBar(gtx layout.Context, title, status string, sc color.NRGBA, act *func()) layout.Dimensions {
	k := u.k
	return layout.Inset{Top: 8, Bottom: 8, Left: 12, Right: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				d, ok := k.Button(gtx, "subBack", kit.CPanelCard, kit.CHover, kit.RCtl, layout.Inset{Top: 4, Bottom: 4, Left: 10, Right: 10},
					k.Lbl("‹ Session", 12, kit.CText2, kit.Semi(kit.Sans)))
				k.Tip(gtx, "subBack", d, "Back to the conversation (Esc)", false)
				if ok {
					*act = func() { u.sub = "" }
				}
				return d
			}),
			layout.Rigid(kit.HGap(12)),
			layout.Flexed(1, k.LblFill(title, 13, kit.CText, kit.Semi(kit.Sans))),
			layout.Rigid(k.Lbl(status, 12, sc, kit.Semi(kit.Sans))))
	})
}

// historyBody is card's finished subagents and shells, side by side,
// newest first; a click opens one.
func (u *ui) historyBody(gtx layout.Context, card string, act *func()) layout.Dimensions {
	k := u.k
	gtx.Constraints.Min = gtx.Constraints.Max
	kit.Fill(gtx, kit.CColumn)
	_, history := u.split(card, gtx.Now)
	var agents, shells []*task
	for _, t := range history {
		if t.Shell {
			shells = append(shells, t)
		} else {
			agents = append(agents, t)
		}
	}
	col := func(name string, ts []*task) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			kids := []layout.FlexChild{layout.Rigid(k.Lbl(fmt.Sprintf("%s · %d", strings.ToUpper(name), len(ts)), 11, kit.CText2, kit.Bold(kit.Sans))), layout.Rigid(kit.VGap(8))}
			if len(ts) == 0 {
				kids = append(kids, layout.Rigid(k.Lbl("None finished yet.", 12, kit.CText3, kit.Sans)))
			}
			for _, t := range ts {
				kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return u.taskCard(gtx, card, t, act) }), layout.Rigid(kit.VGap(6)))
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...)
		}
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return u.paneBar(gtx, "History", "finished subagents and shells, newest first", kit.CText3, act)
		}),
		layout.Rigid(kit.VGap(1)),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return u.histList.Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
				return layout.Inset{Top: 14, Bottom: 14, Left: 16, Right: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Spacing: layout.SpaceBetween}.Layout(gtx,
						layout.Flexed(1, col("Subagents", agents)), layout.Rigid(kit.HGap(20)), layout.Flexed(1, col("Shells", shells)))
				})
			})
		}))
}

// subBody is task t in the pane, in place of the conversation: a
// subagent's prompt, messages and result, or a shell's command and output.
func (u *ui) subBody(gtx layout.Context, card string, t *task, act *func()) layout.Dimensions {
	k := u.k
	gtx.Constraints.Min = gtx.Constraints.Max
	kit.Fill(gtx, kit.CColumn)
	status := t.Status
	if t.Model != "" {
		status = modelName(t.Model) + " · " + status
	}
	if t.Shell {
		status = u.by(card, t) + " · " + status
	}
	if w, ok := u.waiting(card, t, gtx.Now); ok {
		status = w.text + " " + w.held(gtx.Now) + " · " + status
	}
	var items []layout.Widget
	if t.Shell {
		if t.Status == "running" { // its output grows: look again in a second
			gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(time.Second)})
		}
		out := t.tail()
		if out == "" {
			out = "(no output yet)"
		}
		items = append(items,
			func(gtx layout.Context) layout.Dimensions {
				return k.Label(gtx, "$ "+cmp.Or(t.cmd, "(its command started before desk saw it)"), 12, kit.CAccText, kit.Mono, 0)
			})
		if t.cmd != "" {
			items = append(items, func(gtx layout.Context) layout.Dimensions { return u.explainBox(gtx, card, t.cmd, true) })
		}
		items = append(items,
			func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return k.Label(gtx, out, 12, kit.CText2, kit.Mono, 0)
			})
	} else {
		if t.Prompt != "" {
			items = append(items, func(gtx layout.Context) layout.Dimensions {
				return u.message(gtx, card, "sub"+t.key()+"-p", kanban.ChatMsg{Who: "prompt", Text: t.Prompt}, false)
			})
		}
		for i, m := range t.msgs {
			items = append(items, func(gtx layout.Context) layout.Dimensions {
				return u.message(gtx, card, fmt.Sprintf("sub%s-%d", t.key(), i), m, i > 0 && t.msgs[i-1].Who == m.Who)
			})
		}
		if t.Summary != "" {
			items = append(items, func(gtx layout.Context) layout.Dimensions {
				return u.message(gtx, card, "sub"+t.key()+"-s", kanban.ChatMsg{Who: "claude", Text: t.Summary}, false)
			})
		}
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return u.paneBar(gtx, t.title(), status, t.color(), act) }),
		layout.Rigid(kit.VGap(1)),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return u.subList.Layout(gtx, len(items), func(gtx layout.Context, i int) layout.Dimensions {
				return layout.Inset{Top: 6, Bottom: 6, Left: 16, Right: 16}.Layout(gtx, items[i])
			})
		}))
}

// tail is the end of a shell's output file, read again when it grew.
func (t *task) tail() string {
	f, err := os.Open(t.Output)
	if err != nil {
		return t.out
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.Size() == t.outSize {
		return t.out
	}
	const keep = 32 << 10
	off := max(fi.Size()-keep, 0)
	b := make([]byte, fi.Size()-off)
	n, err := f.ReadAt(b, off)
	if err != nil && err != io.EOF {
		return t.out
	}
	s := string(b[:n])
	if off > 0 { // from the first whole line
		if _, rest, ok := strings.Cut(s, "\n"); ok {
			s = "…\n" + rest
		}
	}
	t.out, t.outSize = strings.TrimRight(s, "\n"), fi.Size()
	return t.out
}

// subOf is the task card's pane shows instead of the conversation, if any.
func (u *ui) subOf(card string) *task {
	if u.sub == "" || u.sub == historyKey {
		return nil
	}
	return u.findTask(card, u.sub, u.sub)
}
