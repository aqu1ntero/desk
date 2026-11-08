package ui

import (
	"cmp"
	"fmt"
	"image"
	"image/color"
	"slices"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"

	"github.com/lfdominguez/desk/internal/kanban"
	"github.com/lfdominguez/desk/internal/ui/kit"
)

// A session's team, as the canvas (canvas.go) draws it: the session leads,
// the agents it handed work to are its members, one per kind, and the
// briefs and results between them are the handoffs, listed beside the
// canvas.

// pastels color the team's avatars: the lead takes the first, the agents
// the rest in the order they joined, so each keeps its own.
var pastels = []color.NRGBA{kit.RGB(0xb9a7ff), kit.RGB(0x8fe3bd), kit.RGB(0xffbf8f), kit.RGB(0x8ccaff), kit.RGB(0xffa8c5), kit.RGB(0xf2d98a), kit.RGB(0x8fe0e6)}

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

// settled is how long a done agent stays on the canvas before it leaves for
// the "earlier" chip, which brings the card's back.
const settled = 20 * time.Minute

// onCanvas is ms without the agents done for longer than settled, unless
// the card shows them again, and how many are such.
func (u *ui) onCanvas(card string, ms []member, now time.Time) ([]member, int) {
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

// leadStatus is what the lead is doing: its card's status, or which of
// its agents it waits on while they work and it doesn't.
func leadStatus(c *kanban.Card, ms []member) string {
	if c.Status != kanban.StatusWorking {
		for _, m := range ms {
			if m.last.Status == "running" {
				return "Waiting on " + m.name
			}
		}
	}
	if s, ok := statusName[c.Status]; ok {
		return s
	}
	return "Idle"
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
