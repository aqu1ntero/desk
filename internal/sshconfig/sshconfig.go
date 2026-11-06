// Package sshconfig resolves an OpenSSH client configuration for a host the
// way OpenSSH 10.5's ssh does (readconf.c and ssh.c): the command-line
// options, then the user's file (or the -F one alone), then the system's,
// first value winning, Host and Match blocks, Include, the canonicalization
// pass, defaults, and the expansions ssh makes before connecting. Dump is
// what `ssh -G` prints. It only resolves; connecting is the client's.
package sshconfig

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

// release is the version Match version compares with.
const release = "OpenSSH_10.5p1"

// Options are ssh's command line, and what it takes from the system.
type Options struct {
	File      string   // like -F: the only config file read ("none": none); "" = ~/.ssh/config and the system file
	System    string   // the system file ("" = /etc/ssh/ssh_config, %ProgramData%\ssh\ssh_config on Windows; "none" = none)
	Overrides []string // like -o "Key value", in order, before any file
	User      string   // like -l ("" = none), taken before Overrides
	Port      int      // like -p (0 = none), taken before Overrides
	Home      string   // "" = os.UserHomeDir
	LocalUser string   // "" = the current user's name

	// Exec runs a Match exec command: whether it exited 0, or an error when
	// it couldn't run or died. nil = $SHELL -c (cmd /C on Windows).
	Exec func(cmd string) (bool, error)
	// Lookup resolves a name for CanonicalizeHostname and
	// CanonicalizePermittedCNAMEs: an error when it has no address, else its
	// canonical name ("" or the name itself when there is no CNAME).
	// nil = the system resolver.
	Lookup func(name string) (cname string, err error)
	// Networks are the local interfaces' addresses Match localnetwork
	// checks. nil = net.Interfaces' that are up.
	Networks func() []net.IPNet
}

// resolver is one Resolve: the options being filled and the local facts.
type resolver struct {
	opt                       Options
	o                         *options
	home, localUser, thisHost string
	sysDir                    string
	warnings                  []string
}

func (r *resolver) warn(s string) { r.warnings = append(r.warnings, s) }

func newResolver(opt Options) (*resolver, error) {
	r := &resolver{opt: opt, o: newOptions(), sysDir: "/etc/ssh"}
	if runtime.GOOS == "windows" {
		r.sysDir = filepath.Join(os.Getenv("ProgramData"), "ssh")
	}
	if r.opt.System == "" {
		r.opt.System = r.sysDir + "/ssh_config"
	}
	r.home = opt.Home
	if r.home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		r.home = h
	}
	r.localUser = opt.LocalUser
	if r.localUser == "" {
		u, err := user.Current()
		if err != nil {
			return nil, err
		}
		r.localUser = u.Username[strings.LastIndexByte(u.Username, '\\')+1:] // Windows' DOMAIN\name
	}
	h, err := os.Hostname()
	if err != nil {
		return nil, err
	}
	r.thisHost = h
	if r.opt.Exec == nil {
		r.opt.Exec = runShell
	}
	if r.opt.Lookup == nil {
		r.opt.Lookup = systemLookup
	}
	if r.opt.Networks == nil {
		r.opt.Networks = interfaceNets
	}
	return r, nil
}

// runShell is ssh's execute_in_shell: the command in the user's shell, its
// output discarded.
func runShell(cmd string) (bool, error) {
	var c *exec.Cmd
	if runtime.GOOS == "windows" {
		c = exec.Command("cmd", "/C", cmd)
	} else {
		sh := os.Getenv("SHELL")
		if sh == "" {
			sh = "/bin/sh"
		}
		c = exec.Command(sh, "-c", cmd)
	}
	err := c.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.Exited() {
		return false, nil
	}
	return err == nil, err
}

func systemLookup(name string) (string, error) {
	if _, err := net.LookupHost(name); err != nil {
		return "", err
	}
	cn, err := net.LookupCNAME(name)
	if err != nil {
		return "", nil
	}
	return strings.TrimSuffix(cn, "."), nil
}

func interfaceNets() []net.IPNet {
	var out []net.IPNet
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok {
				out = append(out, *n)
			}
		}
	}
	return out
}

func netipFrom(ip net.IP) (netip.Addr, bool) {
	a, ok := netip.AddrFromSlice(ip)
	return a.Unmap(), ok
}

// connHash is readconf.c's ssh_connection_hash, %C.
func connHash(thisHost, host, port, user, jump string) string {
	h := sha1.Sum([]byte(thisHost + host + port + user + jump))
	return hex.EncodeToString(h[:])
}

func clearOrNone(s string, set bool) bool { return !set || strings.EqualFold(s, "none") }

// matchHost is the host Match host and the %h of Match exec and Include
// see: HostName with %h expanded before the final pass, then the canonical one.
func (r *resolver) matchHost(host string, final bool) (string, error) {
	hn, set := r.o.strs["hostname"]
	switch {
	case final:
		return hn, nil
	case set:
		h, err := expand(hn, map[byte]string{'h': host}, false, true)
		if err != nil {
			return "", fatalf("HostName %s: %v", hn, err)
		}
		return h, nil
	}
	return host, nil
}

// expandMatch is readconf.c's expand_match_exec_or_include_path.
func (r *resolver) expandMatch(s, host string, final, include bool) (string, error) {
	o := r.o
	port := o.int("port")
	if port <= 0 {
		port = 22
	}
	ruser, set := o.strs["user"]
	if !set {
		ruser = r.localUser
	}
	h, err := r.matchHost(host, final)
	if err != nil {
		return "", err
	}
	jmp := o.strs["jumphost"]
	if strings.EqualFold(jmp, "none") {
		jmp = ""
	}
	alias, set := o.strs["hostkeyalias"]
	if !set {
		alias = h
	}
	ps := strconv.Itoa(port)
	return expand(s, map[byte]string{
		'C': connHash(r.thisHost, h, ps, ruser, jmp), 'L': short(r.thisHost), 'd': r.home, 'h': h,
		'k': alias, 'l': r.thisHost, 'n': o.hostArg, 'p': ps, 'r': ruser, 'u': r.localUser,
		'i': strconv.Itoa(os.Getuid()), 'j': jmp,
	}, include, true)
}

func short(h string) string { s, _, _ := strings.Cut(h, "."); return s }

// configs is ssh.c's process_config_files: -F's file alone, or the user's
// then the system's.
func (r *resolver) configs(host string, final bool, want *bool) error {
	p := &parser{r: r, o: r.o, host: host, final: final, wantFinal: want}
	active := true
	if f := r.opt.File; f != "" {
		if strings.EqualFold(f, "none") {
			return nil
		}
		opened, err := p.file(f, &active, fUser, 0)
		if !opened {
			if err == nil {
				err = os.ErrNotExist
			}
			return fmt.Errorf("Can't open user config file %s: %w", f, err)
		}
		return err
	}
	if opened, err := p.file(filepath.Join(r.home, ".ssh", "config"), &active, fCheckPerm|fUser, 0); opened && err != nil {
		return err
	}
	if strings.EqualFold(r.opt.System, "none") {
		return nil
	}
	active = true
	if opened, err := p.file(r.opt.System, &active, 0, 0); opened && err != nil {
		return err
	}
	return nil
}

// Resolve gives host's configuration, as `ssh -G [options] host` does. host
// may be user@host or an ssh:// URI.
func Resolve(host string, opt Options) (*Config, error) {
	r, err := newResolver(opt)
	if err != nil {
		return nil, err
	}
	o := r.o
	userCmdline := false
	if opt.User != "" {
		o.strs["user"], userCmdline = opt.User, true
	}
	if opt.Port != 0 {
		if opt.Port < 0 || opt.Port > 65535 {
			return nil, fmt.Errorf("Bad port '%d'", opt.Port)
		}
		o.ints["port"] = opt.Port
	}
	p := &parser{r: r, o: o}
	for _, l := range opt.Overrides {
		if err := p.line(l, "command-line", 0, nil, fUser, 0); err != nil {
			return nil, err
		}
	}
	switch u, h, port, kind := parseSSHURI(host); kind {
	case -1:
		return nil, fmt.Errorf("invalid destination %q", host)
	case 0:
		if !o.has("user") {
			if u != "" {
				o.strs["user"] = u
			}
			userCmdline = true
		}
		if port != -1 {
			o.setInt("port", port, true)
		}
		host = h
	default:
		if i := strings.LastIndexByte(host, '@'); i >= 0 {
			if i == 0 {
				return nil, fmt.Errorf("invalid destination %q", host)
			}
			if !o.has("user") {
				o.strs["user"], userCmdline = host[:i], true
			}
			host = host[i+1:]
		}
	}
	if u, set := o.strs["user"]; set && !validRuser(u) {
		return nil, errors.New("remote username contains invalid characters")
	}
	if !validHostname(host) {
		return nil, errors.New("hostname contains invalid characters")
	}
	o.hostArg = host

	want := false
	if err := r.configs(host, false, &want); err != nil {
		return nil, err
	}
	o.setInt("canonicalizemaxdots", 1, true)
	o.setInt("canonicalizefallbacklocal", 1, true)
	o.setInt("canonicalizehostname", 0, true)
	if hn, set := o.strs["hostname"]; set {
		if host, err = expand(hn, map[byte]string{'h': host}, false, true); err != nil {
			return nil, fmt.Errorf("HostName %s: %v", hn, err)
		}
		o.strs["hostname"] = host
	}
	wasAddr := r.isAddr(host)
	if !wasAddr {
		host = lower(host)
	}
	found := false
	if o.int("canonicalizehostname") != 0 || wasAddr {
		if host, found, err = r.canonicalize(host); err != nil {
			return nil, err
		}
	}
	direct := r.direct()
	if !found && r.hasCNAMEs() && (direct || o.int("canonicalizehostname") == 2) {
		if cn, err := r.lookup(host); err != nil {
			if direct {
				return nil, fmt.Errorf("Could not resolve hostname %s: %v", host, err)
			}
		} else {
			host = r.followCNAME(direct, host, cn)
		}
	}
	if o.int("canonicalizehostname") != 0 {
		want = true
	}
	if want {
		o.strs["hostname"] = host
		if err := r.configs(host, true, nil); err != nil {
			return nil, err
		}
	}
	if err := r.defaults(); err != nil {
		return nil, err
	}
	return r.finish(host, userCmdline)
}

func (r *resolver) family() int {
	if f := r.o.int("addressfamily"); f != -1 {
		return f
	}
	return afUnspec
}

func (r *resolver) isAddr(h string) bool {
	_, ok := numericAddr(h, r.family())
	return isAddrFast(h) || ok
}

func (r *resolver) direct() bool {
	pc, pcSet := r.o.strs["proxycommand"]
	jh, jhSet := r.o.strs["jumphost"]
	return clearOrNone(pc, pcSet) && clearOrNone(jh, jhSet)
}

// hasCNAMEs is readconf.c's config_has_permitted_cnames.
func (r *resolver) hasCNAMEs() bool {
	c := r.o.cnames
	if len(c) == 1 && strings.EqualFold(c[0].src, "none") && c[0].dst == "" {
		return false
	}
	return len(c) > 0
}

// lookup is ssh.c's resolve_host as canonicalization uses it: a canonical
// name that isn't a valid domain is ignored.
func (r *resolver) lookup(name string) (string, error) {
	cn, err := r.opt.Lookup(name)
	if err != nil {
		return "", err
	}
	if _, bad := validDomain(cn, false); bad != "" || len(cn) >= 1025 {
		return "", nil
	}
	return cn, nil
}

// followCNAME is ssh.c's check_follow_cname.
func (r *resolver) followCNAME(direct bool, name, cn string) string {
	canon := r.o.int("canonicalizehostname")
	if cn == "" || !r.hasCNAMEs() || name == cn || canon == 0 || !direct && canon != 2 {
		return name
	}
	for _, c := range r.o.cnames {
		if matchPatternList(name, c.src, true) == 1 && matchPatternList(cn, c.dst, true) == 1 {
			return lower(cn)
		}
	}
	return name
}

// canonicalize is ssh.c's resolve_canonicalize: an address in its
// canonical form, else the name with each CanonicalDomains suffix until one
// resolves. found is whether it resolved.
func (r *resolver) canonicalize(host string) (string, bool, error) {
	o := r.o
	if a, ok := numericAddr(host, r.family()); ok {
		if s := a.String(); !strings.EqualFold(s, host) {
			host = s
		}
		return host, true, nil
	}
	canon := o.int("canonicalizehostname")
	direct := r.direct()
	if isAddrFast(host) || canon == 0 || !direct && canon != 2 {
		return host, false, nil
	}
	if strings.HasSuffix(host, ".") {
		if cn, err := r.lookup(host); err == nil {
			return r.followCNAME(direct, strings.TrimSuffix(host, "."), cn), true, nil
		}
	} else {
		if strings.Count(host, ".") > o.int("canonicalizemaxdots") {
			return host, false, nil
		}
		for _, d := range o.domains {
			if strings.EqualFold(d, "none") {
				break
			}
			if cn, err := r.lookup(host + "." + d + "."); err == nil {
				return r.followCNAME(direct, host+"."+d, cn), true, nil
			}
		}
	}
	if o.int("canonicalizefallbacklocal") == 0 {
		return "", false, fmt.Errorf("Could not resolve host %q", host)
	}
	return host, false, nil
}

// defaults is readconf.c's fill_default_options.
func (r *resolver) defaults() error {
	o := r.o
	for _, d := range []struct {
		k string
		v int
	}{
		{"forwardagent", 0}, {"forwardx11", 0}, {"forwardx11trusted", 0}, {"forwardx11timeout", 1200},
		{"exitonforwardfailure", 0}, {"clearallforwardings", 0},
	} {
		o.setInt(d.k, d.v, true)
	}
	if o.int("clearallforwardings") == 1 {
		o.local, o.remote = nil, nil
		o.ints["tunnel"] = 0
	}
	o.setStr("xauthlocation", "/usr/X11R6/bin/xauth", true)
	for _, d := range []struct {
		k string
		v int
	}{
		{"gatewayports", 0}, {"streamlocalbindmask", 0o177}, {"streamlocalbindunlink", 0},
		{"pubkeyauthentication", 3}, {"passwordauthentication", 1}, {"kbdinteractiveauthentication", 1},
		{"hostbasedauthentication", 0}, {"batchmode", 0}, {"checkhostip", 0}, {"stricthostkeychecking", 3},
		{"compression", 0}, {"tcpkeepalive", 1}, {"port", 0}, {"addressfamily", afUnspec},
		{"connectionattempts", 1}, {"numberofpasswordprompts", 3},
	} {
		o.setInt(d.k, d.v, true)
	}
	if o.int("addkeystoagent") == -1 {
		o.ints["addkeystoagent"], o.ints["addkeystoagentlifespan"] = 0, 0
	}
	if len(o.identity) == 0 {
		for _, k := range []string{"rsa", "ecdsa", "ecdsa_sk", "ed25519", "ed25519_sk", "mldsa44_ed25519"} {
			addFile(&o.identity, "~/.ssh/id_"+k, 0)
		}
	}
	o.setInt("escapechar", '~', true)
	if len(o.sysHosts) == 0 {
		o.sysHosts = []string{r.sysDir + "/ssh_known_hosts", r.sysDir + "/ssh_known_hosts2"}
	}
	if o.int("updatehostkeys") == -1 {
		u := o.userHosts
		if o.int("verifyhostkeydns") <= 0 && (len(u) == 0 || len(u) == 1 && u[0] == "~/.ssh/known_hosts") {
			o.ints["updatehostkeys"] = 1
		} else {
			o.ints["updatehostkeys"] = 0
		}
	}
	if len(o.userHosts) == 0 {
		o.userHosts = []string{"~/.ssh/known_hosts", "~/.ssh/known_hosts2"}
	}
	for _, d := range []struct {
		k string
		v int
	}{
		{"loglevel", 3}, {"syslogfacility", 1}, {"nohostauthenticationforlocalhost", 0}, {"identitiesonly", 0},
		{"enablesshkeysign", 0}, {"rekeyinterval", 0}, {"verifyhostkeydns", 0}, {"serveraliveinterval", 0},
		{"serveralivecountmax", 3}, {"controlmaster", 0}, {"hashknownhosts", 0}, {"tunnel", 0},
		{"tunlocal", tunAny}, {"tunremote", tunAny}, {"permitlocalcommand", 0}, {"visualhostkey", 0},
		{"ipqos", 0xb8}, {"ipqosbulk", 0}, {"requesttty", 0}, {"sessiontype", 2}, {"stdinnull", 0},
		{"forkafterauthentication", 0}, {"proxyusefdpass", 0}, {"canonicalizemaxdots", 1},
		{"canonicalizefallbacklocal", 1}, {"canonicalizehostname", 0}, {"fingerprinthash", 2},
		{"requiredrsasize", 1024}, {"warnweakcrypto", 1}, {"enableescapecommandline", 0},
		{"obscurekeystroketiming", 20},
	} {
		o.setInt(d.k, d.v, true)
	}
	if o.rekeyLimit == -1 {
		o.rekeyLimit = 0
	}
	if o.int("controlpersist") == -1 {
		o.ints["controlpersist"], o.ints["controlpersisttimeout"] = 0, 0
	}
	o.setStr("securitykeyprovider", "internal", true)
	for _, a := range []struct {
		k, def string
		all    []string
	}{
		{"ciphers", defCiphers, allCiphers}, {"macs", defMACs, allMACs}, {"kexalgorithms", defKex, allKex},
		{"hostbasedacceptedalgorithms", defKeys, allKeys}, {"pubkeyacceptedalgorithms", defKeys, allKeys},
		{"casignaturealgorithms", defSigs, plainKeys()},
	} {
		v, err := assemble(o.strs[a.k], a.def, a.all)
		if err != nil {
			return fmt.Errorf("%s: %v", a.k, err)
		}
		o.strs[a.k] = v
	}
	for _, k := range []string{"localcommand", "remotecommand", "proxycommand", "controlpath",
		"pkcs11provider", "securitykeyprovider", "knownhostscommand"} {
		if v, set := o.strs[k]; set && strings.EqualFold(v, "none") {
			delete(o.strs, k)
		}
	}
	for _, l := range []*[]string{&o.timeouts, &o.revoked} {
		if len(*l) == 1 && strings.EqualFold((*l)[0], "none") {
			*l = nil
		}
	}
	if o.strs["jumphost"] == "none" && o.int("jumpport") == 0 && !o.has("jumpuser") {
		delete(o.strs, "jumphost")
	}
	if len(o.cnames) == 1 && !r.hasCNAMEs() {
		o.cnames = nil
	}
	return nil
}

// finish is the rest of ssh.c's main before -G prints: the default user,
// ProxyJump's checks, and the expansions.
func (r *resolver) finish(host string, userCmdline bool) (*Config, error) {
	o := r.o
	userDefault := !o.has("user")
	if userDefault {
		o.strs["user"] = r.localUser
	}
	if jh, set := o.strs["jumphost"]; set {
		port, jport := o.int("port"), o.int("jumpport")
		if port <= 0 {
			port = 22
		}
		if jport <= 0 {
			jport = 22
		}
		ju, set := o.strs["jumpuser"]
		if !set {
			ju = o.strs["user"]
		}
		if jh == host && port == jport && o.strs["user"] == ju {
			return nil, fmt.Errorf("jumphost loop via %s", jh)
		}
		o.ints["proxyusefdpass"] = 0
	}
	if o.int("port") == 0 {
		o.ints["port"] = 22
	}
	if a, set := o.strs["hostkeyalias"]; set {
		o.strs["hostkeyalias"] = lower(a)
	}
	if o.strs["proxycommand"] == "-" && o.int("proxyusefdpass") == 1 {
		return nil, errors.New("ProxyCommand=- and ProxyUseFDPass are incompatible")
	}
	if o.int("updatehostkeys") == 2 {
		_, cp := o.strs["controlpath"]
		_, rc := o.strs["remotecommand"]
		if o.int("controlpersist") != 0 && cp || rc || o.int("requesttty") == 1 || o.int("loglevel") < 3 {
			o.ints["updatehostkeys"] = 0
		}
	}
	if o.int("connectionattempts") <= 0 {
		return nil, errors.New("Invalid number of ConnectionAttempts")
	}
	if _, rc := o.strs["remotecommand"]; o.int("forkafterauthentication") == 1 && !rc && o.int("sessiontype") != 0 {
		return nil, errors.New("Cannot fork into background without a command to execute.")
	}

	alias, set := o.strs["hostkeyalias"]
	if !set {
		alias = o.hostArg
	}
	ps := strconv.Itoa(o.int("port"))
	tok := map[byte]string{
		'L': short(r.thisHost), 'i': strconv.Itoa(os.Getuid()), 'k': alias, 'l': r.thisHost,
		'n': o.hostArg, 'p': ps, 'd': r.home, 'h': host, 'u': r.localUser, 'j': o.strs["jumphost"],
	}
	if !userCmdline && !userDefault {
		u, err := expand(o.strs["user"], tok, true, true)
		if err != nil {
			return nil, fmt.Errorf("User %s: %v", o.strs["user"], err)
		}
		if u != o.strs["user"] && !validRuser(u) {
			return nil, errors.New("remote username contains invalid characters")
		}
		o.strs["user"] = u
	} else if userCmdline && !validRuser(o.strs["user"]) {
		return nil, errors.New("remote username contains invalid characters")
	}
	tok['r'] = o.strs["user"]
	tok['C'] = connHash(r.thisHost, host, ps, o.strs["user"], o.strs["jumphost"])

	c := &Config{o: o, host: host, tokens: tok, home: r.home}
	// expandPath is ssh.c's tilde_expand_filename then default_client_percent_dollar_expand.
	expandPath := func(s string, tildeToo, env bool) (string, error) {
		if tildeToo {
			var err error
			if s, err = tilde(s, r.home); err != nil {
				return "", err
			}
		}
		v, err := expand(s, tok, env, true)
		if err != nil {
			return "", fmt.Errorf("%s: %v", s, err)
		}
		return v, nil
	}
	for _, k := range []struct {
		key        string
		tilde, env bool
	}{
		{"remotecommand", false, false}, {"controlpath", true, true}, {"identityagent", true, true},
		{"forwardagentpath", true, true}, {"versionaddendum", false, true},
	} {
		if v, set := o.strs[k.key]; set {
			e, err := expandPath(v, k.tilde, k.env)
			if err != nil {
				return nil, err
			}
			o.strs[k.key] = e
		}
	}
	if p, set := o.strs["forwardagentpath"]; set {
		if _, err := os.Stat(p); err != nil {
			r.warn(fmt.Sprintf("Cannot forward agent socket path %q: %v", p, err))
			if o.int("exitonforwardfailure") == 1 {
				return nil, err
			}
		}
	}
	var err error
	each := func(l []string, f func(string) (string, error)) {
		for i := range l {
			if err == nil {
				l[i], err = f(l[i])
			}
		}
	}
	each(o.revoked, func(s string) (string, error) { return expandPath(s, true, true) })
	if len(o.sysHosts) > 0 && strings.EqualFold(o.sysHosts[0], "none") {
		o.sysHosts = nil
	}
	if len(o.userHosts) > 0 && strings.EqualFold(o.userHosts[0], "none") {
		o.userHosts = nil
	}
	each(o.userHosts, func(s string) (string, error) { return expandPath(s, true, true) })
	each(o.setEnv, func(s string) (string, error) {
		name, val, _ := strings.Cut(s, "=")
		v, err := expandPath(val, false, true)
		return name + "=" + v, err
	})
	for _, l := range [][]Forward{o.local, o.remote} {
		for i := range l {
			for _, p := range []*string{&l[i].ListenPath, &l[i].ConnectPath} {
				if *p != "" && err == nil {
					*p, err = expandPath(*p, false, false)
				}
			}
		}
	}
	if err != nil {
		return nil, err
	}
	if o.strs["hostkeyalgorithms"], err = assemble(o.strs["hostkeyalgorithms"], defKeys, allKeys); err != nil {
		return nil, fmt.Errorf("expand HostKeyAlgorithms: %v", err)
	}
	c.Warnings = r.warnings
	c.dump = c.render()
	c.vals = map[string][]string{}
	for _, l := range strings.Split(strings.TrimSuffix(c.dump, "\n"), "\n") {
		k, v, _ := strings.Cut(l, " ")
		c.vals[lower(k)] = append(c.vals[lower(k)], v)
	}
	return c, nil
}

// Config is a resolved configuration.
type Config struct {
	Warnings []string // what ssh would print and go on: unsupported and deprecated keywords, an unexpandable Include…

	o      *options
	host   string
	home   string
	tokens map[byte]string
	dump   string
	vals   map[string][]string
}

// Dump is what `ssh -G` prints.
func (c *Config) Dump() string { return c.dump }

// Get is keyword's value as ssh -G prints it (its first line's, for a
// repeated one); keyword is lowercase.
func (c *Config) Get(keyword string) string {
	if v := c.vals[keyword]; len(v) > 0 {
		return v[0]
	}
	return ""
}

// List is every value ssh -G prints for keyword: IdentityFile, SendEnv,
// SetEnv and the forwards come one per line.
func (c *Config) List(keyword string) []string { return c.vals[keyword] }

// HostName is the host to connect to, after HostName and canonicalization.
func (c *Config) HostName() string { return c.host }

// Port is the port to connect to.
func (c *Config) Port() int { return c.o.int("port") }

// User is the remote user.
func (c *Config) User() string { return c.o.strs["user"] }

// Bool is a yes/no keyword: true for yes or true.
func (c *Config) Bool(keyword string) bool { v := c.Get(keyword); return v == "yes" || v == "true" }

// Int is a numeric keyword's value, 0 when it isn't one.
func (c *Config) Int(keyword string) int { v, _ := strconv.Atoi(c.Get(keyword)); return v }

// Duration is a keyword in seconds (ConnectTimeout, ServerAliveInterval,
// ForwardX11Timeout…); 0 for none.
func (c *Config) Duration(keyword string) time.Duration {
	return time.Duration(c.Int(keyword)) * time.Second
}

// Algorithms is a comma-separated list keyword (Ciphers, MACs,
// KexAlgorithms, HostKeyAlgorithms, PubkeyAcceptedAlgorithms…), resolved.
func (c *Config) Algorithms(keyword string) []string {
	if v := c.Get(keyword); v != "" {
		return strings.Split(v, ",")
	}
	return nil
}

// Tokens are the % tokens of the connection (C d h i j k L l n p r u), for Expand.
func (c *Config) Tokens() map[byte]string {
	m := make(map[byte]string, len(c.tokens))
	for k, v := range c.tokens {
		m[k] = v
	}
	return m
}

// files is the identity or certificate files as ssh loads them: ~ and
// tokens expanded.
func (c *Config) files(l []idFile) ([]string, error) {
	var out []string
	for _, f := range l {
		p, err := tilde(f.path, c.home)
		if err == nil {
			p, err = Expand(p, c.tokens, true)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %v", f.path, err)
		}
		out = append(out, p)
	}
	return out, nil
}

// IdentityFiles are the keys to try, ~ and tokens expanded.
func (c *Config) IdentityFiles() ([]string, error) { return c.files(c.o.identity) }

// CertificateFiles are the certificates to offer, ~ and tokens expanded.
func (c *Config) CertificateFiles() ([]string, error) { return c.files(c.o.certs) }

// UserKnownHostsFiles are the user's known_hosts files, expanded.
func (c *Config) UserKnownHostsFiles() []string { return slices.Clone(c.o.userHosts) }

// GlobalKnownHostsFiles are the system's known_hosts files.
func (c *Config) GlobalKnownHostsFiles() []string { return slices.Clone(c.o.sysHosts) }

// ProxyCommand is the command to connect through ("" for none or with
// ProxyJump), its tokens still to expand.
func (c *Config) ProxyCommand() string {
	if c.o.has("jumphost") {
		return ""
	}
	return c.o.strs["proxycommand"]
}

// ProxyJump is the hops to go through, in order, the last one nearest the host.
func (c *Config) ProxyJump() []Hop {
	jh, set := c.o.strs["jumphost"]
	if !set {
		return nil
	}
	var hops []Hop
	if extra, ok := c.o.strs["jumpextra"]; ok {
		for _, s := range strings.Split(extra, ",") {
			h, _ := parseHop(s, false)
			hops = append(hops, h)
		}
	}
	return append(hops, Hop{c.o.strs["jumpuser"], jh, max(c.o.int("jumpport"), 0)})
}

// LocalForwards are the -L forwards (not the dynamic ones).
func (c *Config) LocalForwards() []Forward {
	return slices.DeleteFunc(slices.Clone(c.o.local), dynamic)
}

// DynamicForwards are the SOCKS forwards.
func (c *Config) DynamicForwards() []Forward {
	return slices.DeleteFunc(slices.Clone(c.o.local), func(f Forward) bool { return !dynamic(f) })
}

func dynamic(f Forward) bool {
	return f.ConnectHost == "socks" && f.ConnectPort == 0 && f.ConnectPath == ""
}

// RemoteForwards are the -R forwards (a dynamic one connects to socks).
func (c *Config) RemoteForwards() []Forward { return slices.Clone(c.o.remote) }

// render is readconf.c's dump_client_config.
func (c *Config) render() string {
	o := c.o
	var b strings.Builder
	line := func(k string, v ...string) { b.WriteString(k + " " + strings.Join(v, " ") + "\n") }
	str := func(k string) {
		if v, set := o.strs[k]; set {
			line(k, v)
		}
	}
	fmtint := func(k string) {
		v := o.int(k)
		switch {
		case v == -1:
			line(k, "unset")
		case k == "fingerprinthash":
			line(k, digests.name(v))
		case k == "addkeystoagent":
			line(k, msConfirm.name(v))
		case multistate[k] != nil && k != "warnweakcrypto": // fmt_intarg prints that one as a flag
			line(k, multistate[k].name(v))
		case v == 0:
			line(k, "no")
		case v == 1:
			line(k, "yes")
		default:
			line(k, "UNKNOWN")
		}
	}
	oneline := func(k string, l []string) {
		if len(l) == 0 {
			l = []string{"none"}
		}
		line(k, l...)
	}
	itoa := strconv.Itoa

	line("host", o.hostArg)
	str("user")
	line("hostname", c.host)
	line("port", itoa(o.int("port")))
	for _, k := range strings.Fields(`addressfamily batchmode canonicalizefallbacklocal canonicalizehostname
		checkhostip compression controlmaster enablesshkeysign clearallforwardings exitonforwardfailure
		fingerprinthash forwardx11 forwardx11trusted gatewayports hashknownhosts hostbasedauthentication
		identitiesonly kbdinteractiveauthentication nohostauthenticationforlocalhost passwordauthentication
		permitlocalcommand proxyusefdpass pubkeyauthentication requesttty sessiontype stdinnull
		forkafterauthentication streamlocalbindunlink stricthostkeychecking tcpkeepalive tunnel
		verifyhostkeydns visualhostkey updatehostkeys enableescapecommandline warnweakcrypto`) {
		fmtint(k)
	}
	for _, k := range strings.Fields(`canonicalizemaxdots connectionattempts forwardx11timeout
		numberofpasswordprompts serveralivecountmax serveraliveinterval requiredrsasize`) {
		line(k, itoa(o.int(k)))
	}
	switch v := o.int("obscurekeystroketiming"); v {
	case 0:
		line("obscurekeystroketiming", "no")
	case 20:
		line("obscurekeystroketiming", "yes")
	default:
		line("obscurekeystroketiming", itoa(v))
	}
	for _, k := range strings.Fields(`bindaddress bindinterface ciphers controlpath hostkeyalgorithms
		hostkeyalias hostbasedacceptedalgorithms identityagent ignoreunknown kbdinteractivedevices
		kexalgorithms casignaturealgorithms localcommand remotecommand`) {
		str(k)
	}
	line("loglevel", logLevels.name(o.int("loglevel")))
	for _, k := range strings.Fields(`macs pkcs11provider securitykeyprovider preferredauthentications
		pubkeyacceptedalgorithms xauthlocation knownhostscommand tag versionaddendum`) {
		str(k)
	}
	fwd := func(k string, l []Forward) {
		for _, f := range l {
			// As readconf.c has it, a forward to a socket prints as dynamic too.
			if k == "dynamicforward" && f.ConnectPath == "" && f.ConnectHost != "socks" || k == "localforward" && f.ConnectHost == "socks" {
				continue
			}
			end := func(host string, set bool, port int, path string) string {
				switch {
				case port == StreamLocal:
					return path
				case !set:
					return itoa(port)
				}
				return "[" + host + "]:" + itoa(port)
			}
			v := []string{end(f.ListenHost, f.ListenHostSet, f.ListenPort, f.ListenPath)}
			if k != "dynamicforward" {
				v = append(v, end(f.ConnectHost, true, f.ConnectPort, f.ConnectPath))
			}
			line(k, v...)
		}
	}
	fwd("dynamicforward", o.local)
	fwd("localforward", o.local)
	fwd("remoteforward", o.remote)
	for _, f := range o.identity {
		line("identityfile", f.path)
	}
	oneline("canonicaldomains", o.domains)
	for _, f := range o.certs {
		line("certificatefile", f.path)
	}
	oneline("globalknownhostsfile", o.sysHosts)
	oneline("userknownhostsfile", o.userHosts)
	oneline("revokedhostkeys", o.revoked)
	for _, e := range o.sendEnv {
		line("sendenv", e)
	}
	for _, e := range o.setEnv {
		line("setenv", e)
	}
	oneline("logverbose", o.verbose)
	oneline("channeltimeout", o.timeouts)
	if len(o.remoteOpen) == 0 {
		line("permitremoteopen", "any")
	} else {
		oneline("permitremoteopen", o.remoteOpen)
	}
	if t := o.int("addkeystoagentlifespan"); t <= 0 {
		fmtint("addkeystoagent")
	} else if o.int("addkeystoagent") == 3 {
		line("addkeystoagent", "confirm", itoa(t))
	} else {
		line("addkeystoagent", itoa(t))
	}
	if p, set := o.strs["forwardagentpath"]; set {
		line("forwardagent", p)
	} else {
		fmtint("forwardagent")
	}
	if t := o.int("connecttimeout"); t == -1 {
		line("connecttimeout", "none")
	} else {
		line("connecttimeout", itoa(t))
	}
	tun := func(v int) string {
		if v == tunAny {
			return "any"
		}
		return itoa(v)
	}
	line("tunneldevice", tun(o.int("tunlocal"))+":"+tun(o.int("tunremote")))
	var cn []string
	for _, x := range o.cnames {
		cn = append(cn, x.src+":"+x.dst)
	}
	oneline("canonicalizePermittedcnames", cn)
	if o.int("controlpersist") == 0 || o.int("controlpersisttimeout") == 0 {
		fmtint("controlpersist")
	} else {
		line("controlpersist", itoa(o.int("controlpersisttimeout")))
	}
	if e := o.int("escapechar"); e == -2 {
		line("escapechar", "none")
	} else {
		line("escapechar", vis(byte(e)))
	}
	line("ipqos", iptos2str(o.int("ipqos")), iptos2str(o.int("ipqosbulk")))
	line("rekeylimit", strconv.FormatInt(o.rekeyLimit, 10), itoa(o.int("rekeyinterval")))
	line("streamlocalbindmask", "0"+strconv.FormatInt(int64(o.int("streamlocalbindmask")), 8))
	line("syslogfacility", facilities.name(o.int("syslogfacility")))
	if jh, set := o.strs["jumphost"]; !set {
		str("proxycommand")
	} else {
		v := ""
		if e, ok := o.strs["jumpextra"]; ok {
			v = e + ","
		}
		if u, ok := o.strs["jumpuser"]; ok {
			v += u + "@"
		}
		if strings.Contains(jh, ":") || strings.Trim(jh, "1234567890.") == "" {
			v += "[" + jh + "]"
		} else {
			v += jh
		}
		if p := o.int("jumpport"); p > 0 {
			v += ":" + itoa(p)
		}
		line("proxyjump", v)
	}
	return b.String()
}
