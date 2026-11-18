package ui

import (
	"maps"

	"gioui.org/layout"
	"gioui.org/widget"

	"github.com/lfdominguez/desk/internal/ui/kit"
)

// meHint is what the Who am I fields ask for.
const meHint = "e.g. I'm the team's backend lead: Go and Postgres. Keep answers short, show the commands, ask before anything that touches production."

// loadMe is an org page's Who am I as read ([me]): its text, and whether it
// extends the global one (on["extend"], on unless extend = false).
func loadMe(raw map[string]any) *cfgEntry {
	e := &cfgEntry{kind: "me", eds: map[string]*widget.Editor{"about": {}}, src: map[string]int{}, on: map[string]bool{}}
	m, _ := raw["me"].(map[string]any)
	about, _ := m["about"].(string)
	e.ed("about").SetText(about)
	e.on["extend"] = m["extend"] != false
	e.old, e.base = true, e.snap()
	return e
}

// saveMe writes the page's Who am I into out: about when set, extend only
// when off; nothing at all, no [me]. Keys desk doesn't know stay.
func (d *cfgDialog) saveMe(out map[string]any) {
	if !d.me.edited() {
		return
	}
	m, _ := out["me"].(map[string]any)
	m = maps.Clone(m)
	if m == nil {
		m = map[string]any{}
	}
	set(m, "about", d.me.text("about"))
	delete(m, "extend")
	if !d.me.on["extend"] {
		m["extend"] = false
	}
	set(out, "me", m)
}

// cfgMe is an org page's Who am I, under its servers: who the user is
// here, and whether that adds to the global one or replaces it.
func (u *ui) cfgMe(gtx layout.Context) layout.Dimensions {
	k, d := u.k, u.cfg
	e := d.me
	sw := "Extend from global"
	what := "Its Claudes get the global Who am I (General), then this."
	if !e.on["extend"] {
		what = "Its Claudes get only this: the global Who am I is left out."
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(kit.VGap(18)), layout.Rigid(k.Caption("Who am I here")), layout.Rigid(kit.VGap(4)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return k.Label(gtx, "Who you are in this organization: your role, what you care about, how you like the work done. Every Claude desk starts for it is told, to focus its answers and choices on you.", 12, kit.CText3, kit.Sans, 0)
		}),
		layout.Rigid(kit.VGap(10)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					dim, ok := k.Switch(gtx, "cfgMeExtend", e.on["extend"])
					if ok {
						d.act = func() { e.on["extend"] = !e.on["extend"] }
					}
					return dim
				}),
				layout.Rigid(kit.HGap(10)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(k.Lbl(sw, 13, kit.CText, kit.Sans)),
						layout.Rigid(k.Lbl(what, 11, kit.CText3, kit.Sans)))
				}))
		}),
		layout.Rigid(kit.VGap(10)),
		layout.Rigid(k.Field(e.ed("about"), meHint, 60)))
}
