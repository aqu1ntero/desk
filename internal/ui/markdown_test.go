package ui

import (
	"fmt"
	"image"
	"testing"

	"gioui.org/layout"
)

// TestParseMarkdown: Claude's markdown becomes the blocks the chat draws,
// links keeping their URL.
func TestParseMarkdown(t *testing.T) {
	bs := parseMarkdown("# Plan\n\nSee **the** [PR](https://x.test/pr/1) and `code`.\n\n" +
		"- one\n  - nested\n2. two\n\n> [!WARNING]\n> careful, it is off by\n> default\n\n```go\nx := 1\n```\n\n| a | n |\n|---|--:|\n| x | 42% |\n\n---\n")
	var kinds []string
	for _, b := range bs {
		kinds = append(kinds, b.kind)
	}
	want := []string{"h", "p", "li", "li", "li", "alert", "code", "table", "hr"}
	if len(kinds) != len(want) {
		t.Fatalf("kinds %v", kinds)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("kinds %v", kinds)
		}
	}
	p := bs[1].spans
	if !p[1].strong || p[3].link != "https://x.test/pr/1" || !p[5].code {
		t.Fatalf("spans %+v", p)
	}
	if bs[3].level != 1 || bs[2].mark != "•" || bs[4].mark != "2." {
		t.Fatalf("lists %+v %+v %+v", bs[2], bs[3], bs[4])
	}
	if bs[5].mark != "WARNING" || plain(bs[5].spans) != "careful, it is off by default" || bs[6].lang != "go" || bs[6].code != "x := 1" || len(bs[7].rows) != 2 || bs[6].level != 0 {
		t.Fatalf("alert %+v code %+v table %+v", bs[5], bs[6], bs[7])
	}
}

// mdShape is each block as kind, level, mark, lang and code (or plain text).
func mdShape(src string) []string {
	var out []string
	for _, b := range parseMarkdown(src) {
		txt := plain(b.spans)
		if b.kind == "code" {
			txt = b.lang + ":" + b.code
		}
		out = append(out, fmt.Sprintf("%s %d %q %s", b.kind, b.level, b.mark, txt))
	}
	return out
}

func sameShape(t *testing.T, got, want []string) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got\n%q\nwant\n%q", got, want)
	}
}

// TestMarkdownCodeInListIndent: a list's code block draws at its item's
// text column (6+16*level dp in), so a line that just fits at the top wraps
// there; a top-level one is not indented.
func TestMarkdownCodeInListIndent(t *testing.T) {
	h := newHarness(t)
	code := "alpha beta gamma delta epsilon zeta eta theta"
	height := func(w int, src string) int {
		return h.draw(image.Pt(w, 2000), func(gtx layout.Context) layout.Dimensions { return h.u.markdown(gtx, "i", src) }).Size.Y
	}
	top := func(w int) int { return height(w, "```\n"+code+"\n```\n") }
	one := top(2000)
	w := 100
	for top(w) != one { // w: the narrowest width that keeps the code on one line
		w++
	}
	for _, c := range []struct{ src, item string }{
		{"- a\n\n  ```\n  " + code + "\n  ```\n", "- a\n"},
		{"- a\n  - b\n\n    ```\n    " + code + "\n    ```\n", "- a\n  - b\n"},
	} {
		lvl := parseMarkdown(c.src)[len(parseMarkdown(c.src))-1].level
		if got, want := height(w, c.src)-height(w, c.item), top(w-(6+16*lvl)); got != want || want == one {
			t.Fatalf("level %d: code drew %dpx tall at %d wide, want %dpx (one line %d)", lvl, got, w, want, one)
		}
	}
}

// TestMarkdownCodeInList: a fenced block inside an item shows in its place,
// under the item's text, the mark staying on the item's first line.
func TestMarkdownCodeInList(t *testing.T) {
	sameShape(t, mdShape("1. Run it:\n\n   ```sh\n   make test\n   ```\n2. Done\n"), []string{
		`li 0 "1." Run it:`, `code 1 "" sh:make test`, `li 0 "2." Done`,
	})
	sameShape(t, mdShape("- a\n  - b\n\n    ```go\n    x := 1\n    ```\n"), []string{
		`li 0 "•" a`, `li 1 "•" b`, `code 2 "" go:x := 1`,
	})
	// An item that opens with the block keeps its mark on a line of its own.
	sameShape(t, mdShape("- ```\n  raw\n  ```\n- next\n"), []string{
		`li 0 "•" `, `code 1 "" :raw`, `li 0 "•" next`,
	})
}
