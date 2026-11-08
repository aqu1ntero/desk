package infra

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ErrAsbruOpen: Ásbrú is running, so its asbru.yml is what it was when it
// last closed; an import from it would miss what changed since.
var ErrAsbruOpen = errors.New("Ásbrú is open: close it first (its menu or tray icon, Quit), since it writes its connections only when it closes")

// asbruProcs is where asbruOpen looks for running programs (a var for tests).
var asbruProcs = "/proc"

// asbruOpen is whether this user's Ásbrú is running: a process of theirs
// whose program, or the script perl runs, is asbru-cm. Another user's has
// its own asbru.yml. Ásbrú only runs on Linux; elsewhere there's no /proc
// and so none.
func asbruOpen() bool {
	uid := []byte("\nUid:\t" + strconv.Itoa(os.Getuid()) + "\t") // the real uid, first of four
	ds, _ := os.ReadDir(asbruProcs)
	for _, d := range ds {
		dir := filepath.Join(asbruProcs, d.Name())
		b, err := os.ReadFile(filepath.Join(dir, "cmdline"))
		if err != nil || !asbruCmd(bytes.Split(b, []byte{0})) {
			continue
		}
		if st, err := os.ReadFile(filepath.Join(dir, "status")); err == nil && bytes.Contains(st, uid) {
			return true
		}
	}
	return false
}

// asbruCmd is whether a command line runs Ásbrú: asbru-cm itself, or perl
// with it as its script (the first argument after perl's options).
func asbruCmd(args [][]byte) bool {
	is := func(a []byte) bool { n := filepath.Base(string(a)); return n == "asbru-cm" || n == "asbru-cm.pl" }
	if is(args[0]) {
		return true
	}
	if !strings.HasPrefix(filepath.Base(string(args[0])), "perl") {
		return false
	}
	for _, a := range args[1:] {
		if !bytes.HasPrefix(a, []byte("-")) {
			return is(a)
		}
	}
	return false
}

// AsbruCurrent is ErrAsbruOpen when file is Ásbrú's own and Ásbrú is
// running; an export is written when asked for, so it is current.
func AsbruCurrent(file string) error {
	if filepath.Clean(file) == filepath.Clean(AsbruPath()) && asbruOpen() {
		return ErrAsbruOpen
	}
	return nil
}
