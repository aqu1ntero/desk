//go:build !windows

package tty

import (
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
)

// PTY is a program running in a pseudo-terminal: Read is its output, Write
// its input.
type PTY struct {
	f *os.File
	p *os.Process
}

// Start runs cmd in a w×h pseudo-terminal, as its controlling terminal.
func Start(cmd *exec.Cmd, w, h int) (*PTY, error) {
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(h), Cols: uint16(w)})
	if err != nil {
		return nil, err
	}
	return &PTY{f, cmd.Process}, nil
}

func (p *PTY) Read(b []byte) (int, error)  { return p.f.Read(b) }
func (p *PTY) Write(b []byte) (int, error) { return p.f.Write(b) }

// Resize tells the program its terminal is now w×h (SIGWINCH).
func (p *PTY) Resize(w, h int) {
	pty.Setsize(p.f, &pty.Winsize{Rows: uint16(h), Cols: uint16(w)})
}

func (p *PTY) wait() (*os.ProcessState, error) { return p.p.Wait() }

// Hangup signals before closing: closing the pty alone leaves the reader
// blocked and the program alive.
func (p *PTY) Hangup() {
	p.p.Signal(syscall.SIGHUP)
	p.f.Close()
}
