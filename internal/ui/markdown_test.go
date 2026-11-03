package ui

import "testing"

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
	if bs[5].mark != "WARNING" || plain(bs[5].spans) != "careful, it is off by default" || bs[6].lang != "go" || bs[6].code != "x := 1" || len(bs[7].rows) != 2 {
		t.Fatalf("alert %+v code %+v table %+v", bs[5], bs[6], bs[7])
	}
}
