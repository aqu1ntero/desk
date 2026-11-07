package claude

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// exe is executables' suffix: Windows' PATH finds claude.exe, not claude.
var exe = map[bool]string{true: ".exe"}[runtime.GOOS == "windows"]

// writeFake puts the fake program name on bin: script, or on Windows (no sh
// to run it) a copy of this test binary that fakeMain turns into the same
// thing, by mode: "args" is fakeClaude's, "stdin" appends its input to
// $FAKE_IN, "spin" never ends, "" exits.
func writeFake(t *testing.T, bin, name, script, mode string) {
	t.Helper()
	if name == "claude" {
		t.Setenv("DESK_FAKE_MODE", mode)
	}
	if exe == "" {
		os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755)
		return
	}
	self, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(bin, name+exe), self, 0o755)
}

// fakeMain is the test binary run as a fake claude or bwrap (writeFake on
// Windows): false when it is the tests.
func fakeMain() bool {
	switch filepath.Base(os.Args[0]) {
	case "claude.exe":
	case "bwrap.exe":
		os.Exit(0)
	default:
		return false
	}
	switch os.Getenv("DESK_FAKE_MODE") {
	case "args":
		if p := os.Getenv("FAKE_ARGS"); p != "" {
			os.WriteFile(p, []byte(strings.Join(os.Args[1:], " ")), 0o600)
		}
		fmt.Println(os.Getenv("FAKE_OUT"))
		fmt.Fprint(os.Stderr, os.Getenv("FAKE_ERR"))
		code, _ := strconv.Atoi(os.Getenv("FAKE_EXIT"))
		os.Exit(code)
	case "stdin":
		f, _ := os.OpenFile(os.Getenv("FAKE_IN"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		r := bufio.NewReader(os.Stdin)
		for {
			l, err := r.ReadString('\n')
			io.WriteString(f, l)
			if err != nil {
				os.Exit(0)
			}
		}
	case "spin":
		for {
			time.Sleep(time.Hour)
		}
	}
	os.Exit(0)
	return true
}
