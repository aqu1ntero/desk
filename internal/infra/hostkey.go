package infra

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/lfdominguez/desk/internal/sshc"
)

// A server whose host key changed (reinstalled, keys rotated, or someone in
// between) fails the connection with a *sshc.HostKeyChangedError: the key
// it sent and the known_hosts lines that hold the old one. The connection
// box shows both side by side; accepting (sshc.AcceptNewKey) drops those
// lines and records the key it sent, as ssh-keygen -R and a reconnect would
// by hand, then reconnects.

// hostKeyChange is the refusal as the connection box shows it.
type hostKeyChange struct {
	Host         string   // the name known_hosts was searched for
	Type, Remote string   // the key the server sent: ED25519, SHA256:…
	Old          []oldKey // the known_hosts lines with the key expected
}

// oldKey is one known_hosts line with the old key.
type oldKey struct {
	File        string // "" when a KnownHostsCommand printed it
	Line        int
	Type, Print string
}

// hostKeyChangeOf is err's changed host key, nil when it isn't one.
func hostKeyChangeOf(err error) *hostKeyChange {
	var e *sshc.HostKeyChangedError
	if !errors.As(err, &e) {
		return nil
	}
	c := &hostKeyChange{Host: e.Host, Type: e.Type, Remote: e.Fingerprint}
	for _, k := range e.Known {
		c.Old = append(c.Old, oldKey{k.File, k.Line, k.Type, k.Fingerprint})
	}
	return c
}

// tildeHome is path with the home folder as ~.
func tildeHome(path string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(path, home+string(filepath.Separator)) {
		return "~" + filepath.ToSlash(path[len(home):]) // ~/.ssh/known_hosts on Windows too
	}
	return path
}
