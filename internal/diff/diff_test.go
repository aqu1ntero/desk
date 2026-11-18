package diff

import (
	"fmt"
	"strings"
	"testing"
)

func ops(ls []Line) string {
	var s []string
	for _, l := range ls {
		s = append(s, string(l.Op)+l.Text)
	}
	return strings.Join(s, ",")
}

func TestLines(t *testing.T) {
	if got := ops(Lines("a\nx\nz\nw\n", "a\ny\nz\nw\n")); got != " a,-x,+y, z, w" {
		t.Errorf("got %s", got)
	}
	if got := ops(Lines("1\n2\n3\nx\n4\n", "0\n2\n3\n4\n5\n")); got != "-1,+0, 2, 3,-x, 4,+5" {
		t.Errorf("got %s", got)
	}
	var a, b []string
	for i := range 1001 {
		a, b = append(a, fmt.Sprint("a", i)), append(b, fmt.Sprint("b", i))
	}
	big := Lines("top\n"+strings.Join(a, "\n")+"\nend\n", "top\n"+strings.Join(b, "\n")+"\nend\n")
	if len(big) != 2004 || big[1].Op != '-' || big[1002].Op != '+' || big[2003].Text != "end" {
		t.Errorf("big: %d lines", len(big))
	}
	if Split("") != nil || len(Lines("", "")) != 0 {
		t.Error("empty")
	}
}

// TestUnified: hunks with their line numbers and context, apart when the
// changes are far apart, and the counts.
func TestUnified(t *testing.T) {
	var old []string
	for i := 1; i <= 20; i++ {
		old = append(old, fmt.Sprint(i))
	}
	next := append([]string{}, old...)
	next[1], next[17] = "two", "eighteen"
	text, add, del := Unified(strings.Join(old, "\n")+"\n", strings.Join(append(next, "21"), "\n")+"\n", 2)
	want := "@@ -1 +1 @@\n 1\n-2\n+two\n 3\n 4\n@@ -16 +16 @@\n 16\n 17\n-18\n+eighteen\n 19\n 20\n+21\n"
	if text != want || add != 3 || del != 2 {
		t.Errorf("got %q +%d -%d", text, add, del)
	}
	// close changes share a hunk
	next = append([]string{}, old...)
	next[4], next[7] = "five", "eight"
	if text, _, _ := Unified(strings.Join(old, "\n"), strings.Join(next, "\n"), 2); strings.Count(text, "@@ -") != 1 {
		t.Errorf("one hunk: %q", text)
	}
	if text, add, del := Unified("same\n", "same\n", 3); text != "" || add+del != 0 {
		t.Errorf("no change: %q", text)
	}
}
