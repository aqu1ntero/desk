package infra

import (
	"strings"
	"time"

	xnotify "gioui.org/x/notify"
)

// longCommand is how long a command runs before its end is worth a desktop
// notification (when you're looking elsewhere).
const longCommand = 20 * time.Second

// desktopNotify shows a desktop notification (gioui.org/x/notify, as desk's
// own: D-Bus on Linux, UserNotifications on macOS), off the caller's goroutine. Errors are dropped; no
// notifier just means no notification. A variable so tests can catch them.
var desktopNotify = func(title, body string) { go pushNotify(title, firstLine(body, 160)) }

// pushNotify is the notifier itself; a test catches what reaches it.
var pushNotify = func(title, body string) { xnotify.Push(title, body) }

func firstLine(s string, max int) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	if r := []rune(s); len(r) > max {
		s = string(r[:max-1]) + "…"
	}
	return s
}

// blockDoneMsg is a long command that finished in a pane of tab.
type blockDoneMsg struct {
	tab   *Tab
	host  string
	block Block
}

// elsewhere says the user isn't looking at t: another tab, or desk lost focus.
func (m *model) elsewhere(t *Tab) bool { return m.away || m.tab() != t }
