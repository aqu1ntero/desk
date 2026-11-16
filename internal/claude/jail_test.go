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
}
