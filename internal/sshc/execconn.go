package sshc

import (
	"bytes"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// lockedBuf is a command's stderr, written by the session as it reads it.
type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.TrimSpace(l.b.String())
}

// ExecConn is cmd run on the server with its stdin and stdout as a
// connection: ssh -W host:port there reaches what only the server reaches,
// the way ProxyCommand's stdio does here. What cmd says on stderr when it
// ends with no output becomes the read error.
func (c *Client) ExecConn(cmd string) (net.Conn, error) {
	s, err := c.session(nil)
	if err != nil {
		return nil, err
	}
	e := &execConn{s: s}
	s.Stderr = &e.stderr
	if e.in, err = s.StdinPipe(); err == nil {
		if e.out, err = s.StdoutPipe(); err == nil {
			err = s.Start(cmd)
		}
	}
	if err != nil {
		s.Close()
		return nil, err
	}
	return e, nil
}

type execConn struct {
	s      *ssh.Session
	in     io.WriteCloser
	out    io.Reader
	stderr lockedBuf
	once   sync.Once
	waited sync.Once
}

func (e *execConn) Read(p []byte) (int, error) {
	n, err := e.out.Read(p)
	if err == io.EOF && n == 0 {
		e.waited.Do(func() { e.s.Wait() }) // stderr is all in once the command has ended
		if msg := e.stderr.String(); msg != "" {
			return 0, errors.New(msg)
		}
	}
	return n, err
}

func (e *execConn) Write(p []byte) (int, error) { return e.in.Write(p) }
func (e *execConn) CloseWrite() error           { return e.in.Close() }
func (e *execConn) Close() error {
	e.once.Do(func() { e.in.Close(); e.s.Close() })
	return nil
}

type execAddr struct{}

func (execAddr) Network() string { return "exec" }
func (execAddr) String() string  { return "exec" }

func (e *execConn) LocalAddr() net.Addr              { return execAddr{} }
func (e *execConn) RemoteAddr() net.Addr             { return execAddr{} }
func (e *execConn) SetDeadline(time.Time) error      { return nil }
func (e *execConn) SetReadDeadline(time.Time) error  { return nil }
func (e *execConn) SetWriteDeadline(time.Time) error { return nil }

// Exec is cmd started on the server for as long as it runs (the hops of a
// remote forward past it), its stop, and its end: a channel closed once it
// ends, then holding its error (what it said on stderr).
func (c *Client) Exec(cmd string) (stop func(), ended <-chan error, err error) {
	s, err := c.session(nil)
	if err != nil {
		return nil, nil, err
	}
	var stderr lockedBuf
	s.Stderr = &stderr
	if err := s.Start(cmd); err != nil {
		s.Close()
		return nil, nil, err
	}
	done := make(chan error, 1)
	go func() {
		err := s.Wait()
		if msg := stderr.String(); msg != "" {
			err = errors.New(msg)
		} else if err == nil {
			err = errors.New("ended")
		}
		done <- err
		close(done)
	}()
	return func() { s.Close() }, done, nil
}
