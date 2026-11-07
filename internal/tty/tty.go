// Package tty runs a program in a pseudo-terminal: a Unix PTY (creack/pty)
// or, on Windows, a ConPTY. What desk's local terminals (internal/term, the
// infra panes) and sshtest's real shells start their programs in.
package tty

// Wait blocks until the program ended and is its exit code, -1 when it was
// signaled or the status is unknown (a nil ProcessState says -1).
func (p *PTY) Wait() int {
	st, _ := p.wait()
	return st.ExitCode()
}
