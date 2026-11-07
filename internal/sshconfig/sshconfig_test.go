package sshconfig

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// dns is a stub resolver: name (with its trailing dot when asked so) to canonical name.
type dns map[string]string

func (d dns) lookup(name string) (string, error) {
	if cn, ok := d[name]; ok {
		return cn, nil
	}
	return "", errors.New("not found")
}

// resolve writes cfg as the -F file of a temp HOME and resolves host.
func resolve(t *testing.T, cfg, host string, mod func(*Options)) (*Config, error) {
	t.Helper()
	dir := t.TempDir()
	f := filepath.Join(dir, "config")
	if err := os.WriteFile(f, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	o := Options{File: f, Home: dir, LocalUser: "me", Lookup: dns{}.lookup,
		Networks: func() []net.IPNet { return nil }}
	if mod != nil {
		mod(&o)
	}
	return Resolve(host, o)
}

func mustResolve(t *testing.T, cfg, host string, mod func(*Options)) *Config {
	t.Helper()
	c, err := resolve(t, cfg, host, mod)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func withDNS(d dns) func(*Options) { return func(o *Options) { o.Lookup = d.lookup } }

func TestCanonicalDomains(t *testing.T) {
	cfg := "CanonicalizeHostname yes\nCanonicalDomains example.org example.com\nMatch canonical host h.example.com\n  User canon\n"
	c := mustResolve(t, cfg, "H", withDNS(dns{"h.example.com.": "h.example.com"}))
	if c.HostName() != "h.example.com" || c.User() != "canon" {
		t.Fatalf("got %s as %s", c.HostName(), c.User())
	}
	if c := mustResolve(t, cfg, "h", nil); c.HostName() != "h" || c.User() != "me" {
		t.Fatalf("no suffix resolves: got %s as %s", c.HostName(), c.User())
	}
	if _, err := resolve(t, "CanonicalizeFallbackLocal no\n"+cfg, "h", nil); err == nil {
		t.Fatal("without fallback an unresolved name must fail")
	}
	if c := mustResolve(t, cfg, "a.b.h", withDNS(dns{"a.b.h.example.org.": "x"})); c.HostName() != "a.b.h" {
		t.Fatalf("more dots than CanonicalizeMaxDots: got %s", c.HostName())
	}
	if c := mustResolve(t, cfg, "a.h", withDNS(dns{"a.h.example.org.": "a.h.example.org"})); c.HostName() != "a.h.example.org" {
		t.Fatalf("one dot is allowed: got %s", c.HostName())
	}
	if c := mustResolve(t, "CanonicalizeHostname yes\nCanonicalDomains none\n", "h",
		withDNS(dns{"h.none.": "h.none"})); c.HostName() != "h" {
		t.Fatalf("none is no domain: got %s", c.HostName())
	}
	if c := mustResolve(t, "CanonicalizeHostname yes\n", "fq.example.com.", withDNS(dns{"fq.example.com.": "fq.example.com"})); c.HostName() != "fq.example.com" {
		t.Fatalf("a rooted name resolves as is: got %s", c.HostName())
	}
	if _, err := resolve(t, "CanonicalizeHostname yes\nCanonicalizeFallbackLocal no\n", "fq.example.com.", nil); err == nil {
		t.Fatal("an unresolved rooted name without fallback must fail")
	}
	if c := mustResolve(t, "CanonicalizeHostname yes\nCanonicalDomains example.com\nProxyCommand nc %h %p\n", "h",
		withDNS(dns{"h.example.com.": "h.example.com"})); c.HostName() != "h" {
		t.Fatalf("a proxied name stays: got %s", c.HostName())
	}
	if c := mustResolve(t, "CanonicalizeHostname always\nCanonicalDomains example.com\nProxyCommand nc %h %p\n", "h",
		withDNS(dns{"h.example.com.": "h.example.com"})); c.HostName() != "h.example.com" {
		t.Fatalf("always canonicalizes proxied names: got %s", c.HostName())
	}
	if c := mustResolve(t, "CanonicalizeHostname yes\nCanonicalDomains example.com\nAddressFamily inet6\n", "192.0.2.1", nil); c.HostName() != "192.0.2.1" {
		t.Fatalf("an address of the other family stays: got %s", c.HostName())
	}
}

func TestCanonicalCNAMEs(t *testing.T) {
	base := "CanonicalizeHostname yes\nCanonicalDomains example.com\nCanonicalizePermittedCNAMEs *.example.com:*.example.net\n"
	d := withDNS(dns{"h.example.com.": "real.example.net", "other.example.com.": "x.example.org", "bad.example.com.": "-bad",
		"up.example.com.": "Up.Example.NET"})
	// The CNAME isn't lowercased to match, only once it is taken.
	for host, want := range map[string]string{"h": "real.example.net", "other": "other.example.com", "bad": "bad.example.com", "up": "up.example.com"} {
		if c := mustResolve(t, base, host, d); c.HostName() != want {
			t.Errorf("%s: got %s, want %s", host, c.HostName(), want)
		}
	}
	if c := mustResolve(t, base+"CanonicalizePermittedCNAMEs *:*\n", "up", d); c.HostName() != "up.example.com" {
		t.Errorf("the first CanonicalizePermittedCNAMEs wins: got %s", c.HostName())
	}
	if c := mustResolve(t, strings.Replace(base, "*.example.net", "*", 1), "up", d); c.HostName() != "up.example.net" {
		t.Errorf("a followed CNAME is lowercased: got %s", c.HostName())
	}
	if c := mustResolve(t, strings.Replace(base, "*.example.com:", "*.example.org:", 1), "h", d); c.HostName() != "h.example.com" {
		t.Errorf("a source that doesn't match: got %s", c.HostName())
	}
	// No suffix resolved: the bare name is looked up and its CNAME followed.
	bare := "CanonicalizeHostname yes\nCanonicalizePermittedCNAMEs h:*.example.net\n"
	if c := mustResolve(t, bare, "h", withDNS(dns{"h": "h.example.net"})); c.HostName() != "h.example.net" {
		t.Errorf("bare CNAME: got %s", c.HostName())
	}
	if c := mustResolve(t, bare, "h", withDNS(dns{"h": "h"})); c.HostName() != "h" {
		t.Errorf("its own name: got %s", c.HostName())
	}
	if c := mustResolve(t, "CanonicalizePermittedCNAMEs h:*.example.net\n", "h", withDNS(dns{"h": "h.example.net"})); c.HostName() != "h" {
		t.Errorf("without canonicalization no CNAME is followed: got %s", c.HostName())
	}
	if _, err := resolve(t, bare, "h", nil); err == nil {
		t.Error("a direct name the resolver doesn't know must fail")
	}
	if c := mustResolve(t, bare+"ProxyJump j\nCanonicalizeHostname always\n", "h", nil); c.HostName() != "h" {
		t.Errorf("a proxied unknown name is fine: got %s", c.HostName())
	}
	if c := mustResolve(t, "CanonicalizeHostname always\nProxyJump j\nCanonicalizePermittedCNAMEs h:*.example.net\n", "h",
		withDNS(dns{"h": "h.example.net"})); c.HostName() != "h.example.net" {
		t.Errorf("always follows a proxied name's CNAME: got %s", c.HostName())
	}
	if c := mustResolve(t, "CanonicalizeHostname yes\nProxyJump j\nCanonicalizePermittedCNAMEs h:*.example.net\n", "h",
		withDNS(dns{"h": "h.example.net"})); c.HostName() != "h" {
		t.Errorf("yes doesn't look a proxied name up: got %s", c.HostName())
	}
	if c := mustResolve(t, "CanonicalizeHostname yes\nCanonicalizePermittedCNAMEs none\n", "h", nil); c.HostName() != "h" {
		t.Errorf("none permits nothing: got %s", c.HostName())
	}
	long := strings.Repeat("a", 1030) + ".example.net"
	if c := mustResolve(t, bare, "h", withDNS(dns{"h": long})); c.HostName() != "h" {
		t.Errorf("an overlong CNAME is ignored: got %s", c.HostName())
	}
}

func TestSystemFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		os.Chmod(p, mode)
		return p
	}
	sys := write("etc/ssh_config", "User sys\nPort 7\n", 0o644)
	write(".ssh/config", "Host h\n  User mine\n", 0o600)
	o := Options{Home: dir, System: sys, LocalUser: "me", Lookup: dns{}.lookup}
	c, err := Resolve("h", o)
	if err != nil {
		t.Fatal(err)
	}
	if c.User() != "mine" || c.Port() != 7 {
		t.Fatalf("user file then system file: got %s:%d", c.User(), c.Port())
	}
	o.System = "none"
	if c, _ := Resolve("h", o); c.Port() != 22 {
		t.Fatalf("System none: got port %d", c.Port())
	}
	o.File = "none"
	if c, _ := Resolve("h", o); c.User() != "me" {
		t.Fatalf("File none reads nothing: got %s", c.User())
	}
	o.File = filepath.Join(dir, "missing")
	if _, err := Resolve("h", o); err == nil {
		t.Fatal("a missing -F file must fail")
	}
	o.File = dir
	if _, err := Resolve("h", o); err != nil {
		t.Fatalf("a directory reads as empty: %v", err)
	}
	o.File, o.System = "", "none"
	write(".ssh/config", "User x\n", 0o666)
	if _, err := Resolve("h", o); err == nil {
		t.Fatal("a writable ~/.ssh/config must fail")
	}
	os.Chmod(filepath.Join(dir, ".ssh/config"), 0o000)
	if os.Getuid() != 0 {
		if c, err := Resolve("h", o); err != nil || c.User() != "me" {
			t.Fatalf("an unreadable ~/.ssh/config is skipped: %v", err)
		}
	}
	os.Remove(filepath.Join(dir, ".ssh/config"))
	for _, body := range []string{"RefuseConnection no\n", "Include config\n"} {
		write(".ssh/config", body, 0o600)
		if _, err := Resolve("h", o); err == nil {
			t.Fatalf("~/.ssh/config with %q must fail", body)
		}
	}
	os.Remove(filepath.Join(dir, ".ssh/config"))
	o.System = write("etc/bad", "User u\nBogus 1\n", 0o644)
	if _, err := Resolve("h", o); err == nil || !strings.Contains(err.Error(), "line 2:") {
		t.Fatalf("a bad system file must fail at its line: %v", err)
	}
}

func TestSystemInclude(t *testing.T) {
	dir := t.TempDir()
	sys := filepath.Join(dir, "ssh_config")
	os.WriteFile(sys, []byte("Include ~/x\n"), 0o644)
	if _, err := Resolve("h", Options{File: "", System: sys, Home: dir, LocalUser: "me"}); err == nil {
		t.Fatal("a system file can't Include from ~")
	}
	os.WriteFile(sys, []byte("Include nosuch-file-here\nUser s\n"), 0o644)
	if c, err := Resolve("h", Options{System: sys, Home: dir, LocalUser: "me"}); err != nil || c.User() != "s" {
		t.Fatalf("a relative system Include: %v", err)
	}
}

func TestIncludeTilde(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "inc"), []byte("User other\n"), 0o644)
	old := lookupUser
	lookupUser = func(name string) (string, error) {
		if name == "friend" {
			return dir, nil
		}
		return "", errors.New("no such user")
	}
	t.Cleanup(func() { lookupUser = old })
	if c := mustResolve(t, "Include ~friend/inc ~nobody/inc\n", "h", nil); c.User() != "other" {
		t.Fatalf("~user Include: got %s", c.User())
	}
	if c := mustResolve(t, "ControlPath ~friend/cp\n", "h", nil); c.Get("controlpath") != dir+"/cp" {
		t.Fatalf("~user path: got %s", c.Get("controlpath"))
	}
	if _, err := resolve(t, "Include [\n", "h", nil); err == nil {
		t.Fatal("a bad glob must fail")
	}
}

func TestIncludeVanished(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config")
	inc := filepath.Join(dir, "inc")
	os.WriteFile(cfg, []byte("Include "+inc+"\n"), 0o644)
	os.WriteFile(inc, []byte("User u\n"), 0o000)
	if os.Getuid() == 0 {
		t.Skip("root reads anything")
	}
	if _, err := Resolve("h", Options{File: cfg, Home: dir, LocalUser: "me"}); err == nil {
		t.Fatal("an included file that can't be read must fail")
	}
}

func TestMatchInjected(t *testing.T) {
	_, n, _ := net.ParseCIDR("192.0.2.5/24")
	n.IP = net.ParseIP("192.0.2.5")
	nets := func(o *Options) {
		o.Networks = func() []net.IPNet { return []net.IPNet{{IP: net.IP{1, 2, 3}}, *n} }
	}
	if c := mustResolve(t, "Match localnetwork 198.51.100.0/24,192.0.2.0/24\n  User lan\n", "h", nets); c.User() != "lan" {
		t.Fatalf("localnetwork: got %s", c.User())
	}
	if c := mustResolve(t, "Match !localnetwork 192.0.2.0/24\n  User lan\n", "h", nets); c.User() != "me" {
		t.Fatalf("!localnetwork: got %s", c.User())
	}
	var ran []string
	exe := func(ok bool, err error) func(*Options) {
		return func(o *Options) {
			o.Exec = func(cmd string) (bool, error) { ran = append(ran, cmd); return ok, err }
		}
	}
	if c := mustResolve(t, "Match exec \"check %h %p\"\n  User yes\n", "h", exe(true, nil)); c.User() != "yes" || ran[0] != "check h 22" {
		t.Fatalf("exec: got %s after %q", c.User(), ran)
	}
	if c := mustResolve(t, "Match !exec x\n  User no\n", "h", exe(false, nil)); c.User() != "no" {
		t.Fatalf("!exec: got %s", c.User())
	}
	if _, err := resolve(t, "Match exec x\n", "h", exe(false, errors.New("died"))); err == nil {
		t.Fatal("an exec that dies must fail")
	}
	if _, err := resolve(t, "Match exec %Z\n", "h", exe(true, nil)); err == nil {
		t.Fatal("an exec with an unknown token must fail")
	}
	if _, err := resolve(t, "HostName %Z\nMatch exec x\n", "h", exe(true, nil)); err == nil {
		t.Fatal("a HostName with an unknown token must fail")
	}
	if c := mustResolve(t, "Match exec \"%k %j\"\n  User x\nHostKeyAlias alias\nProxyJump j\nMatch exec \"%k %j\"\n", "h", exe(true, nil)); ran[len(ran)-2] != "h " || ran[len(ran)-1] != "alias j" || c.User() != "x" {
		t.Fatalf("exec %%k %%j: %q", ran[len(ran)-2:])
	}
}

func TestRunShell(t *testing.T) {
	t.Setenv("SHELL", "")
	if ok, err := runShell("exit 0"); !ok || err != nil {
		t.Fatalf("exit 0: %v %v", ok, err)
	}
	if ok, err := runShell("exit 3"); ok || err != nil {
		t.Fatalf("exit 3: %v %v", ok, err)
	}
	if _, err := runShell("kill -9 $$"); err == nil {
		t.Fatal("a killed command must be an error")
	}
	t.Setenv("SHELL", "/nonexistent/shell")
	if _, err := runShell("true"); err == nil {
		t.Fatal("a missing shell must be an error")
	}
}

func TestInterfaceNets(t *testing.T) {
	for _, n := range interfaceNets() {
		if n.IP == nil {
			t.Fatal("an address without IP")
		}
	}
}

func TestAccessors(t *testing.T) {
	t.Setenv("SSHCFG_TEST", "val")
	c := mustResolve(t, `
User u
Port 2222
IdentityFile ~/.ssh/id_%h
IdentityFile ${SSHCFG_TEST}/k
CertificateFile %d/cert
UserKnownHostsFile ~/kh
GlobalKnownHostsFile /etc/a /etc/b
ConnectTimeout 1m
BatchMode yes
LocalForward 1 h:2
LocalForward 3 socks:4
DynamicForward 1080
RemoteForward 9 h:22
ProxyJump a@j1:2,j2
Ciphers aes128-ctr,aes256-ctr
SendEnv A B
`, "h", nil)
	home := c.home
	if c.HostName() != "h" || c.Port() != 2222 || c.User() != "u" {
		t.Fatalf("got %s@%s:%d", c.User(), c.HostName(), c.Port())
	}
	if ids, err := c.IdentityFiles(); err != nil || !slices.Equal(ids, []string{home + "/.ssh/id_h", "val/k"}) {
		t.Fatalf("IdentityFiles %q %v", ids, err)
	}
	if cs, err := c.CertificateFiles(); err != nil || !slices.Equal(cs, []string{home + "/cert"}) {
		t.Fatalf("CertificateFiles %q %v", cs, err)
	}
	if !slices.Equal(c.UserKnownHostsFiles(), []string{home + "/kh"}) || !slices.Equal(c.GlobalKnownHostsFiles(), []string{"/etc/a", "/etc/b"}) {
		t.Fatalf("known hosts %q %q", c.UserKnownHostsFiles(), c.GlobalKnownHostsFiles())
	}
	if c.Duration("connecttimeout") != time.Minute || !c.Bool("batchmode") || c.Bool("compression") || c.Int("port") != 2222 {
		t.Fatal("Duration/Bool/Int")
	}
	if !slices.Equal(c.Algorithms("ciphers"), []string{"aes128-ctr", "aes256-ctr"}) || c.Algorithms("nothing") != nil {
		t.Fatalf("Algorithms %q", c.Algorithms("ciphers"))
	}
	if !slices.Equal(c.List("sendenv"), []string{"A", "B"}) || c.Get("nothing") != "" || c.Get("port") != "2222" {
		t.Fatal("List/Get")
	}
	if l := c.LocalForwards(); len(l) != 2 || l[0].ConnectHost != "h" || l[1].ConnectHost != "socks" || l[1].ConnectPort != 4 {
		t.Fatalf("LocalForwards %+v", l)
	}
	if d := c.DynamicForwards(); len(d) != 1 || d[0].ListenPort != 1080 {
		t.Fatalf("DynamicForwards %+v", d)
	}
	if r := c.RemoteForwards(); len(r) != 1 || r[0].ListenPort != 9 {
		t.Fatalf("RemoteForwards %+v", r)
	}
	if h := c.ProxyJump(); !slices.Equal(h, []Hop{{"a", "j1", 2}, {"", "j2", 0}}) || c.ProxyCommand() != "" {
		t.Fatalf("ProxyJump %+v, ProxyCommand %q", h, c.ProxyCommand())
	}
	tok := c.Tokens()
	tok['T'] = "tun0"
	if s, err := Expand("%h:%p:%r:%T", tok, false); err != nil || s != "h:2222:u:tun0" {
		t.Fatalf("Expand %q %v", s, err)
	}
	if _, ok := c.Tokens()['T']; ok {
		t.Fatal("Tokens must be a copy")
	}
	if !strings.HasPrefix(c.Dump(), "host h\nuser u\nhostname h\nport 2222\n") {
		t.Fatalf("Dump starts %q", c.Dump()[:40])
	}
	p := mustResolve(t, "ProxyCommand nc %h %p\n", "h", nil)
	if p.ProxyCommand() != "nc %h %p" || p.ProxyJump() != nil {
		t.Fatalf("ProxyCommand %q", p.ProxyCommand())
	}
	bad := mustResolve(t, "IdentityFile ${SSHCFG_NOPE}\nCertificateFile ~nosuchuser_x/c\n", "h", nil)
	if _, err := bad.IdentityFiles(); err == nil {
		t.Fatal("an unset ${VAR} in IdentityFile must fail")
	}
	if _, err := bad.CertificateFiles(); err == nil {
		t.Fatal("an unknown ~user in CertificateFile must fail")
	}
}

func TestProxyJumpNone(t *testing.T) {
	c := mustResolve(t, "ProxyJump none\nProxyJump j\n", "h", nil)
	if c.ProxyJump() != nil || strings.Contains(c.Dump(), "proxyjump") {
		t.Fatalf("ProxyJump none leaves no jump: %v", c.ProxyJump())
	}
}

func TestWarnings(t *testing.T) {
	c := mustResolve(t, "Cipher x\nRSAAuthentication no\nInclude ${SSHCFG_NOPE}\nForwardAgent "+t.TempDir()+"/nosock\n", "h", nil)
	if len(c.Warnings) != 4 {
		t.Fatalf("warnings %q", c.Warnings)
	}
	if _, err := resolve(t, "ForwardAgent /nonexistent/sock\nExitOnForwardFailure yes\n", "h", nil); err == nil {
		t.Fatal("a missing agent socket with ExitOnForwardFailure must fail")
	}
}

func TestResolveErrors(t *testing.T) {
	for name, c := range map[string]struct {
		cfg, host string
		mod       func(*Options)
	}{
		"bad port option":   {"", "h", func(o *Options) { o.Port = 70000 }},
		"negative port":     {"", "h", func(o *Options) { o.Port = -1 }},
		"bad override":      {"", "h", func(o *Options) { o.Overrides = []string{"Bogus 1"} }},
		"bad uri":           {"", "ssh://", nil},
		"empty user":        {"", "@h", nil},
		"bad user":          {"", "a;b@h", nil},
		"bad host":          {"", "a b", nil},
		"proxy fdpass":      {"ProxyCommand -\nProxyUseFdpass yes\n", "h", nil},
		"setenv token":      {"SetEnv A=%Z\n", "h", nil},
		"forward token":     {"LocalForward /tmp/%Z h:1\n", "h", nil},
		"revoked token":     {"RevokedHostKeys %Z\n", "h", nil},
		"hostkey algorithm": {"HostKeyAlgorithms !x*\n", "h", nil},
		"bad user expanded": {"User %Z\n", "h", nil},
		"bad cli user":      {"", "h", func(o *Options) { o.User = "-x" }},
		"uri bad user":      {"", "ssh://-x@h", nil},
	} {
		if _, err := resolve(t, c.cfg, c.host, c.mod); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if c := mustResolve(t, "", "h", func(o *Options) { o.Port = 2; o.User = "cli" }); c.Port() != 2 || c.User() != "cli" {
		t.Fatalf("-p/-l: %s:%d", c.User(), c.Port())
	}
	if c := mustResolve(t, "", "ssh://h:5", func(o *Options) { o.Port = 2 }); c.Port() != 2 {
		t.Fatalf("-p wins over the URI's port: %d", c.Port())
	}
}

func TestNewResolverDefaults(t *testing.T) {
	r, err := newResolver(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if r.home == "" || r.localUser == "" || r.opt.Exec == nil || r.opt.Lookup == nil || r.opt.Networks == nil || r.opt.System != "/etc/ssh/ssh_config" {
		t.Fatalf("defaults %+v", r)
	}
	t.Setenv("HOME", "")
	t.Chdir(t.TempDir())
	if _, err := newResolver(Options{}); err == nil {
		t.Fatal("no home must fail")
	}
}

func TestCanonicalizeEdges(t *testing.T) {
	all := "CanonicalizeHostname yes\nCanonicalizePermittedCNAMEs *:*\n"
	for _, c := range []struct {
		cfg, host string
		d         dns
		want      string // "" = an error
	}{
		{all + "CanonicalDomains example.com\nCanonicalizeMaxDots 9\n", "1.2.3.4.5", dns{"1.2.3.4.5.example.com.": "x", "1.2.3.4.5": "1.2.3.4.5"}, "1.2.3.4.5"},
		{all, "1.2.3.4.5", nil, ""}, // looks like an address: no suffixes, but the bare lookup fails
		{all, "fq.example.com.", dns{"fq.example.com.": "fq.example.com"}, "fq.example.com"},
		{all, "a.b.c", dns{"a.b.c": "x.example.net"}, "x.example.net"}, // too many dots: the bare name's CNAME
		{all, "h", dns{"h": ""}, "h"},
		{all, "h", dns{"h": "h"}, "h"},
		{all, "192.0.2.1", nil, "192.0.2.1"},
		{"CanonicalizePermittedCNAMEs *:*\n", "h", nil, ""}, // looked up even without canonicalization
		{"CanonicalizePermittedCNAMEs *:*\n", "h", dns{"h": "x.example.net"}, "h"},
	} {
		got, err := resolve(t, c.cfg, c.host, withDNS(c.d))
		switch {
		case c.want == "" && err == nil:
			t.Errorf("%s: no error, got %s", c.host, got.HostName())
		case c.want != "" && err != nil:
			t.Errorf("%s: %v", c.host, err)
		case c.want != "" && got.HostName() != c.want:
			t.Errorf("%s: got %s, want %s", c.host, got.HostName(), c.want)
		}
	}
}

func TestLiteralUsers(t *testing.T) {
	for host, mod := range map[string]func(*Options){
		"h":              func(o *Options) { o.User = "x%hy" },
		"x%hy@h":         nil,
		"ssh://x%25hy@h": nil,
	} {
		if c := mustResolve(t, "User cfg-%h\n", host, mod); c.User() != "x%hy" {
			t.Errorf("%s: a command-line user isn't expanded: %s", host, c.User())
		}
	}
	if c := mustResolve(t, "User cfg-%h\n", "h", nil); c.User() != "cfg-h" {
		t.Errorf("a configured user is: %s", c.User())
	}
}

func TestKnownHostsNone(t *testing.T) {
	c := mustResolve(t, "GlobalKnownHostsFile none\nUserKnownHostsFile none\nProxyJump x;y,j\n", "h", nil)
	if len(c.GlobalKnownHostsFiles()) != 0 || len(c.UserKnownHostsFiles()) != 0 {
		t.Fatalf("none is no file: %q %q", c.GlobalKnownHostsFiles(), c.UserKnownHostsFiles())
	}
	if h := c.ProxyJump(); len(h) != 2 || h[0].Host != "x;y" {
		t.Fatalf("a configured hop isn't checked strictly: %+v", h)
	}
}

func TestMissingFile(t *testing.T) {
	_, err := Resolve("h", Options{File: filepath.Join(t.TempDir(), "nope"), LocalUser: "me", Home: t.TempDir()})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a missing -F file: %v", err)
	}
	if os.Getuid() == 0 {
		return
	}
	sys := filepath.Join(t.TempDir(), "sys")
	os.WriteFile(sys, []byte("User s\n"), 0o000)
	if c, err := Resolve("h", Options{System: sys, LocalUser: "me", Home: t.TempDir()}); err != nil || c.User() != "me" {
		t.Fatalf("an unreadable system file is skipped: %v", err)
	}
}

func TestSystemLookup(t *testing.T) {
	if _, err := net.LookupHost("localhost"); err != nil {
		t.Skipf("no local resolver: %v", err)
	}
	if cn, err := systemLookup("localhost"); err != nil || cn == "" {
		t.Fatalf("localhost: %q %v", cn, err)
	}
	if _, err := systemLookup("nosuchhost.invalid"); err == nil {
		t.Fatal(".invalid must not resolve")
	}
}

func TestRelativeToFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a%b[c") // % and [ must survive expansion and glob
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	cfg := filepath.Join(dir, "config")
	os.WriteFile(cfg, []byte(`Include sub/inc
IdentityFile keys/id
CertificateFile keys/id-cert.pub
UserKnownHostsFile kh kh2
GlobalKnownHostsFile gkh
RevokedHostKeys revoked
IdentityAgent agent.sock
ControlPath cp/%h
ForwardAgent fwd.sock
PKCS11Provider p11.so
SecurityKeyProvider sk.so
XAuthLocation xauth
`), 0o644)
	os.WriteFile(filepath.Join(dir, "sub/inc"), []byte("User inc\nIdentityFile k2\n"), 0o644)
	o := Options{File: cfg, System: "none", Home: t.TempDir(), LocalUser: "me", RelativeToFile: true,
		Overrides: []string{"CertificateFile ov"}}
	c, err := Resolve("h", o)
	if err != nil {
		t.Fatal(err)
	}
	if c.User() != "inc" {
		t.Fatal("Include not taken from the file's folder")
	}
	at := func(s string) string { return dir + "/" + s }
	if ids, _ := c.IdentityFiles(); !slices.Equal(ids, []string{at("sub/k2"), at("keys/id")}) {
		t.Fatalf("IdentityFile %q", ids)
	}
	if cs, _ := c.CertificateFiles(); !slices.Equal(cs, []string{"ov", at("keys/id-cert.pub")}) {
		t.Fatalf("CertificateFile (-o's stays) %q", cs)
	}
	if l := c.UserKnownHostsFiles(); !slices.Equal(l, []string{at("kh"), at("kh2")}) {
		t.Fatalf("UserKnownHostsFile %q", l)
	}
	if l := c.GlobalKnownHostsFiles(); !slices.Equal(l, []string{at("gkh")}) {
		t.Fatalf("GlobalKnownHostsFile %q", l)
	}
	for k, want := range map[string]string{
		"revokedhostkeys": at("revoked"), "identityagent": at("agent.sock"), "controlpath": at("cp/h"),
		"forwardagent": at("fwd.sock"), "pkcs11provider": at("p11.so"), "securitykeyprovider": at("sk.so"),
		"xauthlocation": at("xauth"),
	} {
		if got := c.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}

	// Off, as OpenSSH: from the working directory, Include from ~/.ssh.
	o.RelativeToFile = false
	if c, err = Resolve("h", o); err != nil {
		t.Fatal(err)
	}
	if ids, _ := c.IdentityFiles(); c.User() == "inc" || !slices.Equal(ids, []string{"keys/id"}) {
		t.Fatalf("anchored without RelativeToFile: %s %q", c.User(), ids)
	}
}

func TestRelativeToFileKeeps(t *testing.T) {
	t.Setenv("SSHCFG_REL", "/env")
	keep := func(o *Options) { o.RelativeToFile = true }
	c := mustResolve(t, "IdentityFile ~/a\nIdentityFile %d/b\nIdentityFile /abs/c\nIdentityFile ${SSHCFG_REL}/d\n"+
		"IdentityAgent SSH_AUTH_SOCK\nControlPath none\nForwardAgent yes\nSecurityKeyProvider internal\n"+
		"UserKnownHostsFile none\nPKCS11Provider none\n", "h", keep)
	home := c.Tokens()['d']
	if ids, _ := c.IdentityFiles(); !slices.Equal(ids, []string{home + "/a", home + "/b", "/abs/c", "/env/d"}) {
		t.Fatalf("IdentityFile %q", ids)
	}
	for k, want := range map[string]string{
		"identityagent": "SSH_AUTH_SOCK", "controlpath": "", "forwardagent": "yes", // none prints nothing
		"securitykeyprovider": "internal", "pkcs11provider": "",
	} {
		if got := c.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if l := c.UserKnownHostsFiles(); len(l) != 0 {
		t.Errorf("UserKnownHostsFile none: %q", l)
	}
}
