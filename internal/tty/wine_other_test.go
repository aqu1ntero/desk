//go:build !windows

package tty

func wine() bool { return false }
