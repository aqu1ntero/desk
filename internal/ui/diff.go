package ui

import (
	"fmt"
	"image"
	"image/color"
	"path/filepath"
	"regexp"
	"strings"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"golang.org/x/image/math/fixed"

	"github.com/lfdominguez/desk/internal/kanban"
	"github.com/lfdominguez/desk/internal/ui/kit"
)

// diffLine is one line of a change: kept (' '), removed ('-') or added ('+').
type diffLine struct {
	op   byte
	text string
}

// lineDiff is old turned into new, line by line: the common start and end
// kept, the middle by longest common subsequence. ponytail: O(n·m); past a
// million cells the middle is shown as all removed then all added.
func lineDiff(old, new string) []diffLine {
	a, b := splitLines(old), splitLines(new)
	var head, tail []diffLine
	for len(a) > 0 && len(b) > 0 && a[0] == b[0] {
		head = append(head, diffLine{' ', a[0]})
		a, b = a[1:], b[1:]
	}
	for len(a) > 0 && len(b) > 0 && a[len(a)-1] == b[len(b)-1] {
		tail = append([]diffLine{{' ', a[len(a)-1]}}, tail...)
		a, b = a[:len(a)-1], b[:len(b)-1]
	}
	mid := make([]diffLine, 0, len(a)+len(b))
	if len(a)*len(b) > 1_000_000 {
		for _, l := range a {
			mid = append(mid, diffLine{'-', l})
		}
		for _, l := range b {
			mid = append(mid, diffLine{'+', l})
		}
		return append(append(head, mid...), tail...)
	}
	n, m := len(a), len(b)
	lcs := make([][]int, n+1) // lcs[i][j]: of a[i:] and b[j:]
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && a[i] == b[j]:
			mid = append(mid, diffLine{' ', a[i]})
			i, j = i+1, j+1
		case i < n && (j == m || lcs[i+1][j] >= lcs[i][j+1]): // removals first, as diffs read
			mid = append(mid, diffLine{'-', a[i]})
			i++
		default:
			mid = append(mid, diffLine{'+', b[j]})
			j++
		}
	}
	return append(append(head, mid...), tail...)
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// seg is a piece of a line in a word diff: kept (' '), or words it lost
// ('-') or gained ('+').
type seg struct {
	op   byte
	text string
}

// wordRe is a diff's words: identifiers and numbers, runs of space, single
// punctuation.
var wordRe = regexp.MustCompile(`\w+|\s+|[^\w\s]`)

// wordDiff is old and new word by word: old's segments, new's, both merged
// in reading order, and how alike they are (0 nothing kept, 1 the same).
// ponytail: O(n·m) in words; past 40k cells it says everything changed.
func wordDiff(old, new string) (a, b, both []seg, alike float64) {
	add := func(s *[]seg, op byte, t string) {
		if n := len(*s); n > 0 && (*s)[n-1].op == op {
			(*s)[n-1].text += t
		} else {
			*s = append(*s, seg{op, t})
		}
	}
	x, y := wordRe.FindAllString(old, -1), wordRe.FindAllString(new, -1)
	n, m := len(x), len(y)
	if n*m > 40_000 {
		return []seg{{'-', old}}, []seg{{'+', new}}, []seg{{'-', old}, {'+', new}}, 0
	}
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if x[i] == y[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	kept := 0
	for i, j := 0, 0; i < n || j < m; {
		switch {
		case i < n && j < m && x[i] == y[j]:
			add(&a, ' ', x[i])
			add(&b, ' ', y[j])
			add(&both, ' ', x[i])
			if strings.TrimSpace(x[i]) != "" { // spaces alone make no two lines alike
				kept += 2 * len(x[i])
			}
			i, j = i+1, j+1
		case i < n && (j == m || lcs[i+1][j] >= lcs[i][j+1]):
			add(&a, '-', x[i])
			add(&both, '-', x[i])
			i++
		default:
			add(&b, '+', y[j])
			add(&both, '+', y[j])
			j++
		}
	}
	total := len(strings.Join(strings.Fields(old), "")) + len(strings.Join(strings.Fields(new), ""))
	if total == 0 {
		return a, b, both, 1
	}
	return a, b, both, float64(kept) / float64(total)
}

// The views of a change; Auto picks one per block of changed lines.
const (
	diffAuto = iota
	diffUnified
	diffSplit
	diffWords
)

var diffModes = []string{"Auto", "Unified", "Split", "Words"}

// diffRow is one row drawn: a kept line (' '), a removed or added one with
// its changed words marked ('-', '+'), old and new merged ('~'), old beside
// new ('|', a side nil when it has none) or a kept line on both sides ('=',
// Split); op 0 is the gap between two edits of a MultiEdit.
type diffRow struct {
	op   byte
	l, r []seg
}

// Thresholds for how alike two lines are: from wordsAlike a line reads best
// as one merged line, from markAlike its changed words are worth marking.
const (
	wordsAlike = 0.5
	markAlike  = 0.3
)

// diffRows lays lines out in mode. A block of changed lines in Auto: only
// removed or only added, unified; every removed line edited a little into
// its added one, merged by words; rewritten, split when wide, else unified.
func diffRows(lines []diffLine, mode int, wide bool) []diffRow {
	var rows []diffRow
	var dels, adds []string
	whole := func(t string) []seg { return []seg{{' ', t}} } // no words marked: the row's tint says it all
	flush := func() {
		if len(dels)+len(adds) == 0 {
			return
		}
		ps := pairLines(dels, adds)
		m := mode
		if m == diffAuto {
			all := true // every line of the block edited a little into another
			for _, p := range ps {
				all = all && p.d >= 0 && p.a >= 0 && p.alike >= wordsAlike
			}
			switch {
			case len(dels) == 0 || len(adds) == 0:
				m = diffUnified
			case all:
				m = diffWords
			case wide:
				m = diffSplit
			default:
				m = diffUnified
			}
		}
		switch m {
		case diffSplit:
			for _, p := range ps {
				rows = append(rows, diffRow{op: '|', l: p.old, r: p.new})
			}
		case diffWords:
			for _, p := range ps {
				switch {
				case p.d >= 0 && p.a >= 0 && p.alike >= markAlike:
					rows = append(rows, diffRow{op: '~', l: p.both})
				case p.d >= 0 && p.a >= 0:
					rows = append(rows, diffRow{op: '-', l: p.old}, diffRow{op: '+', l: p.new})
				case p.d >= 0:
					rows = append(rows, diffRow{op: '-', l: p.old})
				default:
					rows = append(rows, diffRow{op: '+', l: p.new})
				}
			}
		default: // the removed lines, then the added, as diffs read
			for _, p := range ps {
				if p.d >= 0 {
					rows = append(rows, diffRow{op: '-', l: p.old})
				}
			}
			for _, p := range ps {
				if p.a >= 0 {
					rows = append(rows, diffRow{op: '+', l: p.new})
				}
			}
		}
		dels, adds = nil, nil
	}
	for _, l := range lines {
		switch l.op {
		case '-':
			if len(adds) > 0 { // a removal after additions starts another block
				flush()
			}
			dels = append(dels, l.text)
		case '+':
			adds = append(adds, l.text)
		default:
			flush()
			switch {
			case l.op == 0:
				rows = append(rows, diffRow{})
			case mode == diffSplit:
				rows = append(rows, diffRow{op: '=', l: whole(l.text), r: whole(l.text)})
			default:
				rows = append(rows, diffRow{op: ' ', l: whole(l.text)})
			}
		}
	}
	flush()
	return rows
}

// linePair is a removed line (d, index in the block) beside the added one
// it became (a); -1 on a side it doesn't have. old and new are its words,
// marked when the two are alike enough to tell the change.
type linePair struct {
	d, a     int
	old, new []seg
	both     []seg
	alike    float64
}

// pairLines lines up a block's removed lines with its added ones, in order,
// each with the most alike it can (the alignment that keeps the most
// alikeness); the rest stand alone. ponytail: O(n·m) word diffs; past 40
// lines a side it pairs by position.
func pairLines(dels, adds []string) []linePair {
	whole := func(t string) []seg { return []seg{{' ', t}} }
	pair := func(i, j int) linePair {
		a, b, both, al := wordDiff(dels[i], adds[j])
		p := linePair{d: i, a: j, old: whole(dels[i]), new: whole(adds[j]), both: both, alike: al}
		if al >= markAlike {
			p.old, p.new = a, b
		}
		return p
	}
	n, m := len(dels), len(adds)
	var ps []linePair
	if n > 40 || m > 40 {
		for i := range max(n, m) {
			switch {
			case i < n && i < m:
				ps = append(ps, pair(i, i))
			case i < n:
				ps = append(ps, linePair{d: i, a: -1, old: whole(dels[i])})
			default:
				ps = append(ps, linePair{d: -1, a: i, new: whole(adds[i])})
			}
		}
		return ps
	}
	sim := make([][]linePair, n)
	for i := range sim {
		sim[i] = make([]linePair, m)
		for j := range m {
			sim[i][j] = pair(i, j)
		}
	}
	best := make([][]float64, n+1) // best[i][j]: of dels[i:] and adds[j:]
	for i := range best {
		best[i] = make([]float64, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			best[i][j] = max(best[i+1][j], best[i][j+1])
			if al := sim[i][j].alike; al >= markAlike {
				best[i][j] = max(best[i][j], best[i+1][j+1]+al)
			}
		}
	}
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && sim[i][j].alike >= markAlike && best[i][j] == best[i+1][j+1]+sim[i][j].alike:
			ps = append(ps, sim[i][j])
			i, j = i+1, j+1
		case i < n && (j == m || best[i+1][j] >= best[i][j+1]):
			ps = append(ps, linePair{d: i, a: -1, old: whole(dels[i])})
			i++
		default:
			ps = append(ps, linePair{d: -1, a: j, new: whole(adds[j])})
			j++
		}
	}
	// rewritten lines nothing matched: side by side still, in Split, as they came
	return zipLone(ps, dels, adds)
}

// zipLone puts each run of lone removed lines beside the lone added ones
// right after it (a rewrite: nothing alike, but the same place).
func zipLone(ps []linePair, dels, adds []string) []linePair {
	var out []linePair
	for k := 0; k < len(ps); {
		if ps[k].a >= 0 {
			out = append(out, ps[k])
			k++
			continue
		}
		var ds, as []linePair
		for k < len(ps) && ps[k].d >= 0 && ps[k].a < 0 {
			ds = append(ds, ps[k])
			k++
		}
		for k < len(ps) && ps[k].d < 0 {
			as = append(as, ps[k])
			k++
		}
		for x := range max(len(ds), len(as)) {
			p := linePair{d: -1, a: -1}
			if x < len(ds) {
				p.d, p.old = ds[x].d, ds[x].old
			}
			if x < len(as) {
				p.a, p.new = as[x].a, as[x].new
			}
			if p.d >= 0 && p.a >= 0 {
				_, _, p.both, p.alike = wordDiff(dels[p.d], adds[p.a])
			}
			out = append(out, p)
		}
	}
	return out
}

// diffOpenCount is how many changes show whole, for sig.
func (u *ui) diffOpenCount() (n int) {
	for _, o := range u.diffOpen {
		if o {
			n++
		}
	}
	return n
}

// diffFold is how many rows a change shows before Show all.
const diffFold = 24

// diffOf is message id's change as lines, the edits apart by a nil gap
// marker (op 0); computed once.
func (u *ui) diffOf(id string, m kanban.ChatMsg) []diffLine {
	if d, ok := u.diffs[id]; ok {
		return d
	}
	var d []diffLine
	for i, e := range m.Edits {
		if i > 0 {
			d = append(d, diffLine{})
		}
		d = append(d, lineDiff(e.Old, e.New)...)
	}
	u.diffs[id] = d // ponytail: never pruned, like rsels; a few lines per edit
	return d
}

// diffBlock is an Edit's change: the file, its +/- count and the view
// switch (Auto · Unified · Split · Words), then the rows over their code's
// highlighting; long ones fold at diffFold rows.
func (u *ui) diffBlock(gtx layout.Context, id string, m kanban.ChatMsg) layout.Dimensions {
	k := u.k
	lines := u.diffOf(id, m)
	var add, del int
	for _, l := range lines {
		switch l.op {
		case '+':
			add++
		case '-':
			del++
		}
	}
	lang := filepath.Base(m.Path)
	mode := u.diffMode[id]
	wide := gtx.Constraints.Max.X >= gtx.Dp(640)
	rk := fmt.Sprint(id, mode, wide)
	all, ok := u.diffRowsOf[rk] // its rows: word diffs of every pair, kept (a message never changes)
	if !ok {
		all = diffRows(lines, mode, wide)
		u.diffRowsOf[rk] = all // ponytail: never pruned, like diffs
	}
	shown := all
	open := u.diffOpen[id]
	if !open && len(all) > diffFold {
		shown = all[:diffFold]
	}
	rows := []layout.FlexChild{layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: 5, Bottom: 5, Left: 10, Right: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return k.Label(gtx, strings.Replace(m.Path, u.home, "~", 1), 12, kit.CText2, kit.Mono, 1)
				}),
				layout.Rigid(k.Lbl(fmt.Sprintf("+%d", add), 12, kit.CGreen, kit.Semi(kit.Mono))),
				layout.Rigid(kit.HGap(6)),
				layout.Rigid(k.Lbl(fmt.Sprintf("−%d", del), 12, kit.CRed, kit.Semi(kit.Mono))),
				layout.Rigid(kit.HGap(10)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return u.choice(gtx, "diffMode"+id, diffModes, mode, func(int) color.NRGBA { return color.NRGBA{} }, func(i int) {
						if i == diffAuto {
							delete(u.diffMode, id)
						} else {
							u.diffMode[id] = i
						}
					})
				}))
		})
	})}
	for _, r := range shown {
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return u.diffRow(gtx, r, lang) }))
	}
	if len(all) > diffFold {
		label := fmt.Sprintf("Show all %d rows", len(all))
		if open {
			label = "Show less"
		}
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			d, ok := k.Button(gtx, "diffMore"+id, color.NRGBA{}, kit.CHover, 0, layout.Inset{Top: 5, Bottom: 5, Left: 10, Right: 10}, k.Lbl(label, 12, kit.CAccText, kit.Semi(kit.Sans)))
			if ok {
				u.diffOpen[id] = !open
			}
			return d
		}))
	}
	return kit.Boxed(kit.CPane, kit.CLine, kit.RCard, layout.Inset{}, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
	})(gtx)
}

var (
	diffAdd     = kit.Mix(kit.CPane, kit.CGreen, .12)
	diffDel     = kit.Mix(kit.CPane, kit.CRed, .12)
	diffAddWord = kit.Mix(kit.CPane, kit.CGreen, .38)
	diffDelWord = kit.Mix(kit.CPane, kit.CRed, .38)
	diffNone    = kit.Mix(kit.CPane, kit.CLine, .5) // a split side with no line
)

// cell is one character of a diff row as drawn.
type cell struct {
	r      rune
	fg, bg color.NRGBA
}

// cellKey is a row side's segs (their backing array: rows are kept in
// diffRowsOf, so it is the same each frame), its language and quietness.
type cellKey struct {
	p     *seg
	n     int
	lang  string
	quiet bool
}

// cells is diffCells, kept per row side: it highlights the row's code.
func (u *ui) cells(segs []seg, lang string, quiet bool) []cell {
	var p *seg
	if len(segs) > 0 {
		p = &segs[0]
	}
	k := cellKey{p, len(segs), lang, quiet} // no segs: one entry, nothing to draw
	cs, ok := u.cellsOf[k]
	if !ok {
		cs = diffCells(segs, lang, quiet)
		u.cellsOf[k] = cs // ponytail: never pruned, like diffRowsOf
	}
	return cs
}

// diffCells is segs as characters: their code's highlighting (quieter on
// kept lines and on removed words merged with added ones) over the
// changed words' tint.
func diffCells(segs []seg, lang string, quiet bool) []cell {
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(s.text)
	}
	src := strings.ReplaceAll(b.String(), "\t", "    ") // the fonts draw no tab
	var fgs []color.NRGBA
	for _, sp := range highlight(src, lang) {
		for range sp.Content {
			fgs = append(fgs, sp.Color)
		}
	}
	var cs []cell
	for _, s := range segs {
		bg := map[byte]color.NRGBA{'-': diffDelWord, '+': diffAddWord}[s.op]
		for _, r := range strings.ReplaceAll(s.text, "\t", "    ") {
			fg := kit.CText
			if i := len(cs); i < len(fgs) {
				fg = fgs[i]
			}
			if quiet && s.op == ' ' {
				fg = kit.Mix(fg, kit.CPane, .35)
			}
			cs = append(cs, cell{r, fg, bg})
		}
	}
	return cs
}

// diffMetrics is the code font's size and character cell, the gutter, and
// how many characters fit in w.
func (u *ui) diffMetrics(gtx layout.Context, w int) (size fixed.Int26_6, cw, chh, asc, rowH, gut, cols int) {
	size = fixed.I(gtx.Sp(12))
	cw, chh, asc = cellMetrics(u.k.Sh, kit.Mono, size, &u.diffGl)
	rowH = int(float32(chh) * 1.35)
	gut = gtx.Dp(8) + 2*cw
	cols = max((w-gut-gtx.Dp(8))/max(cw, 1), 8)
	return
}

// diffRow is one row: full width, or old beside new for '|'.
func (u *ui) diffRow(gtx layout.Context, r diffRow, lang string) layout.Dimensions {
	k := u.k
	w := gtx.Constraints.Max.X
	if r.op == 0 { // between two edits of one MultiEdit
		gtx.Constraints.Min.X = w
		return layout.Inset{Left: 10, Top: 1, Bottom: 1}.Layout(gtx, k.Lbl("⋯", 12, kit.CText3, kit.Mono))
	}
	if r.op != '|' && r.op != '=' {
		sign, sc, bg := map[byte]string{' ': " ", '-': "−", '+': "+", '~': "~"}[r.op], kit.CText3, color.NRGBA{}
		switch r.op {
		case '-':
			sc, bg = kit.CRed, diffDel
		case '+':
			sc, bg = kit.CGreen, diffAdd
		case '~':
			sc = kit.COrange
		}
		cs := u.cells(r.l, lang, r.op == ' ' || r.op == '~')
		h := u.diffSide(gtx, 0, w, cs, sign, sc, bg, u.diffHeight(gtx, w, cs))
		return layout.Dimensions{Size: image.Pt(w, h)}
	}
	// old beside new, both as tall as the taller; a side with no line is shaded
	half := w / 2
	var lc, rc []cell
	if r.l != nil {
		lc = u.cells(r.l, lang, false)
	}
	if r.r != nil {
		rc = u.cells(r.r, lang, false)
	}
	h := max(u.diffHeight(gtx, half, lc), u.diffHeight(gtx, w-half, rc))
	side := func(x, w int, segs []seg, cs []cell, sign string, sc, bg color.NRGBA) {
		switch {
		case segs == nil:
			paint.FillShape(gtx.Ops, diffNone, clip.Rect(image.Rect(x, 0, x+w, h)).Op())
		case r.op == '=': // a kept line, on both sides
			u.diffSide(gtx, x, w, u.cells(segs, lang, true), " ", kit.CText3, color.NRGBA{}, h)
		default:
			u.diffSide(gtx, x, w, cs, sign, sc, bg, h)
		}
	}
	side(0, half, r.l, lc, "−", kit.CRed, diffDel)
	side(half, w-half, r.r, rc, "+", kit.CGreen, diffAdd)
	paint.FillShape(gtx.Ops, kit.CLine, clip.Rect(image.Rect(half, 0, half+gtx.Dp(1), h)).Op())
	return layout.Dimensions{Size: image.Pt(w, h)}
}

// diffHeight is how tall cs is drawn w wide, wrapped.
func (u *ui) diffHeight(gtx layout.Context, w int, cs []cell) int {
	_, _, _, _, rowH, _, cols := u.diffMetrics(gtx, w)
	return max((len(cs)+cols-1)/cols, 1)*rowH + 2*gtx.Dp(1)
}

// diffSide draws cs from x, w wide and h tall, wrapped at its width, with
// the sign in its gutter over the row's tint; it returns h.
func (u *ui) diffSide(gtx layout.Context, x, w int, cs []cell, sign string, sc, bg color.NRGBA, h int) int {
	size, cw, chh, asc, rowH, gut, cols := u.diffMetrics(gtx, w)
	lines := max((len(cs)+cols-1)/cols, 1)
	pad := gtx.Dp(1)
	if bg.A != 0 {
		paint.FillShape(gtx.Ops, bg, clip.Rect(image.Rect(x, 0, x+w, h)).Op())
	}
	sh := u.k.Sh
	base := (rowH-chh)/2 + asc + pad
	drawRun(gtx.Ops, sh, kit.Mono, size, sign, x+gtx.Dp(8), base, cw, sc, &u.diffGl)
	for l := range lines {
		row := cs[min(l*cols, len(cs)):min((l+1)*cols, len(cs))]
		y := pad + l*rowH
		for i := 0; i < len(row); {
			j := i
			for j < len(row) && row[j].fg == row[i].fg && row[j].bg == row[i].bg {
				j++
			}
			px := x + gut + i*cw
			if row[i].bg.A != 0 {
				paint.FillShape(gtx.Ops, row[i].bg, clip.Rect(image.Rect(px, y, px+(j-i)*cw, y+rowH)).Op())
			}
			var s strings.Builder
			for _, c := range row[i:j] {
				s.WriteRune(c.r)
			}
			if t := s.String(); strings.TrimSpace(t) != "" {
				drawRun(gtx.Ops, sh, kit.Mono, size, t, px, y+(rowH-chh)/2+asc, cw, row[i].fg, &u.diffGl)
			}
			i = j
		}
	}
	return h
}

// wrote is a Write's line: its size, not its text.
func wrote(m kanban.ChatMsg) string {
	size := fmt.Sprintf("%d B", m.Bytes)
	switch {
	case m.Bytes >= 1<<20:
		size = fmt.Sprintf("%.1f MB", float64(m.Bytes)/(1<<20))
	case m.Bytes >= 1<<10:
		size = fmt.Sprintf("%.1f KB", float64(m.Bytes)/(1<<10))
	}
	return fmt.Sprintf("Wrote %s with %d lines · %s", filepath.Base(m.Path), m.Lines, size)
}
