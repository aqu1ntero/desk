package ui

import (
	"path/filepath"

	"github.com/lfdominguez/desk/internal/orgs"
)

// walls is claude.Confine for desk's sessions: a session working in dir
// doesn't see the other organizations' folders, nor desk's own board,
// chats and state (every organization's), but still sees the pasted
// images it is given. Any goroutine: root and path never change.
func (u *ui) walls(dir string) (hide, keep []string) {
	hide = append(orgs.Walls(u.root, dir), filepath.Dir(u.path))
	if state, err := orgs.XDGDir("XDG_STATE_HOME", filepath.Join(".local", "state")); err == nil {
		hide = append(hide, filepath.Join(state, "desk"))
	}
	return hide, []string{u.imagesDir()}
}
