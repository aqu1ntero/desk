package ui

import (
	"fmt"
	"image"
	_ "image/gif" // pasted images of these kinds draw as thumbnails
	_ "image/jpeg"
	_ "image/png"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget"

	"github.com/lfdominguez/desk/internal/ui/kit"
)

// imgTag is how an image pasted in the message box sits in its text, as
// Claude Code's own: "[Image 1]". The image goes to Claude as an image of
// its message (claude.Content), never as text, so a message is never a
// path or "![…]" Markdown (whose "!" ran it as a shell command).
func imgTag(n int) string { return "[Image " + strconv.Itoa(n) + "]" }

var imgTagRe = regexp.MustCompile(`\[Image (\d+)\]`)

// attach puts a pasted image in the box: its file kept, its tag at the
// caret.
func (p *pane) attach(path string) {
	p.chatImgs = append(p.chatImgs, path)
	p.chatIn.Insert(imgTag(len(p.chatImgs)))
}

// hasImgs says the box holds an image whose tag is still in its text; an
// image whose tag was deleted is dropped, as Claude Code does.
func (p *pane) hasImgs() bool {
	text, any := p.chatIn.Text(), false
	for i, path := range p.chatImgs {
		if path != "" && !strings.Contains(text, imgTag(i+1)) {
			p.chatImgs[i] = ""
		}
		any = any || p.chatImgs[i] != ""
	}
	if !any {
		p.chatImgs = nil // the next one is [Image 1] again
	}
	return any
}

// unattach removes image n (from 1) and its tag, as edits of the box
// (not SetText: its undo history and caret stay).
func (p *pane) unattach(n int) {
	p.chatImgs[n-1] = "" // keepTags leaves its spans alone now
	tag := []rune(imgTag(n))
	for {
		i := strings.LastIndex(p.chatIn.Text(), string(tag))
		if i < 0 {
			break
		}
		s := utf8.RuneCountInString(p.chatIn.Text()[:i])
		p.chatIn.SetCaret(s, s+len(tag))
		p.chatIn.Insert("")
	}
	p.chatPrev = p.chatIn.Text()
	p.hasImgs()
}

// outgoing is the box's text with its images' tags numbered 1… in the
// order of the images still in it, and those images' files.
func outgoing(text string, imgs []string) (string, []string) {
	var files []string
	to := map[int]int{}
	for i, path := range imgs {
		if path != "" && strings.Contains(text, imgTag(i+1)) {
			files = append(files, path)
			to[i+1] = len(files)
		}
	}
	text = imgTagRe.ReplaceAllStringFunc(text, func(m string) string {
		n, _ := strconv.Atoi(imgTagRe.FindStringSubmatch(m)[1])
		if k, ok := to[n]; ok {
			return imgTag(k)
		}
		return m // typed by hand: not one of ours
	})
	return text, files
}

// sendBox sends what the message box holds, images and all, and empties it.
func (u *ui) sendBox(id string) {
	text, files := outgoing(u.chatIn.Text(), u.chatImgs)
	u.setBox("")
	u.chatImgs = nil
	u.fromBox = true // the user's own words: they may close the card (heard)
	u.deliver(id, text, false, files...)
	u.fromBox = false
}

// thumb is a pasted image's picture, decoded once.
type thumb struct {
	op   paint.ImageOp
	size image.Point
	ok   bool
}

// thumbOf decodes path once (u.imgThumbs); a file that isn't there or
// can't be read draws as a blank tile.
func (u *ui) thumbOf(path string) thumb {
	if t, ok := u.imgThumbs[path]; ok {
		return t
	}
	var t thumb
	if f, err := os.Open(path); err == nil {
		if img, _, err := image.Decode(f); err == nil {
			t = thumb{op: paint.NewImageOp(img), size: img.Bounds().Size(), ok: true}
		}
		f.Close()
	}
	u.imgThumbs[path] = t
	return t
}

// imgStrip is the images pasted in the box, above it: each a thumbnail
// with its tag under it and an × that takes it out.
func (u *ui) imgStrip(gtx layout.Context) layout.Dimensions {
	if !u.hasImgs() {
		return layout.Dimensions{}
	}
	k := u.k
	var kids []layout.FlexChild
	for i, path := range u.chatImgs {
		if path == "" {
			continue
		}
		n := i + 1
		kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Stack{Alignment: layout.NE}.Layout(gtx,
							layout.Stacked(func(gtx layout.Context) layout.Dimensions { return u.thumbTile(gtx, path) }),
							layout.Stacked(func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{Top: 3, Right: 3}.Layout(gtx, kit.Boxed(kit.CPanel, kit.CLine, kit.RCtl, layout.Inset{}, func(gtx layout.Context) layout.Dimensions { // dark under the ×: it shows on a light image too
									d, ok := k.IconBtn(gtx, fmt.Sprint("pnImgX", n), kit.IcoX, false)
									k.Tip(gtx, fmt.Sprint("pnImgX", n), d, "Remove "+imgTag(n), true)
									if ok {
										u.unattach(n)
									}
									return d
								}))
							}))
					}),
					layout.Rigid(kit.VGap(4)),
					layout.Rigid(k.Lbl(imgTag(n), 11, kit.CText2, kit.Mono)))
			})
		}))
	}
	return layout.Inset{Bottom: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{}.Layout(gtx, kids...)
	})
}

// thumbTile is one image fitted in a 72dp tile.
func (u *ui) thumbTile(gtx layout.Context, path string) layout.Dimensions {
	sz := gtx.Dp(72)
	box := image.Pt(sz, sz)
	rr := gtx.Dp(kit.RCtl)
	defer clip.UniformRRect(image.Rectangle{Max: box}, rr).Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, kit.CPanelCard)
	if t := u.thumbOf(path); t.ok {
		gtx.Constraints = layout.Exact(box)
		widget.Image{Src: t.op, Fit: widget.Cover, Position: layout.Center, Scale: 1 / gtx.Metric.PxPerDp}.Layout(gtx)
	}
	return layout.Dimensions{Size: box}
}
