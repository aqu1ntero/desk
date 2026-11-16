package ui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gioui.org/io/key"

	"github.com/lfdominguez/desk/internal/claude"
	"github.com/lfdominguez/desk/internal/kanban"
)

// TestImageInBox: an image pasted in the message box is an [Image 1] tag
// with its thumbnail above the box, and goes to Claude as an image of the
// message; a message starting with "!" that holds one is Claude's, not a
// shell command.
func TestImageInBox(t *testing.T) {
	h := newHarness(t)
	pic := pngBytes(4, 3)
	clipImage = func() ([]byte, string, error) { return pic, "image/png", nil }
	c := h.u.b.Add(0, kanban.Card{Title: "x", Org: "Acme"})
	h.u.openPanel(c.ID)
	h.frame()
	h.frame()
	h.send(press("V", key.ModShortcut))
	h.pumpUntil("the tag", func() bool { return strings.Contains(h.u.chatIn.Text(), "[Image 1]") })
	if len(h.u.chatImgs) != 1 || strings.Contains(h.u.chatIn.Text(), "![") {
		t.Fatalf("box %q, images %v", h.u.chatIn.Text(), h.u.chatImgs)
	}
	path := h.u.chatImgs[0]
	h.frame()
	if !h.u.thumbOf(path).ok {
		t.Fatal("no thumbnail")
	}
	h.u.chatIn.SetText("!why is this red " + h.u.chatIn.Text())
	h.frame()
	h.clickButton("pnSend")
	if len(h.claudes) != 1 || len(h.u.shellStop) != 0 {
		t.Fatalf("sessions %d, shells %d", len(h.claudes), len(h.u.shellStop))
	}
	f := h.claudes[0]
	if !strings.HasSuffix(f.prompts[0], "!why is this red [Image 1]") || !slices.Equal(f.images[0], []string{path}) {
		t.Fatalf("prompt %q, images %v", f.prompts[0], f.images)
	}
	if h.u.chatIn.Text() != "" || h.u.chatImgs != nil {
		t.Fatalf("box left %q %v", h.u.chatIn.Text(), h.u.chatImgs)
	}
}

// TestOutgoing: the images whose tags are still there go, renumbered in
// order; a tag typed by hand stays as it is.
func TestOutgoing(t *testing.T) {
	text, files := outgoing("a [Image 2] b [Image 3] [Image 9]", []string{"p1", "p2", "p3"})
	if text != "a [Image 1] b [Image 2] [Image 9]" || !slices.Equal(files, []string{"p2", "p3"}) {
		t.Fatalf("%q %v", text, files)
	}
	if text, files := outgoing("plain", nil); text != "plain" || files != nil {
		t.Fatalf("%q %v", text, files)
	}
}

// TestImageStrip: × takes an image and its tag out, a deleted tag drops its
// image, and a card's images stay with its draft when the box goes to
// another card and back.
func TestImageStrip(t *testing.T) {
	h := newHarness(t)
	good := filepath.Join(t.TempDir(), "a.png")
	os.WriteFile(good, pngBytes(4, 3), 0o644)
	c := h.u.b.Add(0, kanban.Card{Title: "x", Org: "Acme"})
	d := h.u.b.Add(0, kanban.Card{Title: "y", Org: "Acme"})
	h.u.openPanel(c.ID)
	h.frame()
	h.u.attach(good)
	h.u.attach(filepath.Join(t.TempDir(), "gone.png")) // a blank tile
	h.frame()
	h.clickButton("pnImgX1")
	h.frame()
	if strings.Contains(h.u.chatIn.Text(), "[Image 1]") || h.u.chatImgs[0] != "" || h.u.chatImgs[1] == "" {
		t.Fatalf("removed: %q %v", h.u.chatIn.Text(), h.u.chatImgs)
	}
	h.u.openPanel(d.ID)
	h.frame()
	if !slices.Equal(h.u.b.Card(c.ID).DraftImgs, []string{"", h.u.b.Card(c.ID).DraftImgs[1]}) || h.u.chatImgs != nil {
		t.Fatalf("draft images %v, box %v", h.u.b.Card(c.ID).DraftImgs, h.u.chatImgs)
	}
	h.u.openPanel(c.ID)
	h.frame()
	if len(h.u.chatImgs) != 2 || !strings.Contains(h.u.chatIn.Text(), "[Image 2]") {
		t.Fatalf("back: %q %v", h.u.chatIn.Text(), h.u.chatImgs)
	}
	h.u.chatIn.SetText("no image now")
	h.frame()
	if h.u.chatImgs != nil {
		t.Fatalf("a deleted tag kept its image: %v", h.u.chatImgs)
	}
	h.u.attach(good)
	if !strings.Contains(h.u.chatIn.Text(), "[Image 1]") || len(h.u.chatImgs) != 1 {
		t.Fatalf("numbering starts over: %q", h.u.chatIn.Text())
	}
}

// TestImageAnswersAsk: images sent while Claude asks something answer it
// as the next message, since an answer carries text only.
func TestImageAnswersAsk(t *testing.T) {
	h := newHarness(t)
	c := h.u.b.Add(0, kanban.Card{Title: "x", Org: "Acme"})
	h.u.send(c.ID, "go")
	f := h.claudes[0]
	h.u.asks[c.ID] = &askState{r: &claude.Request{ID: "r1"}}
	good := filepath.Join(t.TempDir(), "a.png")
	h.u.deliver(c.ID, "this [Image 1]", false, good)
	if len(f.replies) != 1 || !strings.Contains(f.replies[0], "next message") || h.u.asks[c.ID] != nil ||
		len(f.prompts) != 2 || !slices.Equal(f.images[1], []string{good}) {
		t.Fatalf("replies %v, prompts %v", f.replies, f.prompts)
	}
}

// TestImageBadge: an image's tag is one thing in the box: arrows step over
// it, Backspace and Delete take it whole (and its image), so does a word's
// delete, and a selection reaching into it covers it.
func TestImageBadge(t *testing.T) {
	h := newHarness(t)
	pic := filepath.Join(t.TempDir(), "a.png")
	os.WriteFile(pic, pngBytes(4, 3), 0o644)
	c := h.u.b.Add(0, kanban.Card{Title: "x", Org: "Acme"})
	h.u.openPanel(c.ID)
	h.frame()
	h.frame()
	ed := &h.u.chatIn
	at := func(want string, c0, c1 int) {
		t.Helper()
		if s, e := ed.Selection(); ed.Text() != want || s != c0 || e != c1 {
			t.Fatalf("box %q caret %d,%d; want %q %d,%d", ed.Text(), s, e, want, c0, c1)
		}
	}
	put := func() { // "see [Image 1] now", the caret at its end
		h.u.setBox("see  now")
		ed.SetCaret(4, 4)
		h.u.attach(pic)
		ed.SetCaret(17, 17)
		h.frame()
	}
	put()
	for range 5 { // 17 → 13, then over the tag
		h.send(press(key.NameLeftArrow, 0))
	}
	at("see [Image 1] now", 4, 4)
	h.send(press(key.NameRightArrow, 0))
	at("see [Image 1] now", 13, 13)
	h.send(press(key.NameDeleteBackward, 0))
	at("see  now", 4, 4)
	if h.u.chatImgs != nil {
		t.Fatalf("its image stayed: %v", h.u.chatImgs)
	}

	put()
	ed.SetCaret(4, 4)
	h.frame()
	h.send(press(key.NameDeleteForward, 0))
	at("see  now", 4, 4)

	put()
	ed.SetCaret(13, 13)
	h.frame()
	h.send(key.EditEvent{Range: key.Range{Start: 11, End: 13}, Text: ""}) // a word's delete: "1]"
	at("see  now", 4, 4)

	put()
	ed.SetCaret(2, 7) // from "e" into the tag
	h.frame()
	at("see [Image 1] now", 2, 13)
	ed.SetCaret(7, 2) // the other way round
	h.frame()
	at("see [Image 1] now", 13, 2)
	ed.SetCaret(5, 15) // from inside the tag out
	h.frame()
	at("see [Image 1] now", 4, 15)
	ed.SetCaret(15, 5)
	h.frame()
	at("see [Image 1] now", 15, 4)
	h.send(key.EditEvent{Range: key.Range{Start: 4, End: 15}, Text: "X"})
	if strings.Contains(ed.Text(), "Image") || h.u.chatImgs != nil {
		t.Fatalf("typed over: %q %v", ed.Text(), h.u.chatImgs)
	}
}
