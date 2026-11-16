package ui

import "slices"

// chatSwitch points the message box at card id: what was written for the
// card before stays with it as its draft, and id's own draft comes back,
// the caret at its end.
func (u *ui) chatSwitch(id string) {
	if id == u.chatFor {
		return
	}
	u.keepDraft()
	u.chatFor = id
	text, imgs := "", []string(nil)
	if c := u.b.Card(id); c != nil {
		text, imgs = c.Draft, slices.Clone(c.DraftImgs)
	}
	u.chatIn.SetText(text)
	u.chatImgs = imgs
	n := u.chatIn.Len()
	u.chatIn.SetCaret(n, n)
}

// keepDraft saves the message box's text as its card's draft (on the board,
// so a quit keeps it too).
func (u *ui) keepDraft() {
	c := u.b.Card(u.chatFor)
	if c == nil || c.Draft == u.chatIn.Text() && slices.Equal(c.DraftImgs, u.chatImgs) {
		return
	}
	c.Draft, c.DraftImgs = u.chatIn.Text(), slices.Clone(u.chatImgs)
	u.save()
}
