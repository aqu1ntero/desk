package infra

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
)

// ErrAsbruOpen: Ásbrú is running, so its asbru.yml is what it was when it
// last closed; an import from it would miss what changed since.
var ErrAsbruOpen = errors.New("Ásbrú is open: close it first (its menu or tray icon, Quit), since it writes its connections only when it closes")

// asbruProcs is where asbruOpen looks for running programs (a var for tests).
var asbruProcs = "/proc"

// asbruOpen is whether Ásbrú is running: a process whose program, or the
// script its interpreter runs (perl asbru-cm), is asbru-cm. Ásbrú only runs
// on Linux; elsewhere there's no /proc and so none.
func asbruOpen() bool {
	ds, _ := os.ReadDir(asbruProcs)
	for _, d := range ds {
		b, err := os.ReadFile(filepath.Join(asbruProcs, d.Name(), "cmdline"))
		if err != nil {
			continue
		}
		args := bytes.Split(b, []byte{0})
		for _, a := range args[:min(2, len(args))] {
			if n := filepath.Base(string(a)); n == "asbru-cm" || n == "asbru-cm.pl" {
				return true
			}
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
