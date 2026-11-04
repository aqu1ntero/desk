package infra

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// asbruSample is an asbru.yml as Ásbrú writes it: a nested group tree, SSH
// hosts by password, by key behind a jump host, one with variables, one not
// SSH, Ásbrú's own shell, and two hosts of one name.
const asbruSample = `---
defaults:
  proxy: No Proxy
environments:
  __PAC__ROOT__:
    children:
      g1: 1
  __PAC_SHELL__:
    ip: bash
    method: PACShell
    name: PACShell
  HASHabc:
    name: junk
  g1:
    _is_group: 1
    name: Prod
    parent: __PAC__ROOT__
  g2:
    _is_group: 1
    name: Web
    parent: g1
  g3:
    _is_group: 1
    name: Lab/Test
    parent: __PAC__ROOT__
  h1:
    auth type: userpass
    description: Connection with 'web-01'
    ip: 192.0.2.11
    method: SSH
    name: web-01
    options: ' -x -C -o "ServerAliveInterval=60" -L 8080:localhost:80'
    parent: g2
    pass: 53616c7465645f5f4e61bc0000000000a1b2c3d4e5f60718
    port: 22
    use proxy: 0
    user: deploy
  h2:
    auth fallback: 0
    auth type: publickey
    description: |-
      the public proxy
      second line
    ip: 192.0.2.12
    jump ip: 192.0.2.1
    jump key: /home/me/.ssh/id_bastion
    jump port: 2222
    jump user: jumper
    method: SSH
    name: web 02
    options: ' -X -A -p 2201'
    parent: g2
    passphrase user: admin
    port: 2200
    public key: /home/me/.ssh/id_ed25519
    use proxy: 3
    user: ''
  h3:
    ip: <GV:dbhost>
    method: ssh
    name: db-01
    parent: g1
  h4:
    ip: 192.0.2.50
    method: RDP (xfreerdp)
    name: win-01
    parent: g1
  h5:
    ip: 192.0.2.60
    method: ssh
    name: web-01
    parent: g3
    port: 0
    user: root
  h6:
    method: SSH
    name: nowhere
    parent: g3
  h7:
    name: lost
    ip: 192.0.2.70
    method: SSH
    parent: gone
`

// TestAsbruConfig: groups become #@ group paths, hosts their blocks (the
// key user for a publickey login, the options as keywords, the jump host
// hidden behind ProxyJump); what can't come says why; a taken name gets its
// group in front.
func TestAsbruConfig(t *testing.T) {
	text, added, skipped, err := AsbruConfig([]byte(asbruSample), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{
		"#@ group Prod/Web\nHost web-01\n    HostName 192.0.2.11\n    User deploy\n    Compression yes\n    ServerAliveInterval 60\n    LocalForward 8080 localhost:80\n",
		"Host web-02\n    HostName 192.0.2.12\n    User admin\n    Port 2201\n    IdentityFile /home/me/.ssh/id_ed25519\n    PreferredAuthentications publickey\n    ProxyJump jump-192.0.2.1-2222\n    #@ desc the public proxy\n    ForwardX11 yes\n    ForwardAgent yes\n",
		"#@ group servers\nHost jump-192.0.2.1-2222\n    HostName 192.0.2.1\n    #@ hidden\n    User jumper\n    Port 2222\n    IdentityFile /home/me/.ssh/id_bastion\n",
		"Host lost\n    HostName 192.0.2.70\n", // a parent that isn't there: top level
		"#@ group Lab-Test\nHost Lab-Test-web-01\n    HostName 192.0.2.60\n    User root\n",
		": 4 servers.\n",
	} {
		if !strings.Contains(text, w) {
			t.Errorf("no\n%s\nin\n%s", w, text)
		}
	}
	if strings.Contains(text, "pass") && strings.Contains(text, "53616c74") || strings.Contains(text, "Port 22\n") || strings.Contains(text, "Port 0") || strings.Contains(text, "Connection with") {
		t.Errorf("a password, a default port or Ásbrú's default description came:\n%s", text)
	}
	if !slices.Equal(added, []string{"Lab-Test-web-01", "lost", "web-01", "web-02"}) {
		t.Errorf("added %q", added)
	}
	want := []string{"Lab-Test/nowhere: no address", "Prod/db-01: uses Ásbrú variables (<GV:dbhost>)", "Prod/win-01: RDP (xfreerdp), not SSH"}
	if slices.Sort(skipped); !slices.Equal(skipped, want) {
		t.Errorf("skipped %q", skipped)
	}
	hs, err := parseServers(writeTemp(t, text), filepath.Dir(t.TempDir()), false)
	if err != nil || len(hs) != 5 || hostByAlias(hs, "web-02").group != "Prod/Web" || hostByAlias(hs, "web-02").desc != "the public proxy" || !hostByAlias(hs, "jump-192.0.2.1-2222").hidden {
		t.Fatalf("desk reads it back: %+v %v", hs, err)
	}
}

func writeTemp(t *testing.T, s string) string {
	p := filepath.Join(t.TempDir(), "ssh_config")
	write(t, p, s)
	return p
}

// TestAsbruConfigGroup: one group's hosts only, its own hosts at the top and
// its subgroups relative; names the file has are left out; nothing left is
// no text.
func TestAsbruConfigGroup(t *testing.T) {
	text, added, _, err := AsbruConfig([]byte(asbruSample), "/Prod/", map[string]bool{"web-01": true})
	if err != nil || !slices.Equal(added, []string{"web-02"}) || !strings.Contains(text, "#@ group Web\n") || strings.Contains(text, "192.0.2.60") || strings.Contains(text, "192.0.2.11") {
		t.Fatalf("%q %v\n%s", added, err, text)
	}
	if text, added, _, _ := AsbruConfig([]byte(asbruSample), "Prod/Web", nil); len(added) != 2 || !strings.Contains(text, "#@ group servers\n") || strings.Contains(text, "#@ group Web") {
		t.Fatalf("the group itself: %q\n%s", added, text)
	}
	if text, added, _, err := AsbruConfig([]byte(asbruSample), "Nope", nil); text != "" || added != nil || err != nil {
		t.Fatalf("a group that isn't there: %q %q %v", text, added, err)
	}
	if g, err := AsbruGroups([]byte(asbruSample)); err != nil || !slices.Equal(g, []string{"Lab-Test", "Prod"}) {
		t.Fatalf("groups %q %v", g, err)
	}
}

// TestAsbruExport: a tree export (no environments, nodes under
// __PAC__EXPORTED__, " - copy" names) reads as well; what isn't Ásbrú's says so.
func TestAsbruExport(t *testing.T) {
	exp := `__PAC__EXPORTED__:
  children:
    n1: 1
n1:
  _is_group: 1
  name: Prod - copy
  parent: __PAC__EXPORTED__
n2:
  ip: 192.0.2.5
  method: SSH
  name: app - copy
  parent: n1
  port: 2022
`
	text, added, _, err := AsbruConfig([]byte(exp), "", nil)
	if err != nil || !slices.Equal(added, []string{"app"}) || !strings.Contains(text, "#@ group Prod\nHost app\n    HostName 192.0.2.5\n    Port 2022\n") {
		t.Fatalf("%q %v\n%s", added, err, text)
	}
	for _, bad := range []string{"defaults: {}\n", "{[", "- a\n"} {
		if _, err := AsbruGroups([]byte(bad)); err == nil {
			t.Errorf("%q read", bad)
		}
		if _, _, _, err := AsbruConfig([]byte(bad), "", nil); err == nil {
			t.Errorf("%q converted", bad)
		}
	}
}

// TestAsbruProxy: a SOCKS proxy is a ProxyCommand; "0" follows the global
// setting, a jump or a proxy; one jump host serves every host behind it, a
// second of the same address gets a number, one the file has is not written.
func TestAsbruProxy(t *testing.T) {
	y := `defaults:
  proxy: Jump
  jump ip: 192.0.2.9
  jump user: ops
environments:
  a: {name: a, ip: 192.0.2.1, method: SSH, use proxy: 0}
  b: {name: b, ip: 192.0.2.2, method: SSH}
  c: {name: c, ip: 192.0.2.3, method: SSH, use proxy: 1, proxy ip: 192.0.2.8}
  d: {name: d, ip: 192.0.2.4, method: SSH, use proxy: 3, jump ip: 192.0.2.9, jump user: root}
  e: {name: e, ip: 192.0.2.5, method: SSH, use proxy: 2}
`
	text, _, _, err := AsbruConfig([]byte(y), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"Host a\n    HostName 192.0.2.1\n    ProxyJump jump-192.0.2.9\n", "Host b\n    HostName 192.0.2.2\n    ProxyJump jump-192.0.2.9\n",
		"Host c\n    HostName 192.0.2.3\n    ProxyCommand nc -X 5 -x 192.0.2.8:8080 %h %p\n", "Host e\n    HostName 192.0.2.5\nHost", "    User ops\n", "    User root\n"} {
		if !strings.Contains(text, w) {
			t.Errorf("no %q in\n%s", w, text)
		}
	}
	if strings.Count(text, "Host jump-192.0.2.9") != 2 || !strings.Contains(text, "ProxyJump jump-192.0.2.9-2") {
		t.Errorf("one jump block per jump host:\n%s", text)
	}
	text, _, _, _ = AsbruConfig([]byte(y), "", map[string]bool{"jump-192.0.2.9": true})
	if strings.Contains(text, "Host jump-192.0.2.9\n") || !strings.Contains(text, "ProxyJump jump-192.0.2.9\n") {
		t.Errorf("the file's jump host is used, not written again:\n%s", text)
	}
	socks := "defaults: {proxy: Proxy, proxy ip: 192.0.2.7, proxy port: 1080}\nenvironments:\n  a: {name: a, ip: 192.0.2.1, method: SSH}\n"
	if text, _, _, _ := AsbruConfig([]byte(socks), "", nil); !strings.Contains(text, "ProxyCommand nc -X 5 -x 192.0.2.7:1080 %h %p") {
		t.Errorf("the global proxy:\n%s", text)
	}
}

// TestAsbruOptions: Ásbrú's ssh command line options as keywords; what has
// none stays a comment.
func TestAsbruOptions(t *testing.T) {
	h := &asbruHost{}
	h.set("Port", "22")
	asbruOptions(h, ` -2 -4 -Yg -o "ConnectTimeout 5" -o "=x" -R 9000:localhost:90 -L 1.2.3.4:80:web:8080 -D 1080 -i k -l u -J j -p 2200 -N -Q`)
	want := [][2]string{{"Port", "2200"}, {"AddressFamily", "inet"}, {"ForwardX11Trusted", "yes"}, {"GatewayPorts", "yes"}, {"ConnectTimeout", "5"},
		{"RemoteForward", "9000 localhost:90"}, {"LocalForward", "1.2.3.4:80 web:8080"}, {"DynamicForward", "1080"}, {"IdentityFile", "k"}, {"User", "u"}, {"ProxyJump", "j"}}
	if !slices.Equal(h.lines, want) {
		t.Errorf("lines %q", h.lines)
	}
	if !slices.Equal(h.notes, []string{"Ásbrú ssh option not imported: -N", "Ásbrú ssh option not imported: -Q"}) {
		t.Errorf("notes %q", h.notes)
	}
	h = &asbruHost{}
	asbruOptions(h, "-6 -L 8080 -o")
	if !slices.Equal(h.lines, [][2]string{{"AddressFamily", "inet6"}, {"LocalForward", "8080"}}) {
		t.Errorf("short ones: %q", h.lines)
	}
}

// TestAsbruEdges: Ásbrú's internal entries stay out; the key user only for
// a key login, a key login only refusing passwords when it says so; a "<"
// alone is no variable; jump hosts on one address number on, port 22 is
// no port; an export's and a parentless group are top-level groups.
func TestAsbruEdges(t *testing.T) {
	y := `environments:
  _tmp_1: {name: tmp, ip: 192.0.2.1, method: SSH}
  pacshell_PID9: {name: shell, ip: 192.0.2.2, method: SSH}
  a: {name: a, ip: 192.0.2.3, method: SSH, auth type: userpass, user: me, passphrase user: other, public key: k, auth fallback: 0}
  b: {name: b, ip: 192.0.2.4, method: SSH, auth type: publickey, user: me, passphrase user: ''}
  c: {name: c, ip: 192.0.2.5, method: SSH, user: 'odd<name'}
  d: {name: d, ip: 192.0.2.6, method: SSH, use proxy: 3, jump ip: 192.0.2.9, jump user: u1, jump port: 22}
  e: {name: e, ip: 192.0.2.7, method: SSH, use proxy: 3, jump ip: 192.0.2.9, jump user: u2}
  f: {name: f, ip: 192.0.2.8, method: SSH, use proxy: 3, jump ip: 192.0.2.9, jump user: u3}
  g: {_is_group: 1, name: Loose}
`
	text, added, skipped, err := AsbruConfig([]byte(y), "", nil)
	if err != nil || len(skipped) != 0 || strings.Contains(text, "tmp") || strings.Contains(text, "shell") {
		t.Fatalf("%v %q\n%s", err, skipped, text)
	}
	for _, w := range []string{"Host a\n    HostName 192.0.2.3\n    User me\n    IdentityFile k\nHost", "Host b\n    HostName 192.0.2.4\n    User me\n", "    User odd<name\n",
		"ProxyJump jump-192.0.2.9\n", "ProxyJump jump-192.0.2.9-2\n", "ProxyJump jump-192.0.2.9-3\n", "Host jump-192.0.2.9\n    HostName 192.0.2.9\n    #@ hidden\n    User u1\nHost"} {
		if !strings.Contains(text, w) {
			t.Errorf("no %q in\n%s", w, text)
		}
	}
	if len(added) != 6 || strings.Contains(text, "jump-192.0.2.9-22") || strings.Contains(text, "Port 22") {
		t.Errorf("added %q\n%s", added, text)
	}
	if g, _ := AsbruGroups([]byte(y)); !slices.Equal(g, []string{"Loose"}) {
		t.Errorf("a group with no parent: %q", g)
	}
	exp := "__PAC__EXPORTED__: {children: {n: 1}}\nn: {_is_group: 1, name: Exported, parent: __PAC__EXPORTED__}\n"
	if g, _ := AsbruGroups([]byte(exp)); !slices.Equal(g, []string{"Exported"}) {
		t.Errorf("an export's group: %q", g)
	}
	if text, _, _, _ := AsbruConfig([]byte(y), "", map[string]bool{"a": false}); !strings.Contains(text, "Host a\n") {
		t.Error("a name the file doesn't have is not taken")
	}
	h := &asbruHost{}
	asbruOptions(h, "word")
	if !slices.Equal(h.notes, []string{"Ásbrú ssh option not imported: word"}) {
		t.Errorf("a bare word: %q", h.notes)
	}
}

// TestAsbruValues: Perl's YAML scalars as text; a path through a loop ends.
func TestAsbruValues(t *testing.T) {
	for want, v := range map[string]any{"": nil, "1": true, "0": false, "2.5": 2.5, "22": 22, "x": "x"} {
		if got := asbruStr(v); got != want {
			t.Errorf("%v: %q", v, got)
		}
	}
	tr := &asbruTree{nodes: map[string]*asbruNode{"a": {name: "A", parent: "b", group: true}, "b": {name: "B", parent: "a", group: true}, "h": {name: "h", parent: "a"}}}
	if p := tr.path("h"); p != "B/A" {
		t.Errorf("loop: %q", p)
	}
	used, none := map[string]bool{}, map[string]bool{}
	for range 101 {
		asbruAlias("x", "", none, used)
	}
	if a := asbruAlias("x", "", none, used); a != "" {
		t.Errorf("no name left: %q", a)
	}
	if a := asbruAlias("ü !", "g/h", none, map[string]bool{}); a != "-" {
		t.Errorf("slug %q", a)
	}
	if a := asbruAlias("ü", "", none, map[string]bool{}); a != "host" {
		t.Errorf("nothing left of the name: %q", a)
	}
	if a := asbruAlias("db", "eu/g", map[string]bool{"g-db": true}, map[string]bool{"db": true}); a != "db-2" {
		t.Errorf("past the file's and this import's names: %q", a)
	}
	if a := asbruAlias("DB", "", map[string]bool{"db": true}, none); a != "" {
		t.Errorf("the file has it (any case): %q", a)
	}
}

// TestAsbruPath: $ASBRU_CFG, else ~/.config/asbru.
func TestAsbruPath(t *testing.T) {
	t.Setenv("ASBRU_CFG", "/cfg")
	if p := AsbruPath(); p != "/cfg/asbru.yml" {
		t.Error(p)
	}
	t.Setenv("ASBRU_CFG", "")
	t.Setenv("HOME", "/home/me")
	if p := AsbruPath(); p != "/home/me/.config/asbru/asbru.yml" {
		t.Error(p)
	}
}

// TestImportAsbru: an org's servers file gets the hosts added (made 0600
// when it had none, appended after what it had); a second import adds
// nothing; a missing or unreadable file says so.
func TestImportAsbru(t *testing.T) {
	org := filepath.Join(t.TempDir(), "Acme")
	src := filepath.Join(t.TempDir(), "asbru.yml")
	write(t, src, asbruSample)
	file, added, _, err := ImportAsbru(org, src, "Prod")
	if err != nil || file != filepath.Join(org, "ssh_config") || len(added) != 2 {
		t.Fatalf("%q %q %v", file, added, err)
	}
	if st, _ := os.Stat(file); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode())
	}
	before, _ := os.ReadFile(file)
	if _, added, skipped, err := ImportAsbru(org, src, "Prod"); err != nil || added != nil || !slices.Contains(skipped, "Prod/Web/web-01: already in the servers file") {
		t.Fatalf("again: %q %q %v", added, skipped, err)
	}
	if after, _ := os.ReadFile(file); string(after) != string(before) {
		t.Fatalf("nothing to add changed the file:\n%s", after)
	}
	other := filepath.Join(t.TempDir(), "Globex")
	write(t, filepath.Join(other, "desk.toml"), "[servers]\nfile = \"infra/hosts.conf\"\n")
	write(t, filepath.Join(other, "infra", "hosts.conf"), "Host web-01\n    HostName 192.0.2.99") // no newline at its end
	file, added, _, err = ImportAsbru(other, src, "")
	b, _ := os.ReadFile(file)
	if err != nil || file != filepath.Join(other, "infra", "hosts.conf") || slices.Contains(added, "web-01") || !strings.HasPrefix(string(b), "Host web-01\n    HostName 192.0.2.99\n\n# Imported") {
		t.Fatalf("%q %q %v\n%s", file, added, err, b)
	}
	if _, _, _, err := ImportAsbru(org, filepath.Join(t.TempDir(), "none.yml"), ""); err == nil {
		t.Error("a missing Ásbrú file")
	}
	bad := filepath.Join(t.TempDir(), "Initech")
	os.MkdirAll(filepath.Join(bad, "ssh_config"), 0o755) // a folder where the file goes
	if _, _, _, err := ImportAsbru(bad, src, ""); err == nil {
		t.Error("an unreadable servers file")
	}
	write(t, filepath.Join(bad, "desk.toml"), "[servers]\nfile = \"desk.toml/x\"\n") // its folder is a file
	if _, _, _, err := ImportAsbru(bad, src, ""); err == nil {
		t.Error("a servers file it can't make")
	}
	if _, _, _, err := ImportAsbru(org, writeTemp(t, "{["), ""); err == nil {
		t.Error("a broken Ásbrú file")
	}
}

// TestServersMain: desk servers import-asbru prints the file, or adds it to
// an organization's; its usage and failures exit non-zero.
func TestServersMain(t *testing.T) {
	src := writeTemp(t, asbruSample)
	var out, errw bytes.Buffer
	if c := ServersMain([]string{"import-asbru", "-group", "Prod/Web", src}, &out, &errw); c != 0 || !strings.Contains(out.String(), "Host web-02") {
		t.Fatalf("print: %d %s %s", c, out.String(), errw.String())
	}
	out.Reset()
	errw.Reset()
	org := filepath.Join(t.TempDir(), "Acme")
	if c := ServersMain([]string{"import-asbru", "-org", org, src}, &out, &errw); c != 0 || out.Len() != 0 ||
		!strings.Contains(errw.String(), "4 servers added to "+filepath.Join(org, "ssh_config")) || !strings.Contains(errw.String(), "skipped Prod/win-01") {
		t.Fatalf("add: %d %s", c, errw.String())
	}
	for args, code := range map[string]int{"": 2, "bogus": 2, "import-asbru -nope": 2, "import-asbru " + src + ".none": 1} {
		if c := ServersMain(strings.Fields(args), &out, &errw); c != code {
			t.Errorf("%q: %d, want %d", args, c, code)
		}
	}
	t.Setenv("ASBRU_CFG", filepath.Dir(src))
	os.Rename(src, filepath.Join(filepath.Dir(src), "asbru.yml"))
	out.Reset()
	if c := ServersMain([]string{"import-asbru"}, &out, &errw); c != 0 || !strings.Contains(out.String(), "Host web-01") {
		t.Errorf("Ásbrú's own file: %d", c)
	}
}
