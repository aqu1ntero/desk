package infra

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lfdominguez/desk/internal/sshc/sshtest"
)

// TestRemoteFiles: the files panel on a server, over SFTP and, for a
// server without it, the shell scripts: list (home, a folder, links, a
// missing folder), download and upload of a file and a folder (modes kept,
// bytes counted), mkdir, rename, chmod (octal and symbolic), rm.
func TestRemoteFiles(t *testing.T) {
	for name, noSFTP := range map[string]bool{"sftp": false, "shell": true} {
		t.Run(name, func(t *testing.T) {
			if noSFTP {
				needGNUFind(t) // the server is this machine
			}
			w := newSSHW(t, sshtest.Opts{NoSFTP: noSFTP}, "")
			ctx, r, local := t.Context(), w.remote, t.TempDir()
			mk := func(p, data string, mode os.FileMode) {
				os.MkdirAll(filepath.Dir(p), 0o755)
				os.WriteFile(p, []byte(data), mode)
				os.Chmod(p, mode)
			}
			mk(filepath.Join(r, "srv", "app", "run.sh"), "echo hi", 0o751)
			mk(filepath.Join(r, "srv", "app", "sub", "data.txt"), "data", 0o640)
			os.Symlink("sub", filepath.Join(r, "srv", "app", "current"))

			abs, es, err := listRemote(ctx, "web-01", w.dir, "")
			if err != nil || abs != r || len(es) != 1 || es[0].Name != "srv" || !es[0].Dir {
				t.Fatalf("home: %q %+v %v", abs, es, err)
			}
			app := filepath.Join(r, "srv", "app")
			abs, es, err = listRemote(ctx, "web-01", w.dir, app)
			if err != nil || abs != app || len(es) != 3 {
				t.Fatalf("app: %q %+v %v", abs, es, err)
			}
			if cur, sub, run := es[0], es[1], es[2]; cur.Name != "current" || !cur.Dir || !cur.Link || sub.Name != "sub" || !sub.Dir || sub.Link ||
				run.Name != "run.sh" || run.Dir || run.Size != 7 || run.Mode != 0o751 || run.Mod.IsZero() {
				t.Errorf("entries %+v", es)
			}
			if _, _, err := listRemote(ctx, "web-01", w.dir, filepath.Join(r, "nope")); err == nil || err.Error() != filepath.Join(r, "nope")+": no such directory" {
				t.Errorf("missing: %v", err)
			}
			if _, _, err := listRemote(ctx, "web-01", w.dir, filepath.Join(app, "run.sh")); err == nil || !strings.HasSuffix(err.Error(), ": no such directory") {
				t.Errorf("a file: %v", err)
			}

			var n atomic.Int64
			calls := 0
			on := func() { calls++ }
			if err := copyFromRemote(ctx, "web-01", w.dir, app, local, es[2], &n, on); err != nil {
				t.Fatal(err)
			}
			if b, _ := os.ReadFile(filepath.Join(local, "run.sh")); string(b) != "echo hi" || n.Load() != 7 || calls == 0 {
				t.Errorf("download: %q, %d bytes, %d calls", b, n.Load(), calls)
			}
			if fi, _ := os.Stat(filepath.Join(local, "run.sh")); fi.Mode().Perm() != 0o751 {
				t.Errorf("download mode %o", fi.Mode().Perm())
			}
			n.Store(0)
			if err := copyFromRemote(ctx, "web-01", w.dir, filepath.Join(r, "srv"), local, fileEntry{Name: "app", Dir: true}, &n, on); err != nil {
				t.Fatal(err)
			}
			if b, _ := os.ReadFile(filepath.Join(local, "app", "current", "data.txt")); string(b) != "data" || n.Load() == 0 {
				t.Errorf("folder download: %q, %d bytes", b, n.Load())
			}
			if fi, _ := os.Stat(filepath.Join(local, "app", "sub", "data.txt")); fi == nil || fi.Mode().Perm() != 0o640 {
				t.Errorf("folder download mode %v", fi)
			}
			if to, err := os.Readlink(filepath.Join(local, "app", "current")); to != "sub" {
				t.Errorf("link: %q %v", to, err)
			}
			if err := copyFromRemote(ctx, "web-01", w.dir, app, local, fileEntry{Name: "gone"}, &n, on); err == nil || fileExists(filepath.Join(local, "gone")) {
				t.Errorf("a missing file: %v", err)
			}

			mk(filepath.Join(local, "up.txt"), "uploaded", 0o600)
			mk(filepath.Join(local, "tree", "deep", "f"), "f", 0o644)
			n.Store(0)
			if err := copyToRemote(ctx, "web-01", w.dir, local, filepath.Join(r, "srv"), fileEntry{Name: "up.txt", Mode: 0o600}, &n, on); err != nil {
				t.Fatal(err)
			}
			if b, _ := os.ReadFile(filepath.Join(r, "srv", "up.txt")); string(b) != "uploaded" || n.Load() != 8 {
				t.Errorf("upload: %q %d", b, n.Load())
			}
			if fi, _ := os.Stat(filepath.Join(r, "srv", "up.txt")); fi.Mode().Perm() != 0o600 {
				t.Errorf("upload mode %o", fi.Mode().Perm())
			}
			if err := copyToRemote(ctx, "web-01", w.dir, local, filepath.Join(r, "srv"), fileEntry{Name: "tree", Dir: true}, &n, on); err != nil {
				t.Fatal(err)
			}
			if b, _ := os.ReadFile(filepath.Join(r, "srv", "tree", "deep", "f")); string(b) != "f" {
				t.Errorf("folder upload: %q", b)
			}
			if err := copyToRemote(ctx, "web-01", w.dir, local, filepath.Join(r, "no", "such"), fileEntry{Name: "up.txt", Mode: 0o600}, &n, on); err == nil ||
				fileExists(filepath.Join(r, "no", "such", "up.txt")) {
				t.Errorf("an upload into nowhere: %v", err)
			}

			if err := mkdirRemote(ctx, "web-01", w.dir, filepath.Join(r, "a", "b", "c")); err != nil || !fileExists(filepath.Join(r, "a", "b", "c")) {
				t.Errorf("mkdir: %v", err)
			}
			if err := renameRemote(ctx, "web-01", w.dir, filepath.Join(r, "srv", "up.txt"), filepath.Join(r, "srv", "it's.txt")); err != nil ||
				!fileExists(filepath.Join(r, "srv", "it's.txt")) || fileExists(filepath.Join(r, "srv", "up.txt")) {
				t.Errorf("rename: %v", err)
			}
			if err := chmodRemote(ctx, "web-01", w.dir, "705", []string{filepath.Join(r, "srv", "it's.txt")}); err != nil {
				t.Fatal(err)
			}
			if fi, _ := os.Stat(filepath.Join(r, "srv", "it's.txt")); fi.Mode().Perm() != 0o705 {
				t.Errorf("chmod 705: %o", fi.Mode().Perm())
			}
			if err := chmodRemote(ctx, "web-01", w.dir, "g+r", []string{filepath.Join(r, "srv", "it's.txt")}); err != nil {
				t.Fatal(err)
			}
			if fi, _ := os.Stat(filepath.Join(r, "srv", "it's.txt")); fi.Mode().Perm() != 0o745 {
				t.Errorf("chmod g+r: %o", fi.Mode().Perm())
			}
			if err := chmodRemote(ctx, "web-01", w.dir, "644", []string{filepath.Join(r, "nope")}); err == nil {
				t.Error("chmod of nothing")
			}
			if err := removeRemote(ctx, "web-01", w.dir, []string{app, filepath.Join(r, "a"), filepath.Join(r, "never")}); err != nil ||
				fileExists(app) || fileExists(filepath.Join(r, "a")) || !fileExists(filepath.Join(r, "srv", "tree")) {
				t.Errorf("rm: %v", err)
			}
		})
	}
}

// TestRemoteFilesUnreachable: every operation on a server that can't be
// reached says so.
func TestRemoteFilesUnreachable(t *testing.T) {
	w := newSSHW(t, sshtest.Opts{}, "    Port 1\n")
	ctx := t.Context()
	var n atomic.Int64
	for what, err := range map[string]error{
		"list":     func() error { _, _, err := listRemote(ctx, "web-01", w.dir, ""); return err }(),
		"download": copyFromRemote(ctx, "web-01", w.dir, "/", t.TempDir(), fileEntry{Name: "x"}, &n, func() {}),
		"upload":   copyToRemote(ctx, "web-01", w.dir, t.TempDir(), "/", fileEntry{Name: "x"}, &n, func() {}),
		"rm":       removeRemote(ctx, "web-01", w.dir, []string{"/x"}),
		"mv":       renameRemote(ctx, "web-01", w.dir, "/x", "/y"),
		"chmod":    chmodRemote(ctx, "web-01", w.dir, "644", []string{"/x"}),
		"mkdir":    mkdirRemote(ctx, "web-01", w.dir, "/x"),
		"sh":       remoteSh(ctx, "web-01", w.dir, "true", nil, nil),
	} {
		if err == nil || !strings.Contains(err.Error(), "127.0.0.1") {
			t.Errorf("%s: %v", what, err)
		}
	}
}

// TestRemoteFilesErrors: over SFTP, what can't be read or written stops the
// copy with its error: an unreadable folder or file (either side), a local
// or remote name already taken by the other kind; a link goes up as a link;
// a cancelled context closes the sftp session; setuid and friends survive.
func TestRemoteFilesErrors(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads everything")
	}
	w := newSSHW(t, sshtest.Opts{}, "")
	ctx, r, local := t.Context(), w.remote, t.TempDir()
	var n atomic.Int64
	on := func() {}
	get := func(src string, e fileEntry, dst string) error {
		return copyFromRemote(ctx, "web-01", w.dir, src, dst, e, &n, on)
	}
	put := func(src string, e fileEntry, dst string) error {
		return copyToRemote(ctx, "web-01", w.dir, src, dst, e, &n, on)
	}
	os.MkdirAll(filepath.Join(r, "parent", "locked"), 0o755)
	os.Chmod(filepath.Join(r, "parent", "locked"), 0)
	os.WriteFile(filepath.Join(r, "secret"), []byte("x"), 0)
	os.WriteFile(filepath.Join(r, "plain"), []byte("x"), 0o644)
	t.Cleanup(func() { os.Chmod(filepath.Join(r, "parent", "locked"), 0o755) })
	if _, _, err := listRemote(ctx, "web-01", w.dir, filepath.Join(r, "parent", "locked")); err == nil || !strings.Contains(err.Error(), "listing") {
		t.Errorf("an unreadable folder: %v", err)
	}
	if err := get(r, fileEntry{Name: "parent", Dir: true}, local); err == nil {
		t.Error("a folder with an unreadable one in it")
	}
	if err := get(r, fileEntry{Name: "secret"}, local); err == nil {
		t.Error("an unreadable file")
	}
	os.WriteFile(filepath.Join(local, "taken"), nil, 0o644)
	os.Mkdir(filepath.Join(r, "taken"), 0o755)
	if err := get(r, fileEntry{Name: "taken", Dir: true}, local); err == nil {
		t.Error("a folder onto a local file")
	}
	os.Mkdir(filepath.Join(local, "plain"), 0o755)
	if err := get(r, fileEntry{Name: "plain"}, local); err == nil {
		t.Error("a file onto a local folder")
	}

	up := t.TempDir()
	os.WriteFile(filepath.Join(up, "secret"), []byte("x"), 0)
	os.MkdirAll(filepath.Join(up, "tree", "locked"), 0o755)
	os.Chmod(filepath.Join(up, "tree", "locked"), 0)
	t.Cleanup(func() { os.Chmod(filepath.Join(up, "tree", "locked"), 0o755) })
	os.Symlink("secret", filepath.Join(up, "link"))
	if err := put(up, fileEntry{Name: "secret"}, r); err == nil {
		t.Error("an unreadable local file")
	}
	if err := put(up, fileEntry{Name: "tree", Dir: true}, r); err == nil {
		t.Error("a local folder with an unreadable one in it")
	}
	if err := put(up, fileEntry{Name: "tree", Dir: true}, filepath.Join(r, "plain")); err == nil {
		t.Error("a folder under a remote file")
	}
	if err := put(up, fileEntry{Name: "link"}, r); err != nil {
		t.Fatal(err)
	}
	if to, _ := os.Readlink(filepath.Join(r, "link")); filepath.Base(to) != "secret" { // pkg/sftp's server makes it absolute

		t.Errorf("the link went up as %q", to)
	}
	ro := filepath.Join(r, "ro")
	os.MkdirAll(filepath.Join(ro, "f"), 0o755)
	os.Chmod(ro, 0o555)
	t.Cleanup(func() { os.Chmod(ro, 0o755) })
	if err := removeRemote(ctx, "web-01", w.dir, []string{filepath.Join(ro, "f")}); err == nil {
		t.Error("rm in a read-only folder")
	}

	if err := chmodRemote(ctx, "web-01", w.dir, "4755", []string{filepath.Join(r, "plain")}); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(filepath.Join(r, "plain")); fi.Mode()&os.ModeSetuid == 0 || fi.Mode().Perm() != 0o755 {
		t.Errorf("4755: %v", fi.Mode())
	}
	if m := sftpMode(0o7644); m != 0o644|os.ModeSetuid|os.ModeSetgid|os.ModeSticky {
		t.Errorf("sftpMode %v", m)
	}

	cctx, cancel := context.WithCancel(ctx)
	s, done, err := sftpFor(cctx, "web-01", w.dir)
	if err != nil || s == nil {
		t.Fatal(err)
	}
	cancel()
	waitS(t, "the session closed", func() bool { _, err := s.Getwd(); return err != nil })
	done()
	if s, _, err := sftpFor(ctx, "local", ""); s != nil || err != nil {
		t.Error("the local host has no sftp")
	}
}
