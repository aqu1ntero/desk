package main

import (
	"encoding/json"
	"errors"
	"io"
	"net"
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

// TestSubcommands: peer hands back its exit code, mcp serves
// the protocol on stdin/stdout.
func TestSubcommands(t *testing.T) {
	t.Setenv("DESK_ASKPASS", "")
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

// TestAskpass: as ssh's askpass, desk prints the workspace's answer.
func TestAskpass(t *testing.T) {
	dir, err := os.MkdirTemp("", "ap") // short: unix socket paths are limited
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "s")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	prompt := make(chan string, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		var r struct{ ID, Prompt string }
		json.NewDecoder(c).Decode(&r)
		prompt <- r.Prompt
		json.NewEncoder(c).Encode(map[string]any{"Answer": "s3cret", "OK": true})
	}()
	t.Setenv("DESK_ASKPASS", sock)
	var code int
	out := stdio(t, "", func() { code = run([]string{"desk", "Password:"}) })
	if code != 0 || out != "s3cret\n" || <-prompt != "Password:" {
		t.Errorf("askpass: %d %q", code, out)
	}
}

// TestBadBoard: a board that doesn't load exits 1 before any window.
func TestBadBoard(t *testing.T) {
	t.Setenv("DESK_ASKPASS", "")
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
	t.Setenv("DESK_ASKPASS", "")
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
