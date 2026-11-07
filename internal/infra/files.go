package infra

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
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

	"github.com/pkg/sftp"
)

// File transfer for the GUI's file panels, over SFTP on the server's shared
// connection (sshconn.go). A server without the sftp subsystem gets shell
// scripts run on the same connection instead (it needs sh, find or ls, cat
// and tar), as does the "local" host, run here.

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

// exitError is a script's exit status other than 0, with what it said on
// stderr.
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string {
	if e.msg != "" {
		return e.msg
	}
	return fmt.Sprintf("exit status %d", e.code)
}

// remoteSh runs script with sh on host, whatever the login shell is, its
// stdin and stdout as given (nil: none). The "local" host (no servers
// configured) is this machine. ponytail: a server's output comes whole once
// the script ends (Client.Run buffers it); stream it through a session of
// our own if the fallback's big transfers need it.
func remoteSh(ctx context.Context, host, dir, script string, stdin io.Reader, stdout io.Writer) error {
	var stderr bytes.Buffer
	code := 0
	var err error
	if host == "local" {
		cmd := exec.CommandContext(ctx, "sh", "-c", script)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, &stderr
		err = cmd.Run()
		if ee, ok := err.(*exec.ExitError); ok {
			code, err = ee.ExitCode(), nil
		}
	} else {
		c, release, derr := sshClient(ctx, host, dir)
		if derr != nil {
			return derr
		}
		defer release()
		var out, eout []byte
		out, eout, code, err = c.Run(ctx, "sh -c "+shQuote(script), stdin)
		stderr.Write(eout)
		if stdout != nil && err == nil {
			_, err = stdout.Write(out)
		}
	}
	if err != nil {
		return err
	}
	if code != 0 {
		return &exitError{code, strings.TrimSpace(stderr.String())}
	}
	return nil
}

// sftpFor is host's SFTP session and its close; nil (with no error) for
// the local host and a server without the subsystem: the scripts then.
// ponytail: one sftp session per operation; keep one per connection if
// opening them shows.
func sftpFor(ctx context.Context, host, dir string) (*sftp.Client, func(), error) {
	if host == "local" {
		return nil, nil, nil
	}
	c, release, err := sshClient(ctx, host, dir)
	if err != nil {
		return nil, nil, err
	}
	s, err := c.SFTP()
	if err != nil {
		release()
		return nil, nil, nil
	}
	stop := context.AfterFunc(ctx, func() { s.Close() })
	return s, func() { stop(); s.Close(); release() }, nil
}

// listRemote lists dir p on host ("" is the home directory) and returns its
// absolute path.
func listRemote(ctx context.Context, host, dir, p string) (string, []fileEntry, error) {
	s, done, err := sftpFor(ctx, host, dir)
	if err != nil {
		return "", nil, fmt.Errorf("listing %s on %s: %v", p, host, err)
	}
	if s != nil {
		defer done()
		return listSFTP(s, host, p)
	}
	if host == "local" && macOS { // BSD find has no -printf: listScript would get names only
		if err := ctx.Err(); err != nil {
			return "", nil, fmt.Errorf("listing %s on %s: %v", p, host, err)
		}
		if p == "" {
			p, _ = os.UserHomeDir()
		}
		abs, entries, err := listLocal(p)
		if err != nil {
			return "", nil, fmt.Errorf("%s: no such directory", p)
		}
		return abs, entries, nil
	}
	var out bytes.Buffer
	if err := remoteSh(ctx, host, dir, listScript(p), nil, &out); err != nil {
		var ee *exitError
		if errors.As(err, &ee) && ee.code == 3 {
			return "", nil, fmt.Errorf("%s: no such directory", p)
		}
		return "", nil, fmt.Errorf("listing %s on %s: %v", p, host, err)
	}
	abs, entries := parseListing(out.String())
	return abs, entries, nil
}

// listSFTP is listRemote over SFTP: links show as their target (a folder
// opens), with their own name.
func listSFTP(s *sftp.Client, host, p string) (string, []fileEntry, error) {
	abs, err := s.RealPath(cmp.Or(p, "."))
	if err != nil {
		return "", nil, fmt.Errorf("listing %s on %s: %v", p, host, err)
	}
	if fi, err := s.Stat(abs); err != nil || !fi.IsDir() {
		if err == nil || errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
			return "", nil, fmt.Errorf("%s: no such directory", p)
		}
		return "", nil, fmt.Errorf("listing %s on %s: %v", p, host, err)
	}
	fis, err := s.ReadDir(abs)
	if err != nil {
		return "", nil, fmt.Errorf("listing %s on %s: %v", p, host, err)
	}
	var entries []fileEntry
	for _, fi := range fis {
		e := fileEntry{Name: fi.Name(), Link: fi.Mode()&fs.ModeSymlink != 0}
		if e.Link {
			if t, err := s.Stat(path.Join(abs, fi.Name())); err == nil {
				fi = t
			}
		}
		e.Dir, e.Size, e.Mod, e.Mode = fi.IsDir(), fi.Size(), fi.ModTime().Truncate(time.Second), fi.Mode().Perm()
		entries = append(entries, e)
	}
	sortEntries(entries, 0, false)
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
	s, done, err := sftpFor(ctx, host, dir)
	if err != nil {
		return err
	}
	if s != nil {
		defer done()
		return getSFTP(s, path.Join(srcDir, e.Name), filepath.Join(dst, e.Name), counter{n, on})
	}
	src := path.Join(srcDir, e.Name)
	if !e.Dir {
		to := filepath.Join(dst, e.Name)
		f, err := os.Create(to)
		if err != nil {
			return err
		}
		err = remoteSh(ctx, host, dir, "cat -- "+shQuote(src), nil, io.MultiWriter(f, counter{n, on}))
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
	return through(func(w io.Writer) error {
		return remoteSh(ctx, host, dir, "tar -C "+shQuote(srcDir)+" -cf - -- "+shQuote(e.Name), nil, w)
	}, func(r io.Reader) error {
		untar := exec.CommandContext(ctx, "tar", "-C", dst, "-xf", "-")
		untar.Stdin = r
		return runErr(untar)
	}, counter{n, on})
}

// getSFTP copies src (a file, folder or link) to the local dst, modes kept.
func getSFTP(s *sftp.Client, src, dst string, c counter) error {
	fi, err := s.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		to, err := s.ReadLink(src)
		if err != nil {
			return err
		}
		return os.Symlink(to, dst)
	case fi.IsDir():
		if err := os.MkdirAll(dst, 0o700); err != nil {
			return err
		}
		fis, err := s.ReadDir(src)
		if err != nil {
			return err
		}
		for _, k := range fis {
			if err := getSFTP(s, path.Join(src, k.Name()), filepath.Join(dst, k.Name()), c); err != nil {
				return err
			}
		}
		return os.Chmod(dst, fi.Mode().Perm())
	}
	r, err := s.Open(src)
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

// copyToRemote copies e (in the local srcDir) into the directory dst on host.
func copyToRemote(ctx context.Context, host, dir, srcDir, dst string, e fileEntry, n *atomic.Int64, on func()) error {
	s, done, err := sftpFor(ctx, host, dir)
	if err != nil {
		return err
	}
	if s != nil {
		defer done()
		return putSFTP(s, filepath.Join(srcDir, e.Name), path.Join(dst, e.Name), counter{n, on})
	}
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
		if err := remoteSh(ctx, host, dir, script, io.TeeReader(f, counter{n, on}), nil); err != nil {
			removeRemote(context.Background(), host, dir, []string{path.Join(dst, e.Name)})
			return err
		}
		return nil
	}
	return through(func(w io.Writer) error {
		var mac []string
		if runtime.GOOS == "darwin" { // bsdtar: no ._ AppleDouble files, xattrs or ACLs, which a server's GNU tar warns about
			mac = []string{"--no-xattrs", "--no-acls", "--no-mac-metadata"}
		}
		tar := exec.CommandContext(ctx, "tar", slices.Concat(mac, []string{"-C", srcDir, "-cf", "-", "--", e.Name})...)
		tar.Env = append(os.Environ(), "COPYFILE_DISABLE=1")
		tar.Stdout = w
		return runErr(tar)
	}, func(r io.Reader) error {
		return remoteSh(ctx, host, dir, "tar -C "+shQuote(dst)+" -xf -", r, nil)
	}, counter{n, on})
}

// putSFTP copies the local src (a file, folder or link) to dst on the
// server, modes kept; a file that fails halfway is removed.
func putSFTP(s *sftp.Client, src, dst string, c counter) error {
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
		return s.Symlink(to, dst)
	case fi.IsDir():
		if err := s.MkdirAll(dst); err != nil {
			return err
		}
		des, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, k := range des {
			if err := putSFTP(s, filepath.Join(src, k.Name()), path.Join(dst, k.Name()), c); err != nil {
				return err
			}
		}
		return s.Chmod(dst, fi.Mode().Perm())
	}
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	w, err := s.Create(dst)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, io.TeeReader(f, c))
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = s.Chmod(dst, fi.Mode().Perm())
	}
	if err != nil {
		s.Remove(dst)
	}
	return err
}

// through runs produce into consume, counting what flows between them.
// Whichever side ended first is the error that counts: the other's is
// its fallout (a cut-off archive, a broken pipe).
func through(produce func(io.Writer) error, consume func(io.Reader) error, c counter) error {
	r, w := io.Pipe()
	perr := make(chan error, 1)
	go func() {
		err := produce(w)
		w.CloseWithError(err)
		perr <- err
	}()
	er := &endReader{r: io.TeeReader(r, c)}
	cerr := consume(er)
	r.Close()
	if err := <-perr; err != nil && (er.end || cerr == nil) {
		return err
	}
	return cerr
}

// endReader says whether its reader got to the end (or the producer's error).
type endReader struct {
	r   io.Reader
	end bool
}

func (e *endReader) Read(b []byte) (int, error) {
	n, err := e.r.Read(b)
	if err != nil {
		e.end = true
	}
	return n, err
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
	s, done, err := sftpFor(ctx, host, dir)
	if err != nil {
		return err
	}
	if s != nil {
		defer done()
		for _, p := range paths {
			if err := s.RemoveAll(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
		return nil
	}
	var q []string
	for _, p := range paths {
		q = append(q, shQuote(p))
	}
	return remoteSh(ctx, host, dir, "rm -rf -- "+strings.Join(q, " "), nil, nil)
}

func renameRemote(ctx context.Context, host, dir, from, to string) error {
	s, done, err := sftpFor(ctx, host, dir)
	if err != nil {
		return err
	}
	if s != nil {
		defer done()
		if err := s.PosixRename(from, to); err == nil {
			return nil
		}
		return s.Rename(from, to) // a server without posix-rename@openssh.com
	}
	return remoteSh(ctx, host, dir, "mv -- "+shQuote(from)+" "+shQuote(to), nil, nil)
}

// Opener is the command that opens a file or URL with the desktop's default
// application.
var Opener = map[bool]string{true: "open", false: "xdg-open"}[runtime.GOOS == "darwin"]

// openLocal opens p with Opener (a variable so tests don't launch anything).
var openLocal = func(p string) error { return exec.Command(Opener, p).Start() }

// chmodRemote sets mode (octal, or symbolic like u+x) on paths. A symbolic
// mode needs the server's chmod to read it: the script then.
func chmodRemote(ctx context.Context, host, dir, mode string, paths []string) error {
	if m, err := strconv.ParseUint(mode, 8, 32); err == nil && m <= 0o7777 {
		s, done, err := sftpFor(ctx, host, dir)
		if err != nil {
			return err
		}
		if s != nil {
			defer done()
			for _, p := range paths {
				if err := s.Chmod(p, sftpMode(m)); err != nil {
					return fmt.Errorf("chmod %s: %v", p, err)
				}
			}
			return nil
		}
	}
	q := []string{"chmod", "--", shQuote(mode)}
	for _, p := range paths {
		q = append(q, shQuote(p))
	}
	return remoteSh(ctx, host, dir, strings.Join(q, " "), nil, nil)
}

// sftpMode is an octal mode as os.FileMode, setuid, setgid and sticky
// included (sftp maps them back to the unix bits).
func sftpMode(m uint64) os.FileMode {
	fm := os.FileMode(m & 0o777)
	for bit, f := range map[uint64]os.FileMode{0o4000: os.ModeSetuid, 0o2000: os.ModeSetgid, 0o1000: os.ModeSticky} {
		if m&bit != 0 {
			fm |= f
		}
	}
	return fm
}

func mkdirRemote(ctx context.Context, host, dir, p string) error {
	s, done, err := sftpFor(ctx, host, dir)
	if err != nil {
		return err
	}
	if s != nil {
		defer done()
		return s.MkdirAll(p)
	}
	return remoteSh(ctx, host, dir, "mkdir -p -- "+shQuote(p), nil, nil)
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
