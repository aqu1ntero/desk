//go:build !windows

package sshc

import (
	"io"
	"net"
	"os"
)

// defaultAgent is where ssh finds the agent without IdentityAgent.
func defaultAgent() string { return os.Getenv("SSH_AUTH_SOCK") }

func dialAgent(path string) (io.ReadWriteCloser, error) { return net.Dial("unix", path) }
