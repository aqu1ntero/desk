package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/lfdominguez/desk/internal/kanban"
)

// stdio runs f with stdin from in and returns what it wrote to stdout.
func stdio(t *testing.T, in string, f func()) string {
	t.Helper()
	ir, iw, _ := os.Pipe()
	or, ow, _ := os.Pipe()
	oldIn, oldOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = ir, ow
	defer func() { os.Stdin, os.Stdout = oldIn, oldOut }()
	go func() { iw.WriteString(in); iw.Close() }()
	done := make(chan string)
	go func() { b, _ := io.ReadAll(or); done <- string(b) }()
	f()
	ow.Close()
	return <-done
}

// TestVersion: a release build says its version, a plain build its commit.
func TestVersion(t *testing.T) {
	var code int
	out := stdio(t, "", func() { code = run([]string{"desk", "--version"}) })
	if code != 0 || !strings.HasPrefix(out, "desk dev") {
		t.Errorf("--version: %d %q", code, out)
	}
	bi := &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "1a2b3c4d5e"}, {Key: "vcs.modified", Value: "true"}}}
	for v, want := range map[string]string{"2026.11.03.1": "2026.11.03.1", "": "dev-1a2b3c4-dirty"} {
		if got := versionOf(v, bi); got != want {
			t.Errorf("versionOf(%q) = %q, want %q", v, got, want)
		}
	}
	bi.Settings = []debug.BuildSetting{{Key: "vcs.revision", Value: "1a2"}, {Key: "vcs.modified", Value: "false"}}
	if got := versionOf("", bi); got != "dev" {
		t.Errorf("no usable commit: %q", got)
	}
}

// TestFixShell: a SHELL that isn't there becomes the shell of its name on
// PATH, else bash; one that is there, none, or nothing to find stays.
func TestFixShell(t *testing.T) {
	bin := t.TempDir()
	for _, n := range []string{"bash", "zsh"} {
		os.WriteFile(filepath.Join(bin, n), []byte("#!/bin/sh\n"), 0o755)
	}
	t.Setenv("PATH", bin)
	for in, want := range map[string]string{
		"/no/such/bin/zsh":         filepath.Join(bin, "zsh"),
		"/nix/store/gone/bin/bash": filepath.Join(bin, "bash"),
		"/no/such/bin/fish":        filepath.Join(bin, "bash"),
		filepath.Join(bin, "zsh"):  filepath.Join(bin, "zsh"),
		bin:                        filepath.Join(bin, "bash"), // a folder is no shell
		"":                         "",
	} {
		t.Setenv("SHELL", in)
		if fixShell(); os.Getenv("SHELL") != want {
			t.Errorf("%q: %q, want %q", in, os.Getenv("SHELL"), want)
		}
	}
	t.Setenv("PATH", t.TempDir())
	t.Setenv("SHELL", "/no/such/bin/bash")
	if fixShell(); os.Getenv("SHELL") != "/no/such/bin/bash" {
		t.Errorf("nothing to find: %q", os.Getenv("SHELL"))
	}
}

// TestLoginPath: on macOS PATH is the login shell's; elsewhere, or when the
// shell fails or says nothing, it stays.
func TestLoginPath(t *testing.T) {
	sh := filepath.Join(t.TempDir(), "sh")
	t.Setenv("PATH", "/usr/bin:/bin")
	for script, want := range map[string]string{
		`[ "$1" = -lc ] && printf '/opt/claude/bin:/usr/bin\n'`: "/opt/claude/bin:/usr/bin",
		"exit 1": "/usr/bin:/bin",
		"true":   "/usr/bin:/bin",
	} {
		os.WriteFile(sh, []byte("#!/bin/sh\n"+script+"\n"), 0o755)
		t.Setenv("SHELL", sh)
		loginPath(false)
		if os.Getenv("PATH") != "/usr/bin:/bin" {
			t.Fatal("not on macOS, PATH changed")
		}
		if loginPath(true); os.Getenv("PATH") != want {
			t.Errorf("%s: %q, want %q", script, os.Getenv("PATH"), want)
		}
		t.Setenv("PATH", "/usr/bin:/bin")
	}
}

// TestSubcommands: peer hands back its exit code, mcp serves
// the protocol on stdin/stdout.
func TestSubcommands(t *testing.T) {
	var code int
	out := stdio(t, "", func() { code = run([]string{"desk", "peer", "help"}) })
	if code != 0 || !strings.Contains(out, "desk peer:") {
		t.Errorf("peer help: %d %q", code, out)
	}
	if code := run([]string{"desk", "servers"}); code != 2 {
		t.Errorf("servers usage: %d, want 2", code)
	}
	if code := run([]string{"desk", "peer", "list", "-bogus"}); code != 2 {
		t.Errorf("peer bad flag: %d, want 2", code)
	}
	out = stdio(t, `{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n", func() {
		code = run([]string{"desk", "mcp", filepath.Join(t.TempDir(), "none.sock")})
	})
	if code != 0 || !strings.Contains(out, `"id":1`) || !strings.Contains(out, `"result":{}`) {
		t.Errorf("mcp: %d %q", code, out)
	}
}

// TestBadBoard: a board that doesn't load exits 1 before any window.
func TestBadBoard(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)
	os.MkdirAll(filepath.Join(xdg, "desk"), 0o755)
	os.WriteFile(filepath.Join(xdg, "desk", "board.json"), []byte("{"), 0o644)
	appMain = func() { t.Fatal("window opened") }
	defer func() { appMain = nil }()
	if code := run([]string{"desk"}); code != 1 {
		t.Errorf("bad board: %d, want 1", code)
	}
}

// TestWindow: the board loads, the crash log is opened beside it, and the
// window's end is the exit code (1 on error, 0 otherwise).
func TestWindow(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int
	}{{errors.New("no display"), 1}, {nil, 0}} {
		xdg := t.TempDir()
		t.Setenv("XDG_DATA_HOME", xdg)
		os.MkdirAll(filepath.Join(xdg, "desk"), 0o755)
		codes := make(chan int, 1)
		var gotPath string
		runUI = func(b *kanban.Board, path string) error {
			if b == nil {
				t.Error("no board")
			}
			gotPath = path
			return tc.err
		}
		exit = func(c int) { codes <- c }
		var code int
		appMain = func() { code = <-codes }
		if r := run([]string{"desk"}); r != 0 || code != tc.want {
			t.Errorf("ui err %v: run %d, exit %d, want %d", tc.err, r, code, tc.want)
		}
		if gotPath != filepath.Join(xdg, "desk", "board.json") {
			t.Errorf("path %q", gotPath)
		}
		if _, err := os.Stat(filepath.Join(xdg, "desk", "crash.log")); err != nil {
			t.Errorf("crash log: %v", err)
		}
	}
	// main is run with os.Args, its code to exit.
	old := os.Args
	defer func() { os.Args = old }()
	os.Args = []string{"desk", "peer", "list", "-bogus"}
	got := -1
	exit = func(c int) { got = c }
	main()
	if got != 2 {
		t.Errorf("main exit %d, want 2", got)
	}
}
