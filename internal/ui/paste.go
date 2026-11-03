package ui

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gioui.org/io/clipboard"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/widget"

	"github.com/lfdominguez/desk/internal/clip"
)

// clipImage is the clipboard's image and its type (image/png…), or none
// when it holds something else (clip.Image: Gio reads only text); tests
// replace it.
var clipImage = clip.Image

// imageRef finds the pasted images in a card's notes.
var imageRef = regexp.MustCompile(`!\[[^\]]*\]\(([^)\s]+)\)`)

// imagesDir is where pasted images are kept: beside the board.
func (u *ui) imagesDir() string { return filepath.Join(filepath.Dir(u.path), "images") }

// pasteImage takes Ctrl+V in ed before the editor does: an image in the
// clipboard is saved and goes in as a reference Claude reads; anything else
// is pasted by the editor as always (text set, next frame). live says ed is
// still on screen when the clipboard answers. Called before ed is laid out,
// so it sees the key first.
func (u *ui) pasteImage(gtx layout.Context, ed *widget.Editor, text *bool, live func() bool) {
	if *text {
		*text = false
		gtx.Execute(clipboard.ReadCmd{Tag: ed})
	}
	for {
		ev, ok := gtx.Event(key.Filter{Focus: ed, Name: "V", Required: key.ModShortcut})
		if !ok {
			break
		}
		if e, ok := ev.(key.Event); !ok || e.State != key.Press { // the release too comes here: once per paste
			continue
		}
		go func() {
			b, typ, err := clipImage()
			u.later(func() {
				if !live() {
					return
				}
				if err != nil || b == nil { // text (or no image to be had): the editor pastes it next frame
					*text = true
					return
				}
				p, err := u.saveImage(b, typ)
				if err != nil {
					u.showToast("Couldn't keep the image: "+err.Error(), true)
					return
				}
				ed.Insert(fmt.Sprintf("![image](%s)\n", p))
			})
		}()
	}
}

// saveImage writes a pasted image of type typ to imagesDir, by time.
func (u *ui) saveImage(b []byte, typ string) (string, error) {
	if err := os.MkdirAll(u.imagesDir(), 0o755); err != nil {
		return "", err
	}
	ext := strings.TrimPrefix(typ, "image/")
	if i := strings.IndexAny(ext, "+;"); i >= 0 { // image/svg+xml
		ext = ext[:i]
	}
	p := filepath.Join(u.imagesDir(), time.Now().Format("20060102-150405.000")+"."+ext)
	return p, os.WriteFile(p, b, 0o644)
}

// keepImages puts back the images of notes old that rewritten notes lost
// (Prompt Optimization writes the request anew).
func keepImages(old, notes string) string {
	var lost bytes.Buffer
	for _, m := range imageRef.FindAllStringSubmatch(old, -1) {
		if !strings.Contains(notes, m[1]) {
			fmt.Fprintf(&lost, "\n%s", m[0])
		}
	}
	if lost.Len() == 0 {
		return notes
	}
	return strings.TrimRight(notes, "\n") + "\n" + lost.String()
}
