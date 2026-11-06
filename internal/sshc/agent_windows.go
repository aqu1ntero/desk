//go:build windows

package sshc

import (
	"io"
	"net"
	"os"
	"strings"
)

// defaultAgent is where Windows' OpenSSH finds the agent without
// IdentityAgent: SSH_AUTH_SOCK, else the agent service's named pipe.
func defaultAgent() string {
	if s := os.Getenv("SSH_AUTH_SOCK"); s != "" {
		return s
	}
	return `\\.\pipe\openssh-ssh-agent`
}

// dialAgent opens a named pipe as a plain file (synchronous I/O is enough
// for the agent's request/answer), else a Unix socket.
func dialAgent(path string) (io.ReadWriteCloser, error) {
	if strings.HasPrefix(path, `\\.\pipe\`) {
		return os.OpenFile(path, os.O_RDWR, 0)
	}
	return net.Dial("unix", path)
}
