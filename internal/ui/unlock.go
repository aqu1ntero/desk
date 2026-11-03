package ui

import (
	"context"
	"errors"
	"image"
	"image/color"
	"time"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/widget"

	"github.com/lfdominguez/desk/internal/ui/kit"
	"github.com/lfdominguez/desk/internal/vault"
)

// unlockDialog asks for what a vault needs to open: its password, when
// desk.toml doesn't say where it comes from, and a one-time code, when its
// login asks for one (Passbolt's TOTP). Neither is kept; the unlocked vault
// stays open while desk runs.
type unlockDialog struct {
	le       *vault.LockedError
	pass     widget.Editor
	code     widget.Editor
	needPass bool // the password is asked (not configured)
	needCode bool // the server asked for a code
	err      string
	show     bool   // the password shows as typed
	busy     bool   // unlocking: the KDF and the login take a moment
	focus    any    // the field to focus on the next frame
	act      func() // a button's action, run after the dialog is drawn
}

// vaultLocked notes the vault as waiting, and asks unless the user put it
// off (Later); the toolbar's Unlock button asks again.
func (u *ui) vaultLocked(le *vault.LockedError) {
	k := vault.Key(le.Vault)
	if _, ok := u.locked[k]; ok {
		return
	}
	u.locked[k] = le
	if u.unlock == nil {
		u.askUnlock(le)
	}
}

func (u *ui) askUnlock(le *vault.LockedError) {
	d := &unlockDialog{le: le, needPass: !le.Code, needCode: le.Code}
	d.pass.SingleLine, d.pass.Submit = true, true // the mask follows Show; codeBoxes sets up the code's
	d.focus = &d.pass
	if d.needCode {
		d.focus = &d.code
	}
	u.unlock = d
}

// cancelUnlock puts the vault off: its links say locked until the toolbar's
// Unlock button.
func (u *ui) cancelUnlock() { u.unlock, u.blur = nil, true }

func (u *ui) submitUnlock() {
	d := u.unlock
	pass, code := d.pass.Text(), d.code.Text()
	if d.busy || (d.needPass && pass == "") || (d.needCode && code == "") {
		return
	}
	if !d.needPass {
		pass = "" // the configured one
	}
	d.code.SetText("") // single use
	d.busy, d.err = true, ""
	v := d.le.Vault
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		err := vault.Unlock(ctx, v, pass, code)
		u.later(func() {
			d.busy = false
			var le *vault.LockedError
			switch {
			case errors.As(err, &le) && le.Code: // the password was right: now the code
				d.err = map[bool]string{true: "Wrong or expired code, try the next one", false: ""}[d.needCode]
				d.needCode, d.focus = true, &d.code
				return
			case err != nil:
				d.err = firstLine(err.Error())
				d.pass.SetText("")
				return
			}
			d.pass.SetText("")
			delete(u.locked, vault.Key(v))
			if u.unlock == d {
				u.cancelUnlock()
			}
			u.showToast("Unlocked "+v.Name, false)
			ids := u.linkedIDs()
			go func() { u.checkLinks(ids); u.checkTickets() }()
		})
	}()
}

// lockedVault is a vault still waiting, for the toolbar.
func (u *ui) lockedVault() (*vault.LockedError, bool) {
	for _, le := range u.locked {
		return le, true
	}
	return nil, false
}

func (u *ui) unlockDialog(gtx layout.Context) {
	d := u.unlock
	if d == nil {
		return
	}
	for _, e := range []*widget.Editor{&d.pass, &d.code} {
		for {
			ev, ok := e.Update(gtx)
			if !ok {
				break
			}
			if _, ok := ev.(widget.SubmitEvent); ok {
				if e == &d.pass && d.needCode && d.code.Len() == 0 {
					d.focus = &d.code // enter in the password: on to the code
					continue
				}
				u.submitUnlock()
			}
		}
	}
	if d.focus != nil {
		gtx.Execute(key.FocusCmd{Tag: d.focus})
		d.focus = nil
	}
	k, v := u.k, d.le.Vault
	what := map[string]string{"passbolt": "Passbolt key passphrase"}[v.Kind]
	if what == "" {
		what = "Master password"
	}
	kind := map[string]string{"passbolt": "Passbolt"}[v.Kind]
	if kind == "" {
		kind = "Bitwarden"
	}
	who := v.Email
	if who == "" {
		who = v.URL
	}
	if who == "" {
		who = v.ConfigFile
	}
	d.pass.Mask = map[bool]rune{false: '•'}[d.show]
	k.Modal(gtx, d, 120, 460, nil, func(gtx layout.Context) layout.Dimensions {
		body := func(gtx layout.Context) layout.Dimensions {
			kids := []layout.FlexChild{layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Start}.Layout(gtx,
					layout.Rigid(iconTile(kit.IcoLock, kit.CText, kit.CPanelCard)),
					layout.Rigid(kit.HGap(12)),
					layout.Flexed(1, k.Title("Unlock "+v.Name, kind+" · "+who+"\nA link's token is in this vault")))
			})}
			if d.needPass {
				kids = append(kids, layout.Rigid(kit.VGap(18)), layout.Rigid(k.Caption(what)), layout.Rigid(kit.VGap(6)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Flexed(1, k.Field(&d.pass, "", 0)),
							layout.Rigid(kit.HGap(6)),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								label := map[bool]string{false: "Show", true: "Hide"}[d.show]
								if _, ok := k.KBtn(gtx, "ulShow", kit.Ghost, label, ""); ok {
									d.show = !d.show
								}
								return layout.Dimensions{Size: image.Pt(gtx.Dp(60), gtx.Dp(32))}
							}))
					}))
			}
			if d.needCode {
				kids = append(kids, layout.Rigid(kit.VGap(16)), layout.Rigid(k.Caption("One-time code")), layout.Rigid(kit.VGap(6)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return u.codeBoxes(gtx, &d.code, d.err != "") }))
			}
			if d.err != "" {
				kids = append(kids, layout.Rigid(kit.VGap(8)), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(kit.Icon(13, kit.CRedText, kit.IcoWarn)), layout.Rigid(kit.HGap(6)),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return k.Label(gtx, d.err, 12, kit.CRedText, kit.Sans, 3) }))
				}))
			}
			kids = append(kids, layout.Rigid(kit.VGap(12)),
				layout.Rigid(k.Lbl("Kept in memory until desk quits; never written to disk.", 12, kit.CText3, kit.Sans)))
			return layout.Inset{Top: 20, Bottom: 18, Left: 20, Right: 20}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...)
			})
		}
		foot := func(gtx layout.Context) layout.Dimensions {
			return dialogFoot(gtx, func(gtx layout.Context) layout.Dimensions {
				ch := []layout.FlexChild{layout.Flexed(1, k.Lbl("Asked for a link of this organization", 12, kit.CText3, kit.Sans))}
				ch = append(ch, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					d0, ok := k.KBtn(gtx, "ulCancel", kit.Ghost, "Not now", "esc")
					if ok {
						d.act = u.cancelUnlock
					}
					return d0
				}), layout.Rigid(kit.HGap(8)), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if d.busy {
						return kit.Boxed(kit.CAcc, kit.NRGBA0, kit.RCtl, layout.Inset{Top: 8, Bottom: 8, Left: 12, Right: 12}, func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx, layout.Rigid(kit.Spinner(12, kit.CWhite)), layout.Rigid(kit.HGap(8)),
								layout.Rigid(k.Lbl("Unlocking…", 13, kit.CWhite, kit.Semi(kit.Sans))))
						})(gtx)
					}
					d0, ok := k.KBtn(gtx, "ulOK", kit.Primary, "Unlock", "↵")
					if ok {
						d.act = u.submitUnlock
					}
					return d0
				}))
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx, ch...)
			})
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, layout.Rigid(body), layout.Rigid(foot))
	})
	if act := d.act; act != nil { // after drawing: Not now clears u.unlock
		d.act = nil
		act()
	}
}

// iconTile is a dialog's glyph on a rounded tile.
func iconTile(ico kit.IconPath, fg, bg color.NRGBA) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		s := gtx.Dp(36)
		kit.FillRRect(gtx.Ops, image.Rectangle{Max: image.Pt(s, s)}, gtx.Dp(8), bg)
		defer op.Offset(image.Pt((s-gtx.Dp(18))/2, (s-gtx.Dp(18))/2)).Push(gtx.Ops).Pop()
		kit.Icon(18, fg, ico)(gtx)
		return layout.Dimensions{Size: image.Pt(s, s)}
	}
}

// dialogFoot is a dialog's footer: a line, then the row on a darker band.
func dialogFoot(gtx layout.Context, row layout.Widget) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			sz := image.Pt(gtx.Constraints.Max.X, gtx.Dp(1))
			kit.FillRRect(gtx.Ops, image.Rectangle{Max: sz}, 0, kit.CLine)
			return layout.Dimensions{Size: sz}
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Background{}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				kit.FillRRect(gtx.Ops, image.Rectangle{Max: gtx.Constraints.Min}, 0, kit.CFoot)
				return layout.Dimensions{Size: gtx.Constraints.Min}
			}, func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: 12, Bottom: 12, Left: 20, Right: 14}.Layout(gtx, row)
			})
		}))
}

// codeBoxes draws a one-time code as six boxes over e, which takes the
// typing (digits only) and the clicks: it is laid out under them, invisible.
func (u *ui) codeBoxes(gtx layout.Context, e *widget.Editor, bad bool) layout.Dimensions {
	e.SingleLine, e.Submit, e.MaxLen, e.Filter = true, true, 6, "0123456789"
	k, txt := u.k, []rune(e.Text())
	bw, bh, gap := gtx.Dp(40), gtx.Dp(44), gtx.Dp(8)
	dash := gtx.Dp(16)
	size := image.Pt(6*bw+5*gap+dash, bh)
	cgtx := gtx
	cgtx.Constraints = layout.Exact(size)
	clear := kit.Mat(gtx, kit.NRGBA0)
	e.Layout(cgtx, k.Sh, kit.Mono, 1, clear, clear)
	focused := gtx.Focused(e)
	x := 0
	for i := range 6 {
		if i == 3 {
			st := op.Offset(image.Pt(x, (bh-gtx.Dp(18))/2)).Push(gtx.Ops)
			lg := gtx
			lg.Constraints = layout.Exact(image.Pt(dash, gtx.Dp(18)))
			layout.Center.Layout(lg, k.Lbl("–", 14, kit.CText3, kit.Mono))
			st.Pop()
			x += dash
		}
		rim := kit.CBtnRim
		switch {
		case bad:
			rim = kit.CRed
		case focused && (i == len(txt) || i == 5 && len(txt) == 6):
			rim = kit.CAcc
		}
		r := image.Rect(x, 0, x+bw, bh)
		kit.Panel(gtx, r, kit.RCtl, kit.CPane, rim)
		if i < len(txt) {
			st := op.Offset(r.Min).Push(gtx.Ops)
			lg := gtx
			lg.Constraints = layout.Exact(r.Size())
			layout.Center.Layout(lg, k.Lbl(string(txt[i]), 18, kit.CText, kit.Medium(kit.Mono)))
			st.Pop()
		}
		x += bw + gap
	}
	return layout.Dimensions{Size: size}
}
