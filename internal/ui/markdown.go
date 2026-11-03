package ui

// Markdown: Claude's replies parsed with goldmark (GFM) into blocks drawn
// natively, as the infra chat does (internal/infra gui_chat.go, gui_rich.go).
// Links are clickable here, and every block selectable (selrich.go).
// ponytail: no mermaid charts or diagrams, no math (the infra chat has both);
// port them when card chats need them.

import (
	"cmp"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gioui.org/font"
	"gioui.org/io/clipboard"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/x/richtext"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	gtext "github.com/yuin/goldmark/text"

	"github.com/lfdominguez/desk/internal/ui/kit"
)

type mdSpan struct {
	text             string
	strong, em, code bool
	link             string // the URL, when a link
}

type mdBlock struct {
	kind  string // p h li quote alert code table hr
	spans []mdSpan
	level int    // heading level, list depth
	mark  string // list marker, alert kind
	lang  string
	code  string
	rows  [][][]mdSpan // table: rows of cells; row 0 is the header
	align []east.Alignment
}

var mdParser = goldmark.New(goldmark.WithExtensions(extension.GFM))

// cache is a map of what was worked out once, halved when it gets big: a
// streaming reply adds an entry per partial text. Half, not all: a screen
// showing more than fits would otherwise be worked out again every frame.
type cache[V any] map[string]V

func (c cache[V]) put(k string, v V) V {
	if len(c) > 400 {
		n := 0
		for k := range c { // ponytail: any half (map order), not the least used
			if n++; n > 200 {
				break
			}
			delete(c, k)
		}
	}
	c[k] = v
	return v
}

var mdBlocks = cache[[]mdBlock]{}

func parseMarkdown(src string) []mdBlock {
	if b, ok := mdBlocks[src]; ok {
		return b
	}
	source := []byte(src)
	doc := mdParser.Parser().Parse(gtext.NewReader(source))
	var blocks []mdBlock
	var inline func(n ast.Node, s mdSpan, out *[]mdSpan)
	inline = func(n ast.Node, s mdSpan, out *[]mdSpan) {
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			switch c := c.(type) {
			case *ast.Text:
				s2 := s
				s2.text = string(c.Segment.Value(source))
				if c.SoftLineBreak() || c.HardLineBreak() {
					s2.text += " "
				}
				*out = append(*out, s2)
			case *ast.CodeSpan:
				s2 := s
				s2.code = true
				inline(c, s2, out)
			case *ast.Emphasis:
				s2 := s
				if c.Level >= 2 {
					s2.strong = true
				} else {
					s2.em = true
				}
				inline(c, s2, out)
			case *ast.Link:
				s2 := s
				s2.link = string(c.Destination)
				inline(c, s2, out)
			case *ast.AutoLink:
				s2 := s
				s2.text = string(c.URL(source))
				s2.link = s2.text
				if c.AutoLinkType == ast.AutoLinkEmail && !strings.HasPrefix(s2.link, "mailto:") {
					s2.link = "mailto:" + s2.link
				}
				*out = append(*out, s2)
			default:
				inline(c, s, out)
			}
		}
	}
	var node func(c ast.Node, depth int)
	walk := func(n ast.Node, depth int) {
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			node(c, depth)
		}
	}
	node = func(c ast.Node, depth int) {
		{
			switch c := c.(type) {
			case *ast.Paragraph, *ast.TextBlock:
				b := mdBlock{kind: "p"}
				inline(c, mdSpan{}, &b.spans)
				blocks = append(blocks, b)
			case *ast.Heading:
				b := mdBlock{kind: "h", level: c.Level}
				inline(c, mdSpan{}, &b.spans)
				blocks = append(blocks, b)
			case *ast.List:
				i := c.Start
				for item := c.FirstChild(); item != nil; item = item.NextSibling() {
					mark := "•"
					if c.IsOrdered() {
						mark = fmt.Sprintf("%d.", i)
						i++
					}
					first := true
					for part := item.FirstChild(); part != nil; part = part.NextSibling() {
						if _, ok := part.(*ast.List); ok {
							node(part, depth+1) // nested list
							continue
						}
						b := mdBlock{kind: "li", level: depth}
						if first {
							b.mark, first = mark, false
						}
						inline(part, mdSpan{}, &b.spans)
						blocks = append(blocks, b)
					}
				}
			case *ast.Blockquote:
				b := mdBlock{kind: "quote"}
				inline(c, mdSpan{}, &b.spans)
				if kind, rest, ok := alertSpans(b.spans); ok {
					b = mdBlock{kind: "alert", mark: kind, spans: rest}
				}
				blocks = append(blocks, b)
			case *ast.FencedCodeBlock, *ast.CodeBlock:
				var sb strings.Builder
				lines := c.Lines()
				for i := 0; i < lines.Len(); i++ {
					seg := lines.At(i)
					sb.Write(seg.Value(source))
				}
				b := mdBlock{kind: "code", code: strings.TrimRight(sb.String(), "\n")}
				if f, ok := c.(*ast.FencedCodeBlock); ok {
					b.lang = string(f.Language(source))
				}
				blocks = append(blocks, b)
			case *ast.ThematicBreak:
				blocks = append(blocks, mdBlock{kind: "hr"})
			case *east.Table:
				b := mdBlock{kind: "table", align: c.Alignments}
				for row := c.FirstChild(); row != nil; row = row.NextSibling() {
					var cells [][]mdSpan
					for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
						var spans []mdSpan
						inline(cell, mdSpan{}, &spans)
						cells = append(cells, spans)
					}
					b.rows = append(b.rows, cells)
				}
				blocks = append(blocks, b)
			default:
				walk(c, depth)
			}
		}
	}
	walk(doc, 0)
	return mdBlocks.put(src, blocks)
}

var alertMark = regexp.MustCompile(`^\s*\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]\s*`)

// alertSpans spots a GitHub alert ("> [!NOTE] …") and drops its marker.
func alertSpans(ss []mdSpan) (string, []mdSpan, bool) {
	var joined strings.Builder // untrimmed: the cut counts from the very start
	for _, s := range ss {
		joined.WriteString(s.text)
	}
	m := alertMark.FindStringSubmatch(joined.String())
	if m == nil {
		return "", nil, false
	}
	cut := len(m[0])
	var rest []mdSpan
	for _, s := range ss {
		if cut >= len(s.text) {
			cut -= len(s.text)
			continue
		}
		s.text = s.text[cut:]
		cut = 0
		rest = append(rest, s)
	}
	return m[1], rest, true
}

var alertLooks = map[string]struct {
	title string
	c     color.NRGBA
}{
	"NOTE": {"Note", kit.CAccText}, "TIP": {"Tip", kit.CGreen}, "IMPORTANT": {"Important", kit.CPurple},
	"WARNING": {"Warning", kit.COrange}, "CAUTION": {"Caution", kit.CRed},
}

func plain(ss []mdSpan) string {
	var b strings.Builder
	for _, s := range ss {
		b.WriteString(s.text)
	}
	return strings.TrimSpace(b.String())
}

func alpha(c color.NRGBA, a uint8) color.NRGBA { c.A = a; return c }

// spans styles ss: code in mono, links in the accent (clickable), bold and
// italic as marked.
func spans(ss []mdSpan, sp unit.Sp, c color.NRGBA, base font.Font) []span {
	var out []span
	for _, s := range ss {
		st := span{SpanStyle: richtext.SpanStyle{Font: base, Size: sp, Color: c, Content: s.text}}
		switch {
		case s.code:
			st.Font, st.Size, st.Color = kit.Mono, sp-1, kit.CAccText
		case s.link != "":
			st.Color, st.url = kit.CAccText, s.link
		}
		if s.strong {
			st.Font = kit.Semi(st.Font)
		}
		if s.em {
			st.Font.Style = font.Italic
		}
		out = append(out, st)
	}
	if len(out) == 0 {
		out = append(out, span{SpanStyle: richtext.SpanStyle{Font: base, Size: sp, Color: c, Content: " "}})
	}
	return out
}

// markdown draws src (one message, id) as its blocks.
func (u *ui) markdown(gtx layout.Context, id, src string) layout.Dimensions {
	k := u.k
	var kids []layout.FlexChild
	for bi, b := range parseMarkdown(src) {
		bid := fmt.Sprintf("%s-%d", id, bi)
		kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				switch b.kind {
				case "h":
					sp := map[int]unit.Sp{1: chatSp + 4, 2: chatSp + 2}[b.level]
					return layout.Inset{Top: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return u.rich(gtx, bid, spans(b.spans, max(sp, chatSp+1), kit.CText, kit.Semi(kit.Sans))...)
					})
				case "li":
					return layout.Inset{Left: unit.Dp(4 + 16*b.level)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								gtx.Constraints.Min.X = gtx.Dp(18)
								return k.Label(gtx, b.mark, chatSp, kit.CText3, kit.Sans, 1)
							}),
							layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
								return u.rich(gtx, bid, spans(b.spans, chatSp, kit.CText, kit.Sans)...)
							}))
					})
				case "quote":
					return u.barred(gtx, kit.CText3, func(gtx layout.Context) layout.Dimensions {
						return u.rich(gtx, bid, spans(b.spans, chatSp, kit.CText2, kit.Sans)...)
					})
				case "alert":
					lk := alertLooks[b.mark]
					return u.barred(gtx, lk.c, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(k.Lbl(lk.title, chatSp-1, lk.c, kit.Semi(kit.Sans))),
							layout.Rigid(kit.VGap(3)),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return u.rich(gtx, bid, spans(b.spans, chatSp, kit.CText, kit.Sans)...)
							}))
					})
				case "code":
					return u.codeBlock(gtx, bid, b)
				case "table":
					return u.table(gtx, bid, b)
				case "hr":
					paint.FillShape(gtx.Ops, kit.CLine, clip.Rect(image.Rect(0, gtx.Dp(4), gtx.Constraints.Max.X, gtx.Dp(5))).Op())
					return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, gtx.Dp(9))}
				}
				return u.rich(gtx, bid, spans(b.spans, chatSp, kit.CText, kit.Sans)...)
			})
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...)
}

// barred is w on a faint tint of c with a bar of c on its left: quotes
// and alerts.
func (u *ui) barred(gtx layout.Context, c color.NRGBA, w layout.Widget) layout.Dimensions {
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			paint.FillShape(gtx.Ops, alpha(c, 0x18), clip.Rect{Max: gtx.Constraints.Min}.Op())
			paint.FillShape(gtx.Ops, c, clip.Rect{Max: image.Pt(gtx.Dp(3), gtx.Constraints.Min.Y)}.Op())
			return layout.Dimensions{Size: gtx.Constraints.Min}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Inset{Top: 6, Bottom: 6, Left: 12, Right: 10}.Layout(gtx, w)
		}))
}

// codeBlock is fenced code, highlighted, with its language and Copy.
func (u *ui) codeBlock(gtx layout.Context, id string, b mdBlock) layout.Dimensions {
	k := u.k
	return kit.Boxed(kit.CPane, kit.CLine, kit.RCtl, layout.Inset{}, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: 10, Bottom: 10, Left: 12, Right: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return u.rich(gtx, id+"code", plainSpans(highlight(b.code, b.lang))...)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				paint.FillShape(gtx.Ops, kit.CLine, clip.Rect(image.Rect(0, 0, gtx.Constraints.Max.X, gtx.Dp(1))).Op())
				return layout.Inset{Top: 5, Bottom: 5, Left: 12, Right: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, k.LblFill(cmp.Or(b.lang, "text"), 11, kit.CText3, kit.Sans)),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							d, ok := k.Button(gtx, id+"copy", kit.CPanelCard, kit.CHover, kit.RCtl, layout.Inset{Top: 4, Bottom: 4, Left: 10, Right: 10}, k.Lbl("Copy", 12, kit.CText2, kit.Semi(kit.Sans)))
							if ok {
								gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(b.code))})
								u.showToast("Copied", false)
							}
							return d
						}))
				})
			}))
	})(gtx)
}

var (
	chromaStyle = styles.Get("github-dark")
	hlCache     = cache[[]richtext.SpanStyle]{}
)

// highlight colors code with chroma's github-dark style, once per block.
func highlight(code, lang string) []richtext.SpanStyle {
	key := lang + "\x00" + code
	if s, ok := hlCache[key]; ok {
		return s
	}
	lx := lexers.Get(lang)
	if lx == nil {
		lx = lexers.Fallback
	}
	it, err := chroma.Coalesce(lx).Tokenise(nil, code)
	if err != nil {
		return hlCache.put(key, []richtext.SpanStyle{{Font: kit.Mono, Size: chatSp - 1, Color: kit.CText, Content: code}})
	}
	var out []richtext.SpanStyle
	for tok := it(); tok != chroma.EOF; tok = it() {
		c := kit.CText
		if e := chromaStyle.Get(tok.Type); e.Colour.IsSet() {
			c = color.NRGBA{e.Colour.Red(), e.Colour.Green(), e.Colour.Blue(), 0xff}
		}
		out = append(out, richtext.SpanStyle{Font: kit.Mono, Size: chatSp - 1, Color: c, Content: tok.Value})
	}
	return hlCache.put(key, out)
}

var (
	numCell = regexp.MustCompile(`^[-+~≈]?\$?\d[\d.,]*\s*([%x×]|[kKMGTP]i?[Bb]?|[KMGT]|ms|µs|s|m|h|d|B|/s)?$`)
	pctCell = regexp.MustCompile(`^(\d+(?:\.\d+)?)\s*%$`)
)

func levelColor(pct float64) color.NRGBA {
	switch {
	case pct >= 90:
		return kit.CRed
	case pct >= 70:
		return kit.COrange
	}
	return kit.CGreen
}

// measure lays w out off screen: its size, and the ops to draw it later.
func measure(gtx layout.Context, w layout.Widget) (layout.Dimensions, op.CallOp) {
	m := op.Record(gtx.Ops)
	d := w(gtx)
	return d, m.Stop()
}

// table draws a markdown table: a header band, columns sized to their
// content, numbers right-aligned in mono, percentages with a usage meter.
func (u *ui) table(gtx layout.Context, id string, b mdBlock) layout.Dimensions {
	nc := 0
	for _, r := range b.rows {
		nc = max(nc, len(r))
	}
	if nc == 0 {
		return layout.Dimensions{}
	}
	padX, padY := gtx.Dp(10), gtx.Dp(7)
	numeric := make([]bool, nc)
	for c := range nc {
		seen := false
		numeric[c] = true
		for _, r := range b.rows[1:] {
			if c < len(r) {
				if t := plain(r[c]); t != "" && t != "-" && t != "—" {
					seen = true
					numeric[c] = numeric[c] && numCell.MatchString(t)
				}
			}
		}
		numeric[c] = numeric[c] && seen
	}
	cell := func(gtx layout.Context, ri, c int) layout.Dimensions {
		if c >= len(b.rows[ri]) {
			return layout.Dimensions{}
		}
		sp, col, f := unit.Sp(12), kit.CText, kit.Sans
		ss := b.rows[ri][c]
		switch {
		case ri == 0:
			sp, col, f, ss = 11, kit.CText3, kit.Semi(kit.Sans), []mdSpan{{text: strings.ToUpper(plain(ss))}}
		case numeric[c]:
			f = kit.Mono
		}
		return u.rich(gtx, fmt.Sprintf("%s-%d-%d", id, ri, c), spans(ss, sp, col, f)...)
	}
	nk := fmt.Sprint(id, nc, len(b.rows), gtx.Metric) // the cells' natural widths, measured once: measuring lays each cell out at another width, so it'd be shaped twice a frame
	nat, ok := u.tableNat[nk]
	if !ok {
		nat = make([]int, nc)
		for ri := range b.rows {
			for c := range nc {
				g := gtx
				g.Constraints = layout.Constraints{Max: image.Pt(1<<16, 1<<16)}
				d, _ := measure(g, func(gtx layout.Context) layout.Dimensions { return cell(gtx, ri, c) })
				nat[c] = max(nat[c], min(d.Size.X, gtx.Dp(360))+2*padX)
			}
		}
		u.tableNat[nk] = nat // ponytail: never pruned, like rsels
	}
	W := gtx.Constraints.Max.X
	ws := fitColumns(nat, W)
	align := func(c int) east.Alignment {
		if c < len(b.align) && b.align[c] != east.AlignNone {
			return b.align[c]
		}
		if numeric[c] {
			return east.AlignRight
		}
		return east.AlignLeft
	}
	type laid struct {
		d    layout.Dimensions
		call op.CallOp
	}
	var rows [][]laid
	var hs []int
	for ri := range b.rows {
		var row []laid
		h := 0
		for c := range nc {
			g := gtx
			g.Constraints = layout.Constraints{Max: image.Pt(max(1, ws[c]-2*padX), 1<<16)}
			d, call := measure(g, func(gtx layout.Context) layout.Dimensions { return cell(gtx, ri, c) })
			row = append(row, laid{d, call})
			h = max(h, d.Size.Y)
		}
		rows = append(rows, row)
		hs = append(hs, h+2*padY)
	}
	total := 0
	for _, h := range hs {
		total += h
	}
	frame := image.Rect(0, 0, W, total)
	rad := gtx.Dp(kit.RCtl)
	kit.Panel(gtx, frame, kit.RCtl, kit.CPane, kit.CLine)
	st := clip.UniformRRect(frame.Inset(gtx.Dp(1)), max(0, rad-gtx.Dp(1))).Push(gtx.Ops)
	y := 0
	for ri, row := range rows {
		h := hs[ri]
		switch {
		case ri == 0:
			paint.FillShape(gtx.Ops, kit.CPanelCard, clip.Rect(image.Rect(0, 0, W, h)).Op())
		case ri%2 == 0:
			paint.FillShape(gtx.Ops, alpha(kit.CPanelCard, 0x50), clip.Rect(image.Rect(0, y, W, y+h)).Op())
		}
		if ri > 0 {
			paint.FillShape(gtx.Ops, alpha(kit.CLine, 0xc0), clip.Rect(image.Rect(0, y, W, y+gtx.Dp(1))).Op())
		}
		x := 0
		for c, l := range row {
			inner := ws[c] - 2*padX
			dx := 0
			switch align(c) {
			case east.AlignRight:
				dx = inner - l.d.Size.X
			case east.AlignCenter:
				dx = (inner - l.d.Size.X) / 2
			}
			off := op.Offset(image.Pt(x+padX+dx, y+padY)).Push(gtx.Ops)
			l.call.Add(gtx.Ops)
			off.Pop()
			if ri > 0 && c < len(b.rows[ri]) {
				if m := pctCell.FindStringSubmatch(plain(b.rows[ri][c])); m != nil {
					pct, _ := strconv.ParseFloat(m[1], 64)
					mh := gtx.Dp(3)
					track := image.Rect(x+padX, y+h-padY/2-mh, x+ws[c]-padX, y+h-padY/2)
					kit.FillRRect(gtx.Ops, track, mh/2, alpha(kit.CText3, 0x30))
					if w := int(math.Min(100, pct) / 100 * float64(track.Dx())); w > 0 {
						kit.FillRRect(gtx.Ops, image.Rect(track.Min.X, track.Min.Y, track.Min.X+w, track.Max.Y), mh/2, levelColor(pct))
					}
				}
			}
			x += ws[c]
		}
		y += h
	}
	st.Pop()
	return layout.Dimensions{Size: frame.Size()}
}

// fitColumns gives each column its natural width when they all fit (the
// spare spread in proportion), else narrow columns keep their width and the
// wide ones share what's left (they wrap).
func fitColumns(nat []int, avail int) []int {
	w := make([]int, len(nat))
	total := 0
	for _, n := range nat {
		total += n
	}
	if total <= avail {
		for i, n := range nat {
			w[i] = n + (avail-total)*n/max(1, total)
		}
		return w
	}
	idx := make([]int, len(nat))
	for i := range idx {
		idx[i] = i
	}
	slices.SortStableFunc(idx, func(a, b int) int { return nat[a] - nat[b] })
	left := avail
	for k, i := range idx {
		w[i] = min(nat[i], left/(len(nat)-k))
		left -= w[i]
	}
	return w
}
