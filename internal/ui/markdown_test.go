package ui

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"strconv"
	"strings"
	"testing"

	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/unit"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"

	"github.com/lfdominguez/desk/internal/ui/kit"
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

// richWidth is the width a rich block was laid out at; richStyles what its
// spans looked like (the rest of its key).
func richWidth(t *testing.T, h *harness, id string) int {
	t.Helper()
	st := h.u.rsels[id]
	if st == nil {
		t.Fatalf("%s not drawn", id)
	}
	w, _ := strconv.Atoi(strings.Fields(st.key)[0])
	return w
}

func richStyles(t *testing.T, h *harness, id string) string {
	t.Helper()
	richWidth(t, h, id)
	k := h.u.rsels[id].key
	return k[strings.IndexByte(k, 0):]
}

func styledLike(ss []span) string {
	k := richKey(gtxOf(100), ss)
	return k[strings.IndexByte(k, 0):]
}

// TestMarkdownHeadingsAndItems: headings by level (# and ## bigger, the rest
// a step over the text), list items in by 16dp a level past their 18dp mark.
func TestMarkdownHeadingsAndItems(t *testing.T) {
	h := newHarness(t)
	src := "# A\n\n## B\n\n### C\n\n- x\n  - y\n"
	h.draw(image.Pt(400, 2000), func(gtx layout.Context) layout.Dimensions { return h.u.markdown(gtx, "h", src) })
	bs := parseMarkdown(src)
	for i, sp := range []int{4, 2, 1} {
		id := fmt.Sprintf("h-%d", i)
		if got, want := richStyles(t, h, id), styledLike(spans(bs[i].spans, chatSp+unit.Sp(sp), kit.CText, kit.Semi(kit.Sans))); got != want {
			t.Errorf("%s: %q, want %q", id, got, want)
		}
	}
	for i, want := range map[int]int{3: 400 - 4 - 18, 4: 400 - 20 - 18} {
		if got := richWidth(t, h, fmt.Sprintf("h-%d", i)); got != want {
			t.Errorf("item %d at level %d laid out %d wide, want %d", i, bs[i].level, got, want)
		}
	}
}

// TestMarkdownAlertTitle: an alert is its title a size under the text, a
// 3dp gap, its text, all inside the bar's 6dp padding.
func TestMarkdownAlertTitle(t *testing.T) {
	h := newHarness(t)
	d := h.draw(image.Pt(400, 2000), func(gtx layout.Context) layout.Dimensions {
		return h.u.markdown(gtx, "a", "> [!NOTE]\n> noted\n")
	})
	title := h.u.k.Label(gtxOf(400), "Note", chatSp-1, kit.CAccText, kit.Semi(kit.Sans), 1).Size.Y
	if want := 6 + title + 3 + h.u.rsels["a-0"].size.Y + 6 + 8; d.Size.Y != want {
		t.Fatalf("alert %dpx tall, want %d (title %d)", d.Size.Y, want, title)
	}
}

type brokenLexer struct{}

func (brokenLexer) Config() *chroma.Config { return &chroma.Config{Name: "desk-broken-test"} }
func (brokenLexer) Tokenise(*chroma.TokeniseOptions, string) (chroma.Iterator, error) {
	return nil, errors.New("broken")
}
func (l brokenLexer) SetRegistry(*chroma.LexerRegistry) chroma.Lexer { return l }
func (l brokenLexer) SetAnalyser(func(string) float32) chroma.Lexer  { return l }
func (brokenLexer) AnalyseText(string) float32                       { return 0 }

// TestHighlightSize: highlighted code is mono a size under the text, also
// when its lexer fails (then it is one plain span).
func TestHighlightSize(t *testing.T) {
	if hl := highlight("x := 1", "go"); hl[0].Size != chatSp-1 {
		t.Errorf("highlighted at %v", hl[0].Size)
	}
	lexers.Register(brokenLexer{})
	if hl := highlight("x\ty", "desk-broken-test"); len(hl) != 1 || hl[0].Size != chatSp-1 || hl[0].Content != "x    y" || hl[0].Color != kit.CText {
		t.Errorf("a failed lexer gives %+v", hl)
	}
}

// mdTable: Name is text; Used numbers and percentages (some missing), no
// alignment given; Mixed half text; Dash nothing but dashes; Mid centered
// numbers under a header that reads as a percentage.
const mdTable = "| Name | Used | Mixed | Dash | 100% |\n|---|---|---|---|:-:|\n" +
	"| disk | 95% | abc | - | 1 |\n| mem | 40% | 12 | — | 22 |\n| cpu | - | 7 |  | 333 |\n| gpu | — | x | - | 4 |\n"

// TestTableNumericColumns: a column is numeric (mono) when every cell that
// says something is a number, dashes and blanks saying nothing; a column of
// only dashes is not.
func TestTableNumericColumns(t *testing.T) {
	h := newHarness(t)
	h.draw(image.Pt(600, 2000), func(gtx layout.Context) layout.Dimensions { return h.u.markdown(gtx, "t", mdTable) })
	mono := string(kit.Mono.Typeface)
	for c, want := range []bool{false, true, false, false, true} {
		for ri := 1; ri <= 4; ri++ {
			id := fmt.Sprintf("t-0-%d-%d", ri, c)
			if got := strings.Contains(richStyles(t, h, id), mono); got != want {
				t.Errorf("%s mono %v, want %v", id, got, want)
			}
		}
	}
}

// shot draws w alone on a headless GPU window and returns its pixels (and
// w's size); it skips without a GPU.
func (h *harness) shot(sz image.Point, w layout.Widget) (*image.RGBA, layout.Dimensions) {
	h.t.Helper()
	win, err := headless.NewWindow(sz.X, sz.Y)
	if err != nil {
		h.t.Skipf("no headless GPU: %v", err)
	}
	defer win.Release()
	d := h.draw(sz, w)
	if err := win.Frame(&h.ops); err != nil {
		h.t.Fatal(err)
	}
	img := image.NewRGBA(image.Rectangle{Max: sz})
	if err := win.Screenshot(img); err != nil {
		h.t.Fatal(err)
	}
	return img, d
}

// near says whether two colors are within d of each other on every channel.
func near(a, b color.Color, d int) bool {
	r1, g1, b1, _ := a.RGBA()
	r2, g2, b2, _ := b.RGBA()
	for _, p := range [][2]uint32{{r1, r2}, {g1, g2}, {b1, b2}} {
		if x := int(p[0]>>8) - int(p[1]>>8); x > d || x < -d {
			return false
		}
	}
	return true
}

// tableGrid is a drawn table's column edges and row edges, from its widths
// and its cells' laid out heights.
func tableGrid(t *testing.T, h *harness, id string, b mdBlock, W int) (xs, ys []int) {
	t.Helper()
	if len(h.u.tableNat) != 1 {
		t.Fatalf("%d tables measured", len(h.u.tableNat))
	}
	var nat []int
	for _, n := range h.u.tableNat {
		nat = n
	}
	x := 0
	for _, w := range fitColumns(nat, W) {
		xs = append(xs, x)
		x += w
	}
	xs = append(xs, x)
	y := 0
	for ri := range b.rows {
		ys = append(ys, y)
		hi := 0
		for c := range nat {
			hi = max(hi, h.u.rsels[fmt.Sprintf("%s-%d-%d", id, ri, c)].size.Y)
		}
		y += hi + 2*7
	}
	return xs, append(ys, y)
}

// ink is the box of the pixels in r that stand out from bg (text).
func ink(img *image.RGBA, r image.Rectangle, bg color.Color) image.Rectangle {
	var box image.Rectangle
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if !near(img.At(x, y), bg, 40) {
				box = box.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	return box
}

// TestTableDraws: the header on its band, every other row tinted, a line
// over each row, cells placed by their alignment (given, else numbers right),
// and a meter under each percentage, filled to it in its level's color.
func TestTableDraws(t *testing.T) {
	h := newHarness(t)
	const W = 600
	img, d := h.shot(image.Pt(W, 400), func(gtx layout.Context) layout.Dimensions { return h.u.markdown(gtx, "t", mdTable) })
	b := parseMarkdown(mdTable)[0]
	xs, ys := tableGrid(t, h, "t-0", b, W)
	if d.Size.Y != ys[len(ys)-1]+8 {
		t.Fatalf("table %dpx tall, its rows %v", d.Size.Y, ys)
	}
	mid := func(ri int) int { return (ys[ri] + ys[ri+1]) / 2 }
	px := func(x, y int) color.Color { return img.At(x, y) }

	if !near(px(3, mid(0)), kit.CPanelCard, 2) {
		t.Errorf("header band %v", px(3, mid(0)))
	}
	for _, ri := range []int{1, 3} {
		if !near(px(3, mid(ri)), kit.CPane, 2) {
			t.Errorf("row %d is tinted: %v", ri, px(3, mid(ri)))
		}
	}
	tint := px(3, mid(2))
	if near(tint, kit.CPane, 2) || !near(px(3, mid(4)), tint, 1) {
		t.Errorf("even rows %v %v", tint, px(3, mid(4)))
	}
	if near(px(3, ys[3]), kit.CPane, 8) || !near(px(3, ys[3]+1), kit.CPane, 2) || !near(px(3, ys[3]-1), tint, 1) {
		t.Errorf("row 3's line: %v over %v under %v", px(3, ys[3]-1), px(3, ys[3]), px(3, ys[3]+1))
	}
	if c := px(1, ys[5]-4); !near(c, tint, 3) {
		t.Errorf("the last row's corner %v, its fill %v", c, tint)
	}

	// cells: padded 10dp across, 7dp down
	cellInk := func(ri, c int, bg color.Color) image.Rectangle {
		return ink(img, image.Rect(xs[c], ys[ri]+7, xs[c+1], ys[ri+1]-7), bg)
	}
	if r := ink(img, image.Rect(xs[0], ys[1], xs[1], ys[2]), kit.CPane); r.Empty() || r.Min.X < xs[0]+9 || r.Min.X > xs[0]+12 || r.Min.Y < ys[1]+7 {
		t.Errorf("left cell's text at %v, cell from %d,%d", r, xs[0], ys[1])
	}
	if r := cellInk(1, 1, kit.CPane); r.Empty() || r.Max.X > xs[2]-10 || r.Max.X < xs[2]-13 {
		t.Errorf("numeric cell's text at %v, cell to %d", r, xs[2])
	}
	if r := cellInk(1, 4, kit.CPane); r.Empty() || abs((r.Min.X+r.Max.X)/2-(xs[4]+xs[5])/2) > 3 {
		t.Errorf("centered cell's text at %v, cell %d-%d", r, xs[4], xs[5])
	}

	// meters: 3dp tall, 3dp over the row's end, the cell's width less its padding
	for _, m := range []struct {
		ri  int
		pct float64
		c   color.NRGBA
		bg  color.Color
	}{{1, 95, kit.CRed, kit.CPane}, {2, 40, kit.CGreen, tint}} {
		y := ys[m.ri+1] - 5
		x0, x1 := xs[1]+10, xs[2]-10
		w := int(m.pct / 100 * float64(x1-x0))
		if !near(px(x0+3, y), m.c, 2) || !near(px(x0+w-3, y), m.c, 2) {
			t.Errorf("row %d's meter fill %v … %v", m.ri, px(x0+3, y), px(x0+w-3, y))
		}
		if m.pct < 50 && (near(px(x0+w+3, y), m.c, 30) || near(px(x1-3, y), m.bg, 2)) {
			t.Errorf("row %d's meter past its fill %v, its end %v", m.ri, px(x0+w+3, y), px(x1-3, y))
		}
		for _, p := range []image.Point{{x0 - 3, y}, {x1 + 3, y}, {x0 + 3, y - 3}, {x0 + 3, y + 3}} {
			if !near(px(p.X, p.Y), m.bg, 2) {
				t.Errorf("row %d's meter reaches %v: %v", m.ri, p, px(p.X, p.Y))
			}
		}
	}
	if c := px((xs[4]+xs[5])/2, ys[1]-5); !near(c, kit.CPanelCard, 2) {
		t.Errorf("a header that reads as a percentage has a meter: %v", c)
	}
}

func abs(x int) int { return max(x, -x) }
