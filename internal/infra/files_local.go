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
	"strings"
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

// chmodLocal sets mode, octal or symbolic (u+x, go-w,a=rX), on paths, as
// chmod would. On Windows os.Chmod keeps only the owner's write bit (off:
// read-only), so that's all a mode changes there.
func chmodLocal(mode string, paths []string) error {
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			return err
		}
		m, err := parseMode(mode, unixMode(fi.Mode()), fi.IsDir())
		if err != nil {
			return err
		}
		if err := os.Chmod(p, sftpMode(m)); err != nil {
			return err
		}
	}
	return nil
}

// unixMode is sftpMode backwards: m's permission bits as chmod's octal.
func unixMode(m fs.FileMode) uint64 {
	u := uint64(m.Perm())
	for bit, f := range map[uint64]fs.FileMode{0o4000: fs.ModeSetuid, 0o2000: fs.ModeSetgid, 0o1000: fs.ModeSticky} {
		if m&f != 0 {
			u |= bit
		}
	}
	return u
}

// whoBits are the bits each of chmod's u, g, o, a reach (s and t included).
var whoBits = map[byte]uint64{'u': 0o4700, 'g': 0o2070, 'o': 0o1007, 'a': 0o7777}

// parseMode is chmod's mode applied to cur (dir: whether it's a folder, for
// X): octal, or comma-separated clauses of who (ugoa), one op (+-=) and
// perms (rwxXst). ponytail: no who means a, not "a but the umask" (GNU's
// +w gives only u+w under umask 022); no copying perms (g=u).
func parseMode(mode string, cur uint64, dir bool) (uint64, error) {
	bad := fmt.Errorf("chmod %s: not a mode (644, u+x, go-w)", mode)
	if m, err := strconv.ParseUint(mode, 8, 32); err == nil {
		if m > 0o7777 {
			return 0, bad
		}
		return m, nil
	}
	for _, cl := range strings.Split(mode, ",") {
		i, who := 0, uint64(0)
		for ; i < len(cl) && whoBits[cl[i]] != 0; i++ {
			who |= whoBits[cl[i]]
		}
		if who == 0 {
			who = whoBits['a']
		}
		if i+1 >= len(cl) || !strings.ContainsRune("+-=", rune(cl[i])) {
			return 0, bad
		}
		op, perm := cl[i], uint64(0)
		for _, c := range cl[i+1:] {
			switch c {
			case 'r':
				perm |= 0o444
			case 'w':
				perm |= 0o222
			case 'x':
				perm |= 0o111
			case 'X': // x only for folders and what some x already is
				if dir || cur&0o111 != 0 {
					perm |= 0o111
				}
			case 's':
				perm |= 0o6000
			case 't':
				perm |= 0o1000
			default:
				return 0, bad
			}
		}
		perm &= who
		switch op {
		case '+':
			cur |= perm
		case '-':
			cur &^= perm
		default:
			cur = cur&^who | perm
		}
	}
	return cur, nil
}
