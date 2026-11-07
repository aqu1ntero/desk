package ui

import (
	"cmp"
	"image"
	"io"
	"os/exec"
	"strings"
	"sync/atomic"

	"gioui.org/io/clipboard"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/widget"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/lfdominguez/desk/internal/claude"
	"github.com/lfdominguez/desk/internal/orgs"
	"github.com/lfdominguez/desk/internal/term"
	"github.com/lfdominguez/desk/internal/ui/kit"
)

// signIn is `claude auth login` for an account whose session expired, in a
// small terminal inside the dialog that hit it: it may ask for the code the
// sign-in page shows. An org's own account never opens a browser (own: see
// claude.Login); its URL shows to copy instead. Once signed in, then runs
// (the run that failed, again). One at a time; the dialog closing ends it.
type signIn struct {
	org   string
	s     *sheet
	tt    *termTab
	own   bool
	url   string // the sign-in URL, once the terminal printed it
	then  func()
	code  widget.Editor // the page's code: claude reads it without echo, so it's typed here and sent whole
	typed bool          // the code field took the keyboard once
}

// loginCmd starts the sign-in; tests replace it.
var loginCmd = claude.Login

// loginCols is how wide the sign-in's terminal is kept, so its long URL
// prints on one row to read back (the box shows its left part).
const loginCols = 1000

// startSignIn signs org's account in again from dialog s.
func (u *ui) startSignIn(s *sheet, org string, then func()) {
	cmd, own, err := loginCmd(u.home, orgs.Account(u.root, org))
	if err == nil {
		var tt *termTab
		if tt, err = u.dialogTerm(cmd, loginCols); err == nil {
			u.login = &signIn{org: org, s: s, tt: tt, own: own, then: then}
			u.login.code.SingleLine, u.login.code.Submit = true, true
			return
		}
	}
	u.showToast("Sign in: "+err.Error(), true)
}

// dialogTerm starts cmd in a terminal drawn inside a dialog, cols wide
// whatever the box: its output always redraws (the dialog shows it), and it
// takes the keyboard.
func (u *ui) dialogTerm(cmd *exec.Cmd, cols int) (*termTab, error) {
	var started atomic.Pointer[term.Term]
	t, err := startTerm(cmd, cols, 8, func() {
		if started.Load() != nil {
			u.termGen.Add(1)
			u.w.Invalidate()
		}
	})
	if err != nil {
		return nil, err
	}
	started.Store(t)
	return &termTab{t: t, fixedCols: cols}, nil
}

// screenText is tt's screen as text, a line per row.
func screenText(tt *termTab) string {
	var b strings.Builder
	runs, _, _, _ := tt.t.Grid(0)
	for _, row := range runs {
		for _, r := range row {
			b.WriteString(r.Text)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// loginFrame ends the sign-in when its dialog closed, reads its URL once
// printed, and says how it went once claude exits: signed in, it runs what
// failed again.
func (u *ui) loginFrame() {
	l := u.login
	if l == nil {
		return
	}
	if u.edit != l.s {
		l.tt.t.Close()
		u.login = nil
		return
	}
	if l.url == "" {
		l.url = claude.LoginURL(screenText(l.tt))
	}
	done, code := l.tt.t.Exited()
	if !done {
		return
	}
	u.login = nil
	if code != 0 {
		u.showToast("Sign-in didn't finish: "+cmp.Or(loginWhy(screenText(l.tt)), "try again"), true)
		return
	}
	delete(u.limits, orgs.Account(u.root, l.org)) // the meters read the new login
	u.showToast("Signed in: "+u.accountName(l.org), false)
	l.then()
}

// loginWhy is what claude last said: its screen's last line, past the code
// prompt ("Paste code here if prompted > Login failed: …").
func loginWhy(screen string) string {
	last := strings.TrimSpace(screen)
	last = last[strings.LastIndexByte(last, '\n')+1:]
	if _, after, ok := strings.Cut(last, "> "); ok {
		last = after
	}
	return strings.TrimSpace(last)
}

// sendCode hands the pasted code to claude, with the enter it waits for.
func (l *signIn) sendCode() {
	c := strings.TrimSpace(l.code.Text())
	if c == "" {
		return
	}
	l.tt.t.Paste(c)
	l.tt.t.SendKey(uv.KeyPressEvent{Code: uv.KeyEnter})
	l.code.SetText("")
}

// signInBox is the running sign-in: for an org's own account, its URL to
// open where that account lives (with Copy); then its terminal, for the code.
func (u *ui) signInBox(gtx layout.Context, l *signIn) layout.Dimensions {
	k, name := u.k, u.accountName(l.org)
	ch := []layout.FlexChild{layout.Rigid(kit.VGap(10))}
	if l.own {
		ch = append(ch,
			layout.Rigid(k.Lbl("Open this link in the browser (or container) signed in to "+name+". desk doesn't open one: "+
				"your default browser may be signed in to another Claude account, and this one would sign in as that.", 12, kit.COrange, kit.Sans)),
			layout.Rigid(kit.VGap(6)))
		if l.url == "" {
			ch = append(ch, layout.Rigid(k.Lbl("Waiting for the link…", 12, kit.CText3, kit.Sans)))
		} else {
			ch = append(ch, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return u.selText(gtx, "loginURL", l.url, 12, kit.CAccText, kit.Mono, 3)
					}),
					layout.Rigid(kit.HGap(10)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						d, ok := k.KBtn(gtx, "loginCopy", kit.Secondary, "Copy link", "")
						if ok {
							gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(l.url))})
							u.showToast("Sign-in link copied: open it where "+name+" is signed in", false)
						}
						return d
					}))
			}))
		}
		ch = append(ch, layout.Rigid(kit.VGap(8)),
			layout.Rigid(k.Lbl("Then paste the code the page shows below and press Sign in", 12, kit.CText2, kit.Sans)))
	} else {
		ch = append(ch, layout.Rigid(k.Lbl("Signing in to "+name+": finish in the browser; if it shows a code, paste it below", 12, kit.CText2, kit.Sans)))
	}
	ch = append(ch, layout.Rigid(kit.VGap(6)), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		if !l.typed {
			l.typed = true
			gtx.Execute(key.FocusCmd{Tag: &l.code})
		}
		for {
			ev, ok := l.code.Update(gtx)
			if !ok {
				break
			}
			if _, ok := ev.(widget.SubmitEvent); ok {
				l.sendCode()
			}
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, k.Field(&l.code, "Paste the code here ("+kit.ModText("V")+")", 0)),
			layout.Rigid(kit.HGap(10)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				d, ok := k.KBtn(gtx, "loginSend", kit.Primary, "Sign in", "")
				if ok {
					l.sendCode()
				}
				return d
			}))
	}), layout.Rigid(kit.VGap(6)), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		sz := image.Pt(gtx.Constraints.Max.X, gtx.Dp(110))
		gtx.Constraints = layout.Exact(sz)
		kit.FillRRect(gtx.Ops, image.Rectangle{Max: sz}, gtx.Dp(kit.RCtl), kit.CPane)
		u.termGrid(gtx, l.tt)
		return layout.Dimensions{Size: sz}
	}))
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
}

// optErrRow is why Improve failed, whole and selectable; a login that
// expired offers to sign in again, then shows that sign-in.
func (u *ui) optErrRow(pad func(layout.Widget) layout.Widget) sheetRow {
	k, s := u.k, u.edit
	org := strings.TrimSpace(s.org.Text())
	return sheetRow{w: pad(func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Bottom: 14}.Layout(gtx, kit.Boxed(kit.CRedSoft, kit.Mix(kit.CRed, kit.CPanel, .6), kit.RCard, layout.Inset{Top: 10, Bottom: 10, Left: 12, Right: 12}, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			ch := []layout.FlexChild{
				layout.Rigid(k.Lbl("Improve with Claude failed", 12, kit.CRedText, kit.Semi(kit.Sans))),
				layout.Rigid(kit.VGap(6)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return u.selText(gtx, "optErr", s.optErr, 13, kit.CText, kit.Sans, 0)
				}),
			}
			switch l := u.login; {
			case l != nil && l.s == s:
				ch = append(ch, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return u.signInBox(gtx, l) }))
			case claude.AuthFailed(s.optErr):
				ch = append(ch, layout.Rigid(kit.VGap(10)), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					d, ok := k.KBtn(gtx, "optSignIn", kit.Primary, "Sign in to "+u.accountName(org)+" again", "")
					if ok {
						s.act = func() { u.startSignIn(s, org, u.optimizeCard) }
					}
					return d
				}))
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
		}))
	})}
}
