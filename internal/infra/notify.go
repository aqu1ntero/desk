package infra

import (
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// longCommand is how long a command runs before its end is worth a desktop
// notification (when you're looking elsewhere).
const longCommand = 20 * time.Second

// desktopNotify shows a desktop notification: notify-send on Linux, osascript
// on macOS. Errors are dropped; no notifier just means no notification. A
// variable so tests can catch them.
var desktopNotify = func(title, body string) {
	body = firstLine(body, 160)
	if runtime.GOOS == "darwin" {
		q := func(s string) string { return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"` }
		exec.Command("osascript", "-e", "display notification "+q(body)+" with title "+q(title)).Start()
		return
	}
	exec.Command("notify-send", "-a", "desk", title, body).Start()
}

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
