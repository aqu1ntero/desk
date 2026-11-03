package ui

import (
	"encoding/json"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/widget"

	"github.com/lfdominguez/desk/internal/kanban"
	"github.com/lfdominguez/desk/internal/links"
	"github.com/lfdominguez/desk/internal/ui/kit"
)

// Snoozing an alert hides it, and its count, until a time: for one looked
// into and left as it is on purpose (a container stopped by hand). desk
// keeps them next to the board; Nagios is not told.

// snoozeFor is the picker's ready-made durations.
var snoozeFor = []struct {
	name string
	d    time.Duration
}{{"1 hour", time.Hour}, {"4 hours", 4 * time.Hour}, {"1 day", 24 * time.Hour}, {"1 week", 7 * 24 * time.Hour}}

func alertKey(org string, a links.Alert) string { return org + "|" + a.Host + "|" + a.Service }

// alertName is a key's "host / service".
func alertName(key string) string {
	_, rest, _ := strings.Cut(key, "|")
	host, service, _ := strings.Cut(rest, "|")
	if service != "" {
		return host + " / " + service
	}
	return host
}

func (u *ui) snoozePath() string { return filepath.Join(filepath.Dir(u.path), "snoozed.json") }

func (u *ui) loadSnoozed() {
	u.snoozed = map[string]time.Time{}
	if b, err := os.ReadFile(u.snoozePath()); err == nil {
		json.Unmarshal(b, &u.snoozed)
	}
}

// snooze hides alert key for d; 0 shows it again.
func (u *ui) snooze(key string, d time.Duration) {
	now := time.Now()
	for k, t := range u.snoozed {
		if now.After(t) {
			delete(u.snoozed, k)
		}
	}
	if d > 0 {
		u.snoozed[key] = now.Add(d)
		u.showToast("Snoozed "+alertName(key)+" until "+u.snoozed[key].Format("Mon Jan 2 15:04"), false)
	} else {
		delete(u.snoozed, key)
	}
	b, _ := json.MarshalIndent(u.snoozed, "", "  ")
	if err := os.WriteFile(u.snoozePath(), b, 0o600); err != nil {
		u.showToast("Snooze not saved: "+err.Error(), true)
	}
	u.tkGen++
}

// snoozedUntil is when an org's alert a shows again, if it is snoozed.
func (u *ui) snoozedUntil(org string, a links.Alert) (time.Time, bool) {
	t, ok := u.snoozed[alertKey(org, a)]
	return t, ok && time.Now().Before(t)
}

// parseSnooze reads a custom duration: Go's (2h30m), or days and weeks
// (3d, 2w).
func parseSnooze(s string) (time.Duration, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	for suf, unit := range map[string]time.Duration{"d": 24 * time.Hour, "w": 7 * 24 * time.Hour} {
		if n, ok := strings.CutSuffix(s, suf); ok {
			if f, err := strconv.ParseFloat(n, 64); err == nil && f > 0 {
				return time.Duration(f * float64(unit)), nil
			}
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%q is not a time: try 2h, 3d or 1w", s)
	}
	return d, nil
}

// until is when a snooze of d from now ends, as the picker shows it.
func until(now time.Time, d time.Duration) string {
	t := now.Add(d)
	switch {
	case t.YearDay() == now.YearDay() && t.Year() == now.Year():
		return t.Format("15:04")
	case d < 7*24*time.Hour:
		return t.Format("Mon 15:04")
	}
	return t.Format("Mon, Jan 2")
}

// snoozePicker is the durations with when each ends (a click or its digit
// snoozes), a typed one read as you type, Cancel, and Snooze for the typed
// one or the highlighted one; done gets the pick, 0 for Cancel.
func (u *ui) snoozePicker(gtx layout.Context, id string, done func(time.Duration)) layout.Dimensions {
	k, now := u.k, time.Now()
	pick := time.Duration(-1)
	typed, terr := parseSnooze(u.snoozeEd.Text())
	custom := func() {
		if u.snoozeEd.Len() == 0 {
			pick = snoozeFor[u.snoozeSel].d
			return
		}
		if terr != nil {
			u.showToast(terr.Error(), true)
			return
		}
		pick = typed
		u.snoozeEd.SetText("")
	}
	for {
		ev, ok := u.snoozeEd.Update(gtx)
		if !ok {
			break
		}
		if _, ok := ev.(widget.SubmitEvent); ok {
			custom()
		}
	}
	if !gtx.Focused(&u.snoozeEd) && !u.typing(gtx) {
		var fs []event.Filter
		for i := range snoozeFor {
			fs = append(fs, key.Filter{Name: key.Name(fmt.Sprint(i + 1))})
		}
		for {
			ev, ok := gtx.Event(fs...)
			if !ok {
				break
			}
			if e, ok := ev.(key.Event); ok && e.State == key.Press {
				pick = snoozeFor[int(e.Name[0]-'1')].d
			}
		}
	}
	var rows []layout.FlexChild
	for i, s := range snoozeFor {
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			fill, when := kit.NRGBA0, kit.CText3
			if i == u.snoozeSel && u.snoozeEd.Len() == 0 {
				fill, when = kit.CSel, kit.CText2
			}
			d, ok := k.Button(gtx, id+s.name, fill, kit.CHover, kit.RCtl, layout.Inset{Top: 8, Bottom: 8, Left: 10, Right: 10}, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, k.LblFill(s.name, 13, kit.CText, kit.Sans)),
					layout.Rigid(k.Lbl(until(now, s.d), 12, when, kit.Sans)), layout.Rigid(kit.HGap(10)),
					layout.Rigid(k.KeyCap(fmt.Sprint(i+1), false)))
			})
			if ok {
				pick = s.d
			}
			return d
		}))
	}
	label := "Snooze " + snoozeFor[u.snoozeSel].name
	read, tone := "", kit.CText3
	switch {
	case u.snoozeEd.Len() > 0 && terr == nil:
		label, read, tone = "Snooze "+u.snoozeEd.Text(), "until "+now.Add(typed).Format("Mon, Jan 2 · 15:04"), kit.CGreen
	case u.snoozeEd.Len() > 0:
		label, read, tone = "Snooze", "try 2h, 3d or 1w", kit.CRedText
	}
	box := layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: 4, Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: 6, Left: 10, Right: 10}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(k.Caption("Or type a time")), layout.Rigid(kit.VGap(5)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								gtx.Constraints.Min.X, gtx.Constraints.Max.X = gtx.Dp(100), gtx.Dp(100)
								rim := kit.CBtnRim
								if gtx.Focused(&u.snoozeEd) {
									rim = kit.CAcc
								}
								return kit.Boxed(kit.CPane, rim, kit.RCtl, layout.Inset{Top: 6, Bottom: 6, Left: 10, Right: 10}, func(gtx layout.Context) layout.Dimensions {
									gtx.Constraints.Min.X = gtx.Constraints.Max.X
									return k.Editor(gtx, &u.snoozeEd, "3d", 13, kit.Mono, kit.CText)
								})(gtx)
							}),
							layout.Rigid(kit.HGap(10)),
							layout.Flexed(1, k.LblFill(read, 12, tone, kit.Sans)))
					}))
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: 12, Bottom: 10, Left: 10, Right: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: gtx.Constraints.Min} }),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						d, ok := k.KBtn(gtx, id+"cancel", kit.Ghost, "Cancel", "")
						if ok {
							pick = 0
						}
						return d
					}),
					layout.Rigid(kit.HGap(6)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						d, ok := k.KBtn(gtx, id+"custom", kit.Primary, label, "↵")
						if ok {
							custom()
						}
						return d
					}))
			})
		}))
	if pick >= 0 {
		done(pick)
	}
	return box
}

// snoozePopover floats the picker under an alert's row, right-aligned at
// x, over the rows below: it doesn't push them down.
func (u *ui) snoozePopover(gtx layout.Context, x, y int, name string, done func(time.Duration)) {
	k := u.k
	m := op.Record(gtx.Ops)
	pgtx := gtx
	pgtx.Constraints = layout.Constraints{Max: image.Pt(gtx.Dp(330), gtx.Dp(600))}
	pgtx.Constraints.Min.X = pgtx.Constraints.Max.X
	var d layout.Dimensions
	inner := op.Record(gtx.Ops)
	d = kit.Boxed(kit.CPanelCard, kit.CKeyRim, kit.RCard, layout.Inset{}, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: 12, Left: 14, Right: 14}.Layout(gtx, k.Title("Snooze until…", name+". desk hides it; Nagios isn't told."))
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: 4, Right: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return u.snoozePicker(gtx, "alPick", done)
				})
			}))
	})(pgtx)
	call := inner.Stop()
	op.Offset(image.Pt(x-d.Size.X, y)).Add(gtx.Ops)
	cl := clip.Rect{Max: d.Size}.Push(gtx.Ops) // presses on the popover stop here, not at the rows beneath
	event.Op(gtx.Ops, &u.snoozeSel)
	cl.Pop()
	call.Add(gtx.Ops)
	op.Defer(gtx.Ops, m.Stop())
}

// snoozeBar is offered above a card's composer when its Claude ends an
// alert's investigation leaving it as it is (DESK-SNOOZE).
func (u *ui) snoozeBar(gtx layout.Context, c *kanban.Card, why string) layout.Dimensions {
	k, id := u.k, c.ID
	return layout.Inset{Top: 6, Left: 12, Right: 12}.Layout(gtx, kit.Boxed(kit.CPanelCard, kit.CLine, kit.RCard, layout.Inset{Top: 9, Bottom: 10, Left: 12, Right: 12}, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(k.Lbl("Snooze the alert "+alertName(c.Alert)+"?", 13, kit.CText, kit.Semi(kit.Sans))),
			layout.Rigid(kit.VGap(3)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return k.Label(gtx, why, 12, kit.CText3, kit.Sans, 2) }),
			layout.Rigid(kit.VGap(8)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return u.snoozePicker(gtx, "snz"+id, func(d time.Duration) {
					delete(u.snoozeAsk, id)
					if d > 0 {
						u.snooze(c.Alert, d)
					}
					u.tkGen++
				})
			}))
	}))
}
