package ui

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/unit"

	"github.com/lfdominguez/desk/internal/fzf"
	"github.com/lfdominguez/desk/internal/kanban"
	"github.com/lfdominguez/desk/internal/links"
	"github.com/lfdominguez/desk/internal/orgs"
	"github.com/lfdominguez/desk/internal/ui/kit"
)

// CI: the GitHub Actions runs and GitLab pipelines of the pull and merge
// requests and commits linked on an organization's cards (not Done), in
// its "ci:<org>" view, polled with the links.

// ciRuns asks a link's CI; tests replace it.
var ciRuns = links.CI

// ciItem is one linked request or commit and its CI.
type ciItem struct {
	id, key, title string // the card
	link           kanban.Link
	runs           []links.Run
	err            string
}

// checkCI asks the CI of every such link, off the UI goroutine. Orgs with
// none have no CI view.
func (u *ui) checkCI() {
	got := make(chan map[string][]ciItem, 1)
	u.later(func() {
		m := map[string][]ciItem{}
		for i, col := range u.b.Columns {
			if i == u.b.Column("Done") {
				continue
			}
			for _, c := range col.Cards {
				for _, l := range c.Links {
					if links.HasCI(l) {
						m[c.Org] = append(m[c.Org], ciItem{id: c.ID, key: c.Key(), title: c.Title, link: l})
					}
				}
			}
		}
		got <- m
	})
	want := <-got
	for org, items := range want {
		cfg, err := orgs.Load(u.root, org)
		done := u.busy(org)
		var kept []ciItem // without an account for its provider, a link has no CI here
		for _, it := range items {
			var runs []links.Run
			if err == nil {
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				runs, err = ciRuns(ctx, it.link, cfg)
				cancel()
			}
			if na := (*links.NoAccountError)(nil); !errors.As(err, &na) {
				it.runs, it.err = runs, u.fetchErr(err)
				kept = append(kept, it)
			}
			err = nil
		}
		done()
		u.later(func() {
			if len(kept) == 0 {
				delete(u.ci, org) // no CI view
			} else {
				u.ci[org] = kept
			}
			u.tkGen++
		})
	}
	u.later(func() {
		for org := range u.ci {
			if _, ok := want[org]; !ok {
				delete(u.ci, org)
				u.tkGen++
			}
		}
	})
}

var ciColor = map[string]color.NRGBA{links.CISuccess: kit.CGreen, links.CIFailed: kit.CRed, links.CIRunning: kit.CPurple,
	links.CIPending: kit.COrange, links.CICanceled: kit.CText3, links.CISkipped: kit.CText3}

// ciState is an item's worst status: "" without runs.
func ciState(it ciItem) string {
	if it.err != "" {
		return links.CIFailed
	}
	switch s, _ := links.Summary(it.runs); s {
	case "CI failed":
		return links.CIFailed
	case "CI running":
		return links.CIRunning
	case "CI passed":
		return links.CISuccess
	}
	return ""
}

// ciCount is an org's links whose CI failed.
func (u *ui) ciCount(org string) int {
	n := 0
	for _, it := range u.ci[org] {
		if ciState(it) == links.CIFailed {
			n++
		}
	}
	return n
}

// ciTone is the worst of an org's CI.
func (u *ui) ciTone(org string) color.NRGBA {
	c := kit.CText3
	for _, it := range u.ci[org] {
		switch ciState(it) {
		case links.CIFailed:
			return kit.CRed
		case links.CIRunning:
			c = kit.CPurple
		case links.CISuccess:
			if c == kit.CText3 {
				c = kit.CGreen
			}
		}
	}
	return c
}

// ciSentence is the header for an org's CI: "1 failed · 2 running · 3 passed".
func (u *ui) ciSentence(org string) string {
	n := map[string]int{}
	for _, it := range u.ci[org] {
		n[ciState(it)]++
	}
	return fmt.Sprintf("%d failed · %d running · %d passed", n[links.CIFailed], n[links.CIRunning], n[links.CISuccess])
}

// ciView is an organization's linked requests and commits that match the
// search, each with its runs; a run's jobs show unless all passed. A click
// opens the run or job; the card's line opens its session.
func (u *ui) ciView(gtx layout.Context) layout.Dimensions {
	k := u.k
	org := strings.TrimPrefix(u.view, "ci:")
	gtx.Constraints.Min = gtx.Constraints.Max
	kit.Fill(gtx, kit.CPane)
	q := u.search.Text()
	var rows []layout.Widget
	for _, it := range u.ci[org] {
		hay := it.key + " " + it.title + " " + it.link.Ref
		for _, r := range it.runs {
			hay += " " + r.Name + " " + r.Status
		}
		if _, ok := fzf.Match(q, hay); !ok {
			continue
		}
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			d, ok := k.Button(gtx, "ci"+it.id+it.link.URL, kit.CPane, kit.CHover, kit.RCtl, layout.Inset{Top: 14, Bottom: 6, Left: 4, Right: 4}, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(k.Lbl(it.key, 12, kit.CText3, kit.Mono)),
					layout.Rigid(kit.HGap(8)),
					layout.Flexed(1, k.LblFill(it.title, 13, kit.CText, kit.Semi(kit.Sans))),
					layout.Rigid(kit.HGap(8)),
					layout.Rigid(k.Pill(it.link.Ref, kit.CText2, kit.CPanel)))
			})
			if ok {
				u.openSession(it.id)
			}
			return d
		})
		if it.err != "" {
			rows = append(rows, func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: 4, Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return k.Label(gtx, it.err, 12, kit.CRed, kit.Sans, 3) })
			})
		} else if len(it.runs) == 0 {
			rows = append(rows, func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: 4, Bottom: 4}.Layout(gtx, k.Lbl("No CI runs.", 12, kit.CText3, kit.Sans))
			})
		}
		for _, r := range it.runs {
			rows = append(rows, u.ciRow("cir"+it.link.URL+fmt.Sprint(r.ID), r.Name, r.Status, r.URL, 0))
			if r.Status == links.CISuccess {
				continue
			}
			for _, j := range r.Jobs {
				if j.Status != links.CISuccess && j.Status != links.CISkipped {
					name := j.Name
					if j.Stage != "" {
						name = j.Stage + " · " + name
					}
					rows = append(rows, u.ciRow("cij"+it.link.URL+fmt.Sprint(j.ID), name, j.Status, j.URL, 22))
				}
			}
		}
	}
	if len(rows) == 0 {
		msg := "No CI matches."
		if _, ok := u.ci[org]; !ok {
			msg = "Asking the CI…"
		}
		return layout.Center.Layout(gtx, k.Lbl(msg, 13, kit.CText3, kit.Sans))
	}
	w := min(gtx.Constraints.Max.X-gtx.Dp(48), gtx.Dp(820))
	return layout.N.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Constraints{Min: image.Pt(w, gtx.Constraints.Max.Y), Max: image.Pt(w, gtx.Constraints.Max.Y)}
		return u.ciList.Layout(gtx, len(rows), func(gtx layout.Context, i int) layout.Dimensions { return rows[i](gtx) })
	})
}

// ciRow is a run (indent 0) or a job: its status and name; a click opens it.
func (u *ui) ciRow(id, name, status, url string, indent int) layout.Widget {
	k := u.k
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Left: unit.Dp(indent), Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			d, ok := k.Button(gtx, id, kit.CPanelCard, kit.CHover, kit.RCard, layout.Inset{Top: 7, Bottom: 7, Left: 12, Right: 12}, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				sc := ciColor[status]
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(k.Pill(status, sc, kit.Mix(kit.CPanel, sc, .15))),
					layout.Rigid(kit.HGap(10)),
					layout.Flexed(1, k.LblFill(name, 13, kit.CText, kit.Sans)))
			})
			if ok && url != "" {
				openURL(url)
			}
			return d
		})
	}
}
