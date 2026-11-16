package ui

import (
	"image"
	"strings"
	"unicode/utf8"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget"

	"github.com/lfdominguez/desk/internal/ui/kit"
)

// The message box's image tags are badges: each stands for its image, so
// it is drawn as one and edited as one. Gio's editor holds plain text
// only, so the tag stays text ("[Image 1]") and keepTags makes it whole
// again after every change: an edit that cuts into a tag takes all of it,
// the caret never rests inside one, and a selection reaching into one
// covers it.

// tagSpans are where text's image tags are, in runes: [start, end) each,
// for the images still in the box (a tag typed by hand is plain text).
func (p *pane) tagSpans(text string) [][2]int {
	var spans [][2]int
	for i, path := range p.chatImgs {
		if path == "" {
			continue
		}
		tag := imgTag(i + 1)
		for off := 0; ; {
			j := strings.Index(text[off:], tag)
			if j < 0 {
				break
			}
			s := utf8.RuneCountInString(text[:off+j])
			spans = append(spans, [2]int{s, s + utf8.RuneCountInString(tag)})
			off += j + len(tag)
		}
	}
	return spans
}

// setBox puts text in the message box as the starting point keepTags
// compares the next change with.
func (p *pane) setBox(text string) {
	p.chatIn.SetText(text)
	p.chatPrev, p.chatCaret = text, 0
}

// keepTags runs after the editor took its events: a change that cut into
// a tag is widened to the whole tag (Backspace, Delete, a word's delete,
// typing over part of a selection), then the caret and selection are
// moved off the tags' insides.
func (p *pane) keepTags() {
	ed := &p.chatIn
	if text := ed.Text(); text != p.chatPrev {
		old, cur := []rune(p.chatPrev), []rune(text)
		a := 0
		for a < len(old) && a < len(cur) && old[a] == cur[a] {
			a++
		}
		s := 0
		for s < len(old)-a && s < len(cur)-a && old[len(old)-1-s] == cur[len(cur)-1-s] {
			s++
		}
		b, ins := len(old)-s, len(cur)-s // old[a:b] became cur[a:ins]
		a2, b2 := a, b
		if a < b { // something went: a tag it reached into goes whole
			for _, sp := range p.tagSpans(p.chatPrev) {
				if sp[0] < b && a < sp[1] {
					a2, b2 = min(a2, sp[0]), max(b2, sp[1])
				}
			}
		}
		if a2 < a || b2 > b {
			ed.SetCaret(ins, ins+b2-b) // the tag's rest after what came in
			ed.Insert("")
			ed.SetCaret(a2, a) // and before it
			ed.Insert("")
			c := a2 + ins - a
			ed.SetCaret(c, c)
		}
	}
	caret, end := ed.Selection()
	for _, sp := range p.tagSpans(ed.Text()) {
		inside := func(x int) bool { return sp[0] < x && x < sp[1] }
		switch {
		case caret == end && inside(caret): // to the edge it came towards
			c := sp[1]
			if caret < p.chatCaret {
				c = sp[0]
			}
			caret, end = c, c
		case caret < end:
			if inside(caret) {
				caret = sp[0]
			}
			if inside(end) {
				end = sp[1]
			}
		default:
			if inside(end) {
				end = sp[0]
			}
			if inside(caret) {
				caret = sp[1]
			}
		}
	}
	if c, e := ed.Selection(); c != caret || e != end {
		ed.SetCaret(caret, end)
	}
	p.chatPrev, p.chatCaret = ed.Text(), caret
	p.hasImgs()
}

// badged lays out the message box's editor with a badge under each image
// tag: the editor first (recorded), so its regions are this frame's, then
// the badges, then its text over them.
func (u *ui) badged(gtx layout.Context, ed func(gtx layout.Context) layout.Dimensions) layout.Dimensions {
	m := op.Record(gtx.Ops)
	d := ed(gtx)
	call := m.Stop()
	if u.chatIn.Text() != u.chatPrev { // the editor changed as it laid out: keepTags on the next frame
		gtx.Execute(op.InvalidateCmd{})
	}
	pad, rr := gtx.Dp(3), gtx.Dp(5)
	var regs []widget.Region
	for _, sp := range u.tagSpans(u.chatIn.Text()) {
		regs = u.chatIn.Regions(sp[0], sp[1], regs[:0])
		for _, r := range regs {
			b := image.Rect(r.Bounds.Min.X-pad, r.Bounds.Min.Y, r.Bounds.Max.X+pad, r.Bounds.Max.Y)
			paint.FillShape(gtx.Ops, kit.Mix(kit.CAcc, kit.CPane, .72), clip.UniformRRect(b, rr).Op(gtx.Ops))
		}
	}
	call.Add(gtx.Ops)
	return d
}
