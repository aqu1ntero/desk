package infra

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/lfdominguez/desk/internal/diff"
)

// add_host is how the infra Claude makes a server it reaches from another
// one (ssh from that server, with a key that lives there) a server of desk's,
// in place of nested ssh in a pane: a Host block with "#@ on" and "#@ via"
// (route.go) right after the "on" server's, once the user approves it. Then
// connect opens a pane on it and the file tools work there.

var hostName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// addHostArgs are add_host's arguments.
type addHostArgs struct {
	Name    string `json:"name"`
	On      string `json:"on"`
	Command string `json:"command"`
	Desc    string `json:"desc"`
	Reason  string `json:"reason"`
}

func (t *Tab) addHost(a addHostArgs) toolResult {
	fail := func(err error) toolResult { return toolResult{Text: err.Error(), IsError: true} }
	a.Command, a.Desc = strings.TrimSpace(a.Command), strings.TrimSpace(a.Desc)
	switch {
	case !hostName.MatchString(a.Name):
		return fail(fmt.Errorf("name %q: letters, digits, '.', '_' and '-' only", a.Name))
	case a.Command == "" || strings.ContainsAny(a.Command+a.Desc, "\r\n"):
		return fail(errors.New("command is one line: how the server is reached from on, e.g. ssh -i /root/.ssh/key admin@192.0.2.10"))
	}
	on, err := t.findHost(a.On)
	if err != nil {
		return fail(err)
	}
	for _, h := range serverHosts(on.dir) {
		if strings.EqualFold(h.alias, a.Name) {
			return fail(fmt.Errorf("%s is already a server: connect to it", a.Name))
		}
	}
	block := []string{"Host " + a.Name, "    #@ on " + on.host, "    #@ via " + a.Command}
	if a.Desc != "" {
		block = append(block, "    #@ desc "+a.Desc)
	}
	path := hostFile(on.dir, on.host)
	data, err := os.ReadFile(path)
	if err != nil {
		return fail(err)
	}
	lines := strings.Split(string(data), "\n")
	start, end := blockLines(lines, on.host)
	if start < 0 {
		return fail(fmt.Errorf("no Host %s in %s", on.host, path))
	}
	for end > start+1 && strings.TrimSpace(lines[end-1]) == "" { // after its last line, before the blank ones
		end--
	}
	next := strings.Join(append(append(append([]string{}, lines[:end]...), block...), lines[end:]...), "\n")
	text, _, _ := diff.Unified(string(data), next, 2)
	r := t.approve(&approval{cmd: text, reason: a.Reason, file: path, title: "Claude wants to add a server",
		why: "desk will connect to " + on.label + " and run the command there to reach " + a.Name, task: taskFrom(t)})
	verdict := "approved"
	if !r.ok {
		verdict = "denied"
	}
	audit(auditEntry{Org: t.org, Group: t.group, Host: on.host, Event: "file", By: "claude", Cmd: "add_host " + a.Name + " via " + a.Command, Verdict: verdict})
	if !r.ok {
		return fail(errors.New("the user denied adding this server" + r.note + ". Ask them how they want to reach it."))
	}
	if now, err := os.ReadFile(path); err != nil || string(now) != string(data) {
		return fail(fmt.Errorf("%s changed while the user looked at it: call add_host again", path))
	}
	if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
		return fail(err)
	}
	done := make(chan struct{})
	dispatch(func(m *model) { m.reloadConfig(); close(done) }) // the sidebar shows it, and connect finds it
	<-done
	return toolResult{Text: fmt.Sprintf("added %s, reached from %s. connect to it for a pane; the file tools take host=%s", a.Name, on.label, a.Name)}
}
