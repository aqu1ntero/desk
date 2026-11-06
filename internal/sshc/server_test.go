package sshc

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/lfdominguez/desk/internal/sshc/sshtest"
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

// server is sshtest's, with this package's own helpers on it.
type server struct{ *sshtest.Server }

func newServer(t *testing.T, o sshtest.Opts) *server { return &server{sshtest.New(t, o)} }

var (
	newSigner = func(t *testing.T) ssh.Signer { return sshtest.NewSigner(t) }
	writeKey  = func(t *testing.T, path, pass string) ssh.Signer { return sshtest.WriteKey(t, path, pass) }
	cert      = func(t *testing.T, ca ssh.Signer, key ssh.PublicKey, kind uint32, principals ...string) *ssh.Certificate {
		return sshtest.Cert(t, ca, key, kind, principals...)
	}
)

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
		text = strings.ReplaceAll(text, "%PORT"+strconv.Itoa(i), strconv.Itoa(s.Port))
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
