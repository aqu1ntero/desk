package ui

import (
	"fmt"
	"image"
	"image/color"
	"slices"
	"strings"
	"time"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget"

	"github.com/lfdominguez/desk/internal/kanban"
	"github.com/lfdominguez/desk/internal/ui/kit"
)

// The new-card dialog: one field for what to do, whose @org, #label,
// !priority, due days and ticket keys become pills (smart.go); Claude's
// Improve is optional and comes back as suggestions to accept or undo.

// improveNote is what the footer says before Improve.
const improveNote = "Optional: reads the org and the ticket, fills in the rest"

// newCardRows is the new-card dialog's body: the header, the request (or
// Claude's suggestions), the pills, and the other fields when asked for.
func (u *ui) newCardRows() []sheetRow {
	k, s := u.k, u.edit
	pad := func(w layout.Widget) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: 20, Right: 20}.Layout(gtx, w)
		}
	}
	head := sheetRow{w: func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: 16, Bottom: 14, Left: 20, Right: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			right := "Draft kept if you close"
			if s.optimized {
				right = ""
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(k.Lbl("New card", 15, kit.CText, kit.Bold(kit.Sans))),
				layout.Rigid(kit.HGap(10)),
				layout.Rigid(k.Pill("● "+u.b.Columns[s.col].Title, u.colAccent(s.col), kit.CPanelCard)),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.E.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min = image.Point{}
						return k.Label(gtx, right, 12, kit.CText3, kit.Sans, 1)
					})
				}),
				layout.Rigid(kit.HGap(8)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					d, ok := k.IconBtn(gtx, "shClose", kit.IcoX, false)
					if ok {
						s.act = u.closeSheet
					}
					return d
				}))
		})
	}}
	rows := []sheetRow{head}
	if s.optStop != nil {
		rows = append(rows, u.liveRow(pad))
	}
	if s.optErr != "" {
		rows = append(rows, u.optErrRow(pad))
	}
	if s.optimized {
		rows = append(rows, u.reviewRows(pad)...)
	} else {
		rows = append(rows,
			sheetRow{w: pad(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(k.Caption(map[bool]string{false: "What to do", true: "What to do · locked while Claude works"}[s.optStop != nil])), layout.Rigid(kit.VGap(6)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						rim := kit.CBtnRim
						if gtx.Focused(&s.notes) {
							rim = kit.CAcc
						}
						return kit.Boxed(kit.CPane, rim, kit.RCard, layout.Inset{Top: 12, Bottom: 12, Left: 14, Right: 14}, func(gtx layout.Context) layout.Dimensions {
							gtx.Constraints.Min = image.Pt(gtx.Constraints.Max.X, gtx.Dp(110))
							return k.Editor(gtx, &s.notes, "Renew the TLS certificate on the @Acme api servers before friday, ABC-123 !high #infra", 15, kit.Sans, kit.CText)
						})(gtx)
					}),
					layout.Rigid(kit.VGap(6)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return k.Label(gtx, "Paste screenshots in. Type @org, #label, !priority, a day or a ticket key; click a pill to make its word plain text.", 12, kit.CText3, kit.Sans, 2)
					}))
			}), eds: []*widget.Editor{&s.notes}})
	}
	rows = append(rows, sheetRow{w: func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: 14, Bottom: 16, Left: 20, Right: 20}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			if s.optimized {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(k.Caption("Filled in")), layout.Rigid(kit.VGap(6)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return kit.Wrap(gtx, 6, u.pills(s)) }))
			}
			return kit.Wrap(gtx, 6, u.pills(s))
		})
	}})
	hasOrg := slices.Contains(u.orgList, strings.TrimSpace(s.org.Text())) || smartVal(u.tokens(s, s.notes.Text()), "org") != ""
	if s.more {
		for _, r := range u.sheetRows() {
			rows = append(rows, sheetRow{w: pad(r.w), eds: r.eds})
		}
	} else if s.orgRow = s.orgRow || !hasOrg; s.orgRow { // where its session starts can't be skipped: asked right here, and kept once picked
		r := u.sheetRows()[0] // the organization and folder
		rows = append(rows, sheetRow{w: pad(r.w), eds: r.eds})
	}
	return rows
}

// pills draws a new card's pills; a click runs its f after the dialog.
func (u *ui) pills(s *sheet) []layout.Widget {
	k := u.k
	var ws []layout.Widget
	for _, p := range u.pillsOf(s) {
		ws = append(ws, func(gtx layout.Context) layout.Dimensions {
			d, ok := k.RimButton(gtx, "pill"+p.id, kit.CPanelCard, kit.CHover, kit.CBtnRim, 12, layout.Inset{Top: 4, Bottom: 4, Left: 10, Right: 10}, func(gtx layout.Context) layout.Dimensions {
				face := kit.Medium(kit.Sans)
				if p.mono {
					face = kit.Mono
				}
				ch := []layout.FlexChild{}
				if p.dot != (color.NRGBA{}) {
					ch = append(ch, layout.Rigid(kit.Dot(p.dot)), layout.Rigid(kit.HGap(7)))
				}
				ch = append(ch, layout.Rigid(k.Lbl(p.label, 12, p.fg, face)))
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx, ch...)
			})
			if ok {
				s.act = p.f
			}
			return d
		})
	}
	return ws
}

// pill is one of a new card's chips.
type pill struct {
	id, label string
	fg, dot   color.NRGBA
	mono      bool
	f         func()
}

// pillsOf are a new card's properties as chips: from the request's tokens
// (a click makes the word plain text) and from its fields (a click shows
// the fields).
func (u *ui) pillsOf(s *sheet) []pill {
	var ts []smartTok
	if !s.optimized { // else the draft wrote them into the fields
		ts = u.tokens(s, s.notes.Text())
	}
	var ps []pill
	add := func(id, label string, fg color.NRGBA, dot color.NRGBA, mono bool, f func()) {
		ps = append(ps, pill{id, label, fg, dot, mono, f})
	}
	more := func() { s.more = true }
	plainOf := func(t smartTok) func() { return func() { s.plain[t.raw] = true } }
	tok := func(kind string) (smartTok, bool) {
		i := slices.IndexFunc(ts, func(t smartTok) bool { return t.kind == kind })
		if i < 0 {
			return smartTok{}, false
		}
		return ts[i], true
	}
	org := strings.TrimSpace(s.org.Text())
	if t, ok := tok("org"); ok {
		add("org", t.val, kit.CText, kit.CAcc, false, plainOf(t))
	} else if slices.Contains(u.orgList, org) {
		add("org", org, kit.CText, kit.CAcc, false, more)
	} else {
		add("org", "Pick the organization", kit.COrange, kit.COrange, false, more)
	}
	if d := strings.Trim(s.dir.Text(), "/"); d != "" {
		add("dir", d, kit.CText2, kit.NRGBA0, true, more)
	}
	if u.sheetAgents(s); len(s.agents) > 0 {
		a := s.agent
		if a == "" {
			a = "none"
		}
		add("agent", "Agent: "+a, kit.CText2, kit.NRGBA0, false, more)
	}
	if t, ok := tok("prio"); ok && s.prio == 0 {
		add("prio", t.val, prioColor(slices.Index(prioNames, t.val)), kit.NRGBA0, false, plainOf(t))
	} else if s.prio > 0 {
		add("prio", prioNames[s.prio], prioColor(s.prio), kit.NRGBA0, false, more)
	}
	due := strings.TrimSpace(s.due.Text())
	dueFn := more
	if t, ok := tok("due"); ok && due == "" {
		due, dueFn = t.val, plainOf(t)
	}
	if d, err := time.Parse(kanban.DateLayout, due); err == nil {
		add("due", "Due "+d.Format("Mon, Jan 2"), kit.CText, kit.NRGBA0, false, dueFn)
	}
	labels := kanban.ParseLabels(s.labels.Text())
	for _, l := range labels {
		add("lab"+l, l, kit.CText2, kit.NRGBA0, false, more)
	}
	for _, t := range ts {
		if t.kind == "label" && !slices.Contains(labels, t.val) {
			add("lab"+t.val, t.val, kit.CText2, kit.NRGBA0, false, plainOf(t))
		}
	}
	for _, l := range strings.Fields(s.links.Text()) {
		add("link"+l, l, kit.CAccText, kit.NRGBA0, true, more)
	}
	for _, t := range ts {
		if t.kind == "link" && !strings.Contains(s.links.Text(), t.val) {
			add("link"+t.val, t.val, kit.CAccText, kit.NRGBA0, true, plainOf(t))
		}
	}
	label := "+ Folder, links, due, column…"
	if s.more {
		label = "− Fewer fields"
	}
	add("more", label, kit.CText3, kit.NRGBA0, false, func() { s.more = !s.more })
	return ps
}

// reviewRows are Claude's suggestions for a new card: the title and notes
// it wrote, each marked suggested with Use mine to go back to the user's.
func (u *ui) reviewRows(pad func(layout.Widget) layout.Widget) []sheetRow {
	k, s := u.k, u.edit
	tag := func(gtx layout.Context) layout.Dimensions {
		if s.accepted {
			return layout.Dimensions{}
		}
		return layout.Inset{Left: 8}.Layout(gtx, k.Pill("suggested", kit.CPurple, kit.CPurpleSoft))
	}
	link := func(id, label string, f func()) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			d, ok := k.Button(gtx, id, kit.NRGBA0, kit.CHover, kit.RCtl, layout.Inset{Top: 2, Bottom: 2, Left: 6, Right: 6}, k.Lbl(label, 12, kit.CAccText, kit.Semi(kit.Sans)))
			if ok {
				f()
			}
			return d
		})
	}
	banner := sheetRow{w: pad(func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Bottom: 14}.Layout(gtx, kit.Boxed(kit.CPurpleSoft, kit.Mix(kit.CPurple, kit.CPanel, .6), kit.RCard, layout.Inset{Top: 9, Bottom: 9, Left: 12, Right: 8}, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			ch := []layout.FlexChild{layout.Rigid(kit.Icon(14, kit.CPurple, kit.IcoSpark)), layout.Rigid(kit.HGap(10)),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return k.Label(gtx, s.optNote, 13, kit.CText, kit.Sans, 2) })}
			if !s.accepted {
				ch = append(ch, link("shAccept", "Accept all", func() { s.accepted = true }))
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx, ch...)
		}))
	})}
	struck := func(was, now string) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			was = strings.TrimSpace(was)
			if s.accepted || was == "" || was == strings.TrimSpace(now) {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: 5}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = 0 // the line strikes the text, not the row
				d := k.Label(gtx, firstLine(was), 12, kit.CText3, kit.Sans, 1)
				y := d.Size.Y / 2
				paint.FillShape(gtx.Ops, kit.CText3, clip.Rect(image.Rect(0, y, d.Size.X, y+gtx.Dp(1))).Op())
				return d
			})
		})
	}
	title := sheetRow{w: pad(func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Bottom: 14}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx, layout.Rigid(k.Caption("Title")), layout.Rigid(tag),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: gtx.Constraints.Min} }),
						link("shMineTitle", "Use mine", func() {
							s.title.SetText(smartTitle(s.mineNotes, u.tokens(s, s.mineNotes)))
						}))
				}),
				layout.Rigid(kit.VGap(6)),
				layout.Rigid(k.Field(&s.title, "What needs doing", 0)),
				struck(smartTitle(s.mineNotes, u.tokens(s, s.mineNotes)), s.title.Text()))
		})
	}), eds: []*widget.Editor{&s.title}}
	notes := sheetRow{w: pad(func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Bottom: 2}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			body := k.Field(&s.notes, "Details, next steps…", 64)
			if s.preview { // the notes as they read: Claude writes them in Markdown
				body = kit.Boxed(kit.CPane, kit.CLine, kit.RCard, layout.Inset{Top: 10, Bottom: 10, Left: 14, Right: 14}, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min = image.Pt(gtx.Constraints.Max.X, gtx.Dp(64))
					return u.markdown(gtx, "sheetNotes", s.notes.Text())
				})
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx, layout.Rigid(k.Caption("Notes")), layout.Rigid(tag),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: gtx.Constraints.Min} }),
						link("notesView", map[bool]string{true: "Edit", false: "Preview"}[s.preview], func() {
							if s.preview = !s.preview; !s.preview {
								s.focus = nil
								u.focusLater = &s.notes
							}
						}),
						link("shMineNotes", "Use mine", func() { s.notes.SetText(s.mineNotes); s.preview = false }))
				}),
				layout.Rigid(kit.VGap(6)),
				layout.Rigid(body))
		})
	}), eds: []*widget.Editor{&s.notes}}
	return []sheetRow{banner, title, notes}
}

// optStages are Improve's steps, as liveRow lists them (sheet.optStage).
var optStages = []string{"Tickets", "Reads the org", "Writes the card"}

// liveRow is Improve while it runs: a spinner with its step, the stages
// done and to go, the time it has taken, and the title and prompt as
// Haiku writes them, so there's something to read before it ends.
func (u *ui) liveRow(pad func(layout.Widget) layout.Widget) sheetRow {
	k, s := u.k, u.edit
	return sheetRow{w: pad(func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Bottom: 14}.Layout(gtx, kit.Boxed(kit.CPurpleSoft, kit.Mix(kit.CPurple, kit.CPanel, .6), kit.RCard, layout.Inset{Top: 10, Bottom: 10, Left: 12, Right: 12}, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			el := gtx.Now.Sub(s.optStart).Round(time.Second)
			top := layout.Flex{Alignment: layout.Middle}.Layout
			stages := []layout.FlexChild{}
			for i, n := range optStages {
				if i == 0 && !s.optTickets {
					continue
				}
				fg, mark := kit.CText3, "○ "
				switch {
				case i < s.optStage:
					mark = "✓ "
				case i == s.optStage:
					fg, mark = kit.CPurple, "● "
				}
				if len(stages) > 0 {
					stages = append(stages, layout.Rigid(k.Lbl("  ›  ", 11, kit.CText3, kit.Sans)))
				}
				stages = append(stages, layout.Rigid(k.Lbl(mark+n, 11, fg, kit.Medium(kit.Sans))))
			}
			ch := []layout.FlexChild{
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return top(gtx, layout.Rigid(kit.Spinner(14, kit.CPurple)), layout.Rigid(kit.HGap(10)),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return k.Label(gtx, s.optStep+"…", 13, kit.CText, kit.Medium(kit.Sans), 1)
						}),
						layout.Rigid(k.Lbl(fmt.Sprintf("%d:%02d", int(el.Minutes()), int(el.Seconds())%60), 12, kit.CText3, kit.Mono)))
				}),
				layout.Rigid(kit.VGap(6)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return top(gtx, stages...) }),
			}
			if t := s.optLive.Title; t != "" {
				ch = append(ch, layout.Rigid(kit.VGap(10)), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return k.Label(gtx, t, 14, kit.CText, kit.Bold(kit.Sans), 2)
				}))
			}
			if p := s.optLive.Prompt; p != "" {
				ch = append(ch, layout.Rigid(kit.VGap(6)), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return u.markdown(gtx, "optLive", p)
				}))
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
		}))
	})}
}

// focusNext asks for the keyboard to go to a field on the next frame.
func (u *ui) focusNext(gtx layout.Context) {
	if u.focusLater != nil {
		gtx.Execute(key.FocusCmd{Tag: u.focusLater})
		u.focusLater = nil
	}
}
