//go:build !windows

package sshtest

// Wine is false off Windows: Wine runs Windows programs only.
func Wine() bool { return false }
