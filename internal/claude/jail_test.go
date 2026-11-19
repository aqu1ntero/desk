package claude

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// confine sets Confine for t.
func confine(t *testing.T, f func(dir string) (hide, keep []string)) {
	Confine = f
	t.Cleanup(func() { Confine = nil })
}

// TestJail: a session's command gets a pid namespace, the walls hidden
// and, inside them, its own and --add-dir folders kept; a profile's bwrap
// takes them before its command.
func TestJail(t *testing.T) {
	home, walls := t.TempDir(), t.TempDir()
	other, images, dir := filepath.Join(walls, "Globex"), filepath.Join(walls, "images"), t.TempDir()
	os.MkdirAll(other, 0o755)
	os.MkdirAll(images, 0o755)
	os.MkdirAll(filepath.Join(home, "Secure", "Claudes"), 0o755)
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "bwrap"), []byte("#!/bin/sh\n"), 0o755)
	t.Setenv("PATH", bin)
	t.Setenv("CLAUDE_PROFILE", "")
	plain := func() *exec.Cmd { c := exec.Command("/opt/claude", "-p", "--add-dir="+images); c.Dir = dir; return c }
	if c := Jail(plain(), home, dir); filepath.Base(c.Path) != "claude" {
		t.Fatal("without Confine, as it is")
	}
	confine(t, func(string) (hide, keep []string) {
		return []string{other, filepath.Join(walls, "gone"), walls}, []string{images}
	})
	c := Jail(plain(), home, dir)
	want := []string{filepath.Join(bin, "bwrap"), "--dev-bind", "/", "/", "--unshare-pid",
		"--tmpfs", other, "--tmpfs", walls, "--tmpfs", filepath.Join(home, "Secure", "Claudes"),
		"--bind", images, images, "--", "/opt/claude", "-p", "--add-dir=" + images}
	if !slices.Equal(c.Args, want) || c.Dir != dir {
		t.Fatalf("wrapped:\n%q\nwant\n%q", c.Args, want)
	}
	prof := exec.Command(filepath.Join(bin, "bwrap"), "--dev-bind", "/", "/", "--bind", "a", "b", "--", "/opt/claude", "-p")
	if c := Jail(prof, home, dir); !slices.Equal(c.Args[6:9], []string{"b", "--unshare-pid", "--tmpfs"}) || c.Args[len(c.Args)-3] != "--" {
		t.Fatalf("a profile's bwrap: %q", c.Args)
	}
	t.Setenv("PATH", t.TempDir())
	defer func(g string) { storeBwrap = g }(storeBwrap)
	storeBwrap = filepath.Join(t.TempDir(), "none")
	if c := Jail(plain(), home, dir); filepath.Base(c.Path) != "claude" {
		t.Error("without bwrap, as it is")
	}
	t.Setenv("CLAUDE_PROFILE", "Acme")
	t.Setenv("PATH", bin)
	if c := Jail(plain(), home, dir); filepath.Base(c.Path) != "claude" {
		t.Error("inside a profile already: never nested")
	}
	noBwrap(t)
	if c := Jail(plain(), home, dir); filepath.Base(c.Path) != "claude" {
		t.Error("no bwrap on this OS: as it is")
	}
}

// TestJailSSHConfig: the default account's own bwrap binds a copy of a
// symlinked ~/.ssh/config, kept in its ~/.claude, over the file: in the
// namespace a store's root-owned file is nobody's, and ssh refuses it.
func TestJailSSHConfig(t *testing.T) {
	home, dir, bin := t.TempDir(), t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(bin, "bwrap"), []byte("#!/bin/sh\n"), 0o755)
	t.Setenv("PATH", bin)
	t.Setenv("CLAUDE_PROFILE", "")
	os.MkdirAll(filepath.Join(home, ".ssh"), 0o700)
	os.MkdirAll(filepath.Join(home, ".claude"), 0o700)
	store := filepath.Join(home, "store-config")
	os.WriteFile(store, []byte("Host *\n"), 0o444)
	if err := os.Symlink(store, filepath.Join(home, ".ssh", "config")); err != nil {
		t.Skip("no symlinks here:", err)
	}
	confine(t, func(string) (hide, keep []string) { return nil, nil })
	c := Jail(exec.Command("/opt/claude", "-p"), home, dir)
	cp := filepath.Join(home, ".claude", ".ssh-config-0")
	if !slices.Equal(c.Args[1:7], []string{"--dev-bind", "/", "/", "--ro-bind", cp, store}) {
		t.Fatalf("%q", c.Args)
	}
	if b, _ := os.ReadFile(cp); string(b) != "Host *\n" {
		t.Fatalf("copy %q", b)
	}
}

// TestJailSeatbelt: on macOS a session runs under sandbox-exec, the walls
// denied and, after them, what it keeps allowed (its profile's config
// folder too).
func TestJailSeatbelt(t *testing.T) {
	noBwrap(t)
	seatbelt = true
	defer func() { seatbelt = false }()
	home, walls, dir := t.TempDir(), t.TempDir(), t.TempDir()
	other, prof := filepath.Join(walls, `Glo"bex`), filepath.Join(home, "Secure", "Claudes", "Acme")
	os.MkdirAll(other, 0o755)
	os.MkdirAll(prof, 0o755)
	t.Setenv("CLAUDE_PROFILE", "")
	confine(t, func(string) (hide, keep []string) { return []string{other, filepath.Join(walls, "gone")}, nil })
	cmd := exec.Command("/opt/claude", "-p")
	cmd.Env, cmd.Dir = []string{"CLAUDE_CONFIG_DIR=" + prof}, dir
	c := Jail(cmd, home, dir)
	real := func(p string) string { r, _ := filepath.EvalSymlinks(p); return r }
	q := func(p string) string { return `(subpath "` + strings.ReplaceAll(real(p), `"`, `\"`) + `")` }
	want := "(version 1)\n(allow default)\n(deny file-read* file-write* " + q(other) + " " + q(filepath.Join(home, "Secure", "Claudes")) + ")\n" +
		"(allow file-read* file-write* " + q(prof) + " " + q(dir) + ")\n"
	if c.Path != sandboxExec || len(c.Args) != 5 || c.Args[1] != "-p" || c.Args[2] != want || c.Args[3] != "/opt/claude" || c.Dir != dir || c.Env[0] != cmd.Env[0] {
		t.Fatalf("%q\nprofile want\n%s", c.Args, want)
	}
	confine(t, func(string) (hide, keep []string) { return nil, nil })
	os.RemoveAll(filepath.Join(home, "Secure"))
	if c := Jail(exec.Command("/opt/claude"), home, dir); c.Args[2] != "(version 1)\n(allow default)\n" {
		t.Errorf("no walls: %q", c.Args[2])
	}
}

// TestJailHides: with the real bwrap, a wall is an empty folder and what
// it keeps is there. Skipped without bwrap or user namespaces.
func TestJailHides(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("bwrap is Linux's")
	}
	real := bwrapBin()
	if real == "" {
		t.Skip("no bwrap")
	}
	walls := t.TempDir()
	os.MkdirAll(filepath.Join(walls, "keep"), 0o755)
	os.WriteFile(filepath.Join(walls, "secret"), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(walls, "keep", "mine"), []byte("x"), 0o600)
	confine(t, func(string) (hide, keep []string) { return []string{walls}, []string{filepath.Join(walls, "keep")} })
	t.Setenv("CLAUDE_PROFILE", "")
	c := Jail(exec.Command("sh", "-c", "ls -A "+walls+"; ls "+filepath.Join(walls, "keep")+"; echo $$"), t.TempDir(), t.TempDir())
	out, err := c.CombinedOutput()
	if err != nil {
		t.Skipf("bwrap can't run here: %v %s", err, out)
	}
	if got := strings.Fields(string(out)); !slices.Equal(got, []string{"keep", "mine", "2"}) {
		t.Errorf("inside: %q", got)
	}
	// an organization's folder linked from elsewhere: bwrap can't mount on
	// the link, so the wall goes on what it points at
	link := filepath.Join(t.TempDir(), "Globex")
	if err := os.Symlink(walls, link); err != nil {
		t.Skip("no symlinks here:", err)
	}
	confine(t, func(string) (hide, keep []string) { return []string{link}, []string{filepath.Join(link, "keep")} })
	out, err = Jail(exec.Command("sh", "-c", "ls -A "+link+"/"), t.TempDir(), t.TempDir()).CombinedOutput()
	if got := strings.Fields(string(out)); err != nil || !slices.Equal(got, []string{"keep"}) {
		t.Errorf("through a link: %v %q", err, out)
	}
}
