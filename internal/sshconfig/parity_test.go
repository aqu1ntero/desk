package sshconfig

import (
	"errors"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// pcase is one configuration (with the extra files it includes) resolved
// for each host by both ssh -G and Resolve.
type pcase struct {
	name  string
	cfg   string
	files map[string]string // relative to the temp HOME; mode 0666 when the name ends in "!"
	hosts []string
	args  []string // -o/-l/-p, as given to ssh
}

func sshBinary(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("no ssh on PATH")
	}
	out, _ := exec.Command("ssh", "-V").CombinedOutput()
	if !strings.HasPrefix(string(out), "OpenSSH_10.") {
		t.Skipf("ssh isn't OpenSSH 10: %s", out)
	}
}

func fill(s, dir string) string {
	me, _ := user.Current()
	return strings.NewReplacer("{dir}", dir, "{user}", me.Username, "{USER}", strings.ToUpper(me.Username)).Replace(s)
}

// optsFrom turns ssh's -o/-l/-p arguments into Options.
func optsFrom(t *testing.T, dir string, args []string) Options {
	o := Options{File: filepath.Join(dir, "config"), Home: dir, Lookup: noDNS}
	for i := 0; i+1 < len(args); i += 2 {
		switch args[i] {
		case "-o":
			o.Overrides = append(o.Overrides, args[i+1])
		case "-l":
			o.User = args[i+1]
		case "-p":
			o.Port, _ = strconv.Atoi(args[i+1])
		default:
			t.Fatalf("unknown arg %s", args[i])
		}
	}
	return o
}

func noDNS(string) (string, error) { return "", errors.New("no DNS in tests") }

func runParity(t *testing.T, cases []pcase) { parity(t, cases, false) }

// parity runs the cases; with mustFail each is one ssh rejects.
func parity(t *testing.T, cases []pcase, mustFail bool) {
	sshBinary(t)
	t.Setenv("SSHCFG_TEST", "val")
	t.Setenv("SSHCFG_BAD", "a;b")
	t.Setenv("SSHCFG_NEST", "/x${SSHCFG_TEST}")
	me, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			write := func(name, body string) {
				mode := os.FileMode(0o644)
				if n, bad := strings.CutSuffix(name, "!"); bad {
					name, mode = n, 0o666
				}
				p := filepath.Join(dir, name)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(fill(body, dir)), mode); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(p, mode); err != nil {
					t.Fatal(err)
				}
			}
			write("config", c.cfg)
			for n, b := range c.files {
				write(n, b)
			}
			args := make([]string, len(c.args))
			for i, a := range c.args {
				args[i] = fill(a, dir)
			}
			hosts := c.hosts
			if hosts == nil {
				hosts = []string{"host.example.com"}
			}
			accepted := 0
			defer func() {
				// One bad line fails every host: a case meant to compare values must pass ssh somewhere.
				if !mustFail && accepted == 0 {
					t.Errorf("ssh -G rejects it for every host; errors go to TestParityErrors")
				}
			}()
			for _, h := range hosts {
				cmd := exec.Command("ssh", append(append([]string{"-G", "-F", filepath.Join(dir, "config")}, args...), h)...)
				cmd.Dir = dir
				cmd.Env = append(os.Environ(), "HOME="+dir)
				out, sshErr := cmd.Output()
				want := strings.ReplaceAll(string(out), me.HomeDir, dir)
				got, err := Resolve(h, optsFrom(t, dir, args))
				if sshErr == nil {
					accepted++
					if mustFail {
						t.Errorf("%s: ssh -G accepts it", h)
					}
				}
				switch {
				case sshErr != nil && err != nil:
				case sshErr != nil:
					t.Errorf("%s: ssh failed (%v) but Resolve didn't:\n%s", h, sshErr, got.Dump())
				case err != nil:
					t.Errorf("%s: Resolve: %v; ssh -G gave:\n%s", h, err, want)
				case got.Dump() != want:
					t.Errorf("%s: differs from ssh -G:\n%s", h, diff(want, got.Dump()))
				}
			}
		})
	}
}

// diff lists the lines only one side has.
func diff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	var b strings.Builder
	for i := 0; i < max(len(w), len(g)); i++ {
		var a, c string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			c = g[i]
		}
		if a != c {
			b.WriteString("  ssh: " + a + "\n   go: " + c + "\n")
		}
	}
	return b.String()
}

func TestParityBasics(t *testing.T) {
	runParity(t, []pcase{
		{name: "empty", cfg: "", hosts: []string{"x", "Host.Example.COM", "a.example.com", "me@h.example.com",
			"ssh://me@h.example.com:2222", "ssh://h.example.com", "0:0::1", "FE80::1", "127.1", "0x7f.1", "010.0.0.1",
			"1.2.3", "999.1.1.1", "192.0.2.01", "192.0.2.10", "fe80::1%nosuchif", "u@v@h.example.com", "@h", "-h",
			"h;x", "bad user@h", "ssh://h.example.com/path", "ssh://@h", "ssh://h:0", "ssh://me%41@h", "ssh://me;x=y@h",
			"ssh://h:ssh", "ssh://h:22/", "ssh://[::1]:22", "ssh://me%zz@h", "ssh://a_b"}},
		{name: "hosts", cfg: `
Host *.example.com !bad.example.com
  User me
  Port 2222
Host bad.example.com
  User other
Host ?.example.com
  HostName one.example.com
Host *
  User fallback
  Port 22
  HostName %h.example.net
`, hosts: []string{"a.example.com", "bad.example.com", "ab.example.com", "other", "A.EXAMPLE.COM"}},
		{name: "negation only", cfg: "Host !foo\n  User nobody\nHost foo !\n  Port 2\n", hosts: []string{"foo", "bar"}},
		{name: "syntax", cfg: `
  # a comment
	HOSTNAME=h1.example.com
user = me
Port=2200
IdentityFile "/path with space/key"
IdentityFile ~/.ssh/id_x # trailing comment
IdentityFile2 /other\ key
ProxyCommand ssh -W %h:%p jump # not a comment
LocalCommand  =  echo "%h"
"Compression" yes
BatchMode=yes
GatewayPorts= yes
keepalive no
ChallengeResponseAuthentication no
dsaauthentication no
IdentitiesOnly	true
CheckHostIP "yes"
Protocol 2
Cipher blowfish
UseRoaming no
KerberosAuthentication yes
GSSAPIAuthentication yes
SmartcardDevice /lib/pkcs11.so
HostbasedKeyTypes ssh-ed25519
PubkeyAcceptedKeyTypes +ssh-rsa
` + "ForwardX11 yes \t\f\r\n" + "\n \n=\n\"unterminated\n",
			hosts: []string{"x"}},
		{name: "ignoreunknown", cfg: "IgnoreUnknown Foo*,bar\nFooBar yes\nBAR 1\nIgnoreUnknown other\nfoobaz x\n"},
		{name: "overrides", cfg: "Host *\n  User cfg\n  Port 1\n", args: []string{"-o", "User=cli", "-o", "Port 3",
			"-o", "ProxyJump me@jump.example.com:2,ssh://u@hop.example.com"}},
		{name: "-l and -p first", cfg: "User cfg\nPort 1\n", args: []string{"-l", "luser", "-p", "5", "-o", "User o"},
			hosts: []string{"h", "me@h", "ssh://other@h:7"}},
		{name: "user expansion", cfg: "Host a\n  User %u-x\nHost b\n  User ${SSHCFG_TEST}\nHost c\n  User %h\nHost d\n  User 'x;y'\nHost e\n  User %i\nHost f\n  User ${SSHCFG_NOPE}\n",
			hosts: []string{"a", "b", "c", "d", "e", "f"}},
		{name: "hostname tokens", cfg: "Host a\n  HostName %h.example.com\nHost b\n  HostName %%h\nHost c\n  HostName %d\nHost d\n  HostName X%hY\nHost e\n  HostName 192.0.2.0010\n",
			hosts: []string{"a", "b", "c", "d", "e"}},
	})
}

func TestParityMatch(t *testing.T) {
	runParity(t, []pcase{
		{name: "criteria", cfg: `
Match host a.example.com user me
  Port 1
Match originalhost x* !host y*
  Port 2
Match host=b.example.com
  Port 3
Match localuser {user}
  Compression yes
Match localuser nosuchuser
  BatchMode yes
Match localnetwork 127.0.0.0/8,::1/128
  ForwardX11 yes
Match localnetwork 192.0.2.0/24
  Tag nope
Match version OpenSSH_10*
  ForwardX11Trusted yes
Match command ""
  GatewayPorts yes
Match sessiontype shell
  TCPKeepAlive no
Match !canonical !final
  HashKnownHosts yes
Match exec "test %h = c.example.com"
  Port 4
Match exec false
  Port 5
Match all
  User fallback
Match !all
  User never
`, hosts: []string{"a.example.com", "xy.example.com", "y.example.com", "B.Example.com", "c.example.com", "me@a.example.com"}},
		{name: "tags that don't match", cfg: "User me\nMatch tagged x\n  Port 1\nMatch all\nTag t\nMatch tagged other\n  User o\nMatch tagged T\n  Compression yes\nMatch user ME\n  BatchMode yes\nMatch localuser {USER}\n  ForwardX11 yes\n"},
		{name: "tags", cfg: "Match tagged \"\"\n  User untagged\nTag t1\nMatch tagged t*\n  Port 9\nMatch tagged=\"\"\n  Port 8\n"},
		{name: "exec skipped after a false criterion", cfg: "Match host nomatch exec \"exit 1\"\n  Port 1\nMatch user {user} exec true\n  Port 2\n"},
		{name: "exec tokens", cfg: "Match exec \"test %n = h && test %p = 22 && test %r = {user} && test %u = {user} && test -n %C && test -n %l\"\n  User ok\n", hosts: []string{"h"}},
		{name: "all with a comment", cfg: "Match all # everything\n  User a\n"},
		{name: "all then empty", cfg: "Match all \"\"\n  User a\n"},
		{name: "host after hostname", cfg: "Host x\n  HostName real.example.com\nMatch host real.example.com\n  User r\nMatch originalhost x\n  Port 7\n", hosts: []string{"x"}},
		{name: "user after user", cfg: "Match user nobody\n  Port 1\nUser me\nMatch user me\n  Port 2\n"},
		{name: "sessiontype", cfg: "SessionType none\nMatch sessiontype none\n  User n\n"},
		{name: "subsystem", cfg: "SessionType subsystem\nMatch sessiontype subsystem\n  User s\n"},
		{name: "match final", cfg: "Match final\n  SendEnv FOO\nMatch !final\n  User first\nMatch canonical\n  Port 3\nHost *\n  SendEnv BAR\n"},
		{name: "canonicalize no domains", cfg: "CanonicalizeHostname yes\nSendEnv FOO\nSetEnv A=1\nLocalForward 1 h:2\nIdentityFile ~/k\nCertificateFile ~/c\nMatch canonical\n  User canon\n",
			hosts: []string{"x", "a.b.c.example.com", "192.0.2.1", "X"}},
		{name: "canonicalize always with proxy", cfg: "CanonicalizeHostname always\nProxyCommand none\nCanonicalizeMaxDots 0\nMatch canonical host a.b\n  User c\n", hosts: []string{"a.b"}},
		{name: "canonicalize skipped by proxy", cfg: "CanonicalizeHostname yes\nCanonicalDomains example.com\nProxyJump j.example.com\nMatch canonical\n  User c\n", hosts: []string{"h"}},
		{name: "hosts lowercased only in the final pass", cfg: "Host Mixed.Example.com\n  User exact\nMatch final host mixed.example.com\n  Port 9\n", hosts: []string{"Mixed.Example.com"}},
	})
}

func TestParityInclude(t *testing.T) {
	runParity(t, []pcase{
		{name: "globs and nesting", cfg: "Include inc/*.conf\nInclude {dir}/abs/one.conf nomatch*.conf\nHost x\n  Include cond.conf\nUser last\n",
			files: map[string]string{
				".ssh/inc/b.conf": "Port 2\nInclude inc/sub/*\n", ".ssh/inc/a.conf": "Port 1\nCompression yes\n",
				".ssh/inc/sub/deep": "BatchMode yes\nHost nomatch\n  User inner\n", "abs/one.conf": "Tag abs\nUser fromabs\n",
				".ssh/cond.conf": "ForwardX11 yes\n"},
			hosts: []string{"x", "y"}},
		{name: "host in an include", cfg: "Include inc\nHost x\n  Include inc2\n", files: map[string]string{".ssh/inc": "Host x\n  Port 3\n", ".ssh/inc2": "Host x\n  User u\n"}, hosts: []string{"x"}},
		{name: "match in include does not leak", cfg: "Host x\n  Include a\n  Port 5\n", files: map[string]string{".ssh/a": "Match host nomatch\n  User u\n"}, hosts: []string{"x"}},
		{name: "tokens", cfg: "Include ~/inc-%h.conf ${SSHCFG_TEST}.conf ${SSHCFG_NOPE}\n", files: map[string]string{"inc-h.conf": "User byhost\n", ".ssh/val.conf": "Port 6\n"}, hosts: []string{"h"}},
		{name: "directory", cfg: "Include {dir}/adir\nUser d\n", files: map[string]string{"adir/f": "x"}},
	})
	parity(t, []pcase{
		{name: "checked in blocks that don't apply", cfg: "Host nomatch\n  Include bad\n", files: map[string]string{".ssh/bad": "NotAKeyword x\n"}},
		{name: "depth", cfg: "Include loop\n", files: map[string]string{".ssh/loop": "Include loop\n"}},
		{name: "bad permissions", cfg: "Include perm\n", files: map[string]string{".ssh/perm!": "User x\n"}},
		{name: "unknown token", cfg: "Include %Z\n"},
		{name: "empty word", cfg: "Include \"\"\n"},
		{name: "cmdline include", cfg: "", args: []string{"-o", "Include x"}},
		{name: "cmdline host", cfg: "", args: []string{"-o", "Host x"}},
		{name: "cmdline match", cfg: "", args: []string{"-o", "Match all"}},
	}, true)
}

func TestParityValues(t *testing.T) {
	runParity(t, []pcase{
		{name: "times and sizes", cfg: `
ConnectTimeout 1h30m
ServerAliveInterval 30s
ServerAliveCountMax 0
ForwardX11Timeout 1.9
RekeyLimit 1.5M 2m
ConnectionAttempts 3
NumberOfPasswordPrompts 0
RequiredRSASize 2048
CanonicalizeMaxDots 4
`},
		{name: "rekey default none", cfg: "RekeyLimit default none\nRekeyLimit 1G 1h\n"},
		{name: "rekey sizes", cfg: "RekeyLimit 16 1w2d3h4m5s\n"},
		{name: "rekey fraction", cfg: "RekeyLimit 2.25k\n"},
		{name: "connect none then value", cfg: "ConnectTimeout none\nConnectTimeout 5\n"},
		{name: "ipqos", cfg: "IPQoS af21 cs1\n"},
		{name: "ipqos one", cfg: "IPQoS 16\n"},
		{name: "ipqos deprecated", cfg: "IPQoS lowdelay throughput\n"},
		{name: "ipqos none le", cfg: "IPQoS none LE\n"},
		{name: "escape ctrl", cfg: "EscapeChar ^A\n"},
		{name: "escape none", cfg: "EscapeChar none\n"},
		{name: "escape space", cfg: "EscapeChar \" \"\n"},
		{name: "escape backslash", cfg: "EscapeChar \\\\\n"},
		{name: "escape del", cfg: "EscapeChar \x7f\n"},
		{name: "escape caret last", cfg: "EscapeChar ^_\n"},
		{name: "escape tab", cfg: "EscapeChar \"\t\"\n"},
		{name: "escape high", cfg: "EscapeChar \xe9\n"},
		{name: "escape high space", cfg: "EscapeChar \xa0\n"},
		{name: "escape high ctrl", cfg: "EscapeChar \x81\n"},
		{name: "multistates", cfg: `
StrictHostKeyChecking accept-new
ControlMaster autoask
Tunnel yes
RequestTTY force
SessionType default
AddressFamily inet6
PubkeyAuthentication host-bound
VerifyHostKeyDNS ask
UpdateHostKeys ask
CanonicalizeHostname no
WarnWeakCrypto no-pq-kex
FingerprintHash md5
LogLevel DEBUG2
SyslogFacility LOCAL3
`},
		{name: "multistates 2", cfg: "Tunnel ethernet\nRequestTTY no\nControlMaster ask\nStrictHostKeyChecking off\nPubkeyAuthentication unbound\nLogLevel debug\nAddressFamily inet\n"},
		{name: "update hostkeys ask with tty no", cfg: "UpdateHostKeys ask\nRequestTTY no\n"},
		{name: "update hostkeys ask quiet", cfg: "UpdateHostKeys ask\nLogLevel ERROR\n"},
		{name: "update hostkeys ask persist", cfg: "UpdateHostKeys ask\nControlPersist yes\nControlPath ~/c\n"},
		{name: "update hostkeys ask command", cfg: "UpdateHostKeys ask\nRemoteCommand ls\n"},
		{name: "update hostkeys with dns", cfg: "VerifyHostKeyDNS yes\n"},
		{name: "update hostkeys with known hosts", cfg: "UserKnownHostsFile ~/.ssh/known_hosts\n"},
		{name: "update hostkeys with other known hosts", cfg: "UserKnownHostsFile ~/kh\n"},
		{name: "control persist time", cfg: "ControlPersist 10m\n"},
		{name: "control persist zero", cfg: "ControlPersist 0\n"},
		{name: "control persist yes", cfg: "ControlPersist yes\n"},
		{name: "add keys confirm time", cfg: "AddKeysToAgent confirm 5m\n"},
		{name: "add keys time", cfg: "AddKeysToAgent 1h\n"},
		{name: "add keys ask", cfg: "AddKeysToAgent ask\n"},
		{name: "add keys confirm", cfg: "AddKeysToAgent confirm\n"},
		{name: "obscure interval", cfg: "ObscureKeystrokeTiming interval:50\n"},
		{name: "obscure no", cfg: "ObscureKeystrokeTiming no\n"},
		{name: "obscure 20", cfg: "ObscureKeystrokeTiming interval:20\n"},
		{name: "channel timeout", cfg: "ChannelTimeout session=5m global=1h\nChannelTimeout x=1\n"},
		{name: "channel timeout none", cfg: "ChannelTimeout none\n"},
		{name: "tunnel device", cfg: "TunnelDevice 1:any\n"},
		{name: "tunnel device any", cfg: "TunnelDevice any:7\n"},
		{name: "tunnel device one", cfg: "TunnelDevice 3\n"},
		{name: "streamlocal last wins", cfg: "StreamLocalBindMask 0077\nHost nomatch\n  StreamLocalBindMask 0700x\n"},
		{name: "streamlocal zero", cfg: "StreamLocalBindMask 0\n"},
		{name: "syslog in inactive block", cfg: "Host nomatch\n  SyslogFacility AUTH\nSyslogFacility DAEMON\n"},
		{name: "logverbose", cfg: "LogVerbose kex.c:*:1000 packet.c\nLogVerbose other\n"},
		{name: "logverbose none", cfg: "LogVerbose none\n"},
		{name: "permit remote open", cfg: "PermitRemoteOpen [::1]:22 h.example.com:* 192.0.2.1:ssh\n"},
		{name: "permit remote open any", cfg: "PermitRemoteOpen any\n"},
		{name: "permit remote open none", cfg: "PermitRemoteOpen none\nPermitRemoteOpen h:1\n"},
		// A proxy, so that ssh doesn't look the name up for its CNAME.
		{name: "cnames", cfg: "ProxyCommand nc %h %p\nCanonicalizePermittedCNAMEs *.Example.com:*.example.net,*.x.example.com *:*\n"},
		{name: "cnames star", cfg: "ProxyCommand nc %h %p\nCanonicalizePermittedCNAMEs *\n"},
		{name: "cnames none", cfg: "CanonicalizePermittedCNAMEs none\n"},
		{name: "cnames empty source", cfg: "ProxyCommand nc %h %p\nCanonicalizePermittedCNAMEs :b\n"},
		{name: "cnames with an address", cfg: "CanonicalizePermittedCNAMEs *:*\n", hosts: []string{"192.0.2.1", "::1"}},
		{name: "canonicalize settings stay from the first pass", cfg: "CanonicalizeHostname yes\nMatch canonical\n  CanonicalizeFallbackLocal no\n  CanonicalizeMaxDots 0\n  CanonicalizeHostname always\n", hosts: []string{"h"}},
		{name: "address family and addresses", cfg: "AddressFamily inet6\n", hosts: []string{"127.1", "0:0::1"}},
		{name: "address family inet", cfg: "AddressFamily inet\n", hosts: []string{"127.1", "0:0::1"}},
		{name: "hostname in match", cfg: "Host a\n  HostName %h.example.com\nHost b\n  HostName h${SSHCFG_TEST}\nMatch host a.example.com,h$*\n  User m\n", hosts: []string{"a", "b"}},
		{name: "domains", cfg: "CanonicalDomains example.com Example.NET.\n"},
		{name: "domains none", cfg: "CanonicalDomains none\n"},
		{name: "known hosts none", cfg: "GlobalKnownHostsFile none\nUserKnownHostsFile none\n"},
		{name: "known hosts lists", cfg: "GlobalKnownHostsFile /etc/a %h\nUserKnownHostsFile ~/kh ~/.ssh/k-%h-%p %d/x ${SSHCFG_TEST}\nUserKnownHostsFile ignored\n"},
		{name: "revoked", cfg: "RevokedHostKeys ~/revoked %h\n"},
		{name: "revoked none", cfg: "RevokedHostKeys none\n"},
		{name: "commands none", cfg: "ProxyCommand none\nLocalCommand none\nRemoteCommand none\nKnownHostsCommand none\nControlPath none\nSecurityKeyProvider none\nPKCS11Provider none\n"},
		{name: "commands", cfg: "ProxyCommand nc %h %p\nLocalCommand echo %h %T\nRemoteCommand echo %h %% %n %r\nKnownHostsCommand /bin/kh %h %K\nPKCS11Provider /lib/p11.so\nSecurityKeyProvider $SK\nXAuthLocation /bin/xauth\n"},
		{name: "proxycommand quoted", cfg: "ProxyCommand \"a b\" c\n"},
		{name: "agent paths", cfg: "ForwardAgent ~/agent-%h.sock\nIdentityAgent ${SSHCFG_TEST}/sock-%r\n"},
		{name: "agent socket env", cfg: "ForwardAgent $SSH_AUTH_SOCK\nIdentityAgent SSH_AUTH_SOCK\n"},
		{name: "forward agent yes then path", cfg: "ForwardAgent yes\nForwardAgent /tmp\n"},
		{name: "identity agent none", cfg: "IdentityAgent none\n"},
		{name: "control path tokens", cfg: "ControlPath ~/cm-%C-%h-%p-%r-%u-%L-%l-%n-%d-%i-%k-%j\nHostKeyAlias Alias.Example\nProxyJump j.example.com\n", hosts: []string{"h"}},
		{name: "control path env", cfg: "ControlPath ${SSHCFG_TEST}/%%\n"},
		{name: "version addendum", cfg: "VersionAddendum hi %h # not a comment\n"},
		{name: "version addendum none", cfg: "VersionAddendum None\n"},
		{name: "tag and misc strings", cfg: "Tag t\nBindAddress 192.0.2.1\nBindInterface eth0\nPreferredAuthentications publickey,password\nKbdInteractiveDevices pam\nHostKeyAlias MiXeD\n"},
		{name: "identity files", cfg: "IdentityFile ~/a\nIdentityFile ~/a\nIdentityFile %d/b-%h\nCertificateFile ~/c\nCertificateFile ~/c\n"},
		{name: "deprecated and unsupported", cfg: "FallBackToRsh no\nUsePrivilegedPort no\nRhostsRSAAuthentication no\nCompressionLevel 9\nGlobalKnownHostsFile2 x\n"},
		{name: "flags", cfg: `
ForwardX11Trusted yes
ExitOnForwardFailure yes
PasswordAuthentication no
KbdInteractiveAuthentication no
HostbasedAuthentication yes
NoHostAuthenticationForLocalhost yes
EnableSSHKeysign yes
PermitLocalCommand yes
VisualHostKey yes
StdinNull yes
ProxyUseFdpass yes
CanonicalizeFallbackLocal no
StreamLocalBindUnlink yes
EnableEscapeCommandline yes
HashKnownHosts yes
`},
		{name: "fork with command", cfg: "ForkAfterAuthentication yes\nRemoteCommand true\n"},
		{name: "fork without session", cfg: "ForkAfterAuthentication yes\nSessionType none\n"},
		{name: "services as ports", cfg: "Port ssh\nLocalForward ssh h:ssh\n"},
		{name: "first wins, pairs", cfg: "Host nomatch\n  ControlPersist 5\n  TunnelDevice 1:2\n  IPQoS af11\n  CanonicalDomains x.example.com\n  AddKeysToAgent 1m\n" +
			"ControlPersist 7\nTunnelDevice 3\nIPQoS ef cs1\nCanonicalDomains y.example.com\nAddKeysToAgent confirm 2m\n" +
			"ControlPersist 9\nTunnelDevice 4\nIPQoS af12\nCanonicalDomains z.example.com\nAddKeysToAgent 3m\n" +
			"Host nomatch\n  RevokedHostKeys /a\nHost *\n  RevokedHostKeys /b\n  RevokedHostKeys /c\n"},
		{name: "logverbose nothing", cfg: "LogVerbose #x\nLogVerbose a\n"},
		{name: "update hostkeys ask persist alone", cfg: "UpdateHostKeys ask\nControlPersist yes\n"},
		{name: "expansions per keyword", cfg: "RemoteCommand ~/x ${SSHCFG_TEST}\nIdentityAgent ~/a-${SSHCFG_TEST}\nForwardAgent ~/f-${SSHCFG_TEST}\n" +
			"VersionAddendum ~ ${SSHCFG_TEST}\nRevokedHostKeys ~/r-${SSHCFG_TEST}\nSetEnv T=~/x\nLocalForward ~/l.sock h:22\nRemoteForward 1 ~/c.sock\n" +
			"LocalForward ${SSHCFG_NEST} h:22\n"}, // the variable's own ${…} stays
		{name: "match sessiontype case", cfg: "Match sessiontype SHELL\n  User up\n"},
		{name: "match exec no env", cfg: "Match exec \"test '${SSHCFG_TEST}' = val\"\n  User expanded\n"},
		// Accepted, surprisingly or not.
		{name: "quote in a comment", cfg: "User me # \"\n"},
		{name: "no known hosts", cfg: "UserKnownHostsFile #x\nUser x\n"},
		{name: "match one then all", cfg: "Match host nomatch all\n  User u\n"},
		{name: "include nothing", cfg: "Include nothing\n"},
		{name: "literal user not checked", cfg: "User a;b\n"},
		{name: "duplicate identities don't count", cfg: strings.Repeat("IdentityFile ~/k\n", 101)},
	})
}

func TestParityEnvAndForwards(t *testing.T) {
	runParity(t, []pcase{
		{name: "sendenv", cfg: "SendEnv LANG LC_* X\nSendEnv -LC_*\nHost nomatch\n  SendEnv NOPE\nSendEnv LC_ALL -X\n"},
		{name: "setenv", cfg: "SetEnv A=%h B=${SSHCFG_TEST} A=dup C=\"x y\"\nSetEnv D=ignored\n"},
		{name: "setenv later", cfg: "Host nomatch\n  SetEnv X=1\nSetEnv Y=2\n"},
		{name: "forwards", cfg: `
LocalForward 8080 h.example.com:80
LocalForward 8080 h.example.com:80
LocalForward [::1]:8081 [2001:db8::1]:22
LocalForward *:8082 h:22
LocalForward localhost:8083 /var/run/sock
LocalForward /tmp/l.sock h:22
LocalForward /tmp/a.sock /tmp/b.sock
LocalForward :8084 h:22
LocalForward []:8085 h:22
LocalForward 8086 h\:x:22
LocalForward 8087 [h]:22
LocalForward ${SSHCFG_TEST}/l-%h h:22
RemoteForward 9000 localhost:22
RemoteForward 0 h:22
RemoteForward 1080
RemoteForward [::1]:1081
RemoteForward /tmp/r-%h.sock /tmp/l-%r.sock
DynamicForward 1082
DynamicForward 192.0.2.1:1083
DynamicForward [::1]:1084
DynamicForward /tmp/socks.sock
DynamicForward 1082
`},
		{name: "clear forwardings", cfg: "LocalForward 1 h:2\nTunnel yes\nClearAllForwardings yes\n"},
		{name: "forward four fields trailing colon", cfg: "LocalForward a:1 b:2:\n"},
		{name: "proxyjump forms", cfg: "Host a\n  ProxyJump me@j1.example.com:2200,ssh://u%40x@j2.example.com:23,[192.0.2.1]:22,192.0.2.2\nHost b\n  ProxyJump [2001:db8::1]:2,192.0.2.9\nHost c\n  ProxyJump none\n  ProxyJump j.example.com\nHost d\n  ProxyCommand nc %h %p\n  ProxyJump j.example.com\nHost e\n  ProxyJump j.example.com # comment\n  ProxyCommand ignored\nHost f\n  ProxyJump \"q\"\n",
			hosts: []string{"a", "b", "c", "d", "e", "f"}},
		{name: "proxyjump loop", cfg: "ProxyJump h\n", hosts: []string{"h", "other"}},
		{name: "proxyjump loop with user", cfg: "ProxyJump me@h\nUser other\n", hosts: []string{"h"}},
	})
}

func TestParityAlgorithms(t *testing.T) {
	runParity(t, []pcase{
		{name: "append remove prepend", cfg: "Ciphers +aes128-cbc,3des-cbc\nMACs -*sha1*\nKexAlgorithms ^diffie-hellman-group14-sha1\nHostKeyAlgorithms ssh-ed25519*\nPubkeyAcceptedAlgorithms -rsa*,ssh-rsa*\nCASignatureAlgorithms ^ssh-rsa\nHostbasedAcceptedAlgorithms +ssh-rsa,ssh-rsa-cert-v01@openssh.com\n"},
		{name: "explicit", cfg: "Ciphers aes256-ctr,aes128-ctr,aes256-ctr\nMACs hmac-sha2-256\nKexAlgorithms curve25519-sha256\nHostKeyAlgorithms +ssh-rsa\nPubkeyAcceptedAlgorithms rsa-sha2-512,*-cert-v01@openssh.com\n"},
		{name: "remove all", cfg: "Ciphers -*\n"},
		{name: "empty elements", cfg: "Ciphers aes128-ctr,,bogus\nKexAlgorithms ^curve25519-sha256,,x\n"},
		{name: "hostkey prepend", cfg: "HostKeyAlgorithms ^ssh-rsa,ssh-ed25519\n"},
		{name: "remove unknown", cfg: "KexAlgorithms -bogus\n"},
	})
}

func TestParityErrors(t *testing.T) {
	parity(t, []pcase{
		{name: "unknown keyword", cfg: "NotAKeyword yes\n"},
		{name: "escape caret question", cfg: "EscapeChar ^?\n"},
		{name: "ignoreunknown other", cfg: "IgnoreUnknown foo\nbar x\n"},
		{name: "revoked empty word", cfg: "RevokedHostKeys \"\"\n"},
		{name: "channel timeout nothing", cfg: "ChannelTimeout #x\n"},
		{name: "channel timeout no name", cfg: "ChannelTimeout =5\n"},
		{name: "channel timeout bad time", cfg: "ChannelTimeout x=bad\n"},
		{name: "agent env checked where it doesn't apply", cfg: "Host nomatch\n  IdentityAgent ${SSHCFG_NOPE}\n"},
		{name: "permit open port 0", cfg: "PermitRemoteOpen h:0\n"},
		{name: "domains nothing", cfg: "CanonicalDomains #x\n"},
		{name: "cnames none then more", cfg: "ProxyCommand nc %h %p\nCanonicalizePermittedCNAMEs none *\n"},
		{name: "cnames more then none", cfg: "ProxyCommand nc %h %p\nCanonicalizePermittedCNAMEs * none\n"},
		{name: "proxycommand only equals", cfg: "ProxyCommand =\n"},
		{name: "forward one word", cfg: "LocalForward a:1:b:2\n"},
		{name: "proxyjump bare ipv6", cfg: "ProxyJump 2001:db8::1\n"},
		{name: "no argument", cfg: "User\n"},
		{name: "no argument equals", cfg: "User =\n"},
		{name: "bad quotes", cfg: "User \"me\n"},
		{name: "bad port", cfg: "Port 0\n"},
		{name: "bad port name", cfg: "Port nosuchservice\n"},
		{name: "bad flag", cfg: "Compression maybe\n"},
		{name: "compression true", cfg: "Compression true\n"},
		{name: "extra args", cfg: "User a b\n"},
		{name: "bad int", cfg: "ConnectionAttempts -1\n"},
		{name: "zero attempts", cfg: "ConnectionAttempts 0\n"},
		{name: "bad time", cfg: "ConnectTimeout 5x\n"},
		{name: "bad time 2", cfg: "ConnectTimeout 1s1s\n"},
		{name: "bad time 3", cfg: "ConnectTimeout 1.5m\n"},
		{name: "bad rekey", cfg: "RekeyLimit 10\n"},
		{name: "bad rekey unit", cfg: "RekeyLimit 1X\n"},
		{name: "bad rekey interval", cfg: "RekeyLimit 1G x\n"},
		{name: "none not alone", cfg: "UserKnownHostsFile none ~/kh\n"},
		{name: "empty known hosts", cfg: "UserKnownHostsFile \"\"\n"},
		{name: "bad match attr", cfg: "Match foo bar\n"},
		{name: "match no arg", cfg: "Match host\n"},
		{name: "match empty", cfg: "Match\n#\n"},
		{name: "match only comment", cfg: "Match \"#x\"\n"},
		{name: "match all combined", cfg: "Match all host x\n"},
		{name: "match combined all", cfg: "Match host x host y all\n"},
		{name: "match criteria comment", cfg: "Match host \"#x\"\n"},
		{name: "match empty criteria", cfg: "Match host=\n"},
		{name: "match bad network", cfg: "Match localnetwork 192.0.2.1/24\n"},
		{name: "match bad network chars", cfg: "Match localnetwork 0x7f.0.0.1\n"},
		{name: "match bad network empty", cfg: "Match localnetwork 127.0.0.1,\n"},
		{name: "match bad network mask", cfg: "Match localnetwork 127.0.0.1/33\n"},
		{name: "match sessiontype=", cfg: "Match sessiontype=shell\n"},
		{name: "hostname bad token", cfg: "HostName %x\n"},
		{name: "refuse", cfg: "Host nomatch\n  RefuseConnection no\nHost *\n  RefuseConnection go away\n"},
		{name: "bad forward", cfg: "LocalForward 1\n"},
		{name: "bad forward 2", cfg: "LocalForward 0 h:1\n"},
		{name: "bad forward 3", cfg: "LocalForward 1 h:0\n"},
		{name: "bad forward 4", cfg: "DynamicForward a:1:b\n"},
		{name: "bad forward 5", cfg: "LocalForward [h:1 h:2\n"},
		{name: "bad forward 6", cfg: "LocalForward 1 h:2:3:4\n"},
		{name: "bad forward 7", cfg: "LocalForward 1 h\\\n"},
		{name: "bad forward env", cfg: "LocalForward ${SSHCFG_NOPE} h:2\n"},
		{name: "bad permit open host", cfg: "PermitRemoteOpen h/1\n"},
		{name: "bad permit open port", cfg: "PermitRemoteOpen h\n"},
		{name: "bad permit open any", cfg: "PermitRemoteOpen h:1 any\n"},
		{name: "bad proxyjump", cfg: "ProxyJump a,,b\n"},
		{name: "bad proxyjump port", cfg: "ProxyJump a:0\n"},
		{name: "bad proxyjump uri", cfg: "ProxyJump ssh://a/path\n"},
		{name: "bad proxyjump strict", cfg: "", args: []string{"-o", "ProxyJump a;b"}},
		{name: "bad cipher", cfg: "Ciphers bogus\n"},
		{name: "negated cipher", cfg: "Ciphers !aes128-ctr\n"},
		{name: "negated key", cfg: "PubkeyAcceptedAlgorithms !ssh-rsa\n"},
		{name: "negated hostkey", cfg: "HostKeyAlgorithms !ssh-rsa\n"},
		{name: "ca cert", cfg: "CASignatureAlgorithms ssh-ed25519-cert-v01@openssh.com\n"},
		{name: "key pattern none", cfg: "HostKeyAlgorithms nomatch*\n"},
		{name: "bad mac", cfg: "MACs +bogus\n"},
		{name: "bad kex", cfg: "KexAlgorithms ^bogus\n"},
		{name: "sendenv equals", cfg: "SendEnv FOO=bar\n"},
		{name: "sendenv none", cfg: "SendEnv #x\n"},
		{name: "setenv no equals", cfg: "SetEnv X\n"},
		{name: "agent env unset", cfg: "IdentityAgent ${SSHCFG_NOPE}\n"},
		{name: "agent legacy env", cfg: "IdentityAgent $BAD-NAME\n"},
		{name: "agent dollar", cfg: "ForwardAgent $\n"},
		{name: "control path env unset", cfg: "ControlPath ~/${SSHCFG_NOPE}\n"},
		{name: "control path bad token", cfg: "ControlPath %Z\n"},
		{name: "remote command bad token", cfg: "RemoteCommand %Z\n"},
		{name: "fork without command", cfg: "ForkAfterAuthentication yes\n"},
		{name: "escape bad", cfg: "EscapeChar ab\n"},
		{name: "escape caret", cfg: "EscapeChar ^1\n"},
		{name: "ipqos bad", cfg: "IPQoS bogus\n"},
		{name: "ipqos bad second", cfg: "IPQoS ef bogus\n"},
		{name: "ipqos 256", cfg: "IPQoS 256\n"},
		{name: "tunnel device bad", cfg: "TunnelDevice x\n"},
		{name: "tunnel device bad remote", cfg: "TunnelDevice 1:x\n"},
		{name: "streamlocal bad", cfg: "StreamLocalBindMask 8\n"},
		{name: "streamlocal too big", cfg: "StreamLocalBindMask 1777\n"},
		{name: "fingerprint bad", cfg: "FingerprintHash sha3\n"},
		{name: "add keys bad", cfg: "AddKeysToAgent yes 5m\n"},
		{name: "add keys bad time", cfg: "AddKeysToAgent confirm x\n"},
		{name: "add keys bad word", cfg: "AddKeysToAgent maybe\n"},
		{name: "obscure bad", cfg: "ObscureKeystrokeTiming interval:0\n"},
		{name: "obscure two", cfg: "ObscureKeystrokeTiming yes no\n"},
		{name: "obscure word", cfg: "ObscureKeystrokeTiming Yes\n"},
		{name: "obscure missing", cfg: "ObscureKeystrokeTiming interval:\n"},
		{name: "channel timeout bad", cfg: "ChannelTimeout session\n"},
		{name: "channel timeout none not alone", cfg: "ChannelTimeout none x=1\n"},
		{name: "domain bad", cfg: "CanonicalDomains -bad.example.com\n"},
		{name: "domain dots", cfg: "CanonicalDomains a..b\n"},
		{name: "domain chars", cfg: "CanonicalDomains a!b\n"},
		{name: "cname bad", cfg: "ProxyCommand nc %h %p\nCanonicalizePermittedCNAMEs a.example.com\n"},
		{name: "log level bad", cfg: "LogLevel loud\n"},
		{name: "facility bad", cfg: "SyslogFacility nowhere\n"},
		{name: "logverbose empty", cfg: "LogVerbose \"\"\n"},
		{name: "revoked none not alone", cfg: "RevokedHostKeys none x\n"},
		{name: "revoked empty", cfg: "RevokedHostKeys #\n"},
		{name: "version addendum cr", cfg: "VersionAddendum a\rb\n"},
		{name: "persist bad", cfg: "ControlPersist forever\n"},
		{name: "include system tilde", cfg: "", args: []string{"-o", "Include ~/x"}},
		{name: "user invalid", cfg: "", args: []string{"-l", "a;b"}},
		{name: "user expanded invalid", cfg: "User ${SSHCFG_BAD}\n"},
		{name: "identity many distinct", cfg: func() string {
			var b strings.Builder
			for i := 0; i < 101; i++ {
				b.WriteString("IdentityFile ~/k" + strconv.Itoa(i) + "\n")
			}
			return b.String()
		}()},
		{name: "known hosts too many", cfg: "UserKnownHostsFile" + strings.Repeat(" ~/k", 33) + "\n"},
		{name: "tilde unknown user", cfg: "ControlPath ~nosuchuser_x/cp\n"},
	}, true)
}
