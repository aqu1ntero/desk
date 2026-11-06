package sshc

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// hostLine is a known_hosts line for names and key.
func hostLine(names string, k ssh.PublicKey) string {
	return names + " " + string(ssh.MarshalAuthorizedKey(k))
}

func (s *server) name() string { return "[127.0.0.1]:" + strconv.Itoa(s.port) }

// pwServer is a server taking password pw, the world answering with it last.
func pwServer(t *testing.T, w *world, o srvOpts) *server {
	o.password = "pw"
	return newServer(t, o)
}

func TestUnknownHostStrict(t *testing.T) {
	w := newWorld(t)
	s := pwServer(t, w, srvOpts{})
	_, err := dialT(t, w.opts(base("  StrictHostKeyChecking yes\n"), s))
	if !errors.Is(err, ErrHostKey) || !strings.Contains(err.Error(), "No ED25519 host key is known for "+s.name()+" and you have requested strict checking.") {
		t.Fatalf("err %v", err)
	}
	if _, err := os.Stat(w.known()); err == nil {
		t.Fatal("known_hosts written")
	}
}

func TestUnknownHostAsk(t *testing.T) {
	fp := ""
	for _, tc := range []struct {
		name string
		ans  []string
		ok   bool
	}{
		{"yes", []string{"maybe", "YES", "pw"}, true},
		{"fingerprint", []string{"FP", "pw"}, true},
		{"no", []string{"no"}, false},
		{"empty", []string{""}, false},
		{"cancelled", nil, false},
	} {
		w := newWorld(t)
		s := pwServer(t, w, srvOpts{})
		fp = ssh.FingerprintSHA256(s.host.PublicKey())
		for i, a := range tc.ans {
			if a == "FP" {
				tc.ans[i] = fp
			}
		}
		w.ans = tc.ans
		_, err := dialT(t, w.opts(base("  StrictHostKeyChecking ask\n"), s))
		if (err == nil) != tc.ok {
			t.Fatalf("%s: err %v", tc.name, err)
		}
		if !tc.ok && !errors.Is(err, ErrHostKey) {
			t.Fatalf("%s: err %v", tc.name, err)
		}
		want := "The authenticity of host '" + s.name() + " (" + s.name() + ")' can't be established.\nED25519 key fingerprint is: " + fp +
			"\nAre you sure you want to continue connecting (yes/no/[fingerprint])? "
		if w.asks[0] != want || !w.echos[0] {
			t.Fatalf("%s: asked %q", tc.name, w.asks[0])
		}
		if tc.name == "yes" && w.asks[1] != "Please type 'yes', 'no' or the fingerprint: " {
			t.Fatalf("asked again %q", w.asks[1])
		}
		if got := read(t, w.known()); tc.ok != (got == hostLine(s.name(), s.host.PublicKey())) {
			t.Fatalf("%s: known_hosts %q", tc.name, got)
		}
	}
}

func TestAcceptNewWrites(t *testing.T) {
	w := newWorld(t)
	s := pwServer(t, w, srvOpts{})
	w.ans = []string{"pw"}
	mustDial(t, w.opts(base(""), s))
	if got := read(t, w.known()); got != hostLine(s.name(), s.host.PublicKey()) {
		t.Fatalf("known_hosts %q", got)
	}
	if !w.logged("Warning: Permanently added '" + s.name() + "' (ED25519) to the list of known hosts.") {
		t.Fatalf("logs %q", w.logs)
	}
	// Known now: strict checking passes, nothing more is written.
	w.ans = []string{"pw"}
	mustDial(t, w.opts(base("  StrictHostKeyChecking yes\n"), s))
	if got := read(t, w.known()); got != hostLine(s.name(), s.host.PublicKey()) {
		t.Fatalf("known_hosts %q", got)
	}
}

func TestStrictOffWrites(t *testing.T) {
	w := newWorld(t)
	s := pwServer(t, w, srvOpts{})
	w.ans = []string{"pw"}
	mustDial(t, w.opts(base("  StrictHostKeyChecking no\n"), s))
	if got := read(t, w.known()); got != hostLine(s.name(), s.host.PublicKey()) {
		t.Fatalf("known_hosts %q", got)
	}
}

func TestAcceptNewHashed(t *testing.T) {
	w := newWorld(t)
	s := pwServer(t, w, srvOpts{})
	write(t, w.known(), "# no newline at the end")
	w.ans = []string{"pw"}
	mustDial(t, w.opts(base("  HashKnownHosts yes\n"), s))
	ls := parseKnown([]byte(read(t, w.known())), w.known())
	if len(ls) != 1 || ls[0].line != 2 || !strings.HasPrefix(ls[0].hosts[0], "|1|") || !matchHosts(ls[0].hosts, s.name()) {
		t.Fatalf("known_hosts %q", read(t, w.known()))
	}
}

func TestKnownWithoutPort(t *testing.T) {
	w := newWorld(t)
	s := pwServer(t, w, srvOpts{})
	write(t, w.known(), hostLine("127.0.0.1", s.host.PublicKey()))
	w.ans = []string{"pw"}
	mustDial(t, w.opts(base("  StrictHostKeyChecking yes\n"), s))
	// An alias is looked up as it is.
	w.ans = []string{"pw"}
	if _, err := dialT(t, w.opts(base("  StrictHostKeyChecking yes\n  HostKeyAlias box\n"), s)); err == nil {
		t.Fatal("alias found by the host name")
	}
}

func TestGlobalKnownHosts(t *testing.T) {
	w := newWorld(t)
	s := pwServer(t, w, srvOpts{})
	write(t, w.path("global"), hostLine(s.name(), s.host.PublicKey()))
	w.ans = []string{"pw"}
	mustDial(t, w.opts(base("  StrictHostKeyChecking yes\n  GlobalKnownHostsFile %HOME/global\n"), s))
}

func TestNoUserKnownHostsFile(t *testing.T) {
	w := newWorld(t)
	s := pwServer(t, w, srvOpts{})
	_, err := dialT(t, w.opts(base("  UserKnownHostsFile none\n"), s))
	if !errors.Is(err, ErrHostKey) || !strings.Contains(err.Error(), "can't be recorded") {
		t.Fatalf("err %v", err)
	}
}

func TestHostKeyAlias(t *testing.T) {
	w := newWorld(t)
	s := pwServer(t, w, srvOpts{})
	w.ans = []string{"pw"}
	mustDial(t, w.opts(base("  HostKeyAlias Box.example.com\n"), s))
	if got := read(t, w.known()); got != hostLine("box.example.com", s.host.PublicKey()) {
		t.Fatalf("known_hosts %q", got)
	}
}

func changedWorld(t *testing.T) (*world, *server, ssh.PublicKey) {
	w := newWorld(t)
	s := pwServer(t, w, srvOpts{})
	old := newSigner(t).PublicKey()
	write(t, w.known(), hostLine("other.example.com", old)+hostLine(s.name(), old))
	return w, s, old
}

func TestChangedKey(t *testing.T) {
	w, s, old := changedWorld(t)
	_, err := dialT(t, w.opts(base(""), s))
	var e *HostKeyChangedError
	if !errors.As(err, &e) || !errors.Is(err, ErrHostKey) {
		t.Fatalf("err %v", err)
	}
	want := KnownLine{File: w.known(), Line: 2, Name: s.name(), Type: "ED25519", Fingerprint: ssh.FingerprintSHA256(old)}
	got := e.Known[0]
	got.key = nil
	if e.Host != s.name() || e.Type != "ED25519" || e.Fingerprint != ssh.FingerprintSHA256(s.host.PublicKey()) || len(e.Known) != 1 || got != want {
		t.Fatalf("error %+v", e)
	}
	if !strings.Contains(e.Error(), "HAS CHANGED") || !strings.Contains(e.Error(), "Offending ED25519 key in "+w.known()+":2") {
		t.Fatalf("message %q", e.Error())
	}
	if err := AcceptNewKey(err); err != nil {
		t.Fatal(err)
	}
	if got := read(t, w.known()); got != hostLine("other.example.com", old)+hostLine(s.name(), s.host.PublicKey()) {
		t.Fatalf("known_hosts %q", got)
	}
	w.ans = []string{"pw"}
	mustDial(t, w.opts(base("  StrictHostKeyChecking yes\n"), s))
}

func TestAcceptNewKeyRefuses(t *testing.T) {
	if err := AcceptNewKey(errors.New("other")); err == nil {
		t.Fatal("took another error")
	}
	w, s, old := changedWorld(t)
	_, err := dialT(t, w.opts(base(""), s))
	before := hostLine(s.name(), newSigner(t).PublicKey()) + hostLine("other.example.com", old)
	write(t, w.known(), before) // edited since: line 2 is another host's now
	if err := AcceptNewKey(err); err == nil || !strings.Contains(err.Error(), "no longer holds") {
		t.Fatalf("err %v", err)
	}
	if read(t, w.known()) != before {
		t.Fatal("known_hosts changed")
	}
	os.Remove(w.known())
	if err := AcceptNewKey(err); err == nil {
		t.Fatal("took a missing file")
	}
	if err := AcceptNewKey(&HostKeyChangedError{}); err == nil {
		t.Fatal("recorded nowhere")
	}
}

func TestChangedKeyStrictOff(t *testing.T) {
	w, s, _ := changedWorld(t)
	k := writeKey(t, w.path("id"), "")
	s.with(func(o *srvOpts) { o.keys = []ssh.PublicKey{k.PublicKey()} })
	w.ans = []string{"pw", "pw", "pw"}
	// Passwords are off then: the key logs in, nothing is asked.
	// The forward is never started (port 2 would need root).
	c := mustDial(t, w.opts(base("  StrictHostKeyChecking no\n  PreferredAuthentications password,publickey\n  LocalForward 127.0.0.1:2 127.0.0.1:1\n"), s))
	if len(w.asks) != 0 || !c.hc.unsafe || !w.logged("Port forwarding is disabled") {
		t.Fatalf("asked %q, logs %q", w.asks, w.logs)
	}
	_, err := dialT(t, w.opts(base("  StrictHostKeyChecking no\n  LocalForward 127.0.0.1:2 127.0.0.1:1\n  ExitOnForwardFailure yes\n"), s))
	if err == nil || !strings.Contains(err.Error(), "forwarding disabled") {
		t.Fatalf("err %v", err)
	}
}

func TestChangedKeyStrictOffNoPassword(t *testing.T) {
	for _, m := range []string{"password", "keyboard-interactive"} {
		w, s, _ := changedWorld(t)
		s.with(func(o *srvOpts) { o.kbd = []string{"pw"} })
		w.ans = []string{"pw"}
		_, err := dialT(t, w.opts(base("  StrictHostKeyChecking no\n  PreferredAuthentications "+m+"\n"), s))
		if err == nil || !strings.Contains(err.Error(), "disabled to avoid man-in-the-middle attacks") || len(w.asks) != 0 {
			t.Fatalf("%s: err %v", m, err)
		}
	}
}

// remote makes 127.0.0.1 count as a remote address, for CheckHostIP.
func remote(t *testing.T) {
	isLocal = func(net.IP) bool { return false }
	t.Cleanup(func() { isLocal = func(ip net.IP) bool { return ip.IsLoopback() } })
}

func ipBase(extra string) string {
	return base(extra + "  HostName localhost\n  AddressFamily inet\n  CheckHostIP yes\n")
}

func TestCheckHostIPRecords(t *testing.T) {
	remote(t)
	w := newWorld(t)
	s := pwServer(t, w, srvOpts{})
	w.ans = []string{"pw"}
	mustDial(t, w.opts(ipBase(""), s))
	p := strconv.Itoa(s.port)
	if got := read(t, w.known()); got != hostLine("[localhost]:"+p+",[127.0.0.1]:"+p, s.host.PublicKey()) {
		t.Fatalf("known_hosts %q", got)
	}
	// Hashed: a line per name.
	os.Remove(w.known())
	w.ans = []string{"pw"}
	mustDial(t, w.opts(ipBase("  HashKnownHosts yes\n"), s))
	if ls := parseKnown([]byte(read(t, w.known())), ""); len(ls) != 2 || !matchHosts(ls[1].hosts, "[127.0.0.1]:"+p) {
		t.Fatalf("known_hosts %q", read(t, w.known()))
	}
}

func TestCheckHostIPAddsAddress(t *testing.T) {
	remote(t)
	w := newWorld(t)
	s := pwServer(t, w, srvOpts{})
	p := strconv.Itoa(s.port)
	write(t, w.known(), hostLine("[localhost]:"+p, s.host.PublicKey()))
	w.ans = []string{"pw"}
	mustDial(t, w.opts(ipBase("  StrictHostKeyChecking yes\n"), s))
	if got := read(t, w.known()); !strings.HasSuffix(got, hostLine("[127.0.0.1]:"+p, s.host.PublicKey())) || !w.logged("for IP address") {
		t.Fatalf("known_hosts %q", got)
	}
}

func TestCheckHostIPOffForLiteralAddress(t *testing.T) {
	remote(t)
	w := newWorld(t)
	s := pwServer(t, w, srvOpts{})
	w.ans = []string{"pw"}
	mustDial(t, w.opts(base("  CheckHostIP yes\n"), s))
	if got := read(t, w.known()); got != hostLine(s.name(), s.host.PublicKey()) {
		t.Fatalf("known_hosts %q", got)
	}
}

func TestCheckHostIPDiffers(t *testing.T) {
	remote(t)
	for _, tc := range []struct {
		strict string
		ans    []string
		ok     bool
	}{{"yes", nil, false}, {"ask", []string{"yes", "pw"}, true}, {"ask", []string{"no"}, false}, {"no", []string{"pw"}, true}} {
		w := newWorld(t)
		s := pwServer(t, w, srvOpts{})
		p := strconv.Itoa(s.port)
		write(t, w.known(), hostLine("[localhost]:"+p, s.host.PublicKey())+hostLine("[127.0.0.1]:"+p, newSigner(t).PublicKey()))
		w.ans = tc.ans
		_, err := dialT(t, w.opts(ipBase("  StrictHostKeyChecking "+tc.strict+"\n"), s))
		if (err == nil) != tc.ok || err != nil && !strings.Contains(err.Error(), "differs from the key for the IP address") && !strings.Contains(err.Error(), "not accepted") {
			t.Fatalf("%s %q: err %v", tc.strict, tc.ans, err)
		}
		if tc.strict == "ask" && !strings.Contains(w.asks[0], "Offending key for IP in "+w.known()+":2") {
			t.Fatalf("asked %q", w.asks)
		}
	}
}

func TestCheckHostIPBothChanged(t *testing.T) {
	remote(t)
	w := newWorld(t)
	s := pwServer(t, w, srvOpts{})
	p := strconv.Itoa(s.port)
	old := newSigner(t).PublicKey()
	write(t, w.known(), hostLine("[localhost]:"+p, old)+hostLine("[127.0.0.1]:"+p, old))
	_, err := dialT(t, w.opts(ipBase(""), s))
	var e *HostKeyChangedError
	if !errors.As(err, &e) || len(e.Known) != 2 || e.Known[1].Name != "[127.0.0.1]:"+p || !slices.Equal(e.names, []string{"[localhost]:" + p, "[127.0.0.1]:" + p}) {
		t.Fatalf("err %v", err)
	}
	if err := AcceptNewKey(err); err != nil {
		t.Fatal(err)
	}
	if got := read(t, w.known()); got != hostLine("[localhost]:"+p+",[127.0.0.1]:"+p, s.host.PublicKey()) {
		t.Fatalf("known_hosts %q", got)
	}
}

func TestCheckHostIPAddressStillKnown(t *testing.T) {
	// The host's key changed but its address's didn't: only the host's
	// line goes, and the new key is recorded for the host alone.
	remote(t)
	w := newWorld(t)
	s := pwServer(t, w, srvOpts{})
	p := strconv.Itoa(s.port)
	write(t, w.known(), hostLine("[localhost]:"+p, newSigner(t).PublicKey())+hostLine("[127.0.0.1]:"+p, s.host.PublicKey()))
	_, err := dialT(t, w.opts(ipBase(""), s))
	var e *HostKeyChangedError
	if !errors.As(err, &e) || len(e.Known) != 1 || !slices.Equal(e.names, []string{"[localhost]:" + p}) {
		t.Fatalf("err %v", err)
	}
}

// hostCertServer is a server whose host key is certified by ca for principals.
func hostCertServer(t *testing.T, w *world, ca ssh.Signer, principals ...string) *server {
	k := newSigner(t)
	cs, err := ssh.NewCertSigner(cert(t, ca, k.PublicKey(), ssh.HostCert, principals...), k)
	if err != nil {
		t.Fatal(err)
	}
	return pwServer(t, w, srvOpts{hostKeys: []ssh.Signer{cs, k}})
}

func TestCertAuthority(t *testing.T) {
	w := newWorld(t)
	ca := newSigner(t)
	s := hostCertServer(t, w, ca, "127.0.0.1")
	other := newSigner(t).PublicKey()
	write(t, w.known(), "@revoked "+hostLine("*", other)+"@cert-authority "+hostLine("[127.0.0.1]:*", ca.PublicKey()))
	w.ans = []string{"pw"}
	mustDial(t, w.opts(base("  StrictHostKeyChecking yes\n"), s))
	// Another CA doesn't vouch for it.
	write(t, w.known(), "@cert-authority "+hostLine("*", other))
	w.ans = []string{"pw"}
	if _, err := dialT(t, w.opts(base("  StrictHostKeyChecking yes\n"), s)); err == nil {
		t.Fatal("certificate taken from another CA")
	}
}

func TestCertAuthorityAlias(t *testing.T) {
	w := newWorld(t)
	ca := newSigner(t)
	s := hostCertServer(t, w, ca, "box.example.com")
	write(t, w.known(), "@cert-authority "+hostLine("*.example.com", ca.PublicKey()))
	w.ans = []string{"pw"}
	mustDial(t, w.opts(base("  StrictHostKeyChecking yes\n  HostKeyAlias box.example.com\n"), s))
}

func TestCertAuthorityFallsBackToKey(t *testing.T) {
	for _, tc := range []struct{ extra, log string }{
		{"", "principal"},
		{"  CASignatureAlgorithms ecdsa-sha2-nistp256\n", "not in CASignatureAlgorithms"},
	} {
		w := newWorld(t)
		ca := newSigner(t)
		s := hostCertServer(t, w, ca, "elsewhere.example.com")
		if tc.log != "principal" {
			s = hostCertServer(t, w, ca, "127.0.0.1")
		}
		write(t, w.known(), "@cert-authority "+hostLine("*", ca.PublicKey()))
		_, err := dialT(t, w.opts(base(tc.extra+"  StrictHostKeyChecking yes\n"), s))
		if !errors.Is(err, ErrHostKey) || !w.logged(tc.log) || !w.logged("No matching CA found. Retry with plain key") {
			t.Fatalf("%q: err %v, logs %q", tc.log, err, w.logs)
		}
	}
}

func TestCertAuthorityUserCert(t *testing.T) {
	w := newWorld(t)
	ca := newSigner(t)
	k := newSigner(t)
	cs, _ := ssh.NewCertSigner(cert(t, ca, k.PublicKey(), ssh.UserCert, "127.0.0.1"), k)
	s := pwServer(t, w, srvOpts{hostKeys: []ssh.Signer{cs}})
	write(t, w.known(), "@cert-authority "+hostLine("*", ca.PublicKey()))
	if _, err := dialT(t, w.opts(base("  StrictHostKeyChecking yes\n"), s)); err == nil || !w.logged("not a host certificate") {
		t.Fatalf("err %v", err)
	}
}

func TestRevokedHostKey(t *testing.T) {
	w := newWorld(t)
	s := pwServer(t, w, srvOpts{})
	write(t, w.known(), "@revoked "+hostLine("*", s.host.PublicKey()))
	_, err := dialT(t, w.opts(base(""), s))
	if !errors.Is(err, ErrHostKey) || !strings.Contains(err.Error(), "is marked as revoked") {
		t.Fatalf("err %v", err)
	}
	w.ans = []string{"pw"}
	if _, err := dialT(t, w.opts(base("  StrictHostKeyChecking no\n"), s)); err == nil || len(w.asks) != 0 {
		t.Fatalf("password asked for a revoked host: %v", err)
	}
}

func TestRevokedCertificate(t *testing.T) {
	w := newWorld(t)
	ca := newSigner(t)
	s := hostCertServer(t, w, ca, "127.0.0.1")
	write(t, w.known(), "@cert-authority "+hostLine("*", ca.PublicKey())+"@revoked "+hostLine("*", ca.PublicKey()))
	if _, err := dialT(t, w.opts(base(""), s)); !errors.Is(err, ErrHostKey) {
		t.Fatalf("err %v", err)
	}
}

func TestRevokedHostKeysFile(t *testing.T) {
	w := newWorld(t)
	ca := newSigner(t)
	s := hostCertServer(t, w, ca, "127.0.0.1")
	write(t, w.known(), "@cert-authority "+hostLine("*", ca.PublicKey()))
	for _, tc := range []struct{ content, want string }{
		{"# revoked\n" + string(ssh.MarshalAuthorizedKey(ca.PublicKey())), "revoked by file"},
		{"SSHKRL\n\x00binary", "is a KRL"},
		{"", "no such file"},
	} {
		if tc.content != "" {
			write(t, w.path("revoked"), tc.content)
		} else {
			os.Remove(w.path("revoked"))
		}
		_, err := dialT(t, w.opts(base("  RevokedHostKeys %HOME/revoked\n"), s))
		if !errors.Is(err, ErrHostKey) || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: err %v", tc.want, err)
		}
	}
	write(t, w.path("revoked"), string(ssh.MarshalAuthorizedKey(newSigner(t).PublicKey())))
	w.ans = []string{"pw"}
	mustDial(t, w.opts(base("  RevokedHostKeys %HOME/revoked\n"), s))
}

func TestKnownHostsCommand(t *testing.T) {
	w := newWorld(t)
	s := pwServer(t, w, srvOpts{})
	bin, _ := os.Executable()
	write(t, w.path("cmd-known"), hostLine(s.name(), s.host.PublicKey()))
	w.ans = []string{"pw"}
	t.Setenv("DESK_KH", "env")
	mustDial(t, w.opts(base("  StrictHostKeyChecking yes\n  KnownHostsCommand "+bin+" sshc-known %HOME/cmd-known %HOME/cmd-log %H %I %t \"%f x\" ${DESK_KH}\n"), s))
	want := s.name() + " HOSTNAME ssh-ed25519 " + ssh.FingerprintSHA256(s.host.PublicKey()) + " x env\n"
	if got := read(t, w.path("cmd-log")); got != want {
		t.Fatalf("command got %q", got)
	}
	// The key it gave can't be dropped by AcceptNewKey.
	write(t, w.path("cmd-known"), hostLine(s.name(), newSigner(t).PublicKey()))
	_, err := dialT(t, w.opts(base("  KnownHostsCommand "+bin+" sshc-known %HOME/cmd-known %HOME/cmd-log\n"), s))
	if err := AcceptNewKey(err); err == nil || !strings.Contains(err.Error(), "KnownHostsCommand") {
		t.Fatalf("err %v", err)
	}
}

func TestKnownHostsCommandFails(t *testing.T) {
	for _, tc := range []struct{ cmd, log string }{
		{"relative/cmd", "must be an absolute path"},
		{"/nonexistent/cmd", "KnownHostsCommand /nonexistent/cmd"},
		{"/bin/x %Z", "KnownHostsCommand: "},
	} {
		w := newWorld(t)
		s := pwServer(t, w, srvOpts{})
		if _, err := dialT(t, w.opts(base("  StrictHostKeyChecking yes\n  KnownHostsCommand "+tc.cmd+"\n"), s)); err == nil || !w.logged(tc.log) {
			t.Fatalf("%s: err %v, logs %q", tc.cmd, err, w.logs)
		}
	}
}

func TestNoHostAuthenticationForLocalhost(t *testing.T) {
	w := newWorld(t)
	s := pwServer(t, w, srvOpts{})
	w.ans = []string{"pw"}
	mustDial(t, w.opts(base("  StrictHostKeyChecking yes\n  NoHostAuthenticationForLocalhost yes\n"), s))
	// Not with an alias, nor for a remote address.
	w.ans = []string{"pw"}
	if _, err := dialT(t, w.opts(base("  StrictHostKeyChecking yes\n  NoHostAuthenticationForLocalhost yes\n  HostKeyAlias box\n"), s)); !errors.Is(err, ErrHostKey) {
		t.Fatal("alias skipped the check")
	}
	remote(t)
	if _, err := dialT(t, w.opts(base("  StrictHostKeyChecking yes\n  NoHostAuthenticationForLocalhost yes\n"), s)); !errors.Is(err, ErrHostKey) {
		t.Fatal("remote host skipped the check")
	}
}

func TestOrderPrefersKnownTypes(t *testing.T) {
	w := newWorld(t)
	ek, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	pub, _ := ssh.NewPublicKey(&ek.PublicKey)
	write(t, w.known(), hostLine("box.example.com", pub)+"@cert-authority "+hostLine("ca.example.com", pub))
	cfg := resolve(t, w, "Host *\n  UserKnownHostsFile %HOME/known_hosts\n")
	algs := []string{"ssh-ed25519-cert-v01@openssh.com", "ssh-ed25519", "ecdsa-sha2-nistp256-cert-v01@openssh.com", "ecdsa-sha2-nistp256", "rsa-sha2-256"}
	h := newHostCheck(cfg, w.opts("").withDefaults(cfg), nil)
	h.host = "box.example.com"
	if got := h.order(algs); !slices.Equal(got, []string{"ecdsa-sha2-nistp256", "ssh-ed25519-cert-v01@openssh.com", "ssh-ed25519", "ecdsa-sha2-nistp256-cert-v01@openssh.com", "rsa-sha2-256"}) {
		t.Fatalf("order %q", got)
	}
	h.host = "ca.example.com"
	if got := h.order(algs); !slices.Equal(got, []string{"ssh-ed25519-cert-v01@openssh.com", "ecdsa-sha2-nistp256-cert-v01@openssh.com", "ssh-ed25519", "ecdsa-sha2-nistp256", "rsa-sha2-256"}) {
		t.Fatalf("order %q", got)
	}
}

func TestKeyTypeOf(t *testing.T) {
	for alg, want := range map[string]string{
		"rsa-sha2-512": "ssh-rsa", "rsa-sha2-256-cert-v01@openssh.com": "ssh-rsa", "ssh-rsa-cert-v01@openssh.com": "ssh-rsa",
		"sk-ssh-ed25519-cert-v01@openssh.com": "sk-ssh-ed25519@openssh.com", "ssh-ed25519": "ssh-ed25519",
	} {
		if got := keyTypeOf(alg); got != want {
			t.Errorf("%s: %s", alg, got)
		}
	}
}

func TestMatchHosts(t *testing.T) {
	hashed := knownhosts.HashHostname("box.example.com")
	for _, tc := range []struct {
		pats []string
		name string
		want bool
	}{
		{[]string{"box.example.com"}, "BOX.example.com", true},
		{[]string{"*.EXAMPLE.com"}, "box.example.com", true},
		{[]string{"b?x.example.com"}, "box.example.com", true},
		{[]string{"b?x.example.com"}, "bx.example.com", false},
		{[]string{"*.example.com", "!box.example.com"}, "box.example.com", false},
		{[]string{"!other.example.com", "*"}, "box.example.com", true},
		{[]string{"!box.example.com"}, "box.example.com", false},
		{[]string{hashed}, "box.example.com", true},
		{[]string{"x", hashed}, "box.example.com", true},
		{[]string{hashed}, "other.example.com", false},
		{[]string{"|1|bad|bad"}, "box.example.com", false},
		{[]string{"|1|" + strings.Split(hashed, "|")[2] + "|!!"}, "box.example.com", false},
		{[]string{"|1|!!|" + strings.Split(hashed, "|")[3]}, "box.example.com", false},
		{[]string{"|1|a|b|c"}, "box.example.com", false},
		{[]string{"[box.example.com]:2222"}, "[box.example.com]:2222", true},
		{[]string{"box.example.com"}, "box.example.co", false},
		{[]string{"box.example.co"}, "box.example.com", false},
		{[]string{"*.example.com"}, "box.example.org", false},
		{[]string{"box.example.com?"}, "box.example.com", false},
	} {
		if got := matchHosts(tc.pats, tc.name); got != tc.want {
			t.Errorf("%q %s: %v", tc.pats, tc.name, got)
		}
	}
}

func TestArgvSplit(t *testing.T) {
	for in, want := range map[string][]string{
		`a b  c`:            {"a", "b", "c"},
		`'a b' "c d"`:       {"a b", "c d"},
		`a\ b c\"d \\`:      {"a b", `c"d`, `\`},
		`''`:                {""},
		"\ta\t":             {"a"},
		`"it's" 'say "hi"'`: {"it's", `say "hi"`},
		`a"b c"d`:           {"ab cd"},
		`x\y`:               {`x\y`},
		`a\`:                {`a\`},
	} {
		if got := argvSplit(in); !slices.Equal(got, want) {
			t.Errorf("%s: %q", in, got)
		}
	}
}

func TestFingerprintHash(t *testing.T) {
	k := newSigner(t).PublicKey()
	w := newWorld(t)
	for alg, prefix := range map[string]string{"md5": "MD5:", "sha256": "SHA256:", "sha1": "SHA1:", "sha512": "SHA512:"} {
		cfg := resolve(t, w, "Host *\n  FingerprintHash "+alg+"\n")
		h := &hostCheck{cfg: cfg}
		got := h.fingerprint(k)
		if !strings.HasPrefix(got, prefix) {
			t.Errorf("%s: %s", alg, got)
		}
		if alg == "md5" && got != "MD5:"+ssh.FingerprintLegacyMD5(k) || alg == "sha256" && got != ssh.FingerprintSHA256(k) {
			t.Errorf("%s: %s", alg, got)
		}
	}
	cfg := resolve(t, w, "Host *\n  FingerprintHash sha1\n")
	if got := (&hostCheck{cfg: cfg}).fingerprint(k); len(got) != len("SHA1:")+27 {
		t.Errorf("sha1: %s", got)
	}
}

func TestKeyKind(t *testing.T) {
	for typ, want := range map[string]string{
		"ssh-ed25519": "ED25519", "sk-ssh-ed25519@openssh.com": "ED25519", "ecdsa-sha2-nistp256": "ECDSA",
		"sk-ecdsa-sha2-nistp256@openssh.com": "ECDSA", "ssh-rsa": "RSA", "ssh-dss": "DSS",
	} {
		if got := keyKind(typ); got != want {
			t.Errorf("%s: %s", typ, got)
		}
	}
}

func TestStatus(t *testing.T) {
	a, b := newSigner(t).PublicKey(), newSigner(t).PublicKey()
	db := parseKnown([]byte("x.example.com "+string(ssh.MarshalAuthorizedKey(b))+"box.example.com "+string(ssh.MarshalAuthorizedKey(b))+
		"box.example.com "+string(ssh.MarshalAuthorizedKey(a))+"@cert-authority box.example.com "+string(ssh.MarshalAuthorizedKey(b))), "f")
	if st, found := status(db, "box.example.com", a); st != stOK || len(found) != 1 || found[0].line != 3 {
		t.Fatalf("status %d %v", st, found)
	}
	if st, found := status(db[:2], "box.example.com", a); st != stChanged || len(found) != 1 || found[0].line != 2 {
		t.Fatalf("status %d %v", st, found)
	}
	if st, _ := status(db[3:], "box.example.com", a); st != stNew {
		t.Fatalf("status %d", st)
	}
}

func TestReadKnownLogsUnreadable(t *testing.T) {
	w := newWorld(t)
	for _, tc := range []struct {
		file string
		logs int
	}{{w.home, 1}, {w.path("missing"), 0}} {
		var logs []string
		readKnown([]string{tc.file}, func(f string, a ...any) { logs = append(logs, f) })
		if len(logs) != tc.logs {
			t.Fatalf("%s: logs %q", tc.file, logs)
		}
	}
}

func TestAddKnownKeepsUnreadable(t *testing.T) {
	w := newWorld(t)
	write(t, w.known(), "box.example.com ssh-ed25519 AAAA\n")
	os.Chmod(w.known(), 0o200)
	if err := addKnown(w.known(), []string{"x"}, newSigner(t).PublicKey(), false); err == nil {
		t.Fatal("rewrote an unreadable file")
	}
	os.Chmod(w.known(), 0o600)
	if read(t, w.known()) != "box.example.com ssh-ed25519 AAAA\n" {
		t.Fatal("file changed")
	}
}

func TestForgetChecksEachLine(t *testing.T) {
	w, s, old := changedWorld(t)
	_, err := dialT(t, w.opts(base(""), s))
	for _, content := range []string{
		hostLine("other.example.com", old) + hostLine(s.name(), newSigner(t).PublicKey()), // another key there now
		hostLine("other.example.com", old),                                                // the line is gone
	} {
		write(t, w.known(), content)
		if e := AcceptNewKey(err); e == nil || read(t, w.known()) != content {
			t.Fatalf("%q: %v", content, e)
		}
	}
}

func TestForgetTwoFiles(t *testing.T) {
	remote(t)
	w := newWorld(t)
	s := pwServer(t, w, srvOpts{})
	p := strconv.Itoa(s.port)
	old := newSigner(t).PublicKey()
	write(t, w.known(), hostLine("[localhost]:"+p, old))
	write(t, w.path("global"), hostLine("[127.0.0.1]:"+p, old))
	_, err := dialT(t, w.opts(ipBase("  GlobalKnownHostsFile %HOME/global\n"), s))
	if err := AcceptNewKey(err); err != nil {
		t.Fatal(err)
	}
	if read(t, w.path("global")) != "" || read(t, w.known()) != hostLine("[localhost]:"+p+",[127.0.0.1]:"+p, s.host.PublicKey()) {
		t.Fatalf("known %q, global %q", read(t, w.known()), read(t, w.path("global")))
	}
}

func TestAddKnownFails(t *testing.T) {
	w := newWorld(t)
	write(t, w.path("file"), "")
	k := newSigner(t).PublicKey()
	if err := addKnown(w.path("file/known_hosts"), []string{"x"}, k, false); err == nil {
		t.Fatal("wrote under a file")
	}
	if err := addKnown(w.home, []string{"x"}, k, false); err == nil {
		t.Fatal("wrote a folder")
	}
}
