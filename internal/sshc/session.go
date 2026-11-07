package sshc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// Session is a shell or command on the server, as Shell starts it.
type Session struct {
	Stdin  io.WriteCloser // nil with StdinNull
	Stdout io.Reader
	Stderr io.Reader

	s *ssh.Session
}

// Shell is ssh's interactive session: the RemoteCommand (or the subsystem
// SessionType subsystem names), else the login shell; a PTY of term and
// cols×rows unless RequestTTY says no (with a command, only yes and force
// ask for one); SendEnv, SetEnv, then env; ForwardAgent.
func (c *Client) Shell(term string, cols, rows int, env map[string]string) (*Session, error) {
	cfg := c.cfg
	kind := cfg.Get("sessiontype")
	if kind == "none" {
		return nil, errors.New("SessionType none: no session to open")
	}
	s, err := c.session(env)
	if err != nil {
		return nil, err
	}
	cmd := cfg.Get("remotecommand")
	tty := cfg.Get("requesttty")
	if tty == "true" || tty == "force" || tty == "auto" && cmd == "" && kind == "default" {
		if err := s.RequestPty(term, rows, cols, ssh.TerminalModes{}); err != nil {
			s.Close()
			return nil, err
		}
	}
	out := &Session{s: s}
	if !cfg.Bool("stdinnull") {
		if out.Stdin, err = s.StdinPipe(); err != nil {
			s.Close()
			return nil, err
		}
	}
	if out.Stdout, err = s.StdoutPipe(); err == nil {
		out.Stderr, err = s.StderrPipe()
	}
	if err == nil {
		switch {
		case kind == "subsystem":
			err = s.RequestSubsystem(cmd)
		case cmd != "":
			err = s.Start(cmd)
		default:
			err = s.Shell()
		}
	}
	if err != nil {
		s.Close()
		return nil, err
	}
	return out, nil
}

// session is a new channel with the environment ssh sends and the agent
// forwarded when asked.
func (c *Client) session(env map[string]string) (*ssh.Session, error) {
	s, err := c.NewSession()
	if err != nil {
		return nil, err
	}
	c.setEnv(s, env)
	c.forwardAgent(s)
	return s, nil
}

// setEnv is client_session2_setup's environment: the local variables
// SendEnv's patterns name, then SetEnv, then env, a later one winning. A
// server refusing one is no error, as for ssh.
func (c *Client) setEnv(s *ssh.Session, env map[string]string) {
	set := map[string]string{}
	var order []string
	put := func(k, v string) {
		if _, ok := set[k]; !ok {
			order = append(order, k)
		}
		set[k] = v
	}
	for _, e := range os.Environ() {
		k, v, _ := strings.Cut(e, "=")
		for _, p := range c.cfg.List("sendenv") {
			if wild(p, k) {
				put(k, v)
				break
			}
		}
	}
	for _, e := range c.cfg.List("setenv") {
		k, v, _ := strings.Cut(e, "=")
		put(k, v)
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		put(k, env[k])
	}
	for _, k := range order {
		if err := s.Setenv(k, set[k]); err != nil {
			c.opt.Logf("Setting %s: refused by the server", k)
		}
	}
}

// forwardAgent is ForwardAgent: the IdentityAgent's (or the given socket's)
// agent offered to the session. Not after a changed key was taken unsafely.
func (c *Client) forwardAgent(s *ssh.Session) {
	p := c.cfg.Get("forwardagent")
	switch {
	case p == "no" || p == "":
		return
	case c.hc.unsafe:
		c.opt.Logf("Agent forwarding is disabled to avoid man-in-the-middle attacks.")
		return
	case p == "yes":
		p = agentPath(c.cfg)
	}
	var ferr error
	c.agentFwd.Do(func() {
		var conn io.ReadWriteCloser
		if conn, ferr = dialAgent(p); ferr != nil {
			return
		}
		c.onClose(func() { conn.Close() })
		ferr = agent.ForwardToAgent(c.Client, agent.NewClient(conn))
	})
	if ferr == nil {
		ferr = agent.RequestAgentForwarding(s)
	}
	if ferr != nil {
		c.opt.Logf("Agent forwarding: %v", ferr)
	}
}

// Resize is the terminal's new size (window-change).
func (s *Session) Resize(cols, rows int) error { return s.s.WindowChange(rows, cols) }

// Signal sends sig to the remote process.
func (s *Session) Signal(sig ssh.Signal) error { return s.s.Signal(sig) }

// Wait is the remote command's exit status; err when it gave none (a
// signal ended it, or the connection did), code -1 then.
func (s *Session) Wait() (int, error) { return exitCode(s.s.Wait()) }

// Close ends the session.
func (s *Session) Close() error { return s.s.Close() }

func exitCode(err error) (int, error) {
	var ee *ssh.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &ee) && ee.Signal() == "":
		return ee.ExitStatus(), nil
	}
	return -1, err
}

// Run runs one command (SendEnv/SetEnv applied, no PTY): its output, and
// its exit status with err nil when it exited. ctx's end kills it.
func (c *Client) Run(ctx context.Context, cmd string, stdin io.Reader) (stdout, stderr []byte, code int, err error) {
	s, err := c.session(nil)
	if err != nil {
		return nil, nil, -1, err
	}
	defer s.Close()
	var o, e bytes.Buffer
	if !c.cfg.Bool("stdinnull") {
		s.Stdin = stdin
	}
	s.Stdout, s.Stderr = &o, &e
	stop := context.AfterFunc(ctx, func() {
		s.Signal(ssh.SIGKILL)
		s.Close()
	})
	code, err = exitCode(s.Run(cmd))
	stop()
	if ctx.Err() != nil {
		code, err = -1, ctx.Err()
	}
	return o.Bytes(), e.Bytes(), code, err
}

// SFTP opens the sftp subsystem.
func (c *Client) SFTP() (*sftp.Client, error) { return sftp.NewClient(c.Client) }
