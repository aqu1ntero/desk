package infra

import (
	"fmt"
	"testing"

	"github.com/charmbracelet/x/vt"
)

// TestShrinkKeepsPrompt: a shorter pane pushes its top lines into the
// scrollback and keeps the prompt on screen (third_party/vt's patch).
func TestShrinkKeepsPrompt(t *testing.T) {
	e := vt.NewEmulator(80, 24)
	for i := 1; i <= 44; i++ {
		fmt.Fprintf(e, "line %d\r\n", i)
	}
	e.Write([]byte("$ "))
	e.Resize(80, 14)
	row := func(y int) string {
		s := ""
		for x := 0; x < 8; x++ {
			if c := e.CellAt(x, y); c != nil && c.Content != " " {
				s += c.Content
			}
		}
		return s
	}
	if p := e.CursorPosition(); row(13) != "$" || row(12) != "line44" || p.X != 2 || p.Y != 13 {
		t.Errorf("bottom rows %q %q, cursor %v", row(12), row(13), p)
	}
	if e.ScrollbackLen() != 31 {
		t.Errorf("scrollback %d, want lines 1-31", e.ScrollbackLen())
	}
	e.Resize(80, 20) // growing back moves nothing
	if row(13) != "$" {
		t.Errorf("after growing: row 13 %q", row(13))
	}
}

// TestResizeReflowsBlocks: a width change rewraps the pane's text (vt's
// reflow) and its blocks and open marks move with their lines.
func TestResizeReflowsBlocks(t *testing.T) {
	tm := &Term{emu: vt.NewEmulator(10, 5)}
	tm.emu.WriteString("$ 01234567ABCD\r\nout\r\n$ ")
	tm.blocks = []Block{{N: 1, Start: 0, End: 3, PX: 2}}
	tm.b, tm.c = &pos{3, 2}, &pos{1, 3}
	tm.Resize(20, 5)
	if b := tm.blocks[0]; b.Start != 0 || b.End != 2 || b.PX != 2 {
		t.Errorf("block %+v", b)
	}
	if *tm.b != (pos{2, 2}) || *tm.c != (pos{0, 13}) {
		t.Errorf("marks %+v %+v", *tm.b, *tm.c)
	}
	if l := tm.line(0); l[:14] != "$ 01234567ABCD" {
		t.Errorf("line 0 %q", l)
	}
	tm.Resize(20, 4) // a height change moves nothing
	if *tm.b != (pos{2, 2}) {
		t.Errorf("after a height change %+v", *tm.b)
	}
}

// TestWrapMarks: Unwrapped and Keep join only rows autowrap split, blanks at
// the split included; a row ended by a newline stays one, full width or not.
func TestWrapMarks(t *testing.T) {
	tm := &Term{emu: vt.NewEmulator(10, 4)}
	tm.emu.WriteString("0123456789\r\nabcd      efg\r\nnext")
	if u := tm.Unwrapped(); u != "0123456789\nabcd      efg\nnext\n" {
		t.Errorf("unwrapped %q", u)
	}
	if k := tm.Keep().Text; k != "0123456789\nabcd      efg\nnext" {
		t.Errorf("kept %q", k)
	}
	tm = &Term{emu: vt.NewEmulator(10, 2)}
	tm.emu.WriteString("x\r\ny\r\n0123456789AB") // a wrapped row on screen under a scrollback
	if k := tm.Keep().Text; k != "x\ny\n0123456789AB" {
		t.Errorf("kept %q", k)
	}
	tm = &Term{emu: vt.NewEmulator(10, 2)}
	tm.emu.WriteString("0123456789A\x1b[2K") // the last kept row still wraps
	if k := tm.Keep().Text; k != "0123456789" {
		t.Errorf("kept %q", k)
	}
}
