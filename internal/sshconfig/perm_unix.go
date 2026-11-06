//go:build !windows

package sshconfig

import (
	"os"
	"syscall"
)

// permOK is readconf.c's SSHCONF_CHECKPERM: owned by root or the user, not
// writable by group or others.
func permOK(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && (st.Uid == 0 || int(st.Uid) == os.Getuid()) && fi.Mode().Perm()&0o022 == 0
}
