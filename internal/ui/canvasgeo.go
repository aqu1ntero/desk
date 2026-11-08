package ui

import (
	"image"
	"image/color"
	"math"
	"slices"
	"strings"

	"gioui.org/f32"
)

// The canvas's geometry: a card's process graph laid out left to right by
// columns, the view's pan and zoom, the wires' curves and hit-testing. No
// drawing here, so all of it is tested without a window. Positions are
// canvas units (dp at zoom 1); the view maps them to the pane's px.

// Canvas sizes, in canvas units.
const (
	cvNodeW   = 224
	cvHeadH   = 26
	cvRowH    = 20
	cvPadB    = 8
	cvColGap  = 84
	cvNodeGap = 36 // between nodes in a column: room for a Claude's status line above one
	cvBandGap = 72 // between bands: a frame's foot, the next one's head
	cvFrameX  = 22 // a frame's padding left and right
	cvFrameT  = 44 // over its first node: its label, then a status line
	cvFrameB  = 20
)

// The view's zoom limits.
const cvZoomMin, cvZoomMax = 0.4, 2.5

// cvSock is what a socket carries: control (run order) or a type of data.
type cvSock int

const (
	sockControl cvSock = iota
	sockContext        // the card: title, notes, links, labels
	sockRepo           // a folder
	sockDiff           // changes: a pull request's or a commit's
	sockVerdict        // pass or fail
	sockText           // words: a brief, an answer
	sockLink           // a URL: a ticket, a PR, a CI run
)

// sockColors are the data sockets' and wires' colors, as Blender colors
// its sockets by type; control is white.
var sockColors = map[cvSock]color.NRGBA{
	sockControl: {0xf0, 0xf0, 0xf0, 0xff}, sockContext: {0x9b, 0x7b, 0xe0, 0xff}, sockRepo: {0x5f, 0xc2, 0x8a, 0xff},
	sockDiff: {0xe0, 0x6c, 0x9f, 0xff}, sockVerdict: {0x3f, 0xc1, 0xc9, 0xff}, sockText: {0xa0, 0xa0, 0xa8, 0xff}, sockLink: {0xa0, 0xa0, 0xa8, 0xff},
}

// cvFam is a node's family, which colors its header.
type cvFam int

const (
	famDesk   cvFam = iota // desk's own: the card, its links, Done
	famClaude              // a Claude: the session, a subagent, the workspace's
	famCheck               // a command or a check: a shell, CI
)

var famColors = map[cvFam]color.NRGBA{famDesk: {0x3b, 0x55, 0x5a, 0xff}, famClaude: {0xa3, 0x5a, 0x2a, 0xff}, famCheck: {0x2e, 0x7d, 0x5b, 0xff}}

// cvRow is one row of a node: an input socket (on the left), an output
// (on the right), or a parameter (name and value, no socket).
type cvRow struct {
	name, value string
	in, out     bool
	sock        cvSock
	tone        color.NRGBA // the value's color; zero: the default
	mono        bool        // a command, a path
}

// cvWho is a Claude's presence on its node: an avatar and what it's on.
type cvWho struct {
	initials string
	col      color.NRGBA
	status   string
	busy     bool // working: the avatar breathes
}

// cvNode is one node of the graph and, once laid out, where it is.
type cvNode struct {
	id, title, badge string
	fam              cvFam
	rows             []cvRow
	col, band        int    // its column (left to right) and band (top to bottom)
	frame            string // the frame it sits in, "" none
	who              *cvWho
	open             string // what a click does: "convo", "sub:<task>", "url:<url>", "ws", "edit"; "" nothing
	dim              bool   // done: drawn quieter
	tone             color.NRGBA
	r                image.Rectangle
}

// cvWire joins output out of node from to input in of node to (row names).
type cvWire struct{ from, out, to, in string }

// cvGraph is a card's process as the canvas draws it.
type cvGraph struct {
	nodes   []cvNode
	wires   []cvWire
	earlier int // agents done long ago, off the canvas until asked for
}

// node is the node with that id (in g's nodes: changes stay), nil if none.
func (g cvGraph) node(id string) *cvNode {
	if i := slices.IndexFunc(g.nodes, func(n cvNode) bool { return n.id == id }); i >= 0 {
		return &g.nodes[i]
	}
	return nil
}

// height is how tall n is: its header and rows.
func (n *cvNode) height() int { return cvHeadH + len(n.rows)*cvRowH + cvPadB }

// rowY is the middle of row i.
func (n *cvNode) rowY(i int) float32 { return float32(n.r.Min.Y + cvHeadH + 2 + i*cvRowH + cvRowH/2) }

// sock is where n's row named name has its socket, and its type: inputs on
// the left edge, outputs on the right.
func (n *cvNode) sock(name string, out bool) (f32.Point, cvSock, bool) {
	for i, r := range n.rows {
		if r.name == name && (out && r.out || !out && r.in) {
			x := n.r.Min.X
			if out {
				x = n.r.Max.X
			}
			return f32.Pt(float32(x), n.rowY(i)), r.sock, true
		}
	}
	return f32.Point{}, 0, false
}

// cvLayout places the nodes: columns left to right (empty ones take no
// room), bands top to bottom; in each band a column's nodes stack in order,
// centered on the band's tallest column.
func cvLayout(ns []cvNode) {
	var cols, bands []int
	for _, n := range ns {
		if !slices.Contains(cols, n.col) {
			cols = append(cols, n.col)
		}
		if !slices.Contains(bands, n.band) {
			bands = append(bands, n.band)
		}
	}
	slices.Sort(cols)
	slices.Sort(bands)
	type cell struct{ col, band int }
	stack := map[cell]int{}
	for _, n := range ns {
		k := cell{n.col, n.band}
		if stack[k] > 0 {
			stack[k] += cvNodeGap
		}
		stack[k] += n.height()
	}
	top, tall := map[int]int{}, map[int]int{} // each band's top and height
	y := 0
	for _, b := range bands {
		top[b] = y
		for k, s := range stack {
			if k.band == b {
				tall[b] = max(tall[b], s)
			}
		}
		y += tall[b] + cvBandGap
	}
	at := map[cell]int{}
	for i := range ns {
		n := &ns[i]
		k := cell{n.col, n.band}
		if _, ok := at[k]; !ok {
			at[k] = top[n.band] + (tall[n.band]-stack[k])/2
		}
		x := slices.Index(cols, n.col) * (cvNodeW + cvColGap)
		n.r = image.Rect(x, at[k], x+cvNodeW, at[k]+n.height())
		at[k] += n.height() + cvNodeGap
	}
}

// cvBounds is what the nodes cover, with their frames and status lines.
func cvBounds(ns []cvNode) image.Rectangle {
	var b image.Rectangle
	for i, n := range ns {
		r := n.r
		if n.frame != "" {
			r = image.Rect(r.Min.X-cvFrameX, r.Min.Y-cvFrameT, r.Max.X+cvFrameX, r.Max.Y+cvFrameB)
		} else if n.who != nil {
			r.Min.Y -= 20
		}
		if i == 0 {
			b = r
		}
		b = b.Union(r)
	}
	return b
}

// cvFrames is each frame's rectangle: around its nodes, padded; frames in
// the order their first node comes.
func cvFrames(ns []cvNode) (names []string, rs []image.Rectangle) {
	for _, n := range ns {
		if n.frame == "" {
			continue
		}
		r := image.Rect(n.r.Min.X-cvFrameX, n.r.Min.Y-cvFrameT, n.r.Max.X+cvFrameX, n.r.Max.Y+cvFrameB)
		if i := slices.Index(names, n.frame); i >= 0 {
			rs[i] = rs[i].Union(r)
			continue
		}
		names, rs = append(names, n.frame), append(rs, r)
	}
	return names, rs
}

// cvSeg is a cubic Bézier segment: start, two controls, end.
type cvSeg [4]f32.Point

// at is the segment's point at t in [0, 1].
func (s cvSeg) at(t float32) f32.Point {
	u := 1 - t
	a, b, c, d := u*u*u, 3*u*u*t, 3*u*t*t, t*t*t
	return f32.Pt(a*s[0].X+b*s[1].X+c*s[2].X+d*s[3].X, a*s[0].Y+b*s[1].Y+c*s[2].Y+d*s[3].Y)
}

// cvCurve is a wire from a to b leaving and entering level, as Blender's.
func cvCurve(a, b f32.Point) []cvSeg {
	d := max((b.X-a.X)/2, 40)
	return []cvSeg{{a, a.Add(f32.Pt(d, 0)), b.Sub(f32.Pt(d, 0)), b}}
}

// cvLaned is a wire from a to b along lane y: up (or down) in the gap past
// a, across, and back in the gap before b, so it passes over the nodes
// between instead of through them.
func cvLaned(a, b f32.Point, y float32) []cvSeg {
	g := float32(cvColGap) / 2
	p, q := f32.Pt(a.X+g, y), f32.Pt(b.X-g, y)
	return []cvSeg{
		{a, f32.Pt(a.X+g/2, a.Y), f32.Pt(a.X+g/2, y), p},
		{p, p, q, q},
		{q, f32.Pt(b.X-g/2, y), f32.Pt(b.X-g/2, b.Y), b},
	}
}

// cvHits is whether the curve runs through a node other than skip's.
func cvHits(segs []cvSeg, ns []cvNode, skip ...string) bool {
	for _, n := range ns {
		if slices.Contains(skip, n.id) {
			continue
		}
		r := n.r.Inset(-6)
		for _, s := range segs {
			for i := 0; i <= 24; i++ {
				if p := s.at(float32(i) / 24); p.X > float32(r.Min.X) && p.X < float32(r.Max.X) && p.Y > float32(r.Min.Y) && p.Y < float32(r.Max.Y) {
					return true
				}
			}
		}
	}
	return false
}

// cvRoute is wire w's curve: straight when that crosses no other node, else
// along a lane over (or under, when nearer) the nodes it passes; ok false
// when a socket isn't there.
func cvRoute(g *cvGraph, w cvWire) (segs []cvSeg, typ cvSock, ok bool) {
	from, to := g.node(w.from), g.node(w.to)
	if from == nil || to == nil {
		return nil, 0, false
	}
	a, typ, ok1 := from.sock(w.out, true)
	b, _, ok2 := to.sock(w.in, false)
	if !ok1 || !ok2 {
		return nil, 0, false
	}
	segs = cvCurve(a, b)
	if !cvHits(segs, g.nodes, w.from, w.to) {
		return segs, typ, true
	}
	over, under := float32(math.MaxFloat32), float32(-math.MaxFloat32)
	for _, n := range g.nodes {
		if float32(n.r.Max.X) > a.X && float32(n.r.Min.X) < b.X { // strictly between: the ends' own edges are a and b
			over, under = min(over, float32(n.r.Min.Y-28)), max(under, float32(n.r.Max.Y+20))
		}
	}
	lane := over
	if mid := (a.Y + b.Y) / 2; under-mid < mid-over {
		lane = under
	}
	return cvLaned(a, b, lane), typ, true
}

// cvHit is the node at canvas point p, "" none; the last drawn wins.
func cvHit(ns []cvNode, p f32.Point) string {
	for i := len(ns) - 1; i >= 0; i-- {
		r := ns[i].r
		if p.X >= float32(r.Min.X) && p.X < float32(r.Max.X) && p.Y >= float32(r.Min.Y) && p.Y < float32(r.Max.Y) {
			return ns[i].id
		}
	}
	return ""
}

// cvView is how a card's canvas is looked at: pan and zoom, the pointer's
// press and hover, and last frame's nodes for hit-testing.
type cvView struct {
	pan     f32.Point // px: where the canvas's origin is in the pane
	zoom    float32   // 1: a canvas unit is a dp
	auto    bool      // fitted, untouched: it fits again as the graph grows
	fitted  image.Rectangle
	size    image.Point
	hover   string // the node under the pointer
	press   string // the node pressed, "" empty space
	down    bool
	panning bool
	start   f32.Point
	last    f32.Point
	ptr     f32.Point // where the pointer last moved, in the pane's px
	nodes   []cvNode  // last frame's, laid out
}

// toCanvas is pane point p (px) in canvas units, at px per dp.
func (v *cvView) toCanvas(p f32.Point, px float32) f32.Point {
	return p.Sub(v.pan).Div(v.zoom * px)
}

// zoomAt scales the view by f around pane point p, which stays put; zoom
// stays within its limits.
func (v *cvView) zoomAt(p f32.Point, f float32) {
	z := min(max(v.zoom*f, cvZoomMin), cvZoomMax)
	v.pan = p.Sub(p.Sub(v.pan).Mul(z / v.zoom))
	v.zoom, v.auto = z, false
}

// wheelZoom is the zoom factor for a wheel scroll of dy px (down zooms out).
func wheelZoom(dy float32) float32 { return float32(math.Exp(float64(-dy) / 240)) }

// fit shows all of b (canvas units) in a pane of size px, centered above
// the controls' row, never past 1:1, at px per dp.
func (v *cvView) fit(b image.Rectangle, size image.Point, px float32) {
	m, foot := 20*px, 48*px
	h := float32(size.Y) - m - foot
	z := min((float32(size.X)-2*m)/(float32(b.Dx())*px), h/(float32(b.Dy())*px), 1)
	v.zoom = min(max(z, cvZoomMin), cvZoomMax)
	c := f32.Pt(float32(b.Min.X+b.Max.X)/2, float32(b.Min.Y+b.Max.Y)/2)
	v.pan = f32.Pt(float32(size.X)/2, m+h/2).Sub(c.Mul(v.zoom * px))
	v.auto, v.fitted, v.size = true, b, size
}

// initials is a name's avatar letters: "TW" for test-writer, "PL" for
// planner.
func initials(name string) string {
	ws := strings.FieldsFunc(name, func(r rune) bool { return r == ' ' || r == '-' || r == '_' || r == '.' })
	switch len(ws) {
	case 0:
		return "?"
	case 1:
		rs := []rune(ws[0])
		return strings.ToUpper(string(rs[:min(2, len(rs))]))
	}
	return strings.ToUpper(string([]rune(ws[0])[:1]) + string([]rune(ws[1])[:1]))
}
