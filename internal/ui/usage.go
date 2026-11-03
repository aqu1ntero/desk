package ui

import (
	"context"
	"fmt"
	"image"
	"path"
	"time"

	"gioui.org/layout"

	"github.com/lfdominguez/desk/internal/claude"
	"github.com/lfdominguez/desk/internal/kanban"
	"github.com/lfdominguez/desk/internal/orgs"
	"github.com/lfdominguez/desk/internal/ui/kit"
)

// planLimits is claude.Limits; tests replace it.
var planLimits = claude.Limits

// planUsage is an account's plan limits as last read.
type planUsage struct {
	l   []claude.Limit
	at  time.Time // asked: one request per account every few minutes
	err string
}

// accountName says which Claude account org's sessions use, read once.
// ponytail: a later login under another email shows after a restart.
func (u *ui) accountName(org string) string {
	acct := orgs.Account(u.root, org)
	n, ok := u.accts[acct]
	if !ok {
		n = claude.AccountName(u.home, acct)
		u.accts[acct] = n
	}
	return n
}

// limitsFor is account's plan limits, asking again (off the UI goroutine)
// once they are 3 minutes old.
func (u *ui) limitsFor(acct string) *planUsage {
	p := u.limits[acct]
	if p == nil {
		p = &planUsage{}
		u.limits[acct] = p
	}
	if time.Since(p.at) > 3*time.Minute {
		p.at = time.Now()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			l, err := planLimits(ctx, u.home, acct)
			cancel()
			u.later(func() {
				if err != nil {
					p.err = firstLine(err.Error())
				} else {
					p.l, p.err = l, ""
				}
				u.limGen++
			})
		}()
	}
	return p
}

// usageStrip is the session's context (tokens in it, of the window) and
// the account's plan limits, each with a meter: the session view's footing.
func (u *ui) usageStrip(gtx layout.Context, c *kanban.Card) layout.Dimensions {
	k := u.k
	var items []layout.FlexChild
	item := func(name string, pct float64, val string) {
		if len(items) > 0 {
			items = append(items, layout.Rigid(kit.HGap(18)))
		}
		items = append(items, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			kids := []layout.FlexChild{layout.Rigid(k.Lbl(name, 11, kit.CText3, kit.Medium(kit.Sans))), layout.Rigid(kit.HGap(6))}
			if pct >= 0 {
				kids = append(kids, layout.Rigid(meter(pct)), layout.Rigid(kit.HGap(6)))
			}
			kids = append(kids, layout.Rigid(k.Lbl(val, 11, kit.CText2, kit.Sans)))
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx, kids...)
		}))
	}
	if s := c.Session; s.Context > 0 {
		if s.Window > 0 {
			pct := 100 * float64(s.Context) / float64(s.Window)
			item("Context", pct, fmt.Sprintf("%s / %s (%.0f%%)", tokens(s.Context), tokens(s.Window), pct))
		} else {
			item("Context", -1, tokens(s.Context))
		}
	}
	p := u.limitsFor(orgs.Account(u.root, c.Org))
	for _, l := range p.l {
		item(l.Name, l.Percent, fmt.Sprintf("%.0f%% · resets %s", l.Percent, resets(l.Resets)))
	}
	if len(p.l) == 0 && p.err != "" {
		item("Limits", -1, p.err)
	}
	if len(items) == 0 {
		return layout.Dimensions{}
	}
	return layout.Inset{Top: 6, Bottom: 6, Left: 16, Right: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx, items...)
	})
}

// meter is a small bar filled pct%, colored by how close to full.
func meter(pct float64) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		sz := image.Pt(gtx.Dp(44), gtx.Dp(5))
		kit.FillRRect(gtx.Ops, image.Rectangle{Max: sz}, sz.Y/2, kit.CHover)
		if w := int(float64(sz.X) * min(max(pct, 0), 100) / 100); w > 0 {
			kit.FillRRect(gtx.Ops, image.Rect(0, 0, max(w, sz.Y), sz.Y), sz.Y/2, levelColor(pct))
		}
		return layout.Dimensions{Size: sz}
	}
}

// resets is when a limit resets: the time today, else the weekday too.
func resets(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	t = t.Local()
	if time.Until(t) < 20*time.Hour {
		return t.Format("15:04")
	}
	return t.Format("Mon 15:04")
}

// artifactName is how an artifact's chip reads: its id's start.
func artifactName(url string) string {
	id := path.Base(url)
	if len(id) > 8 {
		id = id[:8]
	}
	return "Artifact " + id
}
