//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package kanban

import (
	"errors"
	"os"
	"syscall"
)

// lockFile takes f's flock without waiting.
func lockFile(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		err = ErrLocked
	}
	return err
}
