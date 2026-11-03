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
