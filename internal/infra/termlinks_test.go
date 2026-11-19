package infra

import (
	"fmt"
	"image"
	"io"
	"strings"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/transfer"
	"gioui.org/layout"
)

func TestURLAt(t *testing.T) {
	for _, c := range []struct {
		line   string
		x      int
		lo, hi int
		url    string
	}{
		{"see https://example.com/a?b=1 now", 6, 4, 29, "https://example.com/a?b=1"},
		{"see https://example.com/a?b=1 now", 28, 4, 29, "https://example.com/a?b=1"},
		{"see https://example.com/a?b=1 now", 29, 0, 0, ""}, // the space after it
		{"see https://example.com/a?b=1 now", 2, 0, 0, ""},
		{"(at https://example.com/x).", 10, 4, 25, "https://example.com/x"},
		{"https://en.example.com/wiki/Go_(lang)", 0, 0, 37, "https://en.example.com/wiki/Go_(lang)"},
		{"[docs: https://example.com/d]", 9, 7, 28, "https://example.com/d"},
		{"«ñ» http://example.com/é, ok", 6, 4, 24, "http://example.com/é"},
		{`url="ftp://192.0.2.7/f"`, 7, 5, 22, "ftp://192.0.2.7/f"},
		{"a https://x.example.com b https://y.example.com", 30, 26, 47, "https://y.example.com"},
		{"no links here", 3, 0, 0, ""},
	} {
		lo, hi, url := urlAt([]rune(c.line), c.x)
		if lo != c.lo || hi != c.hi || url != c.url {
			t.Errorf("%q at %d: %d %d %q, want %d %d %q", c.line, c.x, lo, hi, url, c.lo, c.hi, c.url)
		}
	}
	if trimURL("https://example.com/!?.,;:") != "https://example.com/" || trimURL(".") != "" {
		t.Error("trailing punctuation")
	}
}

func TestPageKey(t *testing.T) {
	for _, c := range []struct {
		e    key.Event
		want int
	}{
		{key.Event{Name: key.NamePageUp, Modifiers: key.ModShift, State: key.Press}, 1},
		{key.Event{Name: key.NamePageDown, Modifiers: key.ModShift, State: key.Press}, -1},
		{key.Event{Name: key.NamePageUp, State: key.Press}, 0},
		{key.Event{Name: key.NamePageUp, Modifiers: key.ModShift | key.ModCtrl, State: key.Press}, 0},
		{key.Event{Name: key.NamePageUp, Modifiers: key.ModShift, State: key.Release}, 0},
		{key.Event{Name: key.NameUpArrow, Modifiers: key.ModShift, State: key.Press}, 0},
	} {
		if got := pageKey(c.e); got != c.want {
			t.Errorf("%v %v %v: %d, want %d", c.e.Name, c.e.Modifiers, c.e.State, got, c.want)
		}
	}
}

// TestGMTermConsoleKeys: what Konsole and Kitty do with the keys and the
// mouse: Shift+PgUp/PgDn page through the scrollback (not in a full-screen
// program), Shift+Insert pastes and Ctrl+Insert copies, a middle click
// pastes, and Ctrl over a URL underlines it, a Ctrl+click opens it.
func TestGMTermConsoleKeys(t *testing.T) {
	h := newGM(t)
	u := h.u
	h.size = image.Pt(1000, 700)
	st, ts, ss := gmTermState(t)
	h.set(st)
	u.org = "Acme"
	for i := range 60 {
		gmOut(ts[0], fmt.Sprintf("line %d\r\n", i))
	}
	gmOut(ts[0], "docs at https://example.com/runbook.\r\n$ ")
	var opened []string
	old := openLocal
	openLocal = func(p string) error { opened = append(opened, p); return nil }
	t.Cleanup(func() { openLocal = old })
	w := func(gtx layout.Context) { u.terminals(gtx) }
	h.draw(w)
	h.draw(w)
	v := u.terms[ts[0]]

	h.key(key.NamePageUp, key.ModShift)
	h.draw(w)
	if page := max(1, v.rows-1); v.scroll != page {
		t.Fatalf("Shift+PgUp: scroll %d, want a page (%d)", v.scroll, page)
	}
	h.key(key.NamePageUp, key.ModShift)
	h.key(key.NamePageDown, key.ModShift)
	h.draw(w)
	if v.scroll != max(1, v.rows-1) {
		t.Errorf("up, up, down: %d", v.scroll)
	}
	for range 50 {
		h.key(key.NamePageUp, key.ModShift)
	}
	h.draw(w)
	if v.scroll != ts[0].Scrollback() {
		t.Errorf("stops at the top: %d of %d", v.scroll, ts[0].Scrollback())
	}
	for range 50 {
		h.key(key.NamePageDown, key.ModShift)
	}
	h.draw(w)
	if v.scroll != 0 {
		t.Errorf("back to the live screen: %d", v.scroll)
	}
	if ss[0].String() != "" {
		t.Errorf("Shift+PgUp/PgDn aren't sent to the shell: %q", ss[0].String())
	}

	// Insert alone goes to the program
	h.key(key.NameInsert, 0)
	h.draw(w)
	ss[0].wait(t, "\x1b[2~")
	ss[0].reset()

	// Ctrl+Insert copies, Shift+Insert pastes
	v.sel = [2]image.Point{{0, 0}, {4, 0}}
	h.key(key.NameInsert, key.ModCtrl)
	h.draw(w)
	if _, b, ok := h.r.WriteClipboard(); !ok || string(b) != "line" {
		t.Errorf("Ctrl+Insert copied %q %v", b, ok)
	}
	h.key(key.NameInsert, key.ModShift)
	h.draw(w)
	if u.pasteWanted {
		t.Error("Shift+Insert asks for the clipboard")
	}
	h.r.Queue(transfer.DataEvent{Type: "application/text", Open: func() io.ReadCloser { return io.NopCloser(strings.NewReader("pasted")) }})
	h.draw(w)
	ss[0].wait(t, "pasted")
	ss[0].reset()

	// the row with the URL, and a point on it
	row := -1
	for k := range v.runs {
		if strings.Contains(string(v.line(k)), "https://") {
			row = k
		}
	}
	if row < 0 {
		t.Fatal("the URL's row isn't drawn")
	}
	if !strings.Contains(string(v.line(row)), "https://example.com/runbook") {
		t.Fatalf("row %q", string(v.line(row)))
	}
	// Ctrl over the URL: underlined (found by sweeping the row with Ctrl held)
	y := float32(v.rowY[row] + 4)
	inX := float32(-1)
	for x := float32(0); x < 1000 && inX < 0; x += 4 {
		h.r.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: f32.Pt(x, y), Modifiers: key.ModCtrl})
		h.draw(w)
		if v.link.url != "" {
			inX = x + 12 // well inside it
		}
	}
	if inX < 0 {
		t.Fatal("Ctrl over the row never found the URL")
	}

	// a middle click pastes, and selects nothing
	h.r.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonTertiary, Position: f32.Pt(inX, y)})
	h.r.Queue(pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: f32.Pt(inX, y)})
	h.draw(w)
	h.r.Queue(transfer.DataEvent{Type: "application/text", Open: func() io.ReadCloser { return io.NopCloser(strings.NewReader("middle")) }})
	h.draw(w)
	ss[0].wait(t, "middle")

	// Ctrl over the URL: underlined; without Ctrl: not
	h.r.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: f32.Pt(inX, y), Modifiers: key.ModCtrl})
	h.draw(w)
	if v.link.url != "https://example.com/runbook" || v.link.row != row {
		t.Fatalf("hover with Ctrl: %+v", v.link)
	}
	h.move(inX, y)
	h.draw(w)
	if v.link.url != "" {
		t.Error("no Ctrl, no link")
	}
	// a plain click on it selects, as before
	h.press(inX, y, 0)
	h.release(inX, y)
	h.draw(w)
	if len(opened) != 0 {
		t.Error("a click without Ctrl opens nothing")
	}
	// Ctrl+click opens it
	h.press(inX, y, key.ModCtrl)
	h.release(inX, y)
	h.draw(w)
	if len(opened) != 1 || opened[0] != "https://example.com/runbook" {
		t.Errorf("opened %q", opened)
	}
	// Ctrl+click off a URL selects as a click does
	h.press(inX, float32(v.rowY[0]+4), key.ModCtrl)
	h.release(inX, float32(v.rowY[0]+4))
	h.draw(w)
	if len(opened) != 1 {
		t.Error("no URL there")
	}

	// a full-screen program keeps Shift+PgUp
	gmOut(ts[0], "\x1b[?1049h")
	h.draw(w)
	ss[0].reset()
	h.key(key.NamePageUp, key.ModShift)
	h.draw(w)
	ss[0].wait(t, "\x1b[5;2~")
	if v.scroll != 0 {
		t.Error("no scrollback paging in a full-screen program")
	}
}
