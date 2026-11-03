package ui

import (
	"cmp"
	"fmt"
	"image"
	"image/color"
	"math"
	"slices"
	"strings"
	"time"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/lfdominguez/desk/internal/kanban"
	"github.com/lfdominguez/desk/internal/ui/kit"
)

// The team view: a card running as an agent (Card.Agent) shows its session
// in the full window as its team, the lead in the middle and the agents it
// handed work to around it, with the briefs and results between them (the
// handoffs). A click on an agent opens its conversation (u.sub), on the lead
// the session's own; the header's Team | Conversation switches (u.convo).

// pastels color the team: the lead takes the first, the agents the rest
// in the order they joined, so each keeps its own.
var pastels = []color.NRGBA{kit.RGB(0xb9a7ff), kit.RGB(0x8fe3bd), kit.RGB(0xffbf8f), kit.RGB(0x8ccaff), kit.RGB(0xffa8c5), kit.RGB(0xf2d98a), kit.RGB(0x8fe0e6)}

// teamMode is whether card c's pane shows its team: the full window, a
// card with an agent and a session, nothing else opened in its place.
func (u *ui) teamMode(c *kanban.Card, full bool) bool {
	return full && c.Agent != "" && !u.convo[c.ID] && u.sub == "" && (len(u.chat(c.ID)) > 0 || len(u.tasks[c.ID]) > 0)
}

// member is one agent of the team: every run of one kind (subagent_type),
// shown by its latest, the running one first.
type member struct {
	name  string
	last  *task
	runs  int
	shell *task // a background shell of its, running
	col   color.NRGBA
}

func (u *ui) members(card string) []member {
	var ms []member
	for _, t := range u.tasks[card] {
		if t.Shell {
			continue
		}
		name := cmp.Or(t.Agent, t.title())
		i := slices.IndexFunc(ms, func(m member) bool { return m.name == name })
		if i < 0 {
			ms = append(ms, member{name: name, col: pastels[1+len(ms)%(len(pastels)-1)]})
			i = len(ms) - 1
		}
		m := &ms[i]
		m.runs++
		if m.last == nil || t.Status == "running" || m.last.Status != "running" {
			m.last = t
		}
	}
	for _, s := range u.tasks[card] {
		by := u.toolBy[s.ToolUse]
		if !s.Shell || s.Status != "running" || by == "" {
			continue
		}
		if a := u.findTask(card, "", by); a != nil {
			for i := range ms {
				if ms[i].name == cmp.Or(a.Agent, a.title()) && ms[i].shell == nil {
					ms[i].shell = s
				}
			}
		}
	}
	return ms
}

// settled is how long a done agent stays on the ring before it leaves for
// the helm's "earlier" chip, which brings the card's back.
const settled = 20 * time.Minute

// onRing is ms without the agents done for longer than settled, unless the
// card shows them again, and how many are such.
func (u *ui) onRing(card string, ms []member, now time.Time) ([]member, int) {
	var show []member
	n := 0
	for _, m := range ms {
		if m.last.Status != "running" && m.shell == nil && !m.last.end.IsZero() && now.Sub(m.last.end) > settled {
			n++
			if !u.earlier[card] {
				continue
			}
		}
		show = append(show, m)
	}
	return show, n
}

// teamBody is the helm beside the handoffs, in place of the conversation.
func (u *ui) teamBody(gtx layout.Context, c *kanban.Card, act *func()) layout.Dimensions {
	gtx.Constraints.Min = gtx.Constraints.Max
	kit.Fill(gtx, kit.CPane)
	if gtx.Constraints.Max.X < gtx.Dp(820) { // too narrow for both: the helm alone
		return u.helm(gtx, c, act)
	}
	return layout.Flex{}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return u.helm(gtx, c, act) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			paint.FillShape(gtx.Ops, kit.CLine, clip.Rect(image.Rect(0, 0, gtx.Dp(1), gtx.Constraints.Max.Y)).Op())
			return layout.Dimensions{Size: image.Pt(gtx.Dp(1), gtx.Constraints.Max.Y)}
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return u.handoffs(gtx, c, act) }))
}

// helm is the lead in the middle and its agents on a ring around it, each
// joined by its brief (dashed while it works), with its running shell
// beside it; a working one breathes.
func (u *ui) helm(gtx layout.Context, c *kanban.Card, act *func()) layout.Dimensions {
	size := gtx.Constraints.Max
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	mid := size.Div(2)
	ms, earlier := u.onRing(c.ID, u.members(c.ID), gtx.Now)
	rl, rn := gtx.Dp(70), gtx.Dp(50)
	orbit := max(min(size.X, size.Y)/2-gtx.Dp(105), rl+rn+gtx.Dp(36))
	circle(gtx.Ops, mid, orbit, kit.CLine, float32(gtx.Dp(1)))
	pos := make([]image.Point, len(ms))
	for i := range ms {
		a := -math.Pi/2 + 2*math.Pi*float64(i)/float64(len(ms))
		pos[i] = mid.Add(image.Pt(int(float64(orbit)*math.Cos(a)), int(float64(orbit)*math.Sin(a))))
	}
	breathe := false
	for i, m := range ms { // the spokes, under the nodes
		run := m.last.Status == "running"
		col := m.col
		col.A = 90
		dash := float32(0)
		if run {
			col.A, dash = 190, float32(gtx.Dp(6))
		}
		from, to := toward(mid, pos[i], rl), toward(pos[i], mid, rn)
		spoke(gtx.Ops, from, to, col, float32(gtx.Dp(2)), dash)
		at := from.Add(to).Div(2)
		u.tag(gtx, at, "brief · "+m.last.took(gtx.Now), kit.Mix(m.col, kit.CText3, .3))
	}

	lead, leadCol := u.leadStatus(c, ms)
	if u.node(gtx, "teamLead"+c.ID, mid, rl, pastels[0], cmp.Or(c.Agent, "lead"), "lead", lead, leadCol, c.Status == kanban.StatusWorking, false) {
		*act = func() { u.convo[c.ID] = true }
	}
	breathe = c.Status == kanban.StatusWorking
	for i, m := range ms {
		col, t := m.col, m.last
		sub := cmp.Or(modelName(t.Model), "subagent")
		if m.runs > 1 {
			sub += fmt.Sprintf(" · ×%d", m.runs)
		}
		w, held := u.waiting(c.ID, t, gtx.Now)
		state, sc := "", col
		switch {
		case t.Status == "running" && held:
			state, sc = "Waiting", kit.Mix(col, kit.COrange, .45)
		case t.Status == "running":
			state = "Working"
		case t.Status == "completed":
			state, sc = "Done · "+t.took(gtx.Now), kit.CGreen
		default:
			state, sc = t.Status, kit.CRed
		}
		run := t.Status == "running" && !held
		breathe = breathe || run
		if u.node(gtx, "teamNode"+m.name, pos[i], rn, col, m.name, sub, state, sc, run, t.Status != "running") {
			key := t.key()
			*act = func() { u.sub = key }
		}
		// what it is on and its facts, on the far side from the lead (the spoke takes the near one)
		line := t.doing()
		if held {
			line = w.text + " · " + w.held(gtx.Now)
		}
		var ls []string
		if line != "running" && line != "" {
			ls = append(ls, line)
		}
		ls = append(ls, teamFacts(t, gtx.Now))
		u.caption(gtx, pos[i], rn, pos[i].Y < mid.Y-gtx.Dp(10), ls)
		if s := m.shell; s != nil { // its shell, a pill at its side
			u.shellPill(gtx, s, pos[i].Add(image.Pt(rn+gtx.Dp(10), -gtx.Dp(12))), col, act)
		}
	}
	if earlier > 0 { // the long-done agents, off the ring until asked for
		label := plural(earlier, "earlier agent", "earlier agents")
		if u.earlier[c.ID] {
			label = "Hide " + label
		}
		g := gtx
		g.Constraints = layout.Constraints{Max: image.Pt(gtx.Dp(240), gtx.Dp(40))}
		m := op.Record(gtx.Ops)
		d, ok := u.k.RimButton(g, "teamEarlier"+c.ID, kit.CColumn, kit.CHover, kit.CRim, kit.RSheet, layout.Inset{Top: 5, Bottom: 5, Left: 12, Right: 12}, u.k.Lbl(label, 12, kit.CText2, kit.Sans))
		call := m.Stop()
		st := op.Offset(image.Pt((size.X-d.Size.X)/2, size.Y-d.Size.Y-gtx.Dp(16))).Push(gtx.Ops)
		call.Add(gtx.Ops)
		st.Pop()
		if ok {
			id := c.ID
			*act = func() {
				if u.earlier[id] {
					delete(u.earlier, id)
				} else {
					u.earlier[id] = true
				}
			}
		}
	}
	if len(ms) == 0 && earlier == 0 {
		u.centered(gtx, image.Pt(mid.X, mid.Y+rl+gtx.Dp(28)), "No agents yet: "+cmp.Or(c.Agent, "the lead")+" hands work out as it goes", 13, kit.CText3, kit.Sans, gtx.Dp(360), 2)
	}
	if breathe { // every frame draws the whole window: ~12 a second is enough for a 1.8 s breath, and slow when not looked at
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(map[bool]time.Duration{true: 80 * time.Millisecond, false: 500 * time.Millisecond}[u.focused])})
	}
	return layout.Dimensions{Size: size}
}

// teamFacts is an agent's tools, tokens and time.
func teamFacts(t *task, now time.Time) string {
	var p []string
	if t.Tools > 0 {
		p = append(p, plural(t.Tools, "tool", "tools"))
	}
	if t.Tokens > 0 {
		p = append(p, tokens(t.Tokens)+" tokens")
	}
	return strings.Join(append(p, t.took(now)), " · ")
}

// caption is lines centered under a node of radius r at at, or over it
// (above: the lead is below it).
func (u *ui) caption(gtx layout.Context, at image.Point, r int, above bool, lines []string) {
	style := func(i int) (unit.Sp, color.NRGBA, font.Font, int) {
		if i == len(lines)-1 {
			return 11, kit.CText3, kit.Mono, 1
		}
		return 12, kit.CText2, kit.Sans, 2
	}
	w := gtx.Dp(220)
	if !above {
		y := at.Y + r + gtx.Dp(8)
		for i, l := range lines {
			sp, c, f, n := style(i)
			y += u.centered(gtx, image.Pt(at.X, y), l, sp, c, f, w, n) + gtx.Dp(3)
		}
		return
	}
	y := at.Y - r - gtx.Dp(8)
	for i := len(lines) - 1; i >= 0; i-- { // bottom up: measured, then drawn where it ends at y
		sp, c, f, n := style(i)
		m := op.Record(gtx.Ops)
		h := u.centered(gtx, image.Point{X: at.X}, lines[i], sp, c, f, w, n)
		call := m.Stop()
		y -= h
		st := op.Offset(image.Pt(0, y)).Push(gtx.Ops)
		call.Add(gtx.Ops)
		st.Pop()
		y -= gtx.Dp(3)
	}
}

// leadStatus is what the lead is doing: its card's status, or which of
// its agents it waits on while they work and it doesn't.
func (u *ui) leadStatus(c *kanban.Card, ms []member) (string, color.NRGBA) {
	if c.Status != kanban.StatusWorking {
		for _, m := range ms {
			if m.last.Status == "running" {
				return "Waiting on " + m.name, pastels[0]
			}
		}
	}
	if s, ok := statusName[c.Status]; ok {
		return s, kit.Mix(pastels[0], statusColor[c.Status], .5)
	}
	return "Idle", kit.CText3
}

// node is one member: a pastel disc ringed in its color (dimmed once done),
// its name, model and state in it; a working one breathes. It says whether
// it was clicked.
func (u *ui) node(gtx layout.Context, id string, at image.Point, r int, col color.NRGBA, name, sub, state string, sc color.NRGBA, breathe, done bool) bool {
	cl := u.k.Click(id)
	clicked := cl.Clicked(gtx)
	if clicked {
		gtx.Execute(op.InvalidateCmd{})
	}
	if breathe {
		ph := float64(gtx.Now.UnixMilli()%1800) / 1800
		ring := col
		ring.A = uint8((1 - ph) * 120)
		circle(gtx.Ops, at, r+int(ph*float64(gtx.Dp(18))), ring, float32(gtx.Dp(3)))
	}
	f := .16
	if done {
		f = .08
	}
	if cl.Hovered() {
		f += .1
	}
	rim := col
	if done {
		rim = kit.Mix(col, kit.CPane, .55)
	}
	circle(gtx.Ops, at, r, rim, 0) // the rim, as a disc under the fill
	circle(gtx.Ops, at, r-gtx.Dp(2), kit.Mix(kit.CPane, col, f), 0)
	nameCol := kit.Mix(col, kit.CWhite, .35)
	sp := unit.Sp(15)
	if r > gtx.Dp(60) {
		sp = 20
	}
	lines := []struct {
		s  string
		sp unit.Sp
		c  color.NRGBA
		f  font.Font
	}{{name, sp, nameCol, kit.Bold(kit.Sans)}, {sub, 11, kit.CText2, kit.Sans}, {state, 11, sc, kit.Semi(kit.Sans)}}
	h := 0
	for _, l := range lines {
		h += gtx.Sp(l.sp) * 13 / 10
	}
	y := at.Y - h/2
	for _, l := range lines {
		u.centered(gtx, image.Pt(at.X, y), l.s, l.sp, l.c, l.f, 2*r-gtx.Dp(14), 1)
		y += gtx.Sp(l.sp) * 13 / 10
	}
	st := op.Offset(at.Sub(image.Pt(r, r))).Push(gtx.Ops)
	g := gtx
	g.Constraints = layout.Exact(image.Pt(2*r, 2*r))
	cl.Layout(g, func(gtx layout.Context) layout.Dimensions {
		defer clip.Ellipse{Max: gtx.Constraints.Max}.Push(gtx.Ops).Pop()
		pointer.CursorPointer.Add(gtx.Ops)
		return layout.Dimensions{Size: gtx.Constraints.Max}
	})
	st.Pop()
	return clicked
}

// shellPill is a running shell beside its agent: its command and time; a
// click opens its output.
func (u *ui) shellPill(gtx layout.Context, s *task, at image.Point, col color.NRGBA, act *func()) {
	k := u.k
	st := op.Offset(at).Push(gtx.Ops)
	defer st.Pop()
	g := gtx
	g.Constraints = layout.Constraints{Max: image.Pt(gtx.Dp(240), gtx.Dp(40))}
	cmd := cmp.Or(s.cmd, s.title())
	bid := "teamShell" + s.key()
	_, ok := k.RimButton(g, bid, kit.CColumn, kit.CHover, kit.Mix(col, kit.CPane, .6), kit.RSheet, layout.Inset{Top: 4, Bottom: 4, Left: 9, Right: 10}, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				sz := gtx.Dp(6)
				kit.FillRRect(gtx.Ops, image.Rect(0, 0, sz, sz), sz/2, col)
				return layout.Dimensions{Size: image.Pt(sz, sz)}
			}),
			layout.Rigid(kit.HGap(6)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return k.Label(gtx, "$ "+cmd+" · "+s.took(gtx.Now), 11, kit.Mix(col, kit.CWhite, .35), kit.Mono, 1)
			}))
	})
	if ok {
		key := s.key()
		*act = func() { u.sub = key }
	}
}

// tag is a small label on a spoke, on the pane's color so the line breaks
// around it.
func (u *ui) tag(gtx layout.Context, at image.Point, s string, c color.NRGBA) {
	m := op.Record(gtx.Ops)
	g := gtx
	g.Constraints = layout.Constraints{Max: image.Pt(gtx.Dp(200), gtx.Dp(30))}
	d := kit.Boxed(kit.CPane, color.NRGBA{}, kit.RCtl, layout.Inset{Top: 2, Bottom: 2, Left: 6, Right: 6}, u.k.Lbl(s, 11, c, kit.Sans))(g)
	call := m.Stop()
	defer op.Offset(at.Sub(d.Size.Div(2))).Push(gtx.Ops).Pop()
	call.Add(gtx.Ops)
}

// centered draws s centered on at.X from at.Y down, up to w wide; it says
// how tall it was.
func (u *ui) centered(gtx layout.Context, at image.Point, s string, sp unit.Sp, c color.NRGBA, f font.Font, w, lines int) int {
	m := op.Record(gtx.Ops)
	g := gtx
	g.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, gtx.Dp(200))}
	d := widget.Label{MaxLines: lines, Alignment: text.Middle}.Layout(g, u.k.Sh, f, sp, s, kit.Mat(g, c))
	call := m.Stop()
	defer op.Offset(image.Pt(at.X-w/2, at.Y)).Push(gtx.Ops).Pop()
	call.Add(gtx.Ops)
	return d.Size.Y
}

// circle fills a disc of radius r at c, or with width strokes its ring.
func circle(ops *op.Ops, c image.Point, r int, col color.NRGBA, width float32) {
	e := clip.Ellipse{Min: c.Sub(image.Pt(r, r)), Max: c.Add(image.Pt(r, r))}
	if width == 0 {
		paint.FillShape(ops, col, e.Op(ops))
		return
	}
	paint.FillShape(ops, col, clip.Stroke{Path: e.Path(ops), Width: width}.Op())
}

// spoke is a line from a to b, dashed when dash > 0.
func spoke(ops *op.Ops, a, b image.Point, col color.NRGBA, width, dash float32) {
	fa, fb := f32.Pt(float32(a.X), float32(a.Y)), f32.Pt(float32(b.X), float32(b.Y))
	d := fb.Sub(fa)
	n := float32(math.Hypot(float64(d.X), float64(d.Y)))
	if n < 1 {
		return
	}
	var p clip.Path
	p.Begin(ops)
	if dash == 0 {
		p.MoveTo(fa)
		p.LineTo(fb)
	}
	for s := float32(0); dash > 0 && s < n; s += 2 * dash {
		p.MoveTo(fa.Add(d.Mul(s / n)))
		p.LineTo(fa.Add(d.Mul(min(s+dash, n) / n)))
	}
	paint.FillShape(ops, col, clip.Stroke{Path: p.End(), Width: width}.Op())
}

// toward is the point r from a on the way to b.
func toward(a, b image.Point, r int) image.Point {
	d := b.Sub(a)
	n := math.Hypot(float64(d.X), float64(d.Y))
	if n == 0 {
		return a
	}
	return a.Add(image.Pt(int(float64(d.X)*float64(r)/n), int(float64(d.Y)*float64(r)/n)))
}

// handoff is one brief the lead gave an agent, or a result it got back.
type handoff struct {
	at       time.Time
	from, to string
	text     string
	key      string // the task to open
	verdict  bool   // a result
}

// handoffs is the team's briefs and results in order, newest last, and
// what the lead still waits for; a click opens that agent.
func (u *ui) handoffs(gtx layout.Context, c *kanban.Card, act *func()) layout.Dimensions {
	k := u.k
	gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(380), gtx.Constraints.Max.Y))
	kit.Fill(gtx, kit.CColumn)
	lead := cmp.Or(c.Agent, "lead")
	ms := u.members(c.ID)
	var hs []handoff
	var waits []string
	for _, t := range u.tasks[c.ID] {
		if t.Shell {
			continue
		}
		name := cmp.Or(t.Agent, t.title())
		if t.Prompt != "" {
			hs = append(hs, handoff{t.start, lead, name, t.Prompt, t.key(), false})
		}
		if !t.end.IsZero() && t.Summary != "" {
			hs = append(hs, handoff{t.end, name, lead, t.Summary, t.key(), true})
		}
		if t.Status == "running" && !slices.Contains(waits, name) {
			waits = append(waits, name)
		}
	}
	slices.SortStableFunc(hs, func(a, b handoff) int { return a.at.Compare(b.at) })
	ws := make([]layout.Widget, 0, len(hs)+1)
	for i, h := range hs {
		ws = append(ws, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			bid := fmt.Sprintf("teamHand%d-%s", i, h.key)
			d, ok := k.Button(gtx, bid, kit.CCard, kit.CHover, kit.RCard, layout.Inset{Top: 10, Bottom: 11, Left: 12, Right: 12}, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				who := func(n string) color.NRGBA {
					if i := slices.IndexFunc(ms, func(m member) bool { return m.name == n }); i >= 0 && n != lead {
						return ms[i].col
					}
					return pastels[0]
				}
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(k.Lbl(h.at.Format("15:04")+" · ", 11, kit.CText3, kit.Sans)),
							layout.Rigid(k.Lbl(h.from, 11, who(h.from), kit.Semi(kit.Sans))),
							layout.Rigid(k.Lbl(" → ", 11, kit.CText3, kit.Sans)),
							layout.Rigid(k.Lbl(h.to, 11, who(h.to), kit.Semi(kit.Sans))))
					}),
					layout.Rigid(kit.VGap(5)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return k.Label(gtx, oneParagraph(h.text), 13, kit.CText, kit.Sans, 4)
					}))
			})
			k.Tip(gtx, bid, d, "Open "+map[bool]string{false: h.to, true: h.from}[h.verdict], false)
			if ok {
				key := h.key
				*act = func() { u.sub = key }
			}
			return layout.Inset{Bottom: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return d })
		})
	}
	if len(waits) > 0 {
		ws = append(ws, kit.Boxed(color.NRGBA{}, kit.CRim, kit.RCard, layout.Inset{Top: 10, Bottom: 10, Left: 12, Right: 12}, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return k.Label(gtx, lead+" is waiting for "+strings.Join(waits, ", ")+"…", 12, kit.CText3, kit.Sans, 2)
		}))
	}
	if len(ws) == 0 {
		ws = append(ws, k.Lbl("No handoffs yet.", 12, kit.CText3, kit.Sans))
	}
	agents := len(ms)
	return layout.Inset{Top: 16, Bottom: 12, Left: 16, Right: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
					layout.Flexed(1, k.LblFill("Handoffs", 14, kit.CText, kit.Bold(kit.Sans))),
					layout.Rigid(k.Lbl(fmt.Sprintf("%s · $%.2f", plural(agents, "agent", "agents"), c.Session.Cost), 11, kit.CText3, kit.Sans)))
			}),
			layout.Rigid(kit.VGap(12)),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return u.teamList.Layout(gtx, len(ws), func(gtx layout.Context, i int) layout.Dimensions { return ws[i](gtx) })
			}))
	})
}

// oneParagraph is s's first paragraph, its lines joined: a brief at a glance.
func oneParagraph(s string) string {
	s = strings.TrimSpace(s)
	if p, _, ok := strings.Cut(s, "\n\n"); ok {
		s = p
	}
	return strings.Join(strings.Fields(s), " ")
}
