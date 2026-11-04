package vt

// desk patch: tests of the soft-wrap mark and the reflow (reflow.go).

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

func text(l uv.Line) string {
	var s strings.Builder
	for _, c := range l {
		if c.Width > 0 {
			s.WriteString(c.Content)
		}
	}
	return strings.TrimRight(s.String(), " ")
}

// shown is the screen's rows, trailing blanks trimmed, then the cursor.
func shown(e *Emulator) (rows []string, cur uv.Position) {
	for y := range e.Height() {
		rows = append(rows, text(e.scr.buf.Line(y)))
	}
	return rows, e.CursorPosition()
}

func history(e *Emulator) (rows []string) {
	for _, l := range e.Scrollback().Lines() {
		rows = append(rows, text(l))
	}
	return rows
}

func eq(a, b []string) bool { return strings.Join(a, "|") == strings.Join(b, "|") }

func newEm(w, h int, s string) *Emulator {
	e := NewEmulator(w, h)
	e.WriteString(s)
	return e
}

// TestReflowJoinsWider: a line autowrap split joins again on a wider screen,
// its colors kept, and the prompt follows it up.
func TestReflowJoinsWider(t *testing.T) {
	e := newEm(10, 4, "\x1b[31mabcdefghij\x1b[0mKLMNO\r\n$ ")
	if !e.Wrapped(0) || e.Wrapped(1) || e.Wrapped(2) {
		t.Fatalf("marks %v", e.scr.wrap)
	}
	if mv := e.ResizeMap(20, 4); mv == nil {
		t.Error("a reflow tells where lines went")
	}
	rows, cur := shown(e)
	if !eq(rows, []string{"abcdefghijKLMNO", "$", "", ""}) || cur != uv.Pos(2, 1) {
		t.Errorf("rows %q cursor %v", rows, cur)
	}
	if e.Wrapped(0) || e.CellAt(9, 0).Style.Fg == nil || e.CellAt(10, 0).Style.Fg != nil {
		t.Errorf("mark %v, styles %v %v", e.Wrapped(0), e.CellAt(9, 0).Style, e.CellAt(10, 0).Style)
	}
}

// TestReflowWrapsNarrower: a long line wraps at the narrower width, marked.
func TestReflowWrapsNarrower(t *testing.T) {
	e := newEm(20, 4, "abcdefghijKLMNO\r\n$ ")
	e.Resize(10, 4)
	rows, cur := shown(e)
	if !eq(rows, []string{"abcdefghij", "KLMNO", "$", ""}) || cur != uv.Pos(2, 2) {
		t.Errorf("rows %q cursor %v", rows, cur)
	}
	if !e.Wrapped(0) || e.Wrapped(1) {
		t.Errorf("marks %v", e.scr.wrap)
	}
}

// TestReflowKeepsNewlines: a row exactly as wide as the screen that ended in
// a newline is no soft wrap: it never joins the next.
func TestReflowKeepsNewlines(t *testing.T) {
	e := newEm(10, 3, "abcdefghij\r\nKLMNO")
	if e.Wrapped(0) {
		t.Fatal("a newline marked as a wrap")
	}
	e.Resize(20, 3)
	if rows, _ := shown(e); !eq(rows, []string{"abcdefghij", "KLMNO", ""}) {
		t.Errorf("rows %q", rows)
	}
}

// TestReflowScrollback: the scrollback reflows too, the prompt kept on its
// screen row; lines past the scrollback's size drop from the top.
func TestReflowScrollback(t *testing.T) {
	e := newEm(10, 2, "0123456789ABCDE\r\nx\r\ny\r\n$ ")
	if h := history(e); !eq(h, []string{"0123456789", "ABCDE", "x"}) || !e.ScrollbackWrapped(0) || e.ScrollbackWrapped(1) {
		t.Fatalf("history %q", h)
	}
	e.Resize(20, 2)
	rows, cur := shown(e)
	if h := history(e); !eq(h, []string{"0123456789ABCDE", "x"}) || e.ScrollbackWrapped(0) {
		t.Errorf("history %q", h)
	}
	if !eq(rows, []string{"y", "$"}) || cur != uv.Pos(2, 1) {
		t.Errorf("rows %q cursor %v", rows, cur)
	}
	e.SetScrollbackSize(3)
	mv := e.ResizeMap(5, 2)
	if h := history(e); !eq(h, []string{"56789", "ABCDE", "x"}) || !e.ScrollbackWrapped(0) || e.ScrollbackWrapped(1) {
		t.Errorf("capped history %q", h)
	}
	if rows, _ := shown(e); !eq(rows, []string{"y", "$"}) {
		t.Errorf("capped rows %q", rows)
	}
	if l, x := mv(0, 12); l != 1 || x != 2 { // "C", in a line whose first row dropped
		t.Errorf("C at %d,%d", l, x)
	}
	if l, x := mv(3, 1); l != 4 || x != 1 { // the cursor's row, then the one past the text
		t.Errorf("cursor row at %d,%d", l, x)
	}
	if l, x := mv(4, 9); l != 5 || x != 4 {
		t.Errorf("row past the text at %d,%d", l, x)
	}
	if l, x := mv(3, 9); l != 4 || x != 4 { // past the text: the row's last column
		t.Errorf("past the prompt at %d,%d", l, x)
	}
	if l, x := mv(-1, 3); l != -1 || x != 3 {
		t.Errorf("before the history: %d,%d", l, x)
	}
	if e.ResizeMap(5, 3) != nil {
		t.Error("a height change moves no line")
	}
}

// TestReflowPromptRow: the cursor's line keeps its screen row: a wider screen
// pulls lines back from the scrollback, a narrower one pushes them there.
func TestReflowPromptRow(t *testing.T) {
	e := newEm(10, 4, "1\r\n2\r\n3\r\nabcdefghijKLM")
	e.Resize(20, 4)
	rows, cur := shown(e)
	if !eq(rows, []string{"1", "2", "3", "abcdefghijKLM"}) || cur != uv.Pos(13, 3) || e.ScrollbackLen() != 0 {
		t.Errorf("wider: rows %q cursor %v history %q", rows, cur, history(e))
	}
	e.Resize(5, 3)
	rows, cur = shown(e)
	if !eq(rows, []string{"abcde", "fghij", "KLM"}) || cur != uv.Pos(3, 2) || !eq(history(e), []string{"1", "2", "3"}) {
		t.Errorf("narrower: rows %q cursor %v history %q", rows, cur, history(e))
	}
	e = newEm(10, 4, "abcdefghijKLM\x1b[1;1H")
	e.Resize(5, 4) // the cursor on the line's first row: it stays on top
	if rows, cur := shown(e); !eq(rows, []string{"abcde", "fghij", "KLM", ""}) || cur != uv.Pos(0, 0) {
		t.Errorf("cursor on top: rows %q cursor %v", rows, cur)
	}
}

// TestReflowCursor: the cursor keeps its place in its line, a pending wrap
// (past the last column) included; saved cursors move along.
func TestReflowCursor(t *testing.T) {
	e := newEm(10, 3, "abcdefghijKLMNO\x1b[2;3H\x1b7")
	e.Resize(20, 3)
	if _, cur := shown(e); cur != uv.Pos(12, 0) || e.scr.saved.Position != uv.Pos(12, 0) {
		t.Errorf("cursor %v saved %v", cur, e.scr.saved.Position)
	}
	e = newEm(10, 3, "abcdefghij")
	e.Resize(5, 3)
	if _, cur := shown(e); cur != uv.Pos(4, 1) || !e.atPhantom {
		t.Errorf("pending wrap: cursor %v phantom %v", cur, e.atPhantom)
	}
	e.WriteString("k")
	e.Resize(20, 3)
	if rows, cur := shown(e); rows[0] != "abcdefghijk" || cur != uv.Pos(11, 0) || e.atPhantom {
		t.Errorf("rows %q cursor %v", rows, cur)
	}
	e = newEm(10, 3, "$ ")
	e.Resize(2, 3) // the prompt's blank stays, the cursor waits past it
	if _, cur := shown(e); cur != uv.Pos(1, 0) || !e.atPhantom {
		t.Errorf("past a full prompt: cursor %v phantom %v", cur, e.atPhantom)
	}
}

// TestReflowEdges: text below the cursor stays, a cursor on a blank row keeps
// its line, a bottom row that still wraps ends its line, a taller screen keeps its top,
// and saved cursors land on their line's new row, clamped to the screen.
func TestReflowEdges(t *testing.T) {
	for _, tc := range []struct {
		in       string
		oh, w, h int // the old height, the new size
		rows     []string
		cur      uv.Position
		saved    uv.Position
	}{
		{"abc\r\n0123456789\x1b[1;1H", 2, 20, 2, []string{"abc", "0123456789"}, uv.Pos(0, 0), uv.Pos(0, 0)},
		{"abc\r\n", 3, 20, 3, []string{"abc", "", ""}, uv.Pos(0, 1), uv.Pos(0, 0)},
		{"0123456789A\x1b[1;1H\x1b[L", 2, 20, 2, []string{"", "0123456789"}, uv.Pos(0, 0), uv.Pos(0, 0)}, // the bottom row wraps into nothing
		{"abc\r\nxyz\x1b[1;5H", 3, 3, 3, []string{"abc", "", "xyz"}, uv.Pos(1, 1), uv.Pos(0, 0)},
		{"$ ", 3, 1, 3, []string{"$", "", ""}, uv.Pos(0, 1), uv.Pos(0, 0)},
		{"a\r\nb\r\nc\r\nd\x1b7", 2, 20, 4, []string{"c", "d", "", ""}, uv.Pos(1, 1), uv.Pos(1, 1)},
		{"0123456789ABCDEFGHIJ\r\nz\x1b7\x1b[1;1H", 3, 5, 3, []string{"01234", "56789", "ABCDE"}, uv.Pos(0, 0), uv.Pos(1, 2)},
	} {
		e := newEm(10, tc.oh, tc.in)
		e.Resize(tc.w, tc.h)
		if rows, cur := shown(e); !eq(rows, tc.rows) || cur != tc.cur || e.scr.saved.Position != tc.saved {
			t.Errorf("%q: rows %q cursor %v saved %v", tc.in, rows, cur, e.scr.saved.Position)
		}
	}
	// Lines dropped off a full scrollback move the saved cursor up with the rest.
	e := newEm(10, 3, "0123456789ABCDE\r\nx\r\ny\r\nz\x1b[2;1H\x1b7\x1b[3;2H")
	e.SetScrollbackSize(2)
	e.Resize(5, 3)
	if rows, cur := shown(e); !eq(rows, []string{"x", "y", "z"}) || cur != uv.Pos(1, 2) || e.scr.saved.Position != uv.Pos(0, 1) || e.ScrollbackLen() != 2 {
		t.Errorf("capped: rows %q cursor %v saved %v", rows, cur, e.scr.saved.Position)
	}
	// Under the alt screen, the main cursor past a full row stays on it.
	e = newEm(10, 3, "$ \x1b[?1049h")
	e.Resize(2, 3)
	if c := e.scrs[0].cur.Position; c != uv.Pos(1, 0) {
		t.Errorf("main cursor %v", c)
	}
}

// TestResizeNoReflow: the resizes that don't reflow keep upstream's ways and
// the shrink patch's: a shorter main screen scrolls its top into the
// scrollback, a shorter alt screen loses its bottom, a pending wrap on a
// wider alt screen moves on; a reflowed cursor's pending wrap is only the
// main screen's.
func TestResizeNoReflow(t *testing.T) {
	e := newEm(10, 3, "1\r\n2\r\n3")
	e.Resize(10, 2)
	if rows, cur := shown(e); !eq(rows, []string{"2", "3"}) || cur != uv.Pos(1, 1) || !eq(history(e), []string{"1"}) {
		t.Errorf("main: rows %q cursor %v", rows, cur)
	}
	e = newEm(10, 3, "\x1b[?1049h1\r\n2\r\n3")
	e.Resize(10, 2)
	if rows, cur := shown(e); !eq(rows, []string{"1", "2"}) || cur != uv.Pos(1, 1) {
		t.Errorf("alt: rows %q cursor %v", rows, cur)
	}
	e = newEm(10, 3, "\x1b[?1049h0123456789")
	e.Resize(20, 4)
	if _, cur := shown(e); cur != uv.Pos(10, 0) || e.atPhantom {
		t.Errorf("alt wider: cursor %v phantom %v", cur, e.atPhantom)
	}
	e = newEm(10, 3, "abcdefghij")
	e.Resize(20, 3)
	if _, cur := shown(e); cur != uv.Pos(10, 0) || e.atPhantom {
		t.Errorf("main wider: cursor %v phantom %v", cur, e.atPhantom)
	}
	e = newEm(10, 3, "$ \x1b[?1049h")
	e.Resize(2, 3) // the main cursor ends past its row; the alt one doesn't
	if e.atPhantom {
		t.Error("the alt screen got the main one's pending wrap")
	}
}

// TestReflowAltScreen: the alt screen is only cut (its program redraws it);
// the main screen under it reflows all the same.
func TestReflowAltScreen(t *testing.T) {
	e := newEm(10, 3, "abcdefghijKLMNO\x1b[?1049h0123456789ab")
	e.Resize(20, 3)
	if rows, _ := shown(e); !eq(rows, []string{"0123456789", "ab", ""}) || e.Wrapped(0) {
		t.Errorf("alt rows %q", rows)
	}
	e.WriteString("\x1b[?1049l")
	if rows, _ := shown(e); !eq(rows, []string{"abcdefghijKLMNO", "", ""}) {
		t.Errorf("main rows %q", rows)
	}
}

// TestReflowWide: a wide character never splits across rows, at print time
// nor in a reflow, and the padding it leaves joins back to nothing.
func TestReflowWide(t *testing.T) {
	e := newEm(5, 3, "abcd漢")
	if rows, _ := shown(e); !eq(rows, []string{"abcd", "漢", ""}) || !e.Wrapped(0) || !e.CellAt(4, 0).IsZero() {
		t.Errorf("print: rows %q", rows)
	}
	e = newEm(6, 3, "ab漢字cd")
	e.Resize(5, 3)
	if rows, _ := shown(e); !eq(rows, []string{"ab漢", "字cd", ""}) || !e.CellAt(4, 0).IsZero() || e.CellAt(2, 0).Width != 2 {
		t.Errorf("narrower: rows %q", rows)
	}
	e.Resize(6, 3)
	if rows, _ := shown(e); !eq(rows, []string{"ab漢字", "cd", ""}) {
		t.Errorf("back: rows %q", rows)
	}
	e = newEm(3, 3, "漢")
	e.Resize(1, 3) // no room at all: the character is cut, nothing hangs
	if rows, cur := shown(e); rows[0] != "" || cur.X != 0 {
		t.Errorf("width 1: rows %q cursor %v", rows, cur)
	}
}

// TestWrapMarkErase: erasing to a row's end, or all of it, drops its mark;
// erasing its start doesn't.
func TestWrapMarkErase(t *testing.T) {
	for _, tc := range []struct {
		seq  string
		want bool
	}{{"\x1b[1;5H\x1b[K", false}, {"\x1b[1;5H\x1b[1K", true}, {"\x1b[2K", false}, {"\x1b[J", false}, {"\x1b[H\x1b[3J", false}, {"\x1bc", false}} {
		e := newEm(10, 3, "abcdefghijKL\x1b[1;1H"+tc.seq)
		if e.Wrapped(0) != tc.want {
			t.Errorf("%q: mark %v", tc.seq, e.Wrapped(0))
		}
	}
	e := newEm(10, 3, "abcdefghijKL\x1b[2J")
	if !e.ScrollbackWrapped(0) || e.ScrollbackWrapped(1) || e.Wrapped(0) {
		t.Errorf("ED 2 keeps the marks in the history: %v %v", e.Scrollback().wrap, e.scr.wrap)
	}
}

// TestWrapMarkScroll: marks move with their rows on IL, DL and scrolling;
// rows shifted inside side margins lose theirs.
func TestWrapMarkScroll(t *testing.T) {
	e := newEm(10, 4, "abcdefghijKL\x1b[1;1H\x1b[L")
	if e.Wrapped(0) || !e.Wrapped(1) {
		t.Errorf("IL: %v", e.scr.wrap)
	}
	e.WriteString("\x1b[M")
	if !e.Wrapped(0) || e.Wrapped(1) {
		t.Errorf("DL: %v", e.scr.wrap)
	}
	e.WriteString("\x1b[2T")
	if !e.Wrapped(2) || e.Wrapped(0) {
		t.Errorf("SD: %v", e.scr.wrap)
	}
	e.WriteString("\x1b[1;1H\x1b[9L") // more than the screen holds
	if e.scr.wrap[2] || len(e.scr.wrap) != 4 {
		t.Errorf("IL 9: %v", e.scr.wrap)
	}
	e = newEm(10, 4, "abcdefghij0123456789KL\x1b[1;1H\x1b[9M")
	if e.Wrapped(0) || !eq(history(e), []string{"abcdefghij", "0123456789", "KL", ""}) || !e.ScrollbackWrapped(1) || e.ScrollbackWrapped(2) {
		t.Errorf("DL 9: %v, history %q %v", e.scr.wrap, history(e), e.Scrollback().wrap)
	}
	e = newEm(10, 4, "abcdefghijKL\x1b[?69h\x1b[2;5s\x1b[1;2H\x1b[L")
	if e.Wrapped(0) || e.Wrapped(1) {
		t.Errorf("IL in margins: %v", e.scr.wrap)
	}
	e = newEm(10, 4, "abcdefghij0123456789KL\x1b[?69h\x1b[1;5s\x1b[1;2H\x1b[M")
	if e.Wrapped(0) {
		t.Errorf("DL in margins: %v", e.scr.wrap)
	}
}

// TestScrollbackWrapMarks: the history keeps a wrapped row's trailing
// blanks, trims an ended one's, and drops marks with their lines.
func TestScrollbackWrapMarks(t *testing.T) {
	s := NewScrollback(2)
	s.push(uv.Line{{Content: "a", Width: 1}, uv.EmptyCell}, true)
	s.Push(uv.Line{{Content: "b", Width: 1}, uv.EmptyCell})
	if len(s.Line(0)) != 2 || len(s.Line(1)) != 1 || !s.Wrapped(0) || s.Wrapped(1) || s.Wrapped(2) || s.Wrapped(-1) {
		t.Errorf("lines %v marks %v", s.lines, s.wrap)
	}
	s.push(uv.Line{{Content: "c", Width: 1}}, true)
	if s.Wrapped(0) || !s.Wrapped(1) || len(s.wrap) != 2 {
		t.Errorf("full: marks %v", s.wrap)
	}
	s.SetMaxLines(1)
	if !s.Wrapped(0) || len(s.wrap) != 1 {
		t.Errorf("cut: marks %v", s.wrap)
	}
	s.Clear()
	if s.Wrapped(0) || len(s.wrap) != 0 {
		t.Errorf("cleared: marks %v", s.wrap)
	}
	var none *Scrollback
	if none.Wrapped(0) {
		t.Error("a nil history has no marks")
	}
	e := newEm(10, 2, "x\r\n0123456789AB")
	e.scrs[0].SetScrollback(nil)
	e.Resize(5, 2) // no history to keep: what goes above is lost
	if rows, cur := shown(e); !eq(rows, []string{"56789", "AB"}) || cur != uv.Pos(2, 1) {
		t.Errorf("no history: rows %q cursor %v", rows, cur)
	}
}
