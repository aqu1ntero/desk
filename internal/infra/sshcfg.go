package infra

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lfdominguez/desk/internal/sshc"
	"github.com/lfdominguez/desk/internal/sshconfig"
	deskvault "github.com/lfdominguez/desk/internal/vault"
)

// sshConfigFor is the ssh_config a pane's dir (its org's infra dir) connects
// with: the org's servers file.
func sshConfigFor(dir string) string { return serversTarget(orgOfInfra(dir)) }

// hostFile is the file holding host's Host block (applyFixes edits it): the
// servers file or the Include that has it.
func hostFile(dir, host string) string {
	for _, h := range serverHosts(dir) {
		if h.alias == host {
			return h.file
		}
	}
	return sshConfigFor(dir)
}

// sshCommand is the shell command line that reaches host as desk does: the
// servers file and the keepalives desk adds.
func sshCommand(host, dir string) string {
	o := sshOpts(host, dir)
	q := []string{"ssh"}
	for _, kv := range o.Overrides {
		q = append(q, "-o", strings.Replace(kv, " ", "=", 1))
	}
	if f := o.File; f != "" {
		if strings.ContainsAny(f, " '\"$\\*?%") {
			f = shQuote(f)
		}
		q = append(q, "-F", f)
	}
	return strings.Join(append(q, host), " ")
}

// wake sends a Wake-on-LAN magic packet: "MAC" or "MAC broadcast:port"
// (default 255.255.255.255:9).
func wake(spec string) error {
	fs := strings.Fields(spec)
	if len(fs) == 0 {
		return fmt.Errorf("wol: no MAC")
	}
	mac, err := net.ParseMAC(fs[0])
	if err != nil || len(mac) != 6 {
		return fmt.Errorf("wol: bad MAC %q", fs[0])
	}
	to := "255.255.255.255:9"
	if len(fs) > 1 {
		to = fs[1]
	}
	c, err := net.Dial("udp", to)
	if err != nil {
		return err
	}
	defer c.Close()
	_, err = c.Write(magicPacket(mac))
	return err
}

// magicPacket is 6 bytes of 0xff, then the MAC 16 times.
func magicPacket(mac net.HardwareAddr) []byte {
	p := bytes.Repeat([]byte{0xff}, 6)
	for range 16 {
		p = append(p, mac...)
	}
	return p
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// passwordRef is where a host's password is (its #@ password in the servers
// file), or nil.
func passwordRef(dir, host string) *SecretRef {
	for _, h := range serverHosts(dir) {
		if h.alias == host {
			return h.password
		}
	}
	return nil
}

// serverPassword reads a host's password from its org's vault. It may ask the
// user for the vault passphrase, so never call it from the UI goroutine.
func serverPassword(dir string, ref *SecretRef) (string, error) {
	if ref == nil {
		return "", fmt.Errorf("no password for this server: add \"#@ password <item>\" to its Host in %s", sshConfigFor(dir))
	}
	if ref.Raw != "" {
		return ref.Raw, nil
	}
	o, err := orgFor(dir)
	if err != nil {
		return "", err
	}
	v, _, err := o.vault(ref.Vault)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	it, err := resolve(ctx, v, ref.Item)
	if dv, ok := v.(deskVault); ok && err == nil && ref.Field != "" && ref.Field != "password" {
		return unlocking(ctx, dv.v, func() (string, error) { return deskvault.Get(ctx, dv.v, it.ID, ref.Field) })
	}
	return it.Password, err // ponytail: a field other than password is Passbolt/Bitwarden only
}

// Very old servers only speak algorithms modern OpenSSH turned off: the
// connection fails with a *sshc.NegotiationError, which desk turns into the
// ssh_config lines that re-enable them for that one host.

// legacyFix re-enables algorithms for one ssh_config keyword.
type legacyFix struct {
	Keyword string
	Algs    []string
}

func (f legacyFix) String() string { return f.Keyword + " +" + strings.Join(f.Algs, ",") }

// legacyFixes is what a failed connection's negotiation error asks for:
// the algorithms the server offers that desk has but leaves off. None when
// it offers only ones desk lacks (ssh-dss), or err isn't one.
func legacyFixes(err error) []legacyFix {
	var ne *sshc.NegotiationError
	if !errors.As(err, &ne) {
		return nil
	}
	algs := ne.Fixable()
	if len(algs) == 0 {
		return nil
	}
	fixes := []legacyFix{{ne.What, algs}}
	if ne.What == "HostKeyAlgorithms" && slices.Contains(algs, "ssh-rsa") {
		fixes = append(fixes, legacyFix{"PubkeyAcceptedAlgorithms", []string{"ssh-rsa"}}) // so RSA keys can log in too
	}
	return fixes
}

// applyFixes writes the fixes into host's block of an ssh_config, merging
// with lines already there (ssh keeps the first value of a keyword).
func applyFixes(path, host string, fixes []legacyFix) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	start, end := -1, len(lines)
	for i, l := range lines {
		fs := strings.Fields(l)
		if len(fs) == 0 {
			continue
		}
		kw := strings.ToLower(fs[0])
		if start >= 0 && (kw == "host" || kw == "match" || kw == "#@" && len(fs) > 1 && fs[1] == "group") {
			end = i
			break
		}
		if kw == "host" && slices.Contains(fs[1:], host) {
			start = i
		}
	}
	if start < 0 {
		return fmt.Errorf("no Host %s in %s", host, path)
	}
	var add []string
	for _, f := range fixes {
		merged := false
		for i := start + 1; i < end; i++ {
			fs := strings.Fields(lines[i])
			if len(fs) < 2 || !strings.EqualFold(fs[0], f.Keyword) {
				continue
			}
			have := strings.Split(strings.TrimPrefix(fs[1], "+"), ",")
			for _, a := range f.Algs {
				if !slices.Contains(have, a) {
					have = append(have, a)
				}
			}
			prefix := ""
			if strings.HasPrefix(fs[1], "+") {
				prefix = "+"
			}
			lines[i] = "    " + f.Keyword + " " + prefix + strings.Join(have, ",")
			merged = true
		}
		if !merged {
			add = append(add, "    "+f.String())
		}
	}
	if len(add) > 0 {
		add = append([]string{"    # old server: algorithms modern OpenSSH disables (added by desk)"}, add...)
		lines = append(lines[:start+1], append(add, lines[start+1:]...)...)
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
}

// portFwd is a port forward on a server's ssh connection: kind "L" (a local
// port reaches the server side), "R" (a server port reaches this machine) or
// "D" (a local SOCKS proxy through the server).
type portFwd struct {
	host, dir, name string // the server (ssh alias, infra dir, sidebar name)
	kind, spec      string // ssh -L/-R/-D and its spec: port[:host:port]
}

var fwdRe = regexp.MustCompile(`^(fwd|forward|rforward|socks)\s+(\S+)\s*$`)

// parseForward reads the palette's forward commands:
//
//	forward 8080 | forward 9000:5432 | forward 9000:db.internal:5432  (-L)
//	rforward 3000 | rforward 9000:3000 | rforward 9000:host:3000     (-R)
//	socks 1080                                                        (-D)
func parseForward(q string) (kind, spec string, ok bool) {
	m := fwdRe.FindStringSubmatch(strings.TrimSpace(q))
	if m == nil {
		return "", "", false
	}
	port := func(s string) bool { n, err := strconv.Atoi(s); return err == nil && n > 0 && n < 65536 }
	ps := strings.Split(m[2], ":")
	if m[1] == "socks" {
		return "D", m[2], len(ps) == 1 && port(ps[0])
	}
	kind = map[bool]string{true: "R", false: "L"}[m[1] == "rforward"]
	switch {
	case len(ps) == 1 && port(ps[0]):
		return kind, ps[0] + ":localhost:" + ps[0], true
	case len(ps) == 2 && port(ps[0]) && port(ps[1]):
		return kind, ps[0] + ":localhost:" + ps[1], true
	case len(ps) == 3 && port(ps[0]) && ps[1] != "" && port(ps[2]):
		return kind, m[2], true
	}
	return "", "", false
}

func (f portFwd) String() string {
	a, b, _ := strings.Cut(f.spec, ":")
	switch f.kind {
	case "R":
		return f.name + ":" + a + " → " + b + " here"
	case "D":
		return "socks localhost:" + f.spec + " via " + f.name
	}
	return "localhost:" + a + " → " + b + " via " + f.name
}

// fwdStops stops the forwards running, by forward.
var fwdStops = struct {
	sync.Mutex
	m map[portFwd]func()
}{m: map[portFwd]func(){}}

// run adds (or with cancel, removes) f on the server's shared connection.
// A forward lives as long as the connection: it goes when that drops, or
// lingers out after the server's last pane closed (as ssh -O forward did).
func (f portFwd) run(cancel bool) error {
	fwdStops.Lock()
	defer fwdStops.Unlock()
	if cancel {
		stop := fwdStops.m[f]
		if stop == nil {
			return errors.New("no such forward")
		}
		delete(fwdStops.m, f)
		stop()
		return nil
	}
	spec, err := f.forward()
	if err != nil {
		return err
	}
	ctx, done := context.WithTimeout(context.Background(), 30*time.Second)
	defer done()
	c, release, err := sshClient(ctx, f.host, f.dir)
	if err != nil {
		return err
	}
	defer release()
	_, stop, err := c.Forward(f.kind[0], spec)
	if err != nil {
		return err
	}
	if old := fwdStops.m[f]; old != nil {
		old() // from a connection gone since: a no-op
	}
	fwdStops.m[f] = stop
	return nil
}

// forward is f's spec (port[:host:port], as parseForward makes it) as sshc
// takes it.
func (f portFwd) forward() (sshconfig.Forward, error) {
	ps := strings.Split(f.spec, ":")
	var fw sshconfig.Forward
	bad := fmt.Errorf("bad forward %q", f.spec)
	var err error
	if fw.ListenPort, err = strconv.Atoi(ps[0]); err != nil {
		return fw, bad
	}
	switch {
	case f.kind == "D" && len(ps) == 1:
		fw.ConnectHost = "socks"
	case f.kind != "D" && len(ps) == 3:
		fw.ConnectHost = ps[1]
		if fw.ConnectPort, err = strconv.Atoi(ps[2]); err != nil {
			return fw, bad
		}
	default:
		return fw, bad
	}
	return fw, nil
}
