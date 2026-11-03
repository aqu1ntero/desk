package infra

import (
	"bytes"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"charm.land/glamour/v2"
	gansi "charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/AlexanderGrooff/mermaid-ascii/pkg/diagram"
	"github.com/AlexanderGrooff/mermaid-ascii/pkg/render"
	"github.com/alecthomas/chroma/v2/quick"
	"github.com/charmbracelet/x/ansi"
)

// Claude's replies, drawn after prismantis (github.com/NahumLitvin/prismantis):
// themed prose and highlighted code, tables, mermaid diagrams and charts,
// ```figure text pictures, GitHub alerts and LaTeX math, with copy buttons,
// and tables/diagrams side by side when they fit.

// guiHint is added to prettyHint when the replies go to desk's window.
const guiHint = `This user reads you in desk's window, not a terminal: xychart-beta (bar and line; add "horizontal"
after xychart-beta for long labels, a y-axis range like 0 --> 100 for percentages) and pie charts are drawn as real
charts, tables get right-aligned numeric columns and a usage meter under cells like "83%", diagrams scroll sideways
instead of being cut. Prefer a chart or table over figure art for numbers.`

// guiReplies says the replies go to the GUI (set by guiMain).
var guiReplies bool

// prettyHint is what Claude is told about them (system prompt).
const prettyHint = `Your replies are drawn by desk in a chat panel; the user's message says how many columns it has.
Markdown tables, GitHub alerts (> [!NOTE], > [!TIP], > [!IMPORTANT], > [!WARNING], > [!CAUTION]), fenced code
with a language tag, ` + "```mermaid" + ` and ` + "```figure" + ` blocks render as colored terminal graphics, and
$$…$$ or ` + "```math" + ` LaTeX formulas render as Unicode math. Mermaid draws flowchart/graph, sequenceDiagram,
erDiagram and xychart-beta bar or line charts. Back-to-back tables and diagrams sit side by side when they fit.
Every table, code block and picture gets a copy button. When a reply carries a numeric series (metrics, disk,
load over time) or a flow that is easier to see than read, add one small chart or diagram with short labels.
Skip pictures for short or simple answers. Keep diagrams narrow: flowchart TD, at most 3 nodes side by side,
labels under 20 characters; sequence diagrams with at most 4 one-word participants.
A ` + "```figure" + ` block is a text picture drawn as written: use it when position carries meaning (timelines,
layouts, before/after). Color with {role:text}, not nested: accent for lines, warn for risk, ok for healthy,
note for time, dim for asides, strong for emphasis. Glyphs one column wide (─│┌┐└┘├┤┬┴┼→←▲▼✓✗●○█▏·), no emoji
or tabs, no wider than the panel; markup takes no columns. The prose never retells the picture.
Use warnings and cautions as alerts, before the step they apply to.

Write all prose following ASD-STE100 Simplified Technical English:
- One topic per sentence. Procedural sentences: 20 words or fewer. Descriptive sentences: 25 words or fewer.
  Paragraphs: 6 sentences or fewer.
- Instructions in the imperative, one instruction per sentence. Put a condition first ("If X, do Y").
- Active voice. Simple tenses (present, past, future). No -ing forms as nouns or adjectives.
- Use one word for one meaning and the same term for the same thing every time. No synonyms for variety.
- Use simple, common words; technical names only when necessary. No phrasal verbs ("start", not "fire up").
- Keep articles (the, a) and do not drop words to be brief. No noun clusters of more than 3 words.
- Use vertical lists for sequential steps and for several items. Start a warning with a clear command, then
  give the risk.
If the user writes in a language other than English, answer in that language and keep these same rules.`

// dim is muted text in the terminal renderings.
var dim = lipgloss.NewStyle().Foreground(lipgloss.Color("#8b93a1"))

// fit pads or cuts s to w columns.
func fit(s string, w int) string {
	if ansi.StringWidth(s) > w {
		s = ansi.Truncate(s, w, "…")
	}
	return s + "\x1b[m" + strings.Repeat(" ", max(0, w-ansi.StringWidth(s)))
}

// spread puts left and right at the two ends of w columns.
func spread(left, right string, w int) string {
	gap := w - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		return fit(left, w)
	}
	return left + strings.Repeat(" ", gap) + right
}

// ---- theme

var (
	themeName = "desk"
	pal       = themes["desk"]
	mdMu      sync.Mutex
	mdCache   = map[int]*glamour.TermRenderer{}
)

// setTheme switches the reply palette; false for an unknown name.
func setTheme(name string) bool {
	p, ok := themes[name]
	if !ok {
		return false
	}
	mdMu.Lock()
	themeName, pal, mdCache = name, p, map[int]*glamour.TermRenderer{}
	mdMu.Unlock()
	return true
}

// fg is a style in a palette color; mono palettes have none.
func fg(c string) lipgloss.Style {
	if c == "" {
		return lipgloss.NewStyle()
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(c))
}

// codeBack is the code block background, as a style and as the raw SGR
// sequence put back after chroma's resets.
func codeBack() (lipgloss.Style, string) {
	if pal.Light {
		return lipgloss.NewStyle().Background(lipgloss.Color("#eef0f3")), "\x1b[48;2;238;240;243m"
	}
	return lipgloss.NewStyle().Background(lipgloss.Color("#161b22")), "\x1b[48;2;22;27;34m"
}

func prose(s string, w int) string {
	mdMu.Lock()
	r, ok := mdCache[w]
	if !ok {
		var st gansi.StyleConfig
		switch {
		case themeName == "mono":
			st = styles.NoTTYStyleConfig
		case pal.Light:
			st = styles.LightStyleConfig
		default:
			st = styles.DarkStyleConfig
		}
		zero := uint(0)
		st.Document.Margin, st.Document.BlockPrefix, st.Document.BlockSuffix = &zero, "", ""
		st.CodeBlock.Margin = &zero
		if themeName != "mono" {
			set := func(p *gansi.StylePrimitive, c string) { c2 := c; p.Color = &c2 }
			for _, h := range []*gansi.StyleBlock{&st.Heading, &st.H2, &st.H3, &st.H4, &st.H5, &st.H6} {
				set(&h.StylePrimitive, pal.Heading)
			}
			bg := pal.Heading
			st.H1.BackgroundColor = &bg
			set(&st.Strong, pal.Strong)
			set(&st.Emph, pal.Emphasis)
			set(&st.Code.StylePrimitive, pal.InlineCode)
			set(&st.Link, pal.Link)
			set(&st.LinkText, pal.Link)
			set(&st.BlockQuote.StylePrimitive, pal.Quote)
			set(&st.HorizontalRule, pal.Rule)
			set(&st.Item, pal.Bullet)
			set(&st.Enumeration, pal.Bullet)
		}
		r, _ = glamour.NewTermRenderer(glamour.WithStyles(st), glamour.WithWordWrap(w), glamour.WithPreservedNewLines())
		mdCache[w] = r
	}
	mdMu.Unlock()
	if r == nil {
		return s
	}
	out, err := r.Render(s)
	if err != nil {
		return s
	}
	return strings.Trim(out, "\n")
}

// ---- layout: pieces with copy buttons

// target is a copy button: a click on cells [col, col+w) of line copies text.
type target struct {
	line, col, w int
	text         string
}

// piece is one block of a reply. Visual pieces (tables, diagrams, pictures,
// math) may sit side by side.
type piece struct {
	lines  []string
	tgts   []target
	visual bool
}

func (p piece) width() int {
	w := 0
	for _, l := range p.lines {
		w = max(w, ansi.StringWidth(l))
	}
	return w
}

// header is a block's first line: a label on the left, a copy button on the
// right that copies text.
func header(label, button, text string, w int, bg *lipgloss.Style) ([]string, []target) {
	btn := "⧉ " + button
	bw := ansi.StringWidth(btn)
	line := spread(label, fg(pal.Accent).Render(btn), w)
	if bg != nil {
		line = bg.Render(fit(line, w))
	}
	return []string{line}, []target{{line: 0, col: w - bw, w: bw, text: text}}
}

// markdown renders a reply at width w, with its copy buttons.
func markdown(s string, w int) (string, []target) {
	var pieces []piece
	add := func(p piece) { pieces = append(pieces, p) }
	text := func(txt string) { // prose, with tables, alerts and $$math$$ drawn apart
		for _, seg := range splitProse(txt) {
			switch seg.kind {
			case "table":
				add(tablePiece(seg.text, w))
			case "alert":
				add(piece{lines: strings.Split(alertBlock(seg.arg, seg.text, w), "\n")})
			case "math":
				add(mathPiece(seg.text, w))
			default:
				if t := strings.TrimSpace(seg.text); t != "" {
					add(piece{lines: strings.Split(prose(t, w), "\n")})
				}
			}
		}
	}
	last := 0
	for _, m := range fence.FindAllStringSubmatchIndex(s, -1) { // m[2:4] language, m[4:6] body
		text(s[last:m[0]])
		last = m[1]
		lang, body := strings.TrimSpace(s[m[2]:m[3]]), dedent(s[m[4]:m[5]])
		switch lang {
		case "figure":
			add(artPiece("figure", figure(body, w), w))
			continue
		case "mermaid":
			if d, ok := mermaid(body, w); ok {
				add(artPiece("diagram", d, w))
				continue
			}
		case "math", "latex", "tex":
			add(mathPiece(body, w))
			continue
		}
		add(codePiece(lang, body, w))
	}
	text(s[last:])
	return stack(pieces, w)
}

// stack piles pieces with a blank line between them; runs of visual pieces
// that fit go side by side, 2 columns apart.
func stack(pieces []piece, w int) (string, []target) {
	var rows []piece
	for i := 0; i < len(pieces); {
		row := pieces[i]
		j := i + 1
		for ; row.visual && j < len(pieces) && pieces[j].visual && row.width()+2+pieces[j].width() <= w; j++ {
			row = beside(row, pieces[j])
		}
		rows = append(rows, row)
		i = j
	}
	var lines []string
	var tgts []target
	for i, r := range rows {
		if i > 0 {
			lines = append(lines, "")
		}
		for _, t := range r.tgts {
			t.line += len(lines)
			tgts = append(tgts, t)
		}
		lines = append(lines, r.lines...)
	}
	return strings.Join(lines, "\n"), tgts
}

func beside(a, b piece) piece {
	aw, off := a.width(), a.width()+2
	n := max(len(a.lines), len(b.lines))
	out := piece{visual: true, tgts: a.tgts}
	for i := 0; i < n; i++ {
		l, r := "", ""
		if i < len(a.lines) {
			l = a.lines[i]
		}
		if i < len(b.lines) {
			r = b.lines[i]
		}
		out.lines = append(out.lines, fit(l, aw)+"  "+r)
	}
	for _, t := range b.tgts {
		t.col += off
		out.tgts = append(out.tgts, t)
	}
	return out
}

// ---- prose segments

type segment struct{ kind, arg, text string }

var (
	alert     = regexp.MustCompile(`^> ?\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]`)
	tableSep  = regexp.MustCompile(`^\s*\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$`)
	mathBlock = regexp.MustCompile(`(?s)\$\$(.+?)\$\$`)
)

// splitProse cuts tables, alerts and $$math$$ out of a prose run.
func splitProse(txt string) []segment {
	var segs []segment
	var buf []string
	flush := func() {
		if len(buf) > 0 {
			segs = append(segs, segment{kind: "prose", text: strings.Join(buf, "\n")})
			buf = nil
		}
	}
	lines := strings.Split(mathBlock.ReplaceAllStringFunc(txt, func(m string) string {
		return "\x00math" + strings.ReplaceAll(m[2:len(m)-2], "\n", " ") + "\n"
	}), "\n")
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		switch {
		case strings.HasPrefix(l, "\x00math"):
			flush()
			segs = append(segs, segment{kind: "math", text: strings.TrimPrefix(l, "\x00math")})
		case strings.HasPrefix(strings.TrimSpace(l), "|") && i+1 < len(lines) && tableSep.MatchString(lines[i+1]):
			flush()
			j := i + 2
			for j < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[j]), "|") {
				j++
			}
			segs = append(segs, segment{kind: "table", text: strings.Join(lines[i:j], "\n")})
			i = j - 1
		case alert.MatchString(l):
			flush()
			kind := alert.FindStringSubmatch(l)[1]
			j := i + 1
			for j < len(lines) && strings.HasPrefix(lines[j], ">") {
				j++
			}
			segs = append(segs, segment{kind: "alert", arg: kind, text: strings.Join(lines[i+1:j], "\n")})
			i = j - 1
		default:
			buf = append(buf, l)
		}
	}
	flush()
	return segs
}

// ---- code

// codePiece draws a fenced block with its language, highlighted in the
// theme's code style, and a copy button.
func codePiece(lang, body string, w int) piece {
	bg, bgSeq := codeBack()
	label := fg(pal.Accent).Render("▌ ") + dim.Render(map[bool]string{true: "code", false: lang}[lang == ""])
	lines, tgts := header(label, "copy", body, w, &bg)
	hl := body
	if lang != "" {
		var b bytes.Buffer
		if quick.Highlight(&b, body, lang, "terminal16m", pal.Chroma) == nil {
			hl = b.String()
		}
	}
	// chroma resets attributes after each token; put the block's background back
	for _, ln := range strings.Split(strings.TrimRight(hl, "\n"), "\n") {
		ln = strings.ReplaceAll(ln, "\x1b[0m", "\x1b[0m"+bgSeq)
		for _, wl := range strings.Split(ansi.Hardwrap(ln, w-2, true), "\n") {
			lines = append(lines, bg.Render(fg(pal.Accent).Render("▌ ")+bgSeq+wl+"\x1b[0m"+bgSeq+strings.Repeat(" ", max(0, w-2-ansi.StringWidth(wl)))))
		}
	}
	return piece{lines: lines, tgts: tgts}
}

// ---- tables

var numeric = regexp.MustCompile(`^[-+]?[\d.,]+\s*(%|[kKMGTP]i?B?|ms|s|m|h)?$`)

func cells(row string) []string {
	row = strings.TrimSpace(row)
	row = strings.TrimSuffix(strings.TrimPrefix(row, "|"), "|")
	var out []string
	var cur strings.Builder
	for i := 0; i < len(row); i++ {
		switch {
		case row[i] == '\\' && i+1 < len(row) && row[i+1] == '|': // \| is a literal pipe
			cur.WriteByte('|')
			i++
		case row[i] == '|':
			out = append(out, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(row[i])
		}
	}
	return append(out, strings.TrimSpace(cur.String()))
}

var inlineCode = regexp.MustCompile("`([^`]+)`")
var boldText = regexp.MustCompile(`\*\*([^*]+)\*\*`)

// cellText draws a cell's inline markdown (`code`, **bold**).
func cellText(s string) string {
	s = inlineCode.ReplaceAllStringFunc(s, func(m string) string { return fg(pal.InlineCode).Render(m[1 : len(m)-1]) })
	return boldText.ReplaceAllStringFunc(s, func(m string) string { return fg(pal.Strong).Bold(true).Render(m[2 : len(m)-2]) })
}

// tablePiece draws a markdown table: theme header and rules, column
// alignment, numbers in the number color, sized to the panel.
func tablePiece(md string, w int) piece {
	rows := strings.Split(strings.TrimSpace(md), "\n")
	head := cells(rows[0])
	var aligns []lipgloss.Position
	for _, c := range cells(rows[1]) {
		a := lipgloss.Left
		switch {
		case strings.HasPrefix(c, ":") && strings.HasSuffix(c, ":"):
			a = lipgloss.Center
		case strings.HasSuffix(c, ":"):
			a = lipgloss.Right
		}
		aligns = append(aligns, a)
	}
	var body [][]string
	raw := map[[2]int]string{}
	for r, l := range rows[2:] {
		cs := cells(l)
		for c := range cs {
			raw[[2]int{r, c}] = cs[c]
			cs[c] = cellText(cs[c])
		}
		body = append(body, cs)
	}
	for i := range head {
		head[i] = cellText(head[i])
	}
	// lipgloss aligns only fixed-width cells: pad each column to its widest cell
	for c, a := range aligns {
		cw := 0
		for _, r := range append([][]string{head}, body...) {
			if c < len(r) {
				cw = max(cw, ansi.StringWidth(r[c]))
			}
		}
		for _, r := range append([][]string{head}, body...) {
			if c < len(r) {
				r[c] = lipgloss.PlaceHorizontal(cw, a, r[c])
			}
		}
	}
	t := table.New().Headers(head...).Rows(body...).Border(lipgloss.RoundedBorder()).
		BorderStyle(fg(pal.TableRule)).StyleFunc(func(row, col int) lipgloss.Style {
		st := lipgloss.NewStyle().Padding(0, 1)
		if row == table.HeaderRow {
			return st.Inherit(fg(pal.TableHeader)).Bold(true)
		}
		if numeric.MatchString(raw[[2]int{row, col}]) {
			return st.Inherit(fg(pal.Number))
		}
		return st
	})
	out := t.Render()
	if ansi.StringWidth(strings.SplitN(out, "\n", 2)[0]) > w {
		out = t.Width(w).Render()
	}
	tw := ansi.StringWidth(strings.SplitN(out, "\n", 2)[0])
	lines, tgts := header(dim.Render("table"), "md", md, tw, nil)
	return piece{lines: append(lines, strings.Split(out, "\n")...), tgts: tgts, visual: true}
}

// ---- pictures

// artPiece is a drawn picture with a button copying it as plain text.
func artPiece(label, art string, w int) piece {
	aw := 0
	for _, l := range strings.Split(art, "\n") {
		aw = max(aw, ansi.StringWidth(l))
	}
	lines, tgts := header(dim.Render(label), "art", ansi.Strip(art), max(aw, 16), nil)
	return piece{lines: append(lines, strings.Split(art, "\n")...), tgts: tgts, visual: true}
}

func roleStyle(role string) lipgloss.Style {
	switch role {
	case "accent":
		return fg(pal.Accent)
	case "warn":
		return fg(pal.DiffRemoved)
	case "ok":
		return fg(pal.DiffAdded)
	case "note":
		return fg(pal.Link)
	case "dim":
		return fg(pal.Quote)
	}
	return fg(pal.Strong).Bold(true) // strong
}

var roleMarkup = regexp.MustCompile(`\{(accent|warn|ok|note|dim|strong):([^{}]*)\}`)

// figure draws a ```figure block as written, coloring {role:text}.
func figure(body string, w int) string {
	var out []string
	for _, ln := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		ln = roleMarkup.ReplaceAllStringFunc(ln, func(m string) string {
			p := roleMarkup.FindStringSubmatch(m)
			return roleStyle(p[1]).Render(p[2])
		})
		out = append(out, ansi.Truncate(ln, w, "…"))
	}
	return strings.Join(out, "\n")
}

var mermaidCfg = func() *diagram.Config {
	c := diagram.DefaultConfig()
	c.BoxBorderPadding, c.PaddingBetweenX, c.PaddingBetweenY = 0, 3, 2
	return c
}()

// mermaid draws a diagram as text: lines in the diagram color, text in the
// diagram text color. ok=false: it couldn't, and the caller shows the source.
func mermaid(src string, w int) (string, bool) {
	if strings.HasPrefix(strings.TrimSpace(src), "xychart") {
		return xychart(src, w)
	}
	out, err := render.RenderDiagram(src, mermaidCfg)
	if err != nil || strings.TrimSpace(out) == "" {
		return "", false
	}
	line, txt := fg(pal.Diagram), fg(pal.DiagramText)
	var lines []string
	for _, ln := range strings.Split(strings.TrimRight(out, "\n "), "\n") {
		ln = strings.TrimRight(ln, " ")
		if ansi.StringWidth(ln) > w {
			return "", false // too wide for the panel: the source reads better than a cut picture
		}
		var b strings.Builder
		for _, r := range ln {
			if (r >= 0x2500 && r <= 0x257f) || strings.ContainsRune("▲▼◄►┈", r) {
				b.WriteString(line.Render(string(r)))
			} else {
				b.WriteString(txt.Render(string(r)))
			}
		}
		lines = append(lines, b.String())
	}
	return strings.Join(lines, "\n"), true
}

var (
	xyTitle  = regexp.MustCompile(`(?m)^\s*title\s+"?([^"\n]*)"?`)
	xyXAxis  = regexp.MustCompile(`(?m)^\s*x-axis\s+(?:"[^"]*"\s*)?\[([^\]]*)\]`)
	xySeries = regexp.MustCompile(`(?m)^\s*(bar|line)\s+(?:"[^"]*"\s*)?\[([^\]]*)\]`)
	xyYRange = regexp.MustCompile(`(?m)^\s*y-axis\b[^\n]*?(-?[\d.]+)\s*-->\s*(-?[\d.]+)`)
)

// xychart draws xychart-beta as one row per x value: █ bars for bar series,
// ───● for line series, scaled to the y-axis range when one is given.
func xychart(src string, w int) (string, bool) {
	xs := xyXAxis.FindStringSubmatch(src)
	series := xySeries.FindAllStringSubmatch(src, -1)
	if xs == nil || len(series) == 0 {
		return "", false
	}
	var labels []string
	for _, l := range strings.Split(xs[1], ",") {
		labels = append(labels, strings.Trim(strings.TrimSpace(l), `"`))
	}
	var out []string
	if t := xyTitle.FindStringSubmatch(src); t != nil {
		out = append(out, fg(pal.Heading).Bold(true).Render(strings.TrimSpace(t[1])))
	}
	lw := 0
	for _, l := range labels {
		lw = max(lw, ansi.StringWidth(l))
	}
	lw = min(lw, w/3)
	for _, s := range series {
		var vs []float64
		for _, v := range strings.Split(s[2], ",") {
			f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err != nil {
				f = math.NaN()
			}
			vs = append(vs, f)
		}
		lo, hi := 0.0, 0.0
		for _, v := range vs {
			if !math.IsNaN(v) {
				hi = math.Max(hi, v)
			}
		}
		if r := xyYRange.FindStringSubmatch(src); r != nil {
			lo, _ = strconv.ParseFloat(r[1], 64)
			if h, err := strconv.ParseFloat(r[2], 64); err == nil && h > lo {
				hi = h
			}
		}
		barW := max(4, min(40, w-lw-12)) // compact, so it can sit beside a table
		for i, v := range vs {
			label := ""
			if i < len(labels) {
				label = labels[i]
			}
			n := 0
			if hi > lo && !math.IsNaN(v) {
				n = max(0, min(barW, int(math.Round((v-lo)/(hi-lo)*float64(barW)))))
			}
			mark := strings.Repeat("█", n) + "▏"
			if s[1] == "line" {
				mark = strings.Repeat("─", n) + "●"
			}
			out = append(out, fmt.Sprintf("%s %s %s", fit(dim.Render(label), lw), fg(pal.Diagram).Render(mark),
				fg(pal.Number).Render(strconv.FormatFloat(v, 'g', 4, 64))))
		}
	}
	return strings.Join(out, "\n"), true
}

// chart is a mermaid xychart-beta or pie the GUI draws as a real chart.
type chart struct {
	pie, horizontal bool
	title, yTitle   string
	labels          []string
	series          []chartSeries
	lo, hi          float64 // the y-axis range, when ranged
	ranged          bool
}

type chartSeries struct {
	line bool
	name string
	vals []float64
}

var (
	xySeriesNamed = regexp.MustCompile(`(?m)^\s*(bar|line)\s+(?:"([^"]*)"\s*)?\[([^\]]*)\]`)
	xyYTitle      = regexp.MustCompile(`(?m)^\s*y-axis\s+"([^"]*)"`)
	pieSlice      = regexp.MustCompile(`(?m)^\s*"([^"]+)"\s*:\s*(-?[\d.]+)`)
)

// parseChart reads xychart-beta (bar and line series, horizontal, a y-axis
// range) and pie; ok=false for any other diagram.
func parseChart(src string) (chart, bool) {
	first, _, _ := strings.Cut(strings.TrimSpace(src), "\n")
	var c chart
	if t := xyTitle.FindStringSubmatch(src); t != nil {
		c.title = strings.TrimSpace(t[1])
	}
	switch fs := strings.Fields(first); {
	case len(fs) > 0 && fs[0] == "pie":
		c.pie = true
		if _, t, ok := strings.Cut(first, "title"); ok && c.title == "" {
			c.title = strings.Trim(strings.TrimSpace(t), `"`)
		}
		s := chartSeries{}
		for _, m := range pieSlice.FindAllStringSubmatch(src, -1) {
			if v, err := strconv.ParseFloat(m[2], 64); err == nil && v > 0 {
				c.labels, s.vals = append(c.labels, m[1]), append(s.vals, v)
			}
		}
		c.series = []chartSeries{s}
		return c, len(s.vals) > 0
	case len(fs) > 0 && strings.HasPrefix(fs[0], "xychart"):
		c.horizontal = slices.Contains(fs[1:], "horizontal")
	default:
		return c, false
	}
	if xs := xyXAxis.FindStringSubmatch(src); xs != nil {
		for _, l := range strings.Split(xs[1], ",") {
			c.labels = append(c.labels, strings.Trim(strings.TrimSpace(l), `"`))
		}
	}
	if t := xyYTitle.FindStringSubmatch(src); t != nil {
		c.yTitle = t[1]
	}
	if r := xyYRange.FindStringSubmatch(src); r != nil {
		lo, err1 := strconv.ParseFloat(r[1], 64)
		hi, err2 := strconv.ParseFloat(r[2], 64)
		c.lo, c.hi, c.ranged = lo, hi, err1 == nil && err2 == nil && hi > lo
	}
	n := 0
	for _, m := range xySeriesNamed.FindAllStringSubmatch(src, -1) {
		s := chartSeries{line: m[1] == "line", name: m[2]}
		for _, v := range strings.Split(m[3], ",") {
			f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err != nil {
				f = math.NaN()
			}
			s.vals = append(s.vals, f)
		}
		n = max(n, len(s.vals))
		c.series = append(c.series, s)
	}
	for len(c.labels) < n {
		c.labels = append(c.labels, strconv.Itoa(len(c.labels)+1))
	}
	return c, n > 0
}

// fitColumns sizes columns from their natural widths: all of them when they
// fit (the spare spread in proportion), else narrow columns keep their width
// and the wide ones share what's left (they wrap).
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

// fmtNum prints a chart value short: 83, 1.5, 0.25, 12.3k.
func fmtNum(v float64) string {
	switch a := math.Abs(v); {
	case math.IsNaN(v):
		return ""
	case a >= 1e6:
		return strconv.FormatFloat(math.Round(v/1e5)/10, 'f', -1, 64) + "M"
	case a >= 1e4:
		return strconv.FormatFloat(math.Round(v/100)/10, 'f', -1, 64) + "k"
	case a >= 100 || v == math.Trunc(v):
		return strconv.FormatFloat(math.Round(v), 'f', -1, 64)
	}
	return strconv.FormatFloat(math.Round(v*100)/100, 'f', -1, 64)
}

// niceTicks spans [lo, hi] with about n round steps (1, 2, 5 × 10^k).
func niceTicks(lo, hi float64, n int) (from, to, step float64) {
	if hi <= lo {
		hi = lo + 1
	}
	raw := (hi - lo) / float64(n)
	mag := math.Pow(10, math.Floor(math.Log10(raw)))
	switch norm := raw / mag; {
	case norm < 1.5:
		step = mag
	case norm < 3:
		step = 2 * mag
	case norm < 7:
		step = 5 * mag
	default:
		step = 10 * mag
	}
	return math.Floor(lo/step) * step, math.Ceil(hi/step) * step, step
}

// ---- alerts (keep their own colors in every theme, like prismantis)

var alertLook = map[string]struct {
	icon  string
	style lipgloss.Style
}{
	"NOTE": {"ℹ Note", fg("#5fb3c9")}, "TIP": {"✓ Tip", fg("#79c08b")}, "IMPORTANT": {"◆ Important", fg("#c678dd")},
	"WARNING": {"▲ Warning", fg("#e5c07b")}, "CAUTION": {"✗ Caution", fg("#e06c75")},
}

// alertBlock draws a GitHub alert as a colored bar with a title.
func alertBlock(kind, body string, w int) string {
	look := alertLook[kind]
	var text []string
	for _, ln := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		text = append(text, strings.TrimPrefix(strings.TrimPrefix(ln, ">"), " "))
	}
	bar := look.style.Render("▌ ")
	lines := []string{bar + look.style.Bold(true).Render(look.icon)}
	for _, ln := range strings.Split(prose(strings.Join(text, "\n"), w-2), "\n") {
		lines = append(lines, bar+ln)
	}
	return strings.Join(lines, "\n")
}

// ---- math

// mathPiece draws a LaTeX formula as Unicode math, centered, with a button
// copying the LaTeX source.
// ponytail: Unicode text, not typeset images (prismantis uses RaTeX + kitty
// graphics); fractions go inline as a/b.
func mathPiece(tex string, w int) piece {
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(tex), "\\\\") {
		u := texToUnicode(strings.TrimSpace(l))
		lines = append(lines, fg(pal.Emphasis).Render(u))
	}
	mw := 16
	for _, l := range lines {
		mw = max(mw, ansi.StringWidth(l)+4)
	}
	mw = min(mw, w)
	hl, tgts := header(dim.Render("math"), "tex", strings.TrimSpace(tex), mw, nil)
	for _, l := range lines {
		hl = append(hl, lipgloss.PlaceHorizontal(mw, lipgloss.Center, l))
	}
	return piece{lines: hl, tgts: tgts, visual: true}
}

var texSymbols = map[string]string{
	"alpha": "α", "beta": "β", "gamma": "γ", "delta": "δ", "epsilon": "ε", "varepsilon": "ε", "zeta": "ζ", "eta": "η",
	"theta": "θ", "iota": "ι", "kappa": "κ", "lambda": "λ", "mu": "μ", "nu": "ν", "xi": "ξ", "pi": "π", "rho": "ρ",
	"sigma": "σ", "tau": "τ", "upsilon": "υ", "phi": "φ", "varphi": "φ", "chi": "χ", "psi": "ψ", "omega": "ω",
	"Gamma": "Γ", "Delta": "Δ", "Theta": "Θ", "Lambda": "Λ", "Xi": "Ξ", "Pi": "Π", "Sigma": "Σ", "Phi": "Φ",
	"Psi": "Ψ", "Omega": "Ω", "cdot": "·", "times": "×", "div": "÷", "pm": "±", "mp": "∓", "leq": "≤", "le": "≤",
	"geq": "≥", "ge": "≥", "neq": "≠", "ne": "≠", "approx": "≈", "equiv": "≡", "sim": "∼", "propto": "∝",
	"infty": "∞", "sum": "∑", "prod": "∏", "int": "∫", "oint": "∮", "partial": "∂", "nabla": "∇", "to": "→",
	"rightarrow": "→", "leftarrow": "←", "Rightarrow": "⇒", "Leftarrow": "⇐", "leftrightarrow": "↔",
	"Leftrightarrow": "⇔", "mapsto": "↦", "in": "∈", "notin": "∉", "subset": "⊂", "subseteq": "⊆", "supset": "⊃",
	"cup": "∪", "cap": "∩", "emptyset": "∅", "forall": "∀", "exists": "∃", "neg": "¬", "land": "∧", "lor": "∨",
	"ldots": "…", "cdots": "⋯", "dots": "…", "degree": "°", "circ": "∘", "log": "log", "ln": "ln", "exp": "exp",
	"sin": "sin", "cos": "cos", "tan": "tan", "max": "max", "min": "min", "lim": "lim", "%": "%", ",": " ", ";": " ",
	"quad": "  ", "qquad": "    ", "!": "", " ": " ", "{": "{", "}": "}", "left": "", "right": "", "big": "", "Big": "",
}

var (
	superDigits = map[rune]rune{'0': '⁰', '1': '¹', '2': '²', '3': '³', '4': '⁴', '5': '⁵', '6': '⁶', '7': '⁷', '8': '⁸', '9': '⁹',
		'+': '⁺', '-': '⁻', '=': '⁼', '(': '⁽', ')': '⁾', 'n': 'ⁿ', 'i': 'ⁱ', 'x': 'ˣ', 'k': 'ᵏ', 'T': 'ᵀ'}
	subDigits = map[rune]rune{'0': '₀', '1': '₁', '2': '₂', '3': '₃', '4': '₄', '5': '₅', '6': '₆', '7': '₇', '8': '₈', '9': '₉',
		'+': '₊', '-': '₋', '=': '₌', '(': '₍', ')': '₎', 'a': 'ₐ', 'e': 'ₑ', 'o': 'ₒ', 'x': 'ₓ', 'h': 'ₕ', 'k': 'ₖ',
		'l': 'ₗ', 'm': 'ₘ', 'n': 'ₙ', 'p': 'ₚ', 's': 'ₛ', 't': 'ₜ', 'i': 'ᵢ', 'j': 'ⱼ'}
	blackboard = map[string]string{"R": "ℝ", "N": "ℕ", "Z": "ℤ", "Q": "ℚ", "C": "ℂ"}
)

// texToUnicode turns common LaTeX into Unicode text.
func texToUnicode(s string) string {
	var out strings.Builder
	rs := []rune(s)
	i := 0
	group := func() string { // the next {…} group or single token, converted
		for i < len(rs) && rs[i] == ' ' {
			i++
		}
		if i >= len(rs) {
			return ""
		}
		if rs[i] == '{' {
			depth, start := 1, i+1
			for i++; i < len(rs) && depth > 0; i++ {
				switch rs[i] {
				case '{':
					depth++
				case '}':
					depth--
				}
			}
			return texToUnicode(string(rs[start : i-1]))
		}
		if rs[i] == '\\' {
			j := i + 1
			for j < len(rs) && ((rs[j] >= 'a' && rs[j] <= 'z') || (rs[j] >= 'A' && rs[j] <= 'Z')) {
				j++
			}
			if j == i+1 && j < len(rs) {
				j++
			}
			tok := string(rs[i:j])
			i = j
			return texToUnicode(tok)
		}
		i++
		return string(rs[i-1])
	}
	script := func(g string, table map[rune]rune, mark string) string {
		var b strings.Builder
		for _, r := range g {
			c, ok := table[r]
			if !ok {
				if len([]rune(g)) == 1 {
					return mark + g
				}
				return mark + "(" + g + ")"
			}
			b.WriteRune(c)
		}
		return b.String()
	}
	for i < len(rs) {
		r := rs[i]
		switch r {
		case '\\':
			j := i + 1
			for j < len(rs) && ((rs[j] >= 'a' && rs[j] <= 'z') || (rs[j] >= 'A' && rs[j] <= 'Z')) {
				j++
			}
			if j == i+1 && j < len(rs) {
				j++ // \, \; \% \{
			}
			cmd := string(rs[i+1 : j])
			i = j
			switch cmd {
			case "frac", "dfrac", "tfrac":
				a, b := group(), group()
				wrap := func(x string) string {
					if strings.ContainsAny(x, " +-") {
						return "(" + x + ")"
					}
					return x
				}
				out.WriteString(wrap(a) + "/" + wrap(b))
			case "sqrt":
				g := group()
				if len([]rune(g)) > 1 {
					g = "(" + g + ")"
				}
				out.WriteString("√" + g)
			case "text", "mathrm", "mathbf", "mathit", "operatorname", "textbf", "mathsf":
				out.WriteString(group())
			case "mathbb":
				g := group()
				if b, ok := blackboard[g]; ok {
					g = b
				}
				out.WriteString(g)
			default:
				if sym, ok := texSymbols[cmd]; ok {
					out.WriteString(sym)
				} else {
					out.WriteString(cmd)
				}
			}
		case '^':
			i++
			out.WriteString(script(group(), superDigits, "^"))
		case '_':
			i++
			out.WriteString(script(group(), subDigits, "_"))
		case '{', '}':
			i++
		default:
			out.WriteRune(r)
			i++
		}
	}
	return out.String()
}
