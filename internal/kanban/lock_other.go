//go:build !(linux || darwin || freebsd || openbsd || netbsd || dragonfly || windows)

package kanban

import "os"

// lockFile has no lock to take here. ponytail: other OSes run unlocked; add
// theirs when desk builds for one.
func lockFile(*os.File) error { return nil }
