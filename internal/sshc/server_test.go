package sshc

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/lfdominguez/desk/internal/sshconfig"
)

// TestMain doubles as the helper commands the configs run: a ProxyCommand
// bridging stdio to a server, and a KnownHostsCommand.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "sshc-bridge": // host port
			c, err := net.Dial("tcp", net.JoinHostPort(os.Args[2], os.Args[3]))
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			fmt.Fprintln(os.Stderr, "bridging")
			go func() { io.Copy(c, os.Stdin); c.(*net.TCPConn).CloseWrite() }()
			io.Copy(os.Stdout, c)
			os.Exit(0)
		case "sshc-known": // file log args...: prints file, logs the args
			os.WriteFile(os.Args[3], []byte(strings.Join(os.Args[4:], " ")+"\n"), 0o600)
			b, _ := os.ReadFile(os.Args[2])
			os.Stdout.Write(b)
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}

// srvOpts is what a test server accepts and how it behaves.
type srvOpts struct {
	password    string          // the password taken ("" = no password auth)
	kbd         []string        // keyboard-interactive: the answers to as many questions
	keys        []ssh.PublicKey // authorized keys
	ca          ssh.PublicKey   // user certificates' CA
	gssapiOnly  bool            // offer only gssapi-with-mic
	hostKeys    []ssh.Signer    // default: one Ed25519 key
	noKeepalive bool            // never answer keepalive@openssh.com
	config      func(*ssh.ServerConfig)
	dir         string // sftp's working folder
}

// server is an in-process SSH server on 127.0.0.1.
type server struct {
	t     *testing.T
	o     srvOpts
	ln    net.Listener
	port  int
	host  ssh.Signer // the first host key
	mu    sync.Mutex
	auths []string          // method:ok|fail, in order
	env   map[string]string // the last session's env requests
	conns int               // connections accepted
	live  []net.Conn
	sig   string // the last signal
	agent bool   // a session asked for agent forwarding
}

func newServer(t *testing.T, o srvOpts) *server {
	t.Helper()
	if o.hostKeys == nil {
		o.hostKeys = []ssh.Signer{newSigner(t)}
	}
	s := &server{t: t, o: o, host: o.hostKeys[0]}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s.ln, s.port = ln, ln.Addr().(*net.TCPAddr).Port
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.conns++
			s.live = append(s.live, c)
			s.mu.Unlock()
			go s.serve(c)
		}
	}()
	return s
}

// drop closes every connection, as a server going away would.
func (s *server) drop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.live {
		c.Close()
	}
}

func (s *server) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conns
}

// with changes what the server accepts from the next connection on.
func (s *server) with(f func(*srvOpts)) {
	s.mu.Lock()
	f(&s.o)
	s.mu.Unlock()
}

func (s *server) forwarded() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.agent
}

func (s *server) conf() *ssh.ServerConfig {
	s.mu.Lock()
	o := s.o
	s.mu.Unlock()
	c := &ssh.ServerConfig{
		AuthLogCallback: func(_ ssh.ConnMetadata, method string, err error) {
			if method == "none" {
				return
			}
			r := "ok"
			if err != nil {
				r = "fail"
			}
			s.mu.Lock()
			s.auths = append(s.auths, method+":"+r)
			s.mu.Unlock()
		},
	}
	for _, k := range o.hostKeys {
		c.AddHostKey(k)
	}
	if o.gssapiOnly {
		c.GSSAPIWithMICConfig = &ssh.GSSAPIWithMICConfig{
			AllowLogin: func(ssh.ConnMetadata, string) (*ssh.Permissions, error) { return nil, fmt.Errorf("no") },
			Server:     noGSSAPI{},
		}
	}
	if o.password != "" {
		c.PasswordCallback = func(_ ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
			if string(p) == o.password {
				return nil, nil
			}
			return nil, fmt.Errorf("bad password")
		}
	}
	if o.kbd != nil {
		c.KeyboardInteractiveCallback = func(_ ssh.ConnMetadata, ch ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
			qs, echos := make([]string, len(o.kbd)), make([]bool, len(o.kbd))
			for i := range o.kbd {
				qs[i], echos[i] = fmt.Sprintf("Question %d: ", i+1), i > 0
			}
			ans, err := ch("Login", "Answer them", qs, echos)
			if err != nil {
				return nil, err
			}
			for i := range ans {
				if ans[i] != o.kbd[i] {
					return nil, fmt.Errorf("bad answer")
				}
			}
			return nil, nil
		}
	}
	if o.keys != nil || o.ca != nil {
		cc := &ssh.CertChecker{
			IsUserAuthority: func(k ssh.PublicKey) bool { return o.ca != nil && keyEqual(k, o.ca) },
			UserKeyFallback: func(_ ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
				for _, a := range o.keys {
					if keyEqual(a, k) {
						return nil, nil
					}
				}
				return nil, fmt.Errorf("unknown key")
			},
		}
		c.PublicKeyCallback = cc.Authenticate
	}
	if o.config != nil {
		o.config(c)
	}
	return c
}

type noGSSAPI struct{}

func (noGSSAPI) AcceptSecContext([]byte) ([]byte, string, bool, error) {
	return nil, "", false, fmt.Errorf("no")
}
func (noGSSAPI) VerifyMIC([]byte, []byte) error { return fmt.Errorf("no") }
func (noGSSAPI) DeleteSecContext() error        { return nil }

func (s *server) serve(c net.Conn) {
	sc, chans, reqs, err := ssh.NewServerConn(c, s.conf())
	if err != nil {
		c.Close()
		return
	}
	defer sc.Close()
	go s.global(sc, reqs)
	for nc := range chans {
		switch nc.ChannelType() {
		case "session":
			ch, rq, err := nc.Accept()
			if err == nil {
				go s.session(sc, ch, rq)
			}
		case "direct-tcpip":
			var p struct {
				Host  string
				Port  uint32
				OHost string
				OPort uint32
			}
			ssh.Unmarshal(nc.ExtraData(), &p)
			s.relay(nc, "tcp", net.JoinHostPort(p.Host, strconv.Itoa(int(p.Port))))
		case "direct-streamlocal@openssh.com":
			var p struct {
				Path string
				R1   string
				R2   uint32
			}
			ssh.Unmarshal(nc.ExtraData(), &p)
			s.relay(nc, "unix", p.Path)
		default:
			nc.Reject(ssh.UnknownChannelType, "no")
		}
	}
}

func (s *server) relay(nc ssh.NewChannel, network, addr string) {
	far, err := net.Dial(network, addr)
	if err != nil {
		nc.Reject(ssh.ConnectionFailed, err.Error())
		return
	}
	ch, rq, err := nc.Accept()
	if err != nil {
		far.Close()
		return
	}
	go ssh.DiscardRequests(rq)
	go func() {
		go func() { io.Copy(far, ch); far.(interface{ CloseWrite() error }).CloseWrite() }()
		io.Copy(ch, far)
		ch.CloseWrite()
		ch.Close()
	}()
}

// global answers the connection's requests: keepalives (unless told not
// to), tcpip-forward and streamlocal-forward.
func (s *server) global(sc *ssh.ServerConn, reqs <-chan *ssh.Request) {
	for r := range reqs {
		switch r.Type {
		case "keepalive@openssh.com":
			if !s.o.noKeepalive {
				r.Reply(false, nil)
			}
		case "tcpip-forward":
			var p struct {
				Host string
				Port uint32
			}
			ssh.Unmarshal(r.Payload, &p)
			ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(p.Port))))
			if err != nil {
				r.Reply(false, nil)
				continue
			}
			port := uint32(ln.Addr().(*net.TCPAddr).Port)
			r.Reply(true, ssh.Marshal(struct{ Port uint32 }{port}))
			go s.forwardOut(sc, ln, "forwarded-tcpip", func() []byte {
				return ssh.Marshal(struct {
					Host  string
					Port  uint32
					OHost string
					OPort uint32
				}{p.Host, port, "127.0.0.1", 1})
			})
		case "streamlocal-forward@openssh.com":
			var p struct{ Path string }
			ssh.Unmarshal(r.Payload, &p)
			ln, err := net.Listen("unix", p.Path)
			if err != nil {
				r.Reply(false, nil)
				continue
			}
			r.Reply(true, nil)
			go s.forwardOut(sc, ln, "forwarded-streamlocal@openssh.com", func() []byte {
				return ssh.Marshal(struct{ Path, R string }{p.Path, ""})
			})
		default:
			if r.WantReply {
				r.Reply(false, nil)
			}
		}
	}
}

func (s *server) forwardOut(sc *ssh.ServerConn, ln net.Listener, kind string, payload func() []byte) {
	go func() { sc.Wait(); ln.Close() }()
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		ch, rq, err := sc.OpenChannel(kind, payload())
		if err != nil {
			c.Close()
			continue
		}
		go ssh.DiscardRequests(rq)
		go func() {
			go func() { io.Copy(ch, c); ch.CloseWrite() }()
			io.Copy(c, ch)
			c.Close()
		}()
	}
}

// session is a fake shell: exec runs one of its commands, shell reads
// them line by line ("$ " prompts), subsystem sftp serves s.o.dir.
func (s *server) session(sc *ssh.ServerConn, ch ssh.Channel, reqs <-chan *ssh.Request) {
	env := map[string]string{}
	pty := ""
	started := false
	exit := func(code int) {
		ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(code)}))
		ch.Close()
	}
	sigs := make(chan string, 4)
	for r := range reqs {
		ok := true
		switch r.Type {
		case "env":
			var p struct{ Name, Value string }
			ssh.Unmarshal(r.Payload, &p)
			env[p.Name] = p.Value
			s.mu.Lock()
			s.env = env
			s.mu.Unlock()
		case "pty-req":
			var p struct {
				Term             string
				Cols, Rows, W, H uint32
				Modes            string
			}
			ssh.Unmarshal(r.Payload, &p)
			pty = fmt.Sprintf("pty %s %dx%d", p.Term, p.Cols, p.Rows)
		case "window-change":
			var p struct{ Cols, Rows, W, H uint32 }
			ssh.Unmarshal(r.Payload, &p)
			fmt.Fprintf(ch, "size %dx%d\n", p.Cols, p.Rows)
		case "auth-agent-req@openssh.com":
			s.mu.Lock()
			s.agent = true
			s.mu.Unlock()
		case "signal":
			var p struct{ Name string }
			ssh.Unmarshal(r.Payload, &p)
			s.mu.Lock()
			s.sig = p.Name
			s.mu.Unlock()
			sigs <- p.Name
		case "exec":
			var p struct{ Cmd string }
			ssh.Unmarshal(r.Payload, &p)
			started = true
			go func() { exit(s.run(sc, ch, p.Cmd, pty, sigs)) }()
		case "shell":
			started = true
			go func() {
				if pty != "" {
					fmt.Fprintln(ch, pty)
				}
				br := bufio.NewReader(ch)
				for {
					fmt.Fprint(ch, "$ ")
					l, err := br.ReadString('\n')
					if err != nil {
						exit(0)
						return
					}
					l = strings.TrimSpace(l)
					if n, ok := strings.CutPrefix(l, "exit "); ok {
						code, _ := strconv.Atoi(n)
						exit(code)
						return
					}
					s.run(sc, ch, l, pty, sigs)
				}
			}()
		case "subsystem":
			var p struct{ Name string }
			ssh.Unmarshal(r.Payload, &p)
			switch p.Name {
			case "sftp":
				started = true
				go func() {
					srv, _ := sftp.NewServer(ch, sftp.WithServerWorkingDirectory(s.o.dir))
					srv.Serve()
					exit(0)
				}()
			case "echo":
				started = true
				go func() { fmt.Fprintln(ch, "subsystem echo"); exit(0) }()
			default:
				ok = false
			}
		default:
			ok = false
		}
		if r.WantReply {
			r.Reply(ok || started && r.Type == "env", nil)
		}
	}
}

// run is one fake command; its exit status.
func (s *server) run(sc *ssh.ServerConn, ch ssh.Channel, cmd, pty string, sigs chan string) int {
	name, arg, _ := strings.Cut(cmd, " ")
	switch name {
	case "echo":
		fmt.Fprintln(ch, arg)
	case "stderr":
		fmt.Fprintln(ch.Stderr(), arg)
	case "exit":
		n, _ := strconv.Atoi(arg)
		return n
	case "cat":
		io.Copy(ch, ch)
	case "pty":
		if pty == "" {
			pty = "nopty"
		}
		fmt.Fprintln(ch, pty)
	case "env":
		s.mu.Lock()
		var l []string
		for k, v := range s.env {
			l = append(l, k+"="+v)
		}
		s.mu.Unlock()
		sort.Strings(l)
		fmt.Fprintln(ch, strings.Join(l, " "))
	case "agent":
		ac, rq, err := sc.OpenChannel("auth-agent@openssh.com", nil)
		if err != nil {
			fmt.Fprintln(ch, "no agent:", err)
			return 1
		}
		go ssh.DiscardRequests(rq)
		keys, err := agent.NewClient(ac).List()
		ac.Close()
		fmt.Fprintln(ch, "agent keys", len(keys), err)
	case "sleep":
		select {
		case sig := <-sigs:
			ch.SendRequest("exit-signal", false, ssh.Marshal(struct {
				Signal string
				Core   bool
				Msg    string
				Lang   string
			}{sig, false, "", ""}))
			ch.Close()
			return -1
		case <-time.After(10 * time.Second):
		}
	default:
		fmt.Fprintln(ch.Stderr(), "unknown command", name)
		return 127
	}
	return 0
}

func newSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, k, _ := ed25519.GenerateKey(rand.Reader)
	s, err := ssh.NewSignerFromKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// writeKey writes a fresh Ed25519 key to path (and path.pub), encrypted
// with pass when given; its signer.
func writeKey(t *testing.T, path, pass string) ssh.Signer {
	t.Helper()
	_, k, _ := ed25519.GenerateKey(rand.Reader)
	var b *pem.Block
	var err error
	if pass == "" {
		b, err = ssh.MarshalPrivateKey(k, "me@example.com")
	} else {
		b, err = ssh.MarshalPrivateKeyWithPassphrase(k, "me@example.com", []byte(pass))
	}
	if err != nil {
		t.Fatal(err)
	}
	s, _ := ssh.NewSignerFromKey(k)
	write(t, path, string(pem.EncodeToMemory(b)))
	write(t, path+".pub", string(ssh.MarshalAuthorizedKey(s.PublicKey())))
	return s
}

func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, _ := os.ReadFile(path)
	return string(b)
}

// cert signs key's certificate with ca.
func cert(t *testing.T, ca ssh.Signer, key ssh.PublicKey, kind uint32, principals ...string) *ssh.Certificate {
	t.Helper()
	c := &ssh.Certificate{Key: key, CertType: kind, ValidPrincipals: principals, ValidBefore: ssh.CertTimeInfinity, KeyId: "test"}
	if err := c.SignCert(rand.Reader, ca); err != nil {
		t.Fatal(err)
	}
	return c
}

// world is a test's home: its config file, known_hosts and keys.
type world struct {
	t     *testing.T
	home  string
	asks  []string // the prompts asked
	echos []bool   // and whether each may echo
	ans   []string // the answers given, in order
	logs  []string
	mu    sync.Mutex
}

func newWorld(t *testing.T) *world {
	t.Setenv("SSH_AUTH_SOCK", "")
	t.Setenv("SHELL", "/bin/sh")
	w := &world{t: t, home: t.TempDir()}
	os.MkdirAll(filepath.Join(w.home, ".ssh"), 0o700)
	return w
}

func (w *world) path(name string) string { return filepath.Join(w.home, name) }

// known is the world's known_hosts file.
func (w *world) known() string { return w.path("known_hosts") }

// opts is a config of text (%PORT and %HOME replaced) as Options.
func (w *world) opts(text string, ports ...*server) Options {
	for i, s := range ports {
		text = strings.ReplaceAll(text, "%PORT"+strconv.Itoa(i), strconv.Itoa(s.port))
	}
	text = strings.ReplaceAll(text, "%HOME", w.home)
	write(w.t, w.path("config"), text)
	return Options{
		Options: sshconfig.Options{File: w.path("config"), System: "none", Home: w.home, LocalUser: "me"},
		Prompt: func(p string, echo bool) (string, error) {
			w.mu.Lock()
			defer w.mu.Unlock()
			w.asks = append(w.asks, p)
			w.echos = append(w.echos, echo)
			if len(w.ans) == 0 {
				return "", fmt.Errorf("unexpected prompt %q", p)
			}
			a := w.ans[0]
			w.ans = w.ans[1:]
			return a, nil
		},
		Logf: func(f string, a ...any) {
			w.mu.Lock()
			w.logs = append(w.logs, fmt.Sprintf(f, a...))
			w.mu.Unlock()
		},
	}
}

// resolve is box.example.com's config from text.
func resolve(t *testing.T, w *world, text string) *sshconfig.Config {
	t.Helper()
	c, err := sshconfig.Resolve("box.example.com", w.opts(text).Options)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (w *world) logged(sub string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, l := range w.logs {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

// base is the usual host block: the extra lines (first, so they win), the
// server, user me, the world's known_hosts taking new keys, no agent.
func base(extra string) string {
	return "Host srv\n" + extra + "  HostName 127.0.0.1\n  Port %PORT0\n  User me\n  UserKnownHostsFile %HOME/known_hosts\n" +
		"  StrictHostKeyChecking accept-new\n  IdentityAgent none\n  IdentityFile %HOME/id\n"
}
