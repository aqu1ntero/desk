package infra

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	deskvault "github.com/lfdominguez/desk/internal/vault"
)

// The group format, for orgs without a servers file (servers.go): a group's
// servers can live in Infra-Claude/<group>/ssh_config: one Host block per
// server, any OpenSSH option inside. desk runs ssh -F with it. One extra line,
// a comment so ssh ignores it, says where the server's password is:
//
//	Host webproxy-1
//	    HostName 192.0.2.10
//	    User deploy
//	    #desk password passbolt 3f2a…   (vault, then item id or name)
//	    #desk wol 52:54:00:12:34:56     (Wake-on-LAN before connecting; optional broadcast addr:port after it)
//
// End the file with "Match all" + "Include ~/.ssh/config" to keep your own
// defaults (-F skips ~/.ssh/config otherwise).
const sshConfigFile = "ssh_config"

type sshHost struct {
	name, addr string
	password   *SecretRef
	wol        string // "#desk wol": MAC [broadcast:port]
}

var (
	passwordLine = regexp.MustCompile(`^#\s*desk\s+password\s+(\S+)\s+(.+)$`)
	wolLine      = regexp.MustCompile(`^#\s*desk\s+wol\s+(.+)$`)
)

// parseSSHConfig lists the concrete Host aliases (no wildcards) in order.
func parseSSHConfig(path string) ([]sshHost, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var hosts []sshHost
	var cur []int // indexes of the hosts the current block applies to
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if m := passwordLine.FindStringSubmatch(line); m != nil {
			for _, i := range cur {
				hosts[i].password = &SecretRef{Vault: m[1], Item: strings.TrimSpace(m[2])}
			}
			continue
		}
		if m := wolLine.FindStringSubmatch(line); m != nil {
			for _, i := range cur {
				hosts[i].wol = strings.TrimSpace(m[1])
			}
			continue
		}
		fs := strings.Fields(line)
		if len(fs) < 2 || strings.HasPrefix(fs[0], "#") {
			continue
		}
		switch strings.ToLower(strings.TrimSuffix(fs[0], "=")) {
		case "host":
			cur = nil
			for _, p := range fs[1:] {
				if !strings.ContainsAny(p, "*?!") {
					cur = append(cur, len(hosts))
					hosts = append(hosts, sshHost{name: p})
				}
			}
		case "match":
			cur = nil
		case "hostname":
			for _, i := range cur {
				hosts[i].addr = fs[1]
			}
		}
	}
	return hosts, sc.Err()
}

// sshConfigFor is the ssh_config a pane's dir runs ssh with: the org's
// servers file for its Infra-Claude dir, else the old group dir's own.
func sshConfigFor(dir string) string {
	if filepath.Base(dir) == infraDir {
		if f := serversPath(filepath.Dir(dir)); f != "" {
			return f
		}
	}
	return filepath.Join(dir, sshConfigFile)
}

// hostFile is the file holding host's Host block (applyFixes edits it): the
// servers file or the Include that has it, or the old group's ssh_config.
func hostFile(dir, host string) string {
	hs, _ := serverHosts(dir)
	for _, h := range hs {
		if h.alias == host {
			return h.file
		}
	}
	return sshConfigFor(dir)
}

// sshArgs is how desk runs ssh for a host of a pane dir. Keepalives notice a
// dead link in ~45s so redial can bring it back; an ssh_config that sets
// ServerAliveInterval itself keeps its own (the command line would win).
func sshArgs(host, dir string) []string {
	args := []string{"-o", "ControlMaster=auto", "-o", "ControlPath=" + runtimeDir() + "/ssh-%C", "-o", "ControlPersist=60"}
	cfg := sshConfigFor(dir)
	data, err := os.ReadFile(cfg)
	if !strings.Contains(strings.ToLower(string(data)), "serveraliveinterval") {
		args = append(args, "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3")
	}
	if err == nil {
		args = append(args, "-F", cfg)
	}
	return append(args, host)
}

// sshCommand is the shell command line for host, the way desk runs it.
func sshCommand(host, dir string) string {
	q := []string{"ssh"}
	for _, a := range sshArgs(host, dir) {
		if strings.ContainsAny(a, " '\"$\\*?%") {
			a = shQuote(a)
		}
		q = append(q, a)
	}
	return strings.Join(q, " ")
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

// SSH_ASKPASS: ssh runs `desk <prompt>` (with DESK_ASKPASS set) for every
// question it has; that process asks the running desk over a socket. Passwords
// come from the vault on the first try; anything else, or a retry, is asked
// in the ask sheet. The password never touches the terminal, so Claude can't see it.

type askTarget struct {
	name, dir string
	password  *SecretRef
	tries     int
	term      *Term // set once ssh started
}

var (
	askOnce    sync.Once
	askSock    string
	askMu      sync.Mutex
	askTargets = map[string]*askTarget{}
	askN       int
)

// askpassEnv registers one ssh connection and returns the env that routes its
// prompts to this desk.
func askpassEnv(host, dir string) ([]string, *askTarget) {
	askOnce.Do(startAskpass)
	if askSock == "" {
		return nil, nil
	}
	t := &askTarget{name: host, dir: dir, password: passwordRef(dir, host)}
	askMu.Lock()
	askN++
	id := fmt.Sprint(askN)
	askTargets[id] = t
	askMu.Unlock()
	exe, _ := os.Executable()
	return []string{"SSH_ASKPASS=" + exe, "SSH_ASKPASS_REQUIRE=force", "DESK_ASKPASS=" + askSock, "DESK_ASKPASS_ID=" + id}, t
}

type askReq struct{ ID, Prompt string }
type askRes struct {
	Answer string
	OK     bool
}

func startAskpass() {
	sock := fmt.Sprintf("%s/askpass-%d.sock", runtimeDir(), os.Getpid())
	os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return
	}
	askSock = sock
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				var r askReq
				if json.NewDecoder(c).Decode(&r) == nil {
					json.NewEncoder(c).Encode(answerAsk(r))
				}
			}()
		}
	}()
}

var passwordPrompt = regexp.MustCompile(`(?i)password[^:]*:\s*$`)

func answerAsk(r askReq) askRes {
	askMu.Lock()
	t := askTargets[r.ID]
	askMu.Unlock()
	if t == nil {
		return askRes{}
	}
	askMu.Lock()
	if t.term != nil {
		t.term.markReached() // not a connect timeout: the server is talking
	}
	askMu.Unlock()
	prompt := strings.TrimSpace(r.Prompt)
	if passwordPrompt.MatchString(prompt) && t.password != nil && t.tries == 0 {
		t.tries++
		if pw, err := vaultPassword(t); err == nil {
			return askRes{pw, true}
		} else {
			prompt += "  (vault: " + err.Error() + ")"
		}
	} else if passwordPrompt.MatchString(prompt) && t.password != nil {
		prompt += "  (the vault password was rejected)"
	}
	masked := !strings.Contains(prompt, "yes/no")
	a, ok := askUser("ssh "+t.name+" · "+prompt, masked)
	return askRes{a, ok}
}

func vaultPassword(t *askTarget) (string, error) { return serverPassword(t.dir, t.password) }

// passwordRef is where a host's password is (its #@ password in the servers
// file, or "#desk password" in its group's ssh_config), or nil.
func passwordRef(dir, host string) *SecretRef {
	if hs, ok := serverHosts(dir); ok {
		for _, h := range hs {
			if h.alias == host {
				return h.password
			}
		}
		return nil
	}
	hs, _ := parseSSHConfig(filepath.Join(dir, sshConfigFile))
	for _, h := range hs {
		if h.name == host {
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

// osExit ends the process (a variable so tests can catch it).
var osExit = os.Exit

// askpassMain is desk run by ssh as SSH_ASKPASS: print the answer, or fail.
func askpassMain(prompt string) {
	c, err := net.Dial("unix", os.Getenv("DESK_ASKPASS"))
	if err != nil {
		osExit(1)
	}
	json.NewEncoder(c).Encode(askReq{os.Getenv("DESK_ASKPASS_ID"), prompt})
	var r askRes
	if json.NewDecoder(c).Decode(&r) != nil || !r.OK {
		osExit(1)
	}
	fmt.Println(r.Answer)
}

// Very old servers only speak algorithms modern OpenSSH turned off; ssh then
// fails with "Unable to negotiate … Their offer: …". desk turns that into the
// ssh_config lines that re-enable them for that one host.

var negotiate = regexp.MustCompile(`no matching (host key type|key exchange method|cipher|MAC) found\. Their offer: (\S+)`)

// legacyFix re-enables algorithms for one ssh_config keyword.
type legacyFix struct {
	Keyword string
	Algs    []string
}

func (f legacyFix) String() string { return f.Keyword + " +" + strings.Join(f.Algs, ",") }

var (
	sshQMu sync.Mutex
	sshQ   = map[string]map[string]bool{}
)

// sshSupports lists what the local ssh knows (ssh -Q key|kex|cipher|mac).
func sshSupports(q string) map[string]bool {
	sshQMu.Lock()
	defer sshQMu.Unlock()
	if s, ok := sshQ[q]; ok {
		return s
	}
	s := map[string]bool{}
	out, _ := exec.Command("ssh", "-Q", q).Output()
	for _, a := range strings.Fields(string(out)) {
		s[a] = true
	}
	sshQ[q] = s
	return s
}

// legacyFixes reads ssh's negotiation errors from the screen. Algorithms the
// local ssh no longer has (ssh-dss in OpenSSH ≥ 9.8) can't be fixed here and
// come back in missing.
func legacyFixes(screen string) (fixes []legacyFix, missing []string) {
	kinds := map[string][2]string{
		"host key type": {"HostKeyAlgorithms", "key"}, "key exchange method": {"KexAlgorithms", "kex"},
		"cipher": {"Ciphers", "cipher"}, "MAC": {"MACs", "mac"},
	}
	for _, m := range negotiate.FindAllStringSubmatch(screen, -1) {
		k := kinds[m[1]]
		var algs []string
		for _, a := range strings.Split(m[2], ",") {
			if sshSupports(k[1])[a] {
				algs = append(algs, a)
			} else {
				missing = append(missing, a)
			}
		}
		if len(algs) == 0 {
			continue
		}
		fixes = append(fixes, legacyFix{k[0], algs})
		if k[0] == "HostKeyAlgorithms" && slices.Contains(algs, "ssh-rsa") {
			fixes = append(fixes, legacyFix{"PubkeyAcceptedAlgorithms", []string{"ssh-rsa"}}) // so RSA keys can log in too
		}
	}
	return fixes, missing
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
	host, dir, name string // the server (ssh alias, group dir, sidebar name)
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

// run adds (or with cancel, removes) f on the server's shared connection.
func (f portFwd) run(cancel bool) error {
	op := "forward"
	if cancel {
		op = "cancel"
	}
	return runErr(exec.Command("ssh", f.args(op)...))
}

// args is ssh's command line for control operation op on f's connection.
func (f portFwd) args(op string) []string {
	args := sshArgs(f.host, f.dir)
	return append(append(args[:len(args)-1:len(args)-1], "-O", op, "-"+f.kind, f.spec), f.host)
}
