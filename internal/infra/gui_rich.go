package infra

import (
	"image"
	"image/color"
	"math"
	"regexp"
	"strconv"
	"strings"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/x/styledtext"
	east "github.com/yuin/goldmark/extension/ast"
)

// Rich chat blocks drawn as GUI, not as terminal art: mermaid xychart-beta and
// pie as vector charts, tables with columns sized to their content, diagrams
// and ```figure pictures colored and scrolled sideways instead of wrapped.
// Colors are read at draw time so themes apply.

// seriesColor is the i-th series' color.
func seriesColor(i int) color.NRGBA {
	return []color.NRGBA{cAcc, cGreen, cOrange, cPurple, cRed, cAccText}[i%6]
}

// levelColor colors a 0–100 usage: green, orange from 70, red from 90.
func levelColor(pct float64) color.NRGBA {
	switch {
	case pct >= 90:
		return cRed
	case pct >= 70:
		return cOrange
	}
	return cGreen
}

// measure lays w out off screen: its size, and the ops to draw it later.
func measure(gtx layout.Context, w layout.Widget) (layout.Dimensions, op.CallOp) {
	m := op.Record(gtx.Ops)
	d := w(gtx)
	return d, m.Stop()
}

// text is one line of s, unconstrained.
func (u *ui) text(gtx layout.Context, s string, sp unit.Sp, c color.NRGBA, f font.Font) (layout.Dimensions, op.CallOp) {
	gtx.Constraints = layout.Constraints{Max: image.Pt(1<<16, 1<<16)}
	return measure(gtx, func(gtx layout.Context) layout.Dimensions { return u.label(gtx, s, sp, c, f, 1) })
}

// textAt draws s at x (ax: 0 left edge, .5 center, 1 right edge) with its
// vertical middle at y.
func (u *ui) textAt(gtx layout.Context, s string, sp unit.Sp, c color.NRGBA, f font.Font, x, y int, ax float32) {
	d, call := u.text(gtx, s, sp, c, f)
	st := op.Offset(image.Pt(x-int(ax*float32(d.Size.X)), y-d.Size.Y/2)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	st.Pop()
}

func (u *ui) textW(gtx layout.Context, s string, sp unit.Sp, f font.Font) int {
	d, _ := u.text(gtx, s, sp, cText, f)
	return d.Size.X
}

// chart draws a parsed chart: title, legend, then the plot.
func (u *ui) chart(gtx layout.Context, c *chart) layout.Dimensions {
	var kids []layout.FlexChild
	if c.title != "" {
		kids = append(kids, layout.Rigid(u.lbl(c.title, 13, cText, fSemi(sansFont))), layout.Rigid(vgap(8)))
	}
	if !c.pie && (len(c.series) > 1 || c.series[0].name != "") {
		kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			var items []layout.FlexChild
			for i, s := range c.series {
				name := s.name
				if name == "" {
					name = "series " + strconv.Itoa(i+1)
				}
				items = append(items, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return u.swatch(gtx, seriesColor(i), name, s.line)
				}), layout.Rigid(hgap(14)))
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx, items...)
		}), layout.Rigid(vgap(8)))
	}
	kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		switch {
		case c.pie:
			return u.pieChart(gtx, c)
		case c.useHorizontal():
			return u.barsH(gtx, c)
		}
		return u.xyChart(gtx, c)
	}))
	return boxed(cPane, cLine, rCtl, layout.UniformInset(12), func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...)
	})(gtx)
}

// useHorizontal: mermaid's horizontal keyword, or bars only with labels too
// long to sit under columns (mount points, host names).
func (c *chart) useHorizontal() bool {
	if c.horizontal {
		return true
	}
	long := false
	for _, l := range c.labels {
		long = long || len([]rune(l)) > 6
	}
	for _, s := range c.series {
		if s.line {
			return false
		}
	}
	return long && len(c.labels) <= 16
}

// bounds is the value range to plot: the y-axis range when given, else from
// zero (or the lowest negative) to the highest value.
func (c *chart) bounds() (lo, hi float64) {
	if c.ranged {
		return c.lo, c.hi
	}
	for _, s := range c.series {
		for _, v := range s.vals {
			if !math.IsNaN(v) {
				lo, hi = math.Min(lo, v), math.Max(hi, v)
			}
		}
	}
	return lo, hi
}

// percent says the values are a 0–100 usage (bars colored by level).
func (c *chart) percent() bool {
	return c.ranged && c.lo == 0 && c.hi == 100 && len(c.series) == 1 ||
		strings.Contains(c.title+c.yTitle, "%")
}

func (u *ui) swatch(gtx layout.Context, c color.NRGBA, name string, line bool) layout.Dimensions {
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			sz := image.Pt(gtx.Dp(10), gtx.Dp(10))
			r := image.Rectangle{Max: sz}
			if line {
				r = image.Rect(0, sz.Y/2-gtx.Dp(1), sz.X, sz.Y/2+gtx.Dp(1))
			}
			fillRRect(gtx.Ops, r, gtx.Dp(2), c)
			return layout.Dimensions{Size: sz}
		}),
		layout.Rigid(hgap(6)),
		layout.Rigid(u.lbl(name, 12, cText2, sansFont)))
}

// xyChart is a column chart (bar series side by side) with line series on
// top, y gridlines on round ticks and the x labels under the columns.
func (u *ui) xyChart(gtx layout.Context, c *chart) layout.Dimensions {
	W, H := gtx.Constraints.Max.X, gtx.Dp(200)
	lo, hi := c.bounds()
	lo, hi, step := niceTicks(lo, hi, 5)
	if c.ranged {
		lo, hi = c.lo, c.hi
	}
	lw := 0
	for v := lo; v <= hi+step/2; v += step {
		lw = max(lw, u.textW(gtx, fmtNum(v), 11, sansFont))
	}
	plot := image.Rect(lw+gtx.Dp(8), gtx.Dp(16), W-gtx.Dp(4), H-gtx.Dp(22))
	yOf := func(v float64) int {
		return plot.Max.Y - int(math.Round((v-lo)/(hi-lo)*float64(plot.Dy())))
	}
	for v := lo; v <= hi+step/2; v += step {
		y := yOf(v)
		lc := withAlpha(cText3, 0x30)
		if v == 0 {
			lc = withAlpha(cText3, 0x80)
		}
		paint.FillShape(gtx.Ops, lc, clip.Rect(image.Rect(plot.Min.X, y, plot.Max.X, y+gtx.Dp(1))).Op())
		u.textAt(gtx, fmtNum(v), 11, cText3, sansFont, plot.Min.X-gtx.Dp(6), y, 1)
	}
	n := len(c.labels)
	slot := float64(plot.Dx()) / float64(n)
	var bars, lines []int
	for i, s := range c.series {
		if s.line {
			lines = append(lines, i)
		} else {
			bars = append(bars, i)
		}
	}
	base := yOf(math.Max(lo, math.Min(0, hi)))
	if len(bars) > 0 {
		bw := math.Min(slot*0.7/float64(len(bars)), float64(gtx.Dp(40)))
		for i := range n {
			cx := float64(plot.Min.X) + slot*(float64(i)+.5)
			for j, si := range bars {
				vals := c.series[si].vals
				if i >= len(vals) || math.IsNaN(vals[i]) {
					continue
				}
				v := math.Max(lo, math.Min(hi, vals[i]))
				x0 := int(cx - bw*float64(len(bars))/2 + bw*float64(j))
				r := image.Rect(x0+gtx.Dp(1), min(yOf(v), base), x0+int(bw)-gtx.Dp(1), max(yOf(v), base))
				col := seriesColor(si)
				if c.percent() && len(bars) == 1 {
					col = levelColor(vals[i])
				}
				rad := min(gtx.Dp(3), r.Dx()/2)
				rr := clip.RRect{Rect: r, NW: rad, NE: rad}
				if vals[i] < 0 {
					rr = clip.RRect{Rect: r, SW: rad, SE: rad}
				}
				paint.FillShape(gtx.Ops, col, rr.Op(gtx.Ops))
				if len(bars) == 1 && len(lines) == 0 && u.textW(gtx, fmtNum(vals[i]), 10, sansFont) < int(slot) {
					u.textAt(gtx, fmtNum(vals[i]), 10, cText2, sansFont, int(cx), r.Min.Y-gtx.Dp(8), .5)
				}
			}
		}
	}
	for _, si := range lines {
		col := seriesColor(si)
		var p clip.Path
		p.Begin(gtx.Ops)
		started := false
		var pts []f32.Point
		for i, v := range c.series[si].vals {
			if i >= n || math.IsNaN(v) {
				continue
			}
			pt := f32.Pt(float32(float64(plot.Min.X)+slot*(float64(i)+.5)), float32(yOf(math.Max(lo, math.Min(hi, v)))))
			if started {
				p.LineTo(pt)
			} else {
				p.MoveTo(pt)
				started = true
			}
			pts = append(pts, pt)
		}
		paint.FillShape(gtx.Ops, col, clip.Stroke{Path: p.End(), Width: float32(gtx.Dp(2))}.Op())
		rad := gtx.Dp(3)
		for _, pt := range pts {
			paint.FillShape(gtx.Ops, col, clip.Ellipse{Min: image.Pt(int(pt.X)-rad, int(pt.Y)-rad), Max: image.Pt(int(pt.X)+rad, int(pt.Y)+rad)}.Op(gtx.Ops))
		}
	}
	every := 1 // x labels that don't fit their slot: show every k-th
	for _, l := range c.labels {
		every = max(every, int(math.Ceil(float64(u.textW(gtx, l, 11, sansFont)+gtx.Dp(6))/slot)))
	}
	for i, l := range c.labels {
		if i%every == 0 {
			u.textAt(gtx, l, 11, cText2, sansFont, int(float64(plot.Min.X)+slot*(float64(i)+.5)), plot.Max.Y+gtx.Dp(11), .5)
		}
	}
	return layout.Dimensions{Size: image.Pt(W, H)}
}

// barsH is a horizontal bar chart: label, a track with the bar, the value.
func (u *ui) barsH(gtx layout.Context, c *chart) layout.Dimensions {
	W := gtx.Constraints.Max.X
	lo, hi := c.bounds()
	if !c.ranged {
		_, hi, _ = niceTicks(lo, hi, 4)
	}
	unit := ""
	if c.percent() {
		unit = "%"
	}
	lw, vw := 0, 0
	for i, l := range c.labels {
		lw = max(lw, u.textW(gtx, l, 12, sansFont))
		for _, s := range c.series {
			if i < len(s.vals) {
				vw = max(vw, u.textW(gtx, fmtNum(s.vals[i])+unit, 12, monoFont))
			}
		}
	}
	lw = min(lw, W*2/5)
	x0, x1 := lw+gtx.Dp(12), W-vw-gtx.Dp(10)
	ns := len(c.series)
	barH := gtx.Dp(10)
	if ns > 1 {
		barH = gtx.Dp(7)
	}
	rowH := barH*ns + gtx.Dp(4)*(ns-1) + gtx.Dp(14)
	y := 0
	for i, l := range c.labels {
		mid := y + rowH/2
		u.textAt(gtx, l, 12, cText2, sansFont, 0, mid, 0)
		by := mid - (barH*ns+gtx.Dp(4)*(ns-1))/2
		for si, s := range c.series {
			if i >= len(s.vals) || math.IsNaN(s.vals[i]) {
				continue
			}
			v := s.vals[i]
			track := image.Rect(x0, by, x1, by+barH)
			fillRRect(gtx.Ops, track, barH/2, withAlpha(cText3, 0x26))
			frac := math.Max(0, math.Min(1, (v-lo)/(hi-lo)))
			col := seriesColor(si)
			if c.percent() && ns == 1 {
				col = levelColor(v)
			}
			if w := int(frac * float64(track.Dx())); w > 0 {
				fillRRect(gtx.Ops, image.Rect(x0, by, x0+max(w, barH), by+barH), barH/2, col)
			}
			u.textAt(gtx, fmtNum(v)+unit, 12, cText, monoFont, W, by+barH/2, 1)
			by += barH + gtx.Dp(4)
		}
		y += rowH
	}
	return layout.Dimensions{Size: image.Pt(W, y)}
}

// pieChart is a donut with a legend: color, label, value and share.
func (u *ui) pieChart(gtx layout.Context, c *chart) layout.Dimensions {
	W := gtx.Constraints.Max.X
	vals := c.series[0].vals
	total := 0.0
	for _, v := range vals {
		total += v
	}
	d := min(gtx.Dp(140), W*2/5)
	ctr := f32.Pt(float32(d)/2, float32(d)/2)
	ro, ri := float32(d)/2, float32(d)*0.31
	a := -math.Pi / 2
	pt := func(r float32, ang float64) f32.Point {
		return f32.Pt(ctr.X+r*float32(math.Cos(ang)), ctr.Y+r*float32(math.Sin(ang)))
	}
	for i, v := range vals {
		sweep := v / total * 2 * math.Pi
		var p clip.Path
		p.Begin(gtx.Ops)
		steps := max(2, int(sweep/(math.Pi/90)))
		p.MoveTo(pt(ro, a))
		for k := 1; k <= steps; k++ {
			p.LineTo(pt(ro, a+sweep*float64(k)/float64(steps)))
		}
		for k := steps; k >= 0; k-- {
			p.LineTo(pt(ri, a+sweep*float64(k)/float64(steps)))
		}
		p.Close()
		paint.FillShape(gtx.Ops, seriesColor(i), clip.Outline{Path: p.End()}.Op())
		if len(vals) > 1 { // a thin gap between slices
			var g clip.Path
			g.Begin(gtx.Ops)
			g.MoveTo(pt(ri-1, a))
			g.LineTo(pt(ro+1, a))
			paint.FillShape(gtx.Ops, cPane, clip.Stroke{Path: g.End(), Width: float32(gtx.Dp(2))}.Op())
		}
		a += sweep
	}
	u.textAt(gtx, fmtNum(total), 14, cText, fSemi(sansFont), d/2, d/2-gtx.Dp(6), .5)
	u.textAt(gtx, "total", 10, cText3, sansFont, d/2, d/2+gtx.Dp(9), .5)
	// legend
	lx, rowH := d+gtx.Dp(20), gtx.Dp(20)
	y := max(0, (d-rowH*len(vals))/2)
	for i, l := range c.labels {
		mid := y + rowH/2
		fillRRect(gtx.Ops, image.Rect(lx, mid-gtx.Dp(5), lx+gtx.Dp(10), mid+gtx.Dp(5)), gtx.Dp(2), seriesColor(i))
		u.textAt(gtx, l, 12, cText, sansFont, lx+gtx.Dp(16), mid, 0)
		u.textAt(gtx, fmtNum(vals[i])+"  "+strconv.FormatFloat(math.Round(vals[i]/total*1000)/10, 'f', -1, 64)+"%", 12, cText3, monoFont, W, mid, 1)
		y += rowH
	}
	return layout.Dimensions{Size: image.Pt(W, max(d, y))}
}

var (
	numCell = regexp.MustCompile(`^[-+~≈]?\$?\d[\d.,]*\s*([%x×]|[kKMGTP]i?[Bb]?|[KMGT]|ms|µs|s|m|h|d|B|/s)?$`)
	pctCell = regexp.MustCompile(`^(\d+(?:\.\d+)?)\s*%$`)
)

func plain(ss []mdSpan) string {
	var b strings.Builder
	for _, s := range ss {
		b.WriteString(s.text)
	}
	return strings.TrimSpace(b.String())
}

// table draws a markdown table: a header band, columns sized to their
// content, numbers right-aligned in mono, percentages with a usage meter.
func (u *ui) table(gtx layout.Context, b mdBlock) layout.Dimensions {
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
	cellFont := func(ri, c int) (unit.Sp, color.NRGBA, font.Font) {
		switch {
		case ri == 0:
			return 11, cText3, fSemi(sansFont)
		case numeric[c]:
			return 12, cText, monoFont
		}
		return 12, cText, sansFont
	}
	cell := func(gtx layout.Context, ri, c int) layout.Dimensions {
		if c >= len(b.rows[ri]) {
			return layout.Dimensions{}
		}
		sp, col, f := cellFont(ri, c)
		spans := b.rows[ri][c]
		if ri == 0 {
			spans = []mdSpan{{text: strings.ToUpper(plain(spans))}}
		}
		return styledtext.Text(u.sh, u.spans(spans, sp, col, f)...).Layout(gtx, nil)
	}
	nat := make([]int, nc)
	for ri := range b.rows {
		for c := range nc {
			g := gtx
			g.Constraints = layout.Constraints{Max: image.Pt(1<<16, 1<<16)}
			d, _ := measure(g, func(gtx layout.Context) layout.Dimensions { return cell(gtx, ri, c) })
			nat[c] = max(nat[c], min(d.Size.X, gtx.Dp(360))+2*padX)
		}
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
	rad := gtx.Dp(rCtl)
	panel(gtx, frame, rCtl, cPane, cLine)
	st := clip.UniformRRect(frame.Inset(gtx.Dp(1)), max(0, rad-gtx.Dp(1))).Push(gtx.Ops)
	y := 0
	for ri, row := range rows {
		h := hs[ri]
		switch {
		case ri == 0:
			paint.FillShape(gtx.Ops, cPanelCard, clip.Rect(image.Rect(0, 0, W, h)).Op())
		case ri%2 == 0:
			paint.FillShape(gtx.Ops, withAlpha(cPanelCard, 0x50), clip.Rect(image.Rect(0, y, W, y+h)).Op())
		}
		if ri > 0 {
			paint.FillShape(gtx.Ops, withAlpha(cLine, 0xc0), clip.Rect(image.Rect(0, y, W, y+gtx.Dp(1))).Op())
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
					fillRRect(gtx.Ops, track, mh/2, withAlpha(cText3, 0x30))
					if w := int(math.Min(100, pct) / 100 * float64(track.Dx())); w > 0 {
						fillRRect(gtx.Ops, image.Rect(track.Min.X, track.Min.Y, track.Min.X+w, track.Max.Y), mh/2, levelColor(pct))
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

// diagramSpans colors mermaid's text art: lines and arrows dim, words bright.
func diagramSpans(art string) []styledtext.SpanStyle {
	var out []styledtext.SpanStyle
	var cur strings.Builder
	curLine := false
	flush := func() {
		if cur.Len() > 0 {
			c := cText
			if curLine {
				c = cAccText
			}
			out = append(out, styledtext.SpanStyle{Font: monoFont, Size: 12, Color: c, Content: cur.String()})
			cur.Reset()
		}
	}
	for _, r := range art {
		isLine := (r >= 0x2500 && r <= 0x257f) || strings.ContainsRune("▲▼◄►┈→←↑↓", r)
		if r != ' ' && r != '\n' && isLine != curLine {
			flush()
			curLine = isLine
		}
		cur.WriteRune(r)
	}
	flush()
	return out
}

// figureSpans colors a ```figure block's {role:text} marks.
func figureSpans(body string) []styledtext.SpanStyle {
	role := map[string]color.NRGBA{"accent": cAccText, "warn": cRed, "ok": cGreen, "note": cAccText, "dim": cText3, "strong": cText}
	var out []styledtext.SpanStyle
	add := func(s string, c color.NRGBA, f font.Font) {
		if s != "" {
			out = append(out, styledtext.SpanStyle{Font: f, Size: 12, Color: c, Content: s})
		}
	}
	body = strings.TrimRight(body, "\n")
	last := 0
	for _, m := range roleMarkup.FindAllStringSubmatchIndex(body, -1) {
		add(body[last:m[0]], cText2, monoFont)
		f := monoFont
		if body[m[2]:m[3]] == "strong" {
			f = fBold(monoFont)
		}
		add(body[m[4]:m[5]], role[body[m[2]:m[3]]], f)
		last = m[1]
	}
	add(body[last:], cText2, monoFont)
	return out
}

// artCell is one character of a text picture.
type artCell struct {
	r    rune
	c    color.NRGBA
	bold bool
}

// artGrid splits colored spans into lines of cells.
func artGrid(spans []styledtext.SpanStyle) [][]artCell {
	grid := [][]artCell{nil}
	for _, sp := range spans {
		for _, r := range sp.Content {
			if r == '\n' {
				grid = append(grid, nil)
				continue
			}
			grid[len(grid)-1] = append(grid[len(grid)-1], artCell{r, sp.Color, sp.Font.Weight >= font.SemiBold})
		}
	}
	return grid
}

// boxArms are the arms (up, right, down, left) of the box-drawing glyphs
// artBlock draws as lines; arrowheads are drawn as triangles.
var boxArms = map[rune][4]bool{
	'─': {false, true, false, true}, '━': {false, true, false, true}, '═': {false, true, false, true}, '┈': {false, true, false, true}, '╌': {false, true, false, true},
	'│': {true, false, true, false}, '┃': {true, false, true, false}, '║': {true, false, true, false}, '┊': {true, false, true, false}, '╎': {true, false, true, false},
	'┌': {false, true, true, false}, '╭': {false, true, true, false}, '╔': {false, true, true, false}, '┏': {false, true, true, false},
	'┐': {false, false, true, true}, '╮': {false, false, true, true}, '╗': {false, false, true, true}, '┓': {false, false, true, true},
	'└': {true, true, false, false}, '╰': {true, true, false, false}, '╚': {true, true, false, false}, '┗': {true, true, false, false},
	'┘': {true, false, false, true}, '╯': {true, false, false, true}, '╝': {true, false, false, true}, '┛': {true, false, false, true},
	'├': {true, true, true, false}, '┤': {true, false, true, true}, '┬': {false, true, true, true}, '┴': {true, true, false, true}, '┼': {true, true, true, true},
	'╠': {true, true, true, false}, '╣': {true, false, true, true}, '╦': {false, true, true, true}, '╩': {true, true, false, true}, '╬': {true, true, true, true},
}

var arrowDir = map[rune]int{'▲': 0, '↑': 0, '►': 1, '▶': 1, '→': 1, '▼': 2, '↓': 2, '◄': 3, '◀': 3, '←': 3}

// artBlock draws a text picture on a character grid: box-drawing glyphs as
// continuous vector lines and arrowheads as triangles (font glyphs leave
// gaps between rows), text runs in place. It scrolls sideways when wider
// than the panel, since wrapping would break the picture.
func (u *ui) artBlock(gtx layout.Context, id string, spans []styledtext.SpanStyle) layout.Dimensions {
	l := u.hlists[id]
	if l == nil {
		l = &layout.List{Axis: layout.Horizontal}
		u.hlists[id] = l
	}
	grid := artGrid(spans)
	for len(grid) > 0 && len(grid[len(grid)-1]) == 0 {
		grid = grid[:len(grid)-1]
	}
	cw := float32(u.textW(gtx, strings.Repeat("0", 20), 12, monoFont)) / 20
	ch := gtx.Dp(18)
	cols := 0
	for _, row := range grid {
		cols = max(cols, len(row))
	}
	return boxed(cPane, cLine, rCtl, layout.UniformInset(10), func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return l.Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
			th := max(1, gtx.Dp(1))
			for y, row := range grid {
				top := y * ch
				mid := top + ch/2
				for x := 0; x < len(row); {
					c := row[x]
					x0 := int(float32(x) * cw)
					x1 := int(float32(x+1) * cw)
					cx := (x0 + x1) / 2
					if arms, ok := boxArms[c.r]; ok {
						if arms[0] {
							paint.FillShape(gtx.Ops, c.c, clip.Rect(image.Rect(cx, top, cx+th, mid+th)).Op())
						}
						if arms[2] {
							paint.FillShape(gtx.Ops, c.c, clip.Rect(image.Rect(cx, mid, cx+th, top+ch)).Op())
						}
						if arms[3] {
							paint.FillShape(gtx.Ops, c.c, clip.Rect(image.Rect(x0, mid, cx+th, mid+th)).Op())
						}
						if arms[1] {
							paint.FillShape(gtx.Ops, c.c, clip.Rect(image.Rect(cx, mid, x1, mid+th)).Op())
						}
						x++
						continue
					}
					if dir, ok := arrowDir[c.r]; ok {
						s := float32(gtx.Dp(4))
						fx, fy := float32(cx), float32(mid)
						tri := map[int][3]f32.Point{
							0: {f32.Pt(fx, fy-s), f32.Pt(fx-s, fy+s*.6), f32.Pt(fx+s, fy+s*.6)},
							1: {f32.Pt(float32(x1), fy), f32.Pt(float32(x1)-2*s, fy-s), f32.Pt(float32(x1)-2*s, fy+s)},
							2: {f32.Pt(fx, fy+s), f32.Pt(fx-s, fy-s*.6), f32.Pt(fx+s, fy-s*.6)},
							3: {f32.Pt(float32(x0), fy), f32.Pt(float32(x0)+2*s, fy-s), f32.Pt(float32(x0)+2*s, fy+s)},
						}[dir]
						if dir == 1 || dir == 3 { // → keeps its shaft
							paint.FillShape(gtx.Ops, c.c, clip.Rect(image.Rect(x0, mid, x1, mid+th)).Op())
						}
						var p clip.Path
						p.Begin(gtx.Ops)
						p.MoveTo(tri[0])
						p.LineTo(tri[1])
						p.LineTo(tri[2])
						p.Close()
						paint.FillShape(gtx.Ops, c.c, clip.Outline{Path: p.End()}.Op())
						x++
						continue
					}
					// a text run: same color and weight, up to the next line glyph
					end := x
					for end < len(row) && row[end].c == c.c && row[end].bold == c.bold {
						if _, ok := boxArms[row[end].r]; ok {
							break
						}
						if _, ok := arrowDir[row[end].r]; ok {
							break
						}
						end++
					}
					if run := strings.TrimRight(string(runesOf(row[x:end])), " "); strings.TrimSpace(run) != "" {
						f := monoFont
						if c.bold {
							f = fBold(monoFont)
						}
						u.textAt(gtx, run, 12, c.c, f, x0, mid, 0)
					}
					x = end
				}
			}
			return layout.Dimensions{Size: image.Pt(int(float32(cols)*cw)+1, len(grid)*ch)}
		})
	})(gtx)
}

func runesOf(cs []artCell) []rune {
	rs := make([]rune, len(cs))
	for i, c := range cs {
		rs[i] = c.r
	}
	return rs
}
