package infra

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/lfdominguez/desk/internal/sshc"
	"github.com/lfdominguez/desk/internal/sshconfig"
)

// A server reached from another ("#@ on" + "#@ via" in its block) is
// worked through that one's connection: desk dials the "on" host and runs
// the "via" command there, each hop's own in turn. Its key stays where it
// lives (on the server before it), which ProxyJump can't do.

// route is how host is reached: the alias desk dials and the commands run
// there, outermost first (none for a host dialed directly).
func route(dir, host string) (string, []string, error) {
	if host == "local" || !isInfraDir(dir) {
		return host, nil, nil
	}
	hs := serverHosts(dir)
	var via []string
	for range 8 {
		var h *serverHost
		for i := range hs {
			if hs[i].alias == host {
				h = &hs[i]
			}
		}
		if h == nil || h.on == "" || h.via == "" {
			return host, via, nil
		}
		via = append([]string{h.via}, via...)
		host = h.on
	}
	return "", nil, fmt.Errorf("%s: its #@ on hosts go round in a loop", host)
}

// isSSH says wrapper w is an ssh command: what it runs on the far side is
// one string the far shell reads again, so it goes quoted.
func isSSH(w string) bool { return w == "ssh" || strings.HasPrefix(w, "ssh ") }

// wrap is cmd run through wrappers (outermost first): each ssh hop given its
// flag (-t for a terminal, -o BatchMode=yes for a script, which must fail
// rather than wait on a question no one sees) and the rest quoted for it.
// An empty cmd is the innermost hop's login shell.
func wrap(wrappers []string, cmd, sshFlag string) string {
	for i := len(wrappers) - 1; i >= 0; i-- {
		w := wrappers[i]
		switch {
		case isSSH(w):
			w = "ssh " + sshFlag + strings.TrimPrefix(w, "ssh")
			if cmd != "" {
				cmd = shQuote(cmd)
			}
		case cmd == "":
			cmd = w
			continue
		}
		cmd = strings.TrimSpace(w + " " + cmd)
	}
	return cmd
}

// hopCmd is the route's hops with the last one run with flags and no
// command (ssh -W host:port: its stdio is the connection). Forwards past a
// server need ssh hops.
func hopCmd(via []string, flags string) (string, error) {
	for _, w := range via {
		if !isSSH(w) {
			return "", fmt.Errorf("forwards go only through ssh hops, and %q isn't one", w)
		}
	}
	last := via[len(via)-1]
	return wrap(via[:len(via)-1], "ssh -o BatchMode=yes "+flags+strings.TrimPrefix(last, "ssh"), "-o BatchMode=yes"), nil
}

// hopDial reaches addr from the route's last hop, as ssh -W there: the far
// end of a forward or SOCKS connection past the server c is.
func hopDial(c *sshc.Client, via []string) func(network, addr string) (net.Conn, error) {
	return func(network, addr string) (net.Conn, error) {
		if network != "tcp" {
			return nil, fmt.Errorf("forwards past a server reach TCP ports only, not %s", addr)
		}
		cmd, err := hopCmd(via, "-W "+shQuote(addr))
		if err != nil {
			return nil, err
		}
		return c.ExecConn(cmd)
	}
}

// hopSettle is how long a remote forward's hops get to fail (a port taken)
// before desk counts them as up.
var hopSettle = 3 * time.Second

// hopRemote is a remote forward on the route's last host: the server c is
// listens on the port and reaches here, and each hop's ssh -R listens on
// the same port of the next host and reaches the one before. ponytail: the
// same port on every host on the way; map them one by one if one is taken.
func hopRemote(c *sshc.Client, via []string, f sshconfig.Forward) (func(), error) {
	p := fmt.Sprint(f.ListenPort)
	cmd := wrap(via, "exec sleep 2147483647", "-o BatchMode=yes -o ExitOnForwardFailure=yes -R "+p+":localhost:"+p)
	if _, err := hopCmd(via, ""); err != nil {
		return nil, err
	}
	stop, ended, err := c.Exec(cmd) // the hops first: they reach the first's port only once a connection comes
	if err != nil {
		return nil, err
	}
	_, first, err := c.Forward('R', f)
	if err != nil {
		stop()
		return nil, err
	}
	select {
	case err := <-ended:
		first()
		return nil, fmt.Errorf("the forward's hops: %w", err)
	case <-time.After(hopSettle):
	}
	return func() { stop(); first() }, nil
}
