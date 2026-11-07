package infra

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
)

// localGo: the local host's files are done here in Go, not by the sh
// scripts, on Windows (no sh); its paths are then this OS's own, so the
// panel's side for it is native (fileSide.native). A variable so tests
// reach it on any OS.
var localGo = runtime.GOOS == "windows"

// copyLocal copies src (a file, folder or link) to dst on this machine,
// modes kept, as getSFTP does from a server; a file that fails halfway is
// removed.
func copyLocal(ctx context.Context, src, dst string, c counter) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fi, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		to, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(to, dst)
	case fi.IsDir():
		if err := os.MkdirAll(dst, 0o700); err != nil {
			return err
		}
		des, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, d := range des {
			if err := copyLocal(ctx, filepath.Join(src, d.Name()), filepath.Join(dst, d.Name()), c); err != nil {
				return err
			}
		}
		return os.Chmod(dst, fi.Mode().Perm())
	}
	r, err := os.Open(src)
	if err != nil {
		return err
	}
	defer r.Close()
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(io.MultiWriter(f, c), r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(dst) // a half-written file looks like a good one
		return err
	}
	return os.Chmod(dst, fi.Mode().Perm())
}

// removeLocal removes paths and all under them; a missing one is fine
// (RemoveAll says nothing of it).
func removeLocal(paths []string) error {
	for _, p := range paths {
		if err := os.RemoveAll(p); err != nil {
			return err
		}
	}
	return nil
}

// chmodLocal sets an octal mode on paths; symbolic ones (u+x) need the
// scripts' chmod, which the local host has none of here.
func chmodLocal(mode string, paths []string) error {
	m, err := strconv.ParseUint(mode, 8, 32)
	if err != nil || m > 0o7777 {
		return fmt.Errorf("chmod %s: give the mode in octal (644)", mode)
	}
	for _, p := range paths {
		if err := os.Chmod(p, sftpMode(m)); err != nil {
			return err
		}
	}
	return nil
}
