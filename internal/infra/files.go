package infra

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// File transfer for the GUI's file panels, over desk's own ssh connection to
// the server (the shared ControlMaster, askpass from the vault). The server
// only needs sh, find (or ls), cat and tar.

type fileEntry struct {
	Name string
	Dir  bool
	Link bool
	Size int64       // -1: unknown (ls fallback)
	Mod  time.Time   // zero: unknown
	Mode os.FileMode // permission bits, 0: unknown
}

// shQuote quotes s for a POSIX shell.
func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// remoteSh runs script with sh on host, whatever the login shell is. The
// "local" host (no servers configured) is this machine.
func remoteSh(ctx context.Context, host, dir, script string) *exec.Cmd {
	if host == "local" {
		return exec.CommandContext(ctx, "sh", "-c", script)
	}
	cmd := exec.CommandContext(ctx, "ssh", append(sshArgs(host, dir), "sh -c "+shQuote(script))...)
	env, _ := askpassEnv(host, dir)
	cmd.Env = append(os.Environ(), env...)
	return cmd
}

// listRemote lists dir p on host ("" is the home directory) and returns its
// absolute path. GNU find gives types and sizes; without it, ls gives names.
func listRemote(ctx context.Context, host, dir, p string) (string, []fileEntry, error) {
	out, err := remoteSh(ctx, host, dir, listScript(p)).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 3 {
			return "", nil, fmt.Errorf("%s: no such directory", p)
		}
		return "", nil, fmt.Errorf("listing %s on %s: %v", p, host, err)
	}
	abs, entries := parseListing(string(out))
	return abs, entries, nil
}

func listScript(p string) string {
	cd := "cd"
	if p != "" {
		cd = "cd -- " + shQuote(p)
	}
	return cd + ` || exit 3; pwd; find . -mindepth 1 -maxdepth 1 -printf '%y\t%Y\t%s\t%T@\t%m\t%f\n' 2>/dev/null || ls -1Ap`
}

// parseListing reads listRemote's output: the directory, then one entry per
// line, either find's "type\ttarget type\tsize\tmtime\tmode\tname" or ls -p's "name[/]".
func parseListing(out string) (string, []fileEntry) {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	var entries []fileEntry
	for _, l := range lines[1:] {
		if f := strings.SplitN(l, "\t", 6); len(f) == 6 {
			size, _ := strconv.ParseInt(f[2], 10, 64)
			sec, _ := strconv.ParseFloat(f[3], 64)
			mode, _ := strconv.ParseUint(f[4], 8, 32)
			entries = append(entries, fileEntry{Name: f[5], Dir: f[1] == "d", Link: f[0] == "l", Size: size,
				Mod: time.Unix(int64(sec), 0), Mode: os.FileMode(mode)})
		} else if l != "" {
			name, dir := strings.CutSuffix(l, "/")
			entries = append(entries, fileEntry{Name: name, Dir: dir, Size: -1})
		}
	}
	sortEntries(entries, 0, false)
	return lines[0], entries
}

func listLocal(p string) (string, []fileEntry, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", nil, err
	}
	des, err := os.ReadDir(abs)
	if err != nil {
		return "", nil, err
	}
	var entries []fileEntry
	for _, de := range des {
		e := fileEntry{Name: de.Name(), Link: de.Type()&os.ModeSymlink != 0, Size: -1}
		if fi, err := os.Stat(filepath.Join(abs, de.Name())); err == nil { // follows links
			e.Dir, e.Size, e.Mod, e.Mode = fi.IsDir(), fi.Size(), fi.ModTime(), fi.Mode().Perm()
		}
		entries = append(entries, e)
	}
	sortEntries(entries, 0, false)
	return abs, entries, nil
}

// sortEntries puts directories first, then sorts by name ignoring case (by
// 0), size (1, folders by name) or modification time (2).
func sortEntries(es []fileEntry, by int, desc bool) {
	slices.SortStableFunc(es, func(a, b fileEntry) int {
		if a.Dir != b.Dir {
			if a.Dir {
				return -1
			}
			return 1
		}
		if by == 1 && a.Dir { // folders have no size to sort by: keep them by name
			return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		}
		c := 0
		switch {
		case by == 1:
			c = cmp.Compare(a.Size, b.Size)
		case by == 2:
			c = a.Mod.Compare(b.Mod)
		}
		if c == 0 {
			c = strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		}
		if desc {
			return -c
		}
		return c
	})
}

// counter counts bytes going through, for progress shown by another
// goroutine.
type counter struct {
	n  *atomic.Int64
	on func()
}

func (c counter) Write(b []byte) (int, error) {
	c.n.Add(int64(len(b)))
	c.on()
	return len(b), nil
}

// copyFromRemote copies e (in srcDir on host) into the local directory dst,
// keeping its permissions. n counts the bytes moved; on is called as they move.
func copyFromRemote(ctx context.Context, host, dir, srcDir, dst string, e fileEntry, n *atomic.Int64, on func()) error {
	src := path.Join(srcDir, e.Name)
	if !e.Dir {
		to := filepath.Join(dst, e.Name)
		f, err := os.Create(to)
		if err != nil {
			return err
		}
		cmd := remoteSh(ctx, host, dir, "cat -- "+shQuote(src))
		cmd.Stdout = io.MultiWriter(f, counter{n, on})
		err = runErr(cmd)
		f.Close()
		if err != nil {
			os.Remove(to) // a half-written file looks like a good one
			return err
		}
		if e.Mode != 0 {
			os.Chmod(to, e.Mode)
		}
		return nil
	}
	cmd := remoteSh(ctx, host, dir, "tar -C "+shQuote(srcDir)+" -cf - -- "+shQuote(e.Name))
	untar := exec.CommandContext(ctx, "tar", "-C", dst, "-xf", "-")
	return pipe(cmd, untar, n, on)
}

// copyToRemote copies e (in the local srcDir) into the directory dst on host.
func copyToRemote(ctx context.Context, host, dir, srcDir, dst string, e fileEntry, n *atomic.Int64, on func()) error {
	if !e.Dir {
		f, err := os.Open(filepath.Join(srcDir, e.Name))
		if err != nil {
			return err
		}
		defer f.Close()
		to := shQuote(path.Join(dst, e.Name))
		script := "cat > " + to
		if e.Mode != 0 {
			script += fmt.Sprintf(" && chmod %o %s", e.Mode, to)
		}
		cmd := remoteSh(ctx, host, dir, script)
		cmd.Stdin = io.TeeReader(f, counter{n, on})
		if err := runErr(cmd); err != nil {
			removeRemote(context.Background(), host, dir, []string{path.Join(dst, e.Name)})
			return err
		}
		return nil
	}
	tar := exec.CommandContext(ctx, "tar", "-C", srcDir, "-cf", "-", "--", e.Name)
	untar := remoteSh(ctx, host, dir, "tar -C "+shQuote(dst)+" -xf -")
	return pipe(tar, untar, n, on)
}

// pipe runs from | to, counting what flows between them.
func pipe(from, to *exec.Cmd, n *atomic.Int64, on func()) error {
	r, err := from.StdoutPipe()
	if err != nil {
		return err
	}
	var fromErr strings.Builder
	from.Stderr = &fromErr
	to.Stdin = io.TeeReader(r, counter{n, on})
	if err := from.Start(); err != nil {
		return err
	}
	toErr := runErr(to)
	if err := from.Wait(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(fromErr.String()))
	}
	return toErr
}

// runErr runs cmd and folds its stderr into the error.
func runErr(cmd *exec.Cmd) error {
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if s := strings.TrimSpace(stderr.String()); s != "" {
			return fmt.Errorf("%s", s)
		}
		return err
	}
	return nil
}

func removeRemote(ctx context.Context, host, dir string, paths []string) error {
	var q []string
	for _, p := range paths {
		q = append(q, shQuote(p))
	}
	return runErr(remoteSh(ctx, host, dir, "rm -rf -- "+strings.Join(q, " ")))
}

func renameRemote(ctx context.Context, host, dir, from, to string) error {
	return runErr(remoteSh(ctx, host, dir, "mv -- "+shQuote(from)+" "+shQuote(to)))
}

// openLocal opens p with the desktop's default application (a variable so
// tests don't launch anything).
var openLocal = func(p string) error {
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}
	return exec.Command(opener, p).Start()
}

// chmodRemote sets mode (octal, or symbolic like u+x) on paths.
func chmodRemote(ctx context.Context, host, dir, mode string, paths []string) error {
	q := []string{"chmod", "--", shQuote(mode)}
	for _, p := range paths {
		q = append(q, shQuote(p))
	}
	return runErr(remoteSh(ctx, host, dir, strings.Join(q, " ")))
}

func mkdirRemote(ctx context.Context, host, dir, p string) error {
	return runErr(remoteSh(ctx, host, dir, "mkdir -p -- "+shQuote(p)))
}

// humanSize is a byte count the way file managers show it.
func humanSize(n int64) string {
	switch {
	case n < 0:
		return ""
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	}
	f, units := float64(n), "KMGTP"
	i := -1
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	if f < 10 {
		return fmt.Sprintf("%.1f %cB", f, units[i])
	}
	return fmt.Sprintf("%.0f %cB", f, units[i])
}
