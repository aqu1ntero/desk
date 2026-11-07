package ui

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"gioui.org/io/key"

	"github.com/lfdominguez/desk/internal/claude"
)

// TestSignInAgain: Improve's error shows whole; an expired login offers to
// sign in, which runs in the dialog and, signed in, improves again; a failed
// sign-in says so, and closing the dialog ends one.
func TestSignInAgain(t *testing.T) {
	h := newHarness(t)
	h.orgToml("Acme", "")
	runs := 0
	optimize = func(context.Context, claude.Ask, func(claude.Progress)) (claude.Draft, error) {
		runs++
		return claude.Draft{}, errors.New("Failed to authenticate: OAuth session expired and could not be refreshed. Please run /login")
	}
	var account string
	script, own := []string{"exit:0"}, false
	loginCmd = func(_, acct string) (*exec.Cmd, bool, error) {
		account = acct
		return prog(script...), own, nil
	}
	defer func() { loginCmd = claude.Login }()
	h.u.newCard(0)
	s := h.u.edit
	s.org.SetText("Acme")
	s.notes.SetText("Fix the login")
	h.u.optimizeCard()
	h.pumpUntil("the error", func() bool { return s.optStop == nil })
	if !strings.Contains(s.optErr, "Please run /login") || !strings.HasPrefix(s.optNote, "⚠") {
		t.Fatalf("error %q, note %q", s.optErr, s.optNote)
	}
	h.clickButton("optSignIn")
	if account != "Acme" || h.u.login != nil && h.u.login.s != s { // `exit 0` may have signed in already (macOS is quick)
		t.Fatalf("no sign-in: %+v for %q", h.u.login, account)
	}
	h.pumpUntil("signed in", func() bool { return h.u.login == nil })
	h.pumpUntil("improving again", func() bool { return runs == 2 && s.optStop == nil })
	if !strings.HasPrefix(h.u.toast, "Signed in: ") || h.u.toastBad {
		t.Fatalf("toast %q (bad %v)", h.u.toast, h.u.toastBad)
	}

	script = []string{"exit:3"}
	h.u.startSignIn(s, "Acme", func() { t.Fatal("a failed sign-in improved again") })
	h.pumpUntil("the failure", func() bool { return h.u.login == nil })
	if h.u.toast != "Sign-in didn't finish: try again" || !h.u.toastBad {
		t.Fatalf("toast %q (bad %v)", h.u.toast, h.u.toastBad)
	}

	script = []string{"out:Paste code here\n", "read"} // waits for the code
	h.u.startSignIn(s, "Acme", func() { t.Fatal("an ended sign-in improved again") })
	tt := h.u.login.tt
	g := h.u.termGen.Load()
	h.pumpUntil("its output redrawing", func() bool { return h.u.termGen.Load() != g })
	h.frame() // the dialog draws its terminal and the code field, which takes the keyboard
	h.frame()
	if tt.cols == 0 || !h.r.Source().Focused(&h.u.login.code) {
		t.Fatalf("the sign-in's terminal: %d columns, code field focused %v", tt.cols, h.r.Source().Focused(&h.u.login.code))
	}
	h.r.Source().Execute(key.FocusCmd{Tag: tt}) // a click on the terminal keeps it
	h.frame()
	h.frame()
	if !h.r.Source().Focused(tt) {
		t.Fatal("the code field took the keyboard back from the terminal")
	}
	h.u.closeSheet()
	h.frame()
	if h.u.login != nil {
		t.Fatal("closing the dialog left the sign-in running")
	}
	h.pumpUntil("its process gone", func() bool { done, _ := tt.t.Exited(); return done })

	// an org's own account: no browser, the link to copy, kept on one row however long
	h.u.newCard(0)
	s = h.u.edit
	s.org.SetText("Acme")
	s.optErr = "OAuth session expired"
	long := "https://claude.com/cai/oauth/authorize?code=true&state=" + strings.Repeat("x", 300)
	script, own = []string{"out:Opening browser to sign in…\nIf the browser didn't open, visit: " + long + "\n", "out:Paste code here if prompted > ", "read"}, true
	h.u.startSignIn(s, "Acme", func() {})
	h.pumpUntil("the link", func() bool { h.frame(); return h.u.login != nil && h.u.login.url != "" })
	if h.u.login.url != long || h.u.login.tt.cols != loginCols {
		t.Fatalf("link %q, %d columns", h.u.login.url, h.u.login.tt.cols)
	}
	h.clickButton("loginCopy")
	if !strings.HasPrefix(h.u.toast, "Sign-in link copied") || h.u.toastBad {
		t.Fatalf("toast %q", h.u.toast)
	}
	// the code goes to claude whole, with its enter; a wrong one says why
	script = []string{"out:Paste code here if prompted > ", "code:CODE"}
	h.u.login.tt.t.Close() // the link's sign-in, replaced
	signed := false
	h.u.startSignIn(s, "Acme", func() { signed = true })
	h.frame()
	h.clickButton("loginSend") // empty: nothing sent
	h.u.login.code.SetText(" CODE ")
	h.clickButton("loginSend")
	h.pumpUntil("the right code", func() bool { return h.u.login == nil })
	if !signed || h.u.toastBad {
		t.Fatalf("signed %v, toast %q", signed, h.u.toast)
	}
	h.u.startSignIn(s, "Acme", func() { t.Fatal("a wrong code signed in") })
	h.frame()
	h.send(key.EditEvent{Text: "WRONG"}, press(key.NameReturn, 0)) // typed in the field, which has the keyboard
	h.pumpUntil("the wrong code", func() bool { return h.u.login == nil })
	if h.u.toast != "Sign-in didn't finish: Login failed: That code wasn't accepted." || !h.u.toastBad {
		t.Fatalf("toast %q", h.u.toast)
	}
	h.u.closeSheet()
	h.frame()

	h.u.newCard(0)
	loginCmd = func(string, string) (*exec.Cmd, bool, error) { return nil, false, errors.New("no claude") }
	h.u.startSignIn(h.u.edit, "Acme", func() {})
	if h.u.login != nil || h.u.toast != "Sign in: no claude" || !h.u.toastBad {
		t.Fatalf("login %+v, toast %q", h.u.login, h.u.toast)
	}
	loginCmd = func(string, string) (*exec.Cmd, bool, error) { return exec.Command("/nonexistent/claude"), false, nil }
	h.u.startSignIn(h.u.edit, "Acme", func() {})
	if h.u.login != nil || !strings.HasPrefix(h.u.toast, "Sign in: ") {
		t.Fatalf("login %+v, toast %q", h.u.login, h.u.toast)
	}
}
