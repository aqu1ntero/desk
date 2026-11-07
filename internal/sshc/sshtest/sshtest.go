// Package sshtest is an in-process SSH server for tests (sshc's and
// infra's; nothing else imports it): password, keyboard-interactive, public
// keys and certificates, keepalives, forwards (TCP and Unix), env, PTYs,
// signals and sftp. Its sessions run either a small fake command set
// (sshc's tests) or, with Real, a real shell and sh -c on this machine.
package sshtest

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// Opts is what a test server accepts and how it behaves.
type Opts struct {
	Password    string          // the password taken ("" = no password auth)
	Kbd         []string        // keyboard-interactive: the answers to as many questions
	Keys        []ssh.PublicKey // authorized keys
	CA          ssh.PublicKey   // user certificates' CA
	GSSAPIOnly  bool            // offer only gssapi-with-mic
	HostKeys    []ssh.Signer    // default: one Ed25519 key
	NoKeepalive bool            // never answer keepalive@openssh.com
	Config      func(*ssh.ServerConfig)
	Dir         string // sftp's working folder (Real: the commands' too, and their HOME)
	NoSFTP      bool   // refuse the sftp subsystem
	// Real runs sessions on this machine: exec as sh -c, a shell as Shell
	// (default bash --norc --noprofile -i), in a PTY when one was asked for.
	Real  bool
	Shell []string
}

// Server is an in-process SSH server on 127.0.0.1.
type Server struct {
	Port int
	Host ssh.Signer // the first host key

	t     testing.TB
	o     Opts
	ln    net.Listener
	mu    sync.Mutex
	auths []string          // method:ok|fail, in order
	env   map[string]string // the last session's env requests
	conns int               // connections accepted
	live  []net.Conn
	sig   string // the last signal
	agent bool   // a session asked for agent forwarding
	cmds  []string
	procs sync.WaitGroup // Real processes running
}

// New starts a server; the test's cleanup stops it.
func New(t testing.TB, o Opts) *Server {
	t.Helper()
	if o.HostKeys == nil {
		o.HostKeys = []ssh.Signer{NewSigner(t)}
	}
	s := &Server{t: t, o: o, Host: o.HostKeys[0]}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s.ln, s.Port = ln, ln.Addr().(*net.TCPAddr).Port
	t.Cleanup(func() { // the processes go too, before the test's folders do
		ln.Close()
		s.Drop()
		done := make(chan struct{})
		go func() { s.procs.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	})
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

// Drop closes every connection, as a server going away would.
func (s *Server) Drop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.live {
		c.Close()
	}
	s.live = nil
}

// Count is the connections accepted so far.
func (s *Server) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conns
}

// With changes what the server accepts from the next connection on.
func (s *Server) With(f func(*Opts)) {
	s.mu.Lock()
	f(&s.o)
	s.mu.Unlock()
}

// Forwarded says a session asked for agent forwarding.
func (s *Server) Forwarded() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.agent
}

// Auths is every authentication attempt so far, method:ok|fail.
func (s *Server) Auths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.auths...)
}

// Signal is the last signal a session got.
func (s *Server) Signal() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sig
}

// Commands is every exec request so far, in order.
func (s *Server) Commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.cmds...)
}

func (s *Server) conf() *ssh.ServerConfig {
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
	for _, k := range o.HostKeys {
		c.AddHostKey(k)
	}
	if o.GSSAPIOnly {
		c.GSSAPIWithMICConfig = &ssh.GSSAPIWithMICConfig{
			AllowLogin: func(ssh.ConnMetadata, string) (*ssh.Permissions, error) { return nil, fmt.Errorf("no") },
			Server:     noGSSAPI{},
		}
	}
	if o.Password != "" {
		c.PasswordCallback = func(_ ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
			if string(p) == o.Password {
				return nil, nil
			}
			return nil, fmt.Errorf("bad password")
		}
	}
	if o.Kbd != nil {
		c.KeyboardInteractiveCallback = func(_ ssh.ConnMetadata, ch ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
			qs, echos := make([]string, len(o.Kbd)), make([]bool, len(o.Kbd))
			for i := range o.Kbd {
				qs[i], echos[i] = fmt.Sprintf("Question %d: ", i+1), i > 0
			}
			ans, err := ch("Login", "Answer them", qs, echos)
			if err != nil {
				return nil, err
			}
			for i := range ans {
				if ans[i] != o.Kbd[i] {
					return nil, fmt.Errorf("bad answer")
				}
			}
			return nil, nil
		}
	}
	if o.Keys != nil || o.CA != nil {
		cc := &ssh.CertChecker{
			IsUserAuthority: func(k ssh.PublicKey) bool { return o.CA != nil && keyEqual(k, o.CA) },
			UserKeyFallback: func(_ ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
				for _, a := range o.Keys {
					if keyEqual(a, k) {
						return nil, nil
					}
				}
				return nil, fmt.Errorf("unknown key")
			},
		}
		c.PublicKeyCallback = cc.Authenticate
	}
	if o.Config != nil {
		o.Config(c)
	}
	return c
}

func keyEqual(a, b ssh.PublicKey) bool { return bytes.Equal(a.Marshal(), b.Marshal()) }

type noGSSAPI struct{}

func (noGSSAPI) AcceptSecContext([]byte) ([]byte, string, bool, error) {
	return nil, "", false, fmt.Errorf("no")
}
func (noGSSAPI) VerifyMIC([]byte, []byte) error { return fmt.Errorf("no") }
func (noGSSAPI) DeleteSecContext() error        { return nil }

func (s *Server) serve(c net.Conn) {
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

func (s *Server) relay(nc ssh.NewChannel, network, addr string) {
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
func (s *Server) global(sc *ssh.ServerConn, reqs <-chan *ssh.Request) {
	for r := range reqs {
		switch r.Type {
		case "keepalive@openssh.com":
			s.mu.Lock()
			quiet := s.o.NoKeepalive
			s.mu.Unlock()
			if !quiet {
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

func (s *Server) forwardOut(sc *ssh.ServerConn, ln net.Listener, kind string, payload func() []byte) {
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

// ptyReq is a session's pty-req.
type ptyReq struct {
	Term             string
	Cols, Rows, W, H uint32
	Modes            string
}

// session serves one session channel: exec runs a command, shell a shell
// (fake: one command per line, "$ " prompts), subsystem sftp serves Dir.
func (s *Server) session(sc *ssh.ServerConn, ch ssh.Channel, reqs <-chan *ssh.Request) {
	env := map[string]string{}
	var tty *ptyReq
	started := false
	var real realProc
	exit := func(code int) {
		ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(code)}))
		ch.Close()
	}
	sigs := make(chan string, 4)
	s.mu.Lock()
	o := s.o
	s.mu.Unlock()
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
			tty = &ptyReq{}
			ssh.Unmarshal(r.Payload, tty)
		case "window-change":
			var p struct{ Cols, Rows, W, H uint32 }
			ssh.Unmarshal(r.Payload, &p)
			if o.Real {
				real.resize(p.Cols, p.Rows)
			} else {
				fmt.Fprintf(ch, "size %dx%d\n", p.Cols, p.Rows)
			}
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
			if o.Real {
				real.signal(p.Name)
			} else {
				sigs <- p.Name
			}
		case "exec":
			var p struct{ Cmd string }
			ssh.Unmarshal(r.Payload, &p)
			s.mu.Lock()
			s.cmds = append(s.cmds, p.Cmd)
			s.mu.Unlock()
			started = true
			if o.Real {
				s.procs.Add(1)
				go func() { exit(real.run(s, ch, o, env, tty, "sh", "-c", p.Cmd)) }()
			} else {
				go func() { exit(s.run(sc, ch, p.Cmd, fakePty(tty), sigs)) }()
			}
		case "shell":
			started = true
			if o.Real {
				sh := o.Shell
				if sh == nil {
					sh = []string{"bash", "--norc", "--noprofile", "-i"}
				}
				s.procs.Add(1)
				go func() { exit(real.run(s, ch, o, env, tty, sh...)) }()
				break
			}
			go func() {
				if p := fakePty(tty); p != "" {
					fmt.Fprintln(ch, p)
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
					s.run(sc, ch, l, fakePty(tty), sigs)
				}
			}()
		case "subsystem":
			var p struct{ Name string }
			ssh.Unmarshal(r.Payload, &p)
			switch {
			case p.Name == "sftp" && !o.NoSFTP:
				started = true
				go func() {
					srv, _ := sftp.NewServer(ch, sftp.WithServerWorkingDirectory(o.Dir))
					srv.Serve()
					exit(0)
				}()
			case p.Name == "echo":
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
	real.signal("HUP") // the channel went: hang up what runs on it
}

func fakePty(p *ptyReq) string {
	if p == nil {
		return ""
	}
	return fmt.Sprintf("pty %s %dx%d", p.Term, p.Cols, p.Rows)
}

// realProc is a Real session's process, once started.
type realProc struct {
	mu  sync.Mutex
	cmd *exec.Cmd
	pty *os.File
}

func (r *realProc) resize(cols, rows uint32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pty != nil {
		pty.Setsize(r.pty, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	}
}

var signals = map[string]syscall.Signal{"HUP": syscall.SIGHUP, "INT": syscall.SIGINT, "KILL": syscall.SIGKILL, "TERM": syscall.SIGTERM}

func (r *realProc) signal(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cmd != nil && r.cmd.Process != nil {
		if sig, ok := signals[name]; ok {
			r.cmd.Process.Signal(sig)
		}
	}
}

// run starts argv here, as the session asked (env, a PTY), and is its exit
// status (255 when it couldn't start, -1 when signaled). The caller added
// it to s.procs.
func (r *realProc) run(s *Server, ch ssh.Channel, o Opts, env map[string]string, tty *ptyReq, argv ...string) int {
	defer s.procs.Done()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = o.Dir
	cmd.Env = append(os.Environ(), "HISTFILE=") // no history written into Dir
	if o.Dir != "" {
		cmd.Env = append(cmd.Env, "HOME="+o.Dir) // the remote user's home
	}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.WaitDelay = time.Second
	r.mu.Lock()
	r.cmd = cmd
	if tty != nil {
		cmd.Env = append(cmd.Env, "TERM="+tty.Term)
		f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(tty.Cols), Rows: uint16(tty.Rows)})
		if err != nil {
			r.mu.Unlock()
			fmt.Fprintln(ch.Stderr(), err)
			return 255
		}
		r.pty = f
		r.mu.Unlock()
		go io.Copy(f, ch)
		io.Copy(ch, f) // ends once the process (and its children) let go of the PTY
		r.mu.Lock()
		f.Close()
		r.pty = nil
		r.mu.Unlock()
	} else {
		cmd.Stdin, cmd.Stdout, cmd.Stderr = ch, ch, ch.Stderr()
		err := cmd.Start()
		r.mu.Unlock()
		if err != nil {
			fmt.Fprintln(ch.Stderr(), err)
			return 255
		}
	}
	cmd.Wait()
	return cmd.ProcessState.ExitCode()
}

// run is one fake command; its exit status.
func (s *Server) run(sc *ssh.ServerConn, ch ssh.Channel, cmd, pty string, sigs chan string) int {
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

// NewSigner is a fresh Ed25519 key.
func NewSigner(t testing.TB) ssh.Signer {
	t.Helper()
	_, k, _ := ed25519.GenerateKey(rand.Reader)
	s, err := ssh.NewSignerFromKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// WriteKey writes a fresh Ed25519 key to path (and path.pub), encrypted
// with pass when given; its signer.
func WriteKey(t testing.TB, path, pass string) ssh.Signer {
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
	for p, data := range map[string][]byte{path: pem.EncodeToMemory(b), path + ".pub": ssh.MarshalAuthorizedKey(s.PublicKey())} {
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// Cert signs key's certificate with ca.
func Cert(t testing.TB, ca ssh.Signer, key ssh.PublicKey, kind uint32, principals ...string) *ssh.Certificate {
	t.Helper()
	c := &ssh.Certificate{Key: key, CertType: kind, ValidPrincipals: principals, ValidBefore: ssh.CertTimeInfinity, KeyId: "test"}
	if err := c.SignCert(rand.Reader, ca); err != nil {
		t.Fatal(err)
	}
	return c
}
